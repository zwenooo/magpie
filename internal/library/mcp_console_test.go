package library

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpsConsolePageChecksIdentity(t *testing.T) {
	var mode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ping" {
			t.Errorf("unexpected probe %s", r.URL.Path)
		}
		switch mode {
		case "redirect":
			http.Redirect(w, r, "/logs", 302)
		case "other":
			io.WriteString(w, `{"service":"another-app"}`)
		default:
			io.WriteString(w, `{"service":"ops-mcp-config-console"}`)
		}
	}))
	defer srv.Close()
	for _, logs := range []bool{false, true} {
		got, err := checkedConsolePage(context.Background(), srv.URL, logs)
		want := srv.URL + "/"
		if logs {
			want += "logs"
		}
		if err != nil || got != want {
			t.Fatalf("got %s %v", got, err)
		}
	}
	for _, mode = range []string{"other", "redirect"} {
		if got, err := checkedConsolePage(context.Background(), srv.URL, false); err == nil || got != "" {
			t.Fatalf("accepted %s: %s", mode, got)
		}
	}
}

func TestOpsConsoleBaseUsesConfirmedSource(t *testing.T) {
	sandbox(t)
	t.Setenv("OPS_MCP_CONSOLE", "")
	t.Setenv("OPS_MCP_CONSOLE_PORT", "")
	s := bundleServer()
	s.RepoLocalHash = "here"
	s.RepoHash = "here"
	write(t, programDir(s.Name)+"/server.py", "fixture")
	if got := opsConsoleBase(&s); got != "http://127.0.0.1:8770" {
		t.Fatal(got)
	}
	s.Env["OPS_MCP_CONSOLE_PORT"] = "9101"
	if got := opsConsoleBase(&s); got != "http://127.0.0.1:9101" {
		t.Fatal(got)
	}
	s.Env["OPS_MCP_CONSOLE_PORT"] = "https://example.test/"
	if opsConsoleBase(&s) != "" {
		t.Fatal("accepted non-port")
	}
	delete(s.Env, "OPS_MCP_CONSOLE_PORT")
	s.Env["OPS_MCP_CONSOLE"] = "off"
	if opsConsoleBase(&s) != "" {
		t.Fatal("disabled console offered")
	}
	delete(s.Env, "OPS_MCP_CONSOLE")
	s.RepoLocalSource = nil
	if opsConsoleBase(&s) != "" {
		t.Fatal("missing receipt accepted")
	}
}
