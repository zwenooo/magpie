package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A download that drops part way is tried again, and reports its progress.
func TestDownloadRetriesAndReports(t *testing.T) {
	body := make([]byte, 256<<10)
	for i := range body {
		body[i] = byte(i)
	}
	sum := sha256.Sum256(body)
	var tries atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := tries.Add(1)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if attempt == 1 {
			w.Write(body[:1000]) // then the connection goes
			return
		}
		w.Write(body)
	}))
	defer srv.Close()

	var done, total int64
	ctx := WithProgress(context.Background(), func(d, t int64) { done, total = d, t })
	path := filepath.Join(t.TempDir(), "magpie.new")
	if err := download(ctx, Asset{URL: srv.URL, SHA256: hex.EncodeToString(sum[:])}, path); err != nil {
		t.Fatal(err)
	}
	if got := tries.Load(); got != 2 {
		t.Fatalf("tries = %d, want 2", got)
	}
	if done != int64(len(body)) || total != int64(len(body)) {
		t.Fatalf("progress = %d/%d, want %d/%d", done, total, len(body), len(body))
	}
	if b, _ := os.ReadFile(path); len(b) != len(body) {
		t.Fatalf("file has %d bytes", len(b))
	}
}

// quickRetries has download's tries follow each other at once.
func quickRetries(t *testing.T) {
	t.Helper()
	old := retryWaits
	retryWaits = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { retryWaits = old })
}

// A download the connection cuts part way goes on from where it broke
// off: the next try asks for the rest with Range, and the hash is checked
// over the whole file, the bytes of the first try included.
func TestDownloadResumesWhereItBrokeOff(t *testing.T) {
	quickRetries(t)
	body := make([]byte, 512<<10)
	for i := range body {
		body[i] = byte(i * 7)
	}
	sum := sha256.Sum256(body)
	var rangesMu sync.Mutex
	var ranges []string
	cut := []int{100 << 10, 300 << 10} // where the first two tries break off
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangesMu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		attempt := len(ranges) - 1
		rangesMu.Unlock()
		from := 0
		if rg := r.Header.Get("Range"); rg != "" {
			from, _ = strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"))
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, len(body)-1, len(body)))
			w.Header().Set("Content-Length", strconv.Itoa(len(body)-from))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		}
		to := len(body)
		if attempt < len(cut) {
			to = cut[attempt]
		}
		w.Write(body[from:to])
		if to < len(body) {
			w.(http.Flusher).Flush()
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close() // mid-stream
		}
	}))
	defer srv.Close()

	var done, total int64
	ctx := WithProgress(context.Background(), func(d, t int64) { done, total = d, t })
	path := filepath.Join(t.TempDir(), "magpie.new")
	if err := download(ctx, Asset{URL: srv.URL, SHA256: hex.EncodeToString(sum[:])}, path); err != nil {
		t.Fatal(err)
	}
	want := []string{"", "bytes=102400-", "bytes=307200-"}
	rangesMu.Lock()
	gotRanges := slices.Clone(ranges)
	rangesMu.Unlock()
	if !slices.Equal(gotRanges, want) {
		t.Fatalf("ranges asked = %q, want %q", gotRanges, want)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, body) {
		t.Fatalf("file has %d bytes, not the release's", len(b))
	}
	if done != int64(len(body)) || total != int64(len(body)) {
		t.Fatalf("progress = %d/%d, want %d/%d", done, total, len(body), len(body))
	}
}

// The rest of a file sent with other bytes than the release's (a host
// that resumes wrongly) fails the hash, which covers the whole file, and
// nothing is kept.
func TestDownloadResumedFileIsStillChecked(t *testing.T) {
	quickRetries(t)
	body := bytes.Repeat([]byte("magpie"), 50<<10)
	sum := sha256.Sum256(body)
	var tries atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tries.Add(1) == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.Write(body[:1000])
			w.(http.Flusher).Flush()
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 1000-%d/%d", len(body)-1, len(body)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(bytes.Repeat([]byte("x"), len(body)-1000))
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "magpie.new")
	err := download(context.Background(), Asset{URL: srv.URL, SHA256: hex.EncodeToString(sum[:])}, path)
	if err == nil || !strings.Contains(err.Error(), "does not match its checksum") {
		t.Fatalf("err = %v, want a checksum mismatch", err)
	}
	if _, serr := os.Stat(path); !os.IsNotExist(serr) {
		t.Fatal("the mismatched file was kept")
	}
}

// A connection dropped before any answer (EOF at the connect, what the
// reporter's Windows box got from GitHub's asset host) is tried again
// every time retryWaits allows, and the error that ends it names the file
// and the cause, not the signed address it was redirected to.
func TestDownloadDroppedConnectionErrorKeepsAddressOut(t *testing.T) {
	quickRetries(t)
	var tries atomic.Int32
	assets := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tries.Add(1)
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close() // EOF, no answer at all
	}))
	defer assets.Close()
	signed := assets.URL + "/github-production-release-asset/1?sig=SECRETSIG&jwt=SECRETJWT"
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, signed, http.StatusFound)
	}))
	defer gh.Close()
	path := filepath.Join(t.TempDir(), "magpie.new")
	err := download(context.Background(), Asset{URL: gh.URL + "/magpie-windows-amd64.exe", SHA256: "00"}, path)
	if err == nil || !Network(err) {
		t.Fatalf("err = %v, want a network error", err)
	}
	if got := tries.Load(); int(got) != len(retryWaits)+1 {
		t.Fatalf("tries = %d, want %d", got, len(retryWaits)+1)
	}
	if msg := err.Error(); strings.Contains(msg, "SECRET") || strings.Contains(msg, "http") || !strings.Contains(msg, "magpie.new") || !strings.Contains(msg, "EOF") {
		t.Fatalf("error = %q", msg)
	}
	if _, serr := os.Stat(path); !os.IsNotExist(serr) {
		t.Fatal("a partial file was kept")
	}
}

// A refusal that isn't the network's (404: not uploaded yet) isn't tried
// again and again.
func TestDownloadNotFoundIsNotRetried(t *testing.T) {
	quickRetries(t)
	var tries atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tries.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	err := download(context.Background(), Asset{URL: srv.URL, SHA256: "00"}, filepath.Join(t.TempDir(), "magpie.new"))
	if got := tries.Load(); err == nil || Network(err) || got != 1 {
		t.Fatalf("err = %v after %d tries, want one 404", err, got)
	}
}
