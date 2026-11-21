package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/library"
)

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
	after, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("repository commands changed an unbound server: %v", err)
	}
}
