package library

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/mcpauth"
)

const webSite = "https://docs.example/guide"
const webInitFixture = `{"protocolVersion":"2025-06-18","serverInfo":{"name":"fake","version":"1","websiteUrl":"` + webSite + `"},"_meta":{"io.github.zwenooo.magpie/web-entries":{"console":"https://service.example:443/admin","logs":"http://127.0.0.1:8787/logs"}}}`

func TestWebEntryPolicy(t *testing.T) {
	base, _ := url.Parse("https://service.example/mcp?session=opaque")
	for _, tc := range []struct {
		raw, kind     string
		local, remote bool
	}{
		{"http://localhost:8888/logs", "logs", true, false},
		{"https://LOCALHOST./app", "website", true, false},
		{"http://127.3.2.1/app", "console", true, false},
		{"http://[::1]/", "website", true, false},
		{"https://[::ffff:127.0.0.1]/", "website", true, false},
		{webSite, "website", true, true},
		{webSite, "console", true, false},
		{"https://SERVICE.example:443/logs", "logs", true, true},
		{"https://service.example:444/logs", "logs", true, false},
		{"https://service.example.evil.example/logs", "logs", true, false},
		{"http://service.example/admin", "console", false, false},
	} {
		if allowWebEntry(tc.raw, tc.kind, true, nil) != tc.local || allowWebEntry(tc.raw, tc.kind, false, base) != tc.remote {
			t.Errorf("wrong local or remote policy for %s %q", tc.kind, tc.raw)
		}
	}
	for _, raw := range []string{"", "/logs", "//docs.example/logs", "javascript:alert(1)", "file:///a", "data:text/plain,a", "https:a", "https://", "https://u:p@docs.example", "https://@docs.example", "https://docs.example/?q=s", "https://docs.example/?", "https://docs.example/#", "https://docs.example/#frag", "https://docs.example/\nx", "https://docs.example/%0d", "https://docs.example/x\x7f", `https://docs.example\@localhost`, "https://:80/logs", "https://docs.example:65536", "https://docs.example:", "https://127.1", "https://2130706433", "https://0177.0.0.1", "https://0x7f000001", "https://１２７.０.０.１", "https://x.localhost", "https://[127.0.0.1]", "https://0.0.0.0", "https://[::]"} {
		if allowWebEntry(raw, "website", true, nil) || allowWebEntry(raw, "website", false, base) {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, origin := range []string{"http://service.example", "https://service.example:444", "https://other.example"} {
		u, _ := url.Parse(origin)
		if allowWebEntry("https://service.example/logs", "logs", false, u) {
			t.Errorf("logs accepted for origin %s", origin)
		}
	}
}

func TestWebEntriesStdio(t *testing.T) {
	s := fakeStdio(t, "web", nil)
	h := CheckServer(t.Context(), s)
	want := []MCPWebEntry{{"website", webSite}, {"console", "https://service.example:443/admin"}, {"logs", "http://127.0.0.1:8787/logs"}}
	if h.State != "ok" || !slices.Equal(h.WebEntries, want) {
		t.Fatalf("stdio: %+v", h)
	}
	// Exercise the launch-side decision without asking WSL or running a wrapper.
	for _, tc := range []struct{ command, distro string }{{"node", "Debian"}, {`C:\\Windows\\System32\\wsl.exe`, ""}, {"ssh", ""}} {
		s.Command = tc.command
		got := (ServerHealth{State: "ok"}).withWebEntries([]byte(webInitFixture), s, tc.distro)
		if !slices.Equal(got.WebEntries, want[:1]) {
			t.Errorf("%+v: %+v", tc, got.WebEntries)
		}
	}
}

func TestWebEntriesExactMetadata(t *testing.T) {
	s := &Server{Transport: "stdio"}
	for _, raw := range []string{`{}`, `{"serverInfo":{"websiteURL":"https://a.example"}}`, `{"_meta":{"console":"https://a.example"}}`, `{"_meta":{"io.github.zwenooo.magpie/web-entries":["https://a.example"]}}`, `{"capabilities":` + webInitFixture + `}`} {
		h := (ServerHealth{State: "ok"}).withWebEntries([]byte(raw), s, "")
		b, err := json.Marshal(h)
		if err != nil || strings.Contains(string(b), "webEntries") {
			t.Errorf("unexpected JSON %s (%v)", b, err)
		}
	}
	raw := `{"serverInfo":{"websiteUrl":123},"_meta":{"io.github.zwenooo.magpie/web-entries":{"console":"https://a.example","logs":{"url":"https://b.example"}}}}`
	h := (ServerHealth{State: "ok"}).withWebEntries([]byte(raw), s, "")
	b, _ := json.Marshal(h.WebEntries)
	if string(b) != `[{"kind":"console","url":"https://a.example"}]` {
		t.Fatalf("entries: %s", b)
	}
}

type webCheckTransport func(*http.Request) (*http.Response, error)

func (f webCheckTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWebEntriesHTTP(t *testing.T) {
	sandbox(t)
	s := &Server{Name: "web", Transport: "http", URL: "https://service.example/mcp", Agents: []string{}}
	ok(t)(SaveServer("", *s))
	old := checkClient
	t.Cleanup(func() { checkClient = old; checked.Lock(); delete(checked.m, s.Name); checked.Unlock() })
	var methods []string
	mode := "ok"
	checkClient = &http.Client{Transport: webCheckTransport(func(r *http.Request) (*http.Response, error) {
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		if r.URL.String() != s.URL || r.Method != "POST" {
			return nil, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		var req rpcMsg
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}
		methods = append(methods, req.Method)
		body := ""
		switch req.Method {
		case "initialize":
			id := "1"
			if mode == "wrong id" {
				id = "3"
			}
			body = `{"jsonrpc":"2.0","id":` + id + `,"result":` + webInitFixture + `}`
		case "notifications/initialized":
		case "tools/list":
			body = `{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}`
			if mode == "failed" {
				body = `{"jsonrpc":"2.0","id":2,"error":{"code":-1,"message":"unavailable"}}`
			}
		default:
			return nil, fmt.Errorf("unexpected method %q", req.Method)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	check := func(ctx context.Context, fresh bool) ServerHealth {
		t.Helper()
		hs, err := CheckServers(ctx, []string{s.Name}, fresh)
		if err != nil {
			t.Fatal(err)
		}
		return hs[s.Name]
	}
	// The first OAuth status read takes an empty mcp-signins.json.lock on
	// Windows. Establish it before any server has advertised a URL.
	mcpauth.StatusOf(s.Name, s.URL)
	before := snapshot(t, os.Getenv("HOME"))
	want := []MCPWebEntry{{"website", webSite}, {"console", "https://service.example:443/admin"}}
	if h := check(t.Context(), true); h.State != "ok" || !slices.Equal(h.WebEntries, want) {
		t.Fatalf("HTTP: %+v", h)
	}
	if !slices.Equal(methods, []string{"initialize", "notifications/initialized", "tools/list"}) {
		t.Fatalf("requests: %v", methods)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if h := check(canceled, true); h.State != "error" || len(h.WebEntries) != 0 {
		t.Fatalf("canceled check: %+v", h)
	}
	mode = "failed"
	// Even a non-fresh read must not resurrect the success from before cancellation.
	for _, fresh := range []bool{false, true} {
		if h := check(t.Context(), fresh); h.State != "error" || len(h.WebEntries) != 0 {
			t.Fatalf("after tools/list failure: %+v", h)
		}
	}
	mode = "wrong id"
	if h := check(t.Context(), true); h.State != "error" || len(h.WebEntries) != 0 {
		t.Fatalf("unmatched initialize: %+v", h)
	}
	if after := snapshot(t, os.Getenv("HOME")); before != after {
		t.Errorf("health check changed sandbox:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
