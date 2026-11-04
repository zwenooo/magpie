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
)

func TestSessionSourceDesktopCarryRoutes(t *testing.T) {
	h := sandboxHome(t)
	for variable, dir := range map[string]string{
		"CLAUDE_CONFIG_DIR": filepath.Join(h, "claude-target"),
		"CODEX_HOME":        filepath.Join(h, "codex-target"),
	} {
		t.Setenv(variable, dir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := sessions.SetReads(nil, map[string]bool{"claude-desktop": true, "claude": true, "codex": true}); err != nil {
		t.Fatal(err)
	}
	const id = "11111111-2222-4333-8444-555555555555"
	root := filepath.Join(h, "external-account", "projects")
	file := filepath.Join(root, "project", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-10-25T10:00:00Z","message":{"role":"user","content":"Continue this Desktop conversation"}}` + "\n" +
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-10-25T10:00:01Z","message":{"role":"assistant","model":"claude-sonnet-5","content":[{"type":"text","text":"The earlier plan stays available."}]}}` + "\n")
	if err := os.WriteFile(file, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.ImportSource(sessions.SessionSource{Path: root, Agent: "claude-desktop"}); err != nil {
		t.Fatal(err)
	}
	sessions.Reset()
	forgetStats()
	t.Cleanup(func() { sessions.Reset(); forgetStats() })
	mux := http.NewServeMux()
	sessionManageRoutes(mux, folderOnly{})
	sessionRoutes(mux, folderOnly{})
	call := func(method, path string, in any, want int) []byte {
		t.Helper()
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(b)))
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		return w.Body.Bytes()
	}
	var managed struct {
		Sessions []sessions.Managed `json:"sessions"`
	}
	if err := json.Unmarshal(call("GET", "/api/sessions/manage?agent=claude-desktop", nil, 200), &managed); err != nil {
		t.Fatal(err)
	}
	if len(managed.Sessions) != 1 {
		t.Fatalf("source sessions: %+v", managed.Sessions)
	}
	s := managed.Sessions[0]
	if !s.ReadOnly || s.Deletable || s.Resume != "" || len(s.Carry) != 2 {
		t.Fatalf("read-only source must offer two copy conversions: %+v", s)
	}
	for _, target := range []string{"claude", "codex"} {
		in := map[string]any{"agent": "claude-desktop", "id": id, "to": target, "open": false}
		var result struct{ Agent, ID, Command string }
		encoded := call("POST", "/api/sessions/carry", in, 200)
		if err := json.Unmarshal(encoded, &result); err != nil {
			t.Fatal(err)
		}
		if result.Agent != target || result.ID == "" || result.ID == id || !strings.Contains(result.Command, result.ID) {
			t.Fatalf("conversion result: %+v", result)
		}
		if again := call("POST", "/api/sessions/carry", in, 200); !bytes.Equal(again, encoded) {
			t.Fatalf("repeat conversion changed: %s -> %s", encoded, again)
		}
		sessions.Reset()
		copied, ok := sessions.Find(target, result.ID)
		if !ok || copied.ReadOnly || copied.Path == file || copied.Resume == "" {
			t.Fatalf("target not resumable: %+v, found=%v", copied, ok)
		}
		tr, err := sessions.TranscriptOf(copied)
		if err != nil {
			t.Fatal(err)
		}
		text, err := json.Marshal(tr)
		if err != nil || !bytes.Contains(text, []byte("Continue this Desktop conversation")) || !bytes.Contains(text, []byte("The earlier plan stays available.")) {
			t.Fatalf("target lost conversation: %s, %v", text, err)
		}
	}
	call("POST", "/api/sessions/carry", map[string]string{"agent": "claude-desktop", "id": id, "to": "omp"}, 400)
	if err := sessions.SetReads(nil, map[string]bool{"claude-desktop": false}); err != nil {
		t.Fatal(err)
	}
	call("POST", "/api/sessions/carry", map[string]string{"agent": "claude-desktop", "id": id, "to": "codex"}, 400)
	if after, err := os.ReadFile(file); err != nil || !bytes.Equal(after, body) {
		t.Fatalf("source history changed: %v", err)
	}
}
