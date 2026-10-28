package gui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/settings"
	"github.com/yetone/magpie/internal/update"
)

// The GitHub adapter is tested in update; here its normalized Release goes
// through the real GUI actions and downloader, without installing anything.
func TestForkUpdateCheckAndDownload(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		name := "verified download"
		if corrupt {
			name = "checksum mismatch"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
			for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy", "NO_PROXY", "no_proxy"} {
				t.Setenv(key, "")
			}
			if err := settings.Save(settings.Settings{NoAutoDownload: true, Proxy: "direct"}); err != nil {
				t.Fatal(err)
			}
			const current, latest = "0.1.1725-vb.0.0.5", "0.1.1725-vb.0.0.6"
			page := update.ReleasePage(latest)
			payload := []byte("fork update fixture")
			sum := sha256.Sum256(payload)
			var downloads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/latest":
					json.NewEncoder(w).Encode(update.Release{Version: latest, URL: page, Notes: "Fork update fixture notes.", Assets: map[string]update.Asset{
						update.BinaryAsset(): {URL: "http://" + r.Host + "/asset", Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])},
					}})
				case "/asset":
					downloads.Add(1)
					body := bytes.Clone(payload)
					if corrupt {
						body[0] ^= 1 // Same length: checksum validation must reject it.
					}
					w.Write(body)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			t.Setenv("MAGPIE_UPDATE_FEED", server.URL+"/latest")
			oldVersion, oldUpdates := Version, updates
			Version = current
			t.Cleanup(func() { Version, updates = oldVersion, oldUpdates })
			exe, err := update.Executable()
			if err != nil {
				t.Fatal(err)
			}
			// StageBinary writes beside this disposable Go test binary, never
			// a user's installed app. It is not followed by an install action.
			staged := exe + ".new"
			if _, err := os.Stat(staged); !os.IsNotExist(err) {
				t.Fatalf("unexpected pre-existing stage file: %v", err)
			}
			t.Cleanup(func() { os.Remove(staged) })
			before := forkUpdateExecutableHash(t, exe)
			u := &updater{exe: exe}
			u.self, err = os.Stat(exe)
			if err != nil {
				t.Fatal(err)
			}
			updates = u
			windows := &quitCount{}
			mux := http.NewServeMux()
			updateRoutes(mux, windows)
			post := func(path string) updateJSON {
				t.Helper()
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}")))
				var result updateJSON
				if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
					t.Fatalf("%s: %d %s", path, response.Code, response.Body)
				}
				return result
			}
			if got := post("/api/update/check"); got.State != "found" || got.Current != current || got.Latest != latest || got.Size != int64(len(payload)) || got.Notes == "" || got.URL != page {
				t.Fatalf("fork check: %+v", got)
			}
			u.checkByClock()
			if got := u.json(); got.State != "found" || downloads.Load() != 0 {
				t.Fatalf("checking downloaded without an action: %+v, downloads=%d", got, downloads.Load())
			}
			if _, err := os.Stat(staged); !os.IsNotExist(err) {
				t.Fatalf("check created a stage file: %v", err)
			}
			post("/api/update/install")
			var got updateJSON
			for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
				got = u.json()
				if got.State != "checking" && got.State != "downloading" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("download did not settle: %+v", got)
				}
			}
			if downloads.Load() != 1 || windows.quits.Load() != 0 || before != forkUpdateExecutableHash(t, exe) {
				t.Fatalf("download actions=%d, quits=%d; executable must stay intact", downloads.Load(), windows.quits.Load())
			}
			if corrupt {
				if got.State != "error" || !got.Retry || !strings.Contains(got.Error, "does not match its checksum") || u.staged != "" {
					t.Fatalf("corrupted fork download accepted: %+v", got)
				}
				if _, err := os.Stat(staged); !os.IsNotExist(err) {
					t.Fatalf("corrupted stage file retained: %v", err)
				}
			} else {
				if got.State != "ready" || got.Latest != latest || u.staged != staged {
					t.Fatalf("verified fork download not ready: %+v", got)
				}
				if body, err := os.ReadFile(staged); err != nil || !bytes.Equal(body, payload) {
					t.Fatalf("staged file differs from verified payload: %v", err)
				}
			}
		})
	}
}

func forkUpdateExecutableHash(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		t.Fatal(err)
	}
	return [sha256.Size]byte(hash.Sum(nil))
}
