package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/library"
)

func TestMaintenanceCLIContract(t *testing.T) {
	h := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", h)
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	root := filepath.Join(library.Dir(), "skills", "notes")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(library.Dir()), "library.json"), []byte(`{"skills":[{"name":"notes"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(pass bool, args ...string) map[string]any {
		t.Helper()
		b, err := stdoutOf(t, func() error { return libraryCmd(append([]string{"library"}, args...)) })
		var v map[string]any
		if e := json.Unmarshal([]byte(b), &v); e != nil || (err == nil) != pass || (!pass && (v["error"] == nil || v["writePath"] != nil)) {
			t.Fatalf("%v: %q %v", args, b, err)
		}
		return v
	}
	run(false, "where", "skill", "notes")
	run(false, "maintain", "skill", "notes", "checkout")
	run(false, "maintain", "skill", "notes", "unknown")
	run(true, "maintain", "skill", "notes", "library")
	run(false, "where", "skill", "notes", "")
	run(false, "where", "skill", "notes", ".git/config")
	v := run(true, "where", "skill", "notes", "new.md")
	if v["mode"] != "library" || v["kind"] != "skill" || v["repoSource"] != nil || v["writePath"] != filepath.Join(v["maintenancePath"].(string), "new.md") {
		t.Fatal(v)
	}
}

func TestMCPRepoCommandsLeavePlainServerAlone(t *testing.T) {
	home := t.TempDir()
	for key, value := range map[string]string{"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"), "APPDATA": "", "LOCALAPPDATA": "", "CLAUDE_CONFIG_DIR": "", "CODEX_HOME": ""} {
		t.Setenv(key, value)
	}
	if _, err := library.SaveServer("", library.Server{Name: "notes", Transport: "stdio", Command: "notes"}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(filepath.Dir(library.Dir()), "library.json")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := libraryCmd([]string{"library", "mcp", "repo-check", "notes"}); err == nil || strings.Contains(err.Error(), "usage:") {
		t.Fatalf("checking a server without a repository: %v", err)
	}
	if err := libraryCmd([]string{"library", "mcp", "repo-update", "notes"}); err == nil {
		t.Fatal("updated without a reviewed commit")
	}
	for _, command := range [][]string{{"verify-local", "notes"}, {"bundle-restore", "notes", "bogus"}} {
		if err := libraryCmd(append([]string{"library", "mcp"}, command...)); err == nil {
			t.Fatalf("plain server accepted %s", command[0])
		}
	}
	after, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("repository commands changed an unbound server: %v", err)
	}
}
