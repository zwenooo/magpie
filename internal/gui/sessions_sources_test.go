package gui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/sessions"
	"github.com/yetone/magpie/internal/settings"
)

func TestSessionSourceRoutes(t *testing.T) {
	h := sandboxHome(t)
	root := filepath.Join(h, "account", "config", "projects")
	file := filepath.Join(root, "project", "11111111-2222-4333-8444-555555555555.jsonl")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"type":"user","uuid":"u1","sessionId":"11111111-2222-4333-8444-555555555555","cwd":"/work/app","timestamp":"2026-09-20T10:00:00Z","message":{"role":"user","content":"Find the custom history"}}` + "\n" +
		`{"type":"assistant","uuid":"a1","sessionId":"11111111-2222-4333-8444-555555555555","cwd":"/work/app","timestamp":"2026-09-20T10:00:01Z","message":{"role":"assistant","model":"claude-sonnet-5","content":[{"type":"text","text":"Found the conversation."}],"usage":{"input_tokens":10,"output_tokens":4}}}` + "\n")
	if err := os.WriteFile(file, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := settings.SetSessionReads(func(*settings.SessionReads) *settings.SessionReads {
		return &settings.SessionReads{Asked: true, Agents: map[string]bool{"claude-desktop": true}}
	}); err != nil {
		t.Fatal(err)
	}
	sessions.Reset()
	forgetStats()
	t.Cleanup(func() { sessions.Reset(); forgetStats() })
	mux := http.NewServeMux()
	sessionManageRoutes(mux, folderOnly{})
	sessionRoutes(mux, folderOnly{})
	call := func(method, path string, in any, status int) []byte {
		t.Helper()
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(b)))
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		return w.Body.Bytes()
	}
	source := sessions.SessionSource{Path: root, Agent: "claude-desktop"}
	var preview sessions.SourceScan
	if err := json.Unmarshal(call("POST", "/api/sessions/scan", source, 200), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Count != 1 || len(preview.Sessions) != 1 || preview.Sessions[0].ID != "11111111-2222-4333-8444-555555555555" {
		t.Fatalf("scan = %+v", preview)
	}
	var sources struct {
		Sources []sessions.SessionSource `json:"sources"`
	}
	if got := call("GET", "/api/sessions/sources", nil, 200); json.Unmarshal(got, &sources) != nil || sources.Sources == nil || len(sources.Sources) != 0 {
		t.Fatalf("scan registered the directory: %s", got)
	}
	if got := sessions.ListAgent("claude-desktop"); len(got) != 0 {
		t.Fatalf("preview imported sessions: %+v", got)
	}
	call("POST", "/api/sessions/import", source, 200)
	call("POST", "/api/sessions/import", source, 200)
	json.Unmarshal(call("GET", "/api/sessions/sources", nil, 200), &sources)
	if len(sources.Sources) != 1 || sources.Sources[0] != preview.Source {
		t.Fatalf("import not idempotent: %+v", sources)
	}
	original, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	registered, err := os.Stat(sources.Sources[0].Path)
	if err != nil || !os.SameFile(original, registered) {
		t.Fatalf("registered source is not the scanned directory: %v", err)
	}
	var managed struct {
		Agent    string             `json:"agent"`
		Sessions []sessions.Managed `json:"sessions"`
	}
	json.Unmarshal(call("GET", "/api/sessions/manage?agent=claude-desktop", nil, 200), &managed)
	if managed.Agent != "claude-desktop" || len(managed.Sessions) != 1 || managed.Sessions[0].Deletable {
		t.Fatalf("import not listed as read-only Desktop: %+v", managed)
	}
	transcript := call("GET", "/api/sessions/transcript?agent=claude-desktop&id=11111111-2222-4333-8444-555555555555", nil, 200)
	if !strings.Contains(string(transcript), "Found the conversation.") {
		t.Fatalf("imported conversation missing: %s", transcript)
	}
	call("POST", "/api/sessions/reads", map[string]any{"agents": map[string]bool{"claude-desktop": false}}, 200)
	call("POST", "/api/sessions/scan", source, 400)
	call("POST", "/api/sessions/import", source, 400)
	call("GET", "/api/sessions/transcript?agent=claude-desktop&id=11111111-2222-4333-8444-555555555555", nil, 400)
	call("POST", "/api/sessions/sources/remove", sources.Sources[0], 200)
	if got := call("GET", "/api/sessions/sources", nil, 200); json.Unmarshal(got, &sources) != nil || len(sources.Sources) != 0 {
		t.Fatalf("removed source remains registered: %s", got)
	}
	if got := sessions.ListAgent("claude-desktop"); len(got) != 0 {
		t.Fatalf("removed source remains listed: %+v", got)
	}
	if after, err := os.ReadFile(file); err != nil || !bytes.Equal(after, body) {
		t.Fatalf("original conversation changed: %v", err)
	}
}

func TestSessionSourceRoutesRejectInvalidRequests(t *testing.T) {
	sandboxHome(t)
	mux := http.NewServeMux()
	sessionSourceRoutes(mux)
	for _, endpoint := range []string{"scan", "import", "sources/remove"} {
		for _, body := range []string{"{", `{"path":"relative","agent":"claude-desktop"}`, `{"path":"/missing","agent":"unknown"}`, strings.Repeat(" ", 16385) + "{}"} {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/sessions/"+endpoint, strings.NewReader(body)))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s accepted invalid request: %d %s", endpoint, w.Code, w.Body)
			}
		}
	}
}
