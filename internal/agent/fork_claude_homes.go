package agent

// Claude Code folders an account switcher keeps, zwenooo/magpie fork:
// ReClaude's ~/.rec/claude/accounts/<id>/config, and folders the user adds
// by hand (`magpie library claude-dirs add`, kept in claude-dirs.json in
// magpie's folder). Each is a claude#<name> row of its own, as upstream's
// ~/.claude-work is (claudeDirAgents), and the Library gives it what it
// gives Claude Code unless the user turns that off
// (internal/library/profile_follow.go). magpie's own saved accounts
// (claude-accounts) are not rows: the gateway runs Claude Code there.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yetone/magpie/internal/appdir"
)

// ClaudeDirsFile lists the Claude Code folders added by hand.
func ClaudeDirsFile() string { return filepath.Join(appdir.Config(), "claude-dirs.json") }

// HandAddedClaudeDirs are the Claude Code folders added by hand, as
// written. None when the list can't be read: those rows are left out
// until it can, and nothing is written to them meanwhile.
func HandAddedClaudeDirs() []string {
	b, err := os.ReadFile(ClaudeDirsFile())
	if err != nil {
		return nil
	}
	var st struct {
		Dirs []string `json:"dirs"`
	}
	if json.Unmarshal(b, &st) != nil {
		return nil
	}
	return st.Dirs
}

type accountDir struct{ name, dir string }

// accountClaudeDirs are ReClaude's account folders, then those added by
// hand, each with the name of its row.
func accountClaudeDirs(home string) []accountDir {
	var out []accountDir
	found, _ := filepath.Glob(filepath.Join(home, ".rec", "claude", "accounts", "*", "config"))
	sort.Strings(found)
	for _, d := range found {
		out = append(out, accountDir{"rec-" + profilePart(filepath.Base(filepath.Dir(d))), d})
	}
	for _, d := range HandAddedClaudeDirs() {
		d = filepath.Clean(d)
		base := filepath.Base(d)
		if strings.EqualFold(base, "config") {
			base = filepath.Base(filepath.Dir(d))
		}
		out = append(out, accountDir{profilePart(base), d})
	}
	return out
}

// profilePart is s as a row's name after "claude#": letters, digits, - and _.
func profilePart(s string) string {
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, strings.ToLower(s))
	if s = strings.Trim(s, "-_"); s == "" {
		return "folder"
	}
	return s
}

// addAccountClaudeDirs puts the account switchers' folders and those added
// by hand among claudeDirAgents' (byName, names), each under a name no other
// folder has; one that is Claude Code's own folder, a folder found already
// or no Claude Code's is left out.
func addAccountClaudeDirs(home, main string, byName map[string]string, names []string) []string {
	def := filepath.Join(home, ".claude")
	for _, d := range accountClaudeDirs(home) {
		if sameFolder(d.dir, main) || sameFolder(d.dir, def) || !claudeDirIsClaude(d.dir) {
			continue
		}
		dup := false
		for _, have := range byName {
			dup = dup || sameFolder(have, d.dir)
		}
		if dup {
			continue
		}
		name := d.name
		for i := 2; byName[name] != "" || name == "default"; i++ {
			name = fmt.Sprintf("%s-%d", d.name, i)
		}
		byName[name] = d.dir
		names = append(names, name)
	}
	return names
}

// IsClaudeConfigDir says dir holds Claude Code's own files, as a row's
// folder must.
func IsClaudeConfigDir(dir string) bool { return claudeDirIsClaude(dir) }

// AccountClaudeDir says dir is an account switcher's Claude Code folder or
// one added by hand: its row follows Claude Code's Library by default.
func AccountClaudeDir(dir string) bool {
	home, _ := os.UserHomeDir()
	for _, d := range accountClaudeDirs(home) {
		if sameFolder(d.dir, dir) {
			return true
		}
	}
	return false
}
