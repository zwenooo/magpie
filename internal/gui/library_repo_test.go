package gui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLibraryRepoRequiresSourceAndCommit(t *testing.T) {
	sandboxHome(t)
	mux := http.NewServeMux()
	libraryRoutes(mux, nil)
	post := func(action, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/library/servers/"+action, strings.NewReader(body)))
		return rec
	}
	if rec := post("save", `{"server":{"name":"kept","transport":"stdio","command":"node","args":["{{program}}/main.js"],"agents":[]}}`); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	// Even a client supplying a toolkit source cannot turn an unbound entry
	// into a credential target. Neither operation should return any file data.
	for _, action := range []string{"bundle-preview", "bundle-restore"} {
		rec := post(action, `{"name":"kept","repoSource":{"repo":"https://github.com/zwenooo/magpie-agent-toolkit","path":"mcp/ops-mcp"},"expected":"invented"}`)
		if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"state":"unsupported"}` {
			t.Errorf("%s: %d %s; want only unsupported status", action, rec.Code, rec.Body)
		}
	}
	for _, tc := range []struct{ action, body, reason string }{
		{"bundle-restore", `{"name":"kept"}`, "credential status not confirmed"},
		{"managed-verify", `{"name":"kept"}`, "local process probe not confirmed"},
		{"repo-check", `{"name":"kept"}`, "no repository source"},
		{"repo-check", `{"name":"kept","repoSource":{"repo":"not a repository"}}`, "GitHub repository URL"},
		{"repo-update", `{"name":"kept","repoSource":{"repo":"https://github.com/example/project"},"expected":"abc"}`, "commit SHA"},
		{"repo-check", `{"name":"kept"}`, "no repository source"},
	} {
		rec := post(tc.action, tc.body)
		if rec.Code < 400 || !strings.Contains(rec.Body.String(), tc.reason) {
			t.Errorf("%s: %d %s; want rejection with %q", tc.action, rec.Code, rec.Body, tc.reason)
		}
	}
}
