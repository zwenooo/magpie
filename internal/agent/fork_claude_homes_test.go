package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// ReClaude's account folders and a folder added by hand are claude# rows as
// ~/.claude-work is, each once; magpie's saved accounts, which the gateway
// runs Claude Code in, are not.
func TestAccountSwitcherClaudeFoldersAreRows(t *testing.T) {
	home, _ := claudeDottedHome(t, provider.Provider{ID: "relay", Name: "Relay", Anthropic: "https://example.test", Key: "k",
		Models: []string{"claude-sonnet-4.5"}}, `{}`)
	t.Setenv("PATH", t.TempDir())
	work := filepath.Join(home, ".claude-work")
	writeFile(t, filepath.Join(work, "settings.json"), `{}`)
	rec := filepath.Join(home, ".rec", "claude", "accounts", "48142", "config")
	writeFile(t, filepath.Join(rec, "settings.json"), `{}`)
	// an account ReClaude made that Claude Code never ran in
	writeFile(t, filepath.Join(home, ".rec", "claude", "accounts", "50000", "config", "notes.txt"), `x`)
	byHand := filepath.Join(home, "work", "Team Config")
	writeFile(t, filepath.Join(byHand, ".credentials.json"), `{}`)
	saved := filepath.Join(home, ".config", "magpie", "claude-accounts", "0123456789abcdef")
	writeFile(t, filepath.Join(saved, "settings.json"), `{}`)
	list, _ := json.Marshal(map[string][]string{"dirs": {byHand, work, rec}})
	writeFile(t, ClaudeDirsFile(), string(list))

	got := map[string]string{}
	for _, a := range claudeDirAgents(home) {
		if _, twice := got[a.ID]; twice {
			t.Fatalf("%s listed twice", a.ID)
		}
		got[a.ID] = a.Dir
	}
	want := map[string]string{"claude#work": work, "claude#rec-48142": rec, "claude#team-config": byHand}
	if len(got) != len(want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
	for id, dir := range want {
		if !sameFolder(got[id], dir) {
			t.Fatalf("row %s at %q, want %q (rows %v)", id, got[id], dir, got)
		}
	}
	for dir, follows := range map[string]bool{rec: true, byHand: true, work: true, saved: false, filepath.Join(home, ".claude"): false} {
		if AccountClaudeDir(dir) != follows {
			t.Errorf("AccountClaudeDir(%s) = %v, want %v", dir, !follows, follows)
		}
	}
	// ~/.claude-work is only an account folder for being added by hand
	list, _ = json.Marshal(map[string][]string{"dirs": {byHand}})
	writeFile(t, ClaudeDirsFile(), string(list))
	if AccountClaudeDir(work) {
		t.Fatal("~/.claude-work follows without being added by hand")
	}
	// an unreadable list is no folders by hand, not an error
	if err := os.WriteFile(ClaudeDirsFile(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, a := range claudeDirAgents(home) {
		if a.ID == "claude#team-config" {
			t.Fatal("a folder from an unreadable list became a row")
		}
	}
}
