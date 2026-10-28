package stats

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestForkSendOmitsDisabledPartnerCounts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("MAGPIE_PARTNERS", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("MAGPIE_NO_STATS", "")
	wasUsage := usage
	usage = func() Usage { return Usage{} }
	t.Cleanup(func() { usage = wasUsage })
	counts := []byte(`{"days":{"2026-09-27":{"acme":{"shown":12,"opened":2}}}}`)
	path := filepath.Join(home, ".cache", "magpie", "partner-counts.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, counts, 0o600); err != nil {
		t.Fatal(err)
	}
	batches := make(chan []map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Batch []map[string]any `json:"batch"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		batches <- body.Batch
	}))
	t.Cleanup(server.Close)
	t.Setenv("MAGPIE_STATS_HOST", server.URL)
	if err := Send(context.Background(), "0.1.300", "app", time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	select {
	case batch := <-batches:
		if len(batch) != 1 || batch[0]["event"] != "magpie active" {
			t.Fatalf("default fork sent partner events: %v", batch)
		}
	default:
		t.Fatal("test did not exercise a real stats send")
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, counts) {
		t.Fatalf("sending normal stats changed disabled partner counts: %v", err)
	}
}
