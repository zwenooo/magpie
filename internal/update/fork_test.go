package update

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type forkTransport func(*http.Request) (*http.Response, error)

func (f forkTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// An explicitly disabled feed must never discover or install a build.
// Catch requests with a transport so a broken guard still reaches no network.
func TestForkFeedsCanBeDisabledBeforeHTTP(t *testing.T) {
	t.Setenv("MAGPIE_UPDATE_FEED", "off")
	t.Setenv("MAGPIE_NOTES_FEED", "off")
	var requests atomic.Int32
	old := client
	client = &http.Client{Transport: forkTransport(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"version":"9.9.9","releases":[{"version":"9.9.9","notes":"upstream"}]}`)),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { client = old })
	ctx := context.Background()
	for _, c := range []struct {
		name string
		ask  func() error
		want error
	}{
		{"latest", func() error { _, err := Latest(ctx); return err }, errUpdatesDisabled},
		{"latest in language", func() error { _, err := LatestIn(ctx, "zh"); return err }, errUpdatesDisabled},
		{"notes between", func() error { _, err := NotesBetween(ctx, "", "9.9.9", ""); return err }, errNotesDisabled},
		{"notes back", func() error { _, err := NotesBack(ctx, "9.9.9", 10, "zh"); return err }, errNotesDisabled},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := c.ask(); !errors.Is(err, c.want) {
				t.Errorf("got %v, want %v", err, c.want)
			}
		})
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("default fork feeds made %d HTTP requests", n)
	}
}

func TestForkFeedsHonorExplicitOverrides(t *testing.T) {
	var updates, notes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			updates.Add(1)
			json.NewEncoder(w).Encode(Release{Version: "1.2.3", Notes: "fork release"})
		case "/notes":
			notes.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"releases": []Note{{Version: "1.2.3", Notes: "fork release"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("MAGPIE_UPDATE_FEED", srv.URL+"/latest")
	t.Setenv("MAGPIE_NOTES_FEED", srv.URL+"/notes")
	ctx := context.Background()
	if rel, err := Latest(ctx); err != nil || rel.Version != "1.2.3" {
		t.Fatalf("configured update feed: %+v, %v", rel, err)
	}
	if got, err := NotesBetween(ctx, "", "1.2.3", ""); err != nil || len(got) != 1 || got[0].Notes != "fork release" {
		t.Fatalf("configured notes feed: %+v, %v", got, err)
	}
	if updates.Load() != 1 || notes.Load() != 1 {
		t.Fatalf("configured requests: updates=%d notes=%d", updates.Load(), notes.Load())
	}
}

func TestForkReleasePage(t *testing.T) {
	for _, version := range []string{"1.2.3", "v1.2.3", " v1.2.3 "} {
		if got := ReleasePage(version); got != "https://github.com/zwenooo/magpie/releases/tag/v1.2.3" {
			t.Errorf("ReleasePage(%q) = %q", version, got)
		}
	}
	for _, version := range []string{"0.1.1725-vb.0.0.1", "v0.1.1725-vb.0.0.1", " va.0.1.1725-vb.0.0.1 "} {
		if got := ReleasePage(version); got != "https://github.com/zwenooo/magpie/releases/tag/va.0.1.1725-vb.0.0.1" {
			t.Errorf("ReleasePage(%q) = %q", version, got)
		}
	}
}

func TestForkBinaryVersionKeepsNumericOrdering(t *testing.T) {
	if !Released("0.1.1725-vb.0.0.1") || !Newer("0.1.1725-vb.0.0.10", "0.1.1725-vb.0.0.9") || !Newer("0.1.1726-vb.0.0.1", "0.1.1725-vb.9.9.9") {
		t.Fatal("fork binary versions must retain both numeric version components")
	}
}
