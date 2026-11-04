package sessions

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func desktopCarryFixture(t *testing.T, imported bool, agent string) Session {
	t.Helper()
	src, _ := carrySetup(t)
	root := t.TempDir()
	projects := filepath.Join(root, "projects")
	if !imported {
		old := callDesktopDirs
		callDesktopDirs = func() []string { return []string{root} }
		t.Cleanup(func() { callDesktopDirs = old; Reset() })
		projects = filepath.Join(root, "local-agent-mode-sessions", "account", "org", "local_session", ".claude", "projects")
	}
	path := filepath.Join(projects, "project", carryClaude+".jsonl")
	lines := readLines(t, src)
	parent := ""
	for _, l := range lines {
		if l["type"] == "user" || l["type"] == "assistant" {
			parent, _ = l["uuid"].(string)
		}
	}
	image := jsonLine(o("parentUuid", parent, "uuid", "carry-image", "type", "user", "sessionId", carryClaude,
		"cwd", carryCwd, "timestamp", "2026-10-25T12:00:00Z", "message", o("role", "user", "content", []any{
			o("type", "text", "text", "Inspect this picture"),
			o("type", "image", "source", o("type", "base64", "media_type", "image/png", "data", "iVBORw0KGgo=")),
		})))
	writeLines(t, path, strings.TrimSpace(sum(t, src)), string(image))
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if imported {
		if _, err := ImportSource(SessionSource{Path: projects, Agent: agent}); err != nil {
			t.Fatal(err)
		}
	}
	Reset()
	s, ok := Find(agent, carryClaude)
	if !ok {
		t.Fatal("Desktop history not found")
	}
	return s
}

func TestCarryDesktopHistory(t *testing.T) {
	for _, kind := range []string{"native", "imported", "imported-claude"} {
		t.Run(kind, func(t *testing.T) {
			agent, targets := "claude-desktop", []string{"claude", "codex"}
			if kind == "imported-claude" {
				agent, targets = "claude", []string{"codex"}
			}
			s := desktopCarryFixture(t, kind != "native", agent)
			before := sum(t, s.Path)
			if len(s.Carry) != len(targets) {
				t.Fatalf("carry targets %+v, want %v", s.Carry, targets)
			}
			for _, to := range targets {
				if !slices.Contains(s.Carry, Carry{Agent: to, Convert: true}) {
					t.Fatalf("missing %s conversion: %+v", to, s.Carry)
				}
				r, err := CarryOver(s, to)
				if err != nil {
					t.Fatal(err)
				}
				if r.Agent != to || r.ID == s.ID || r.Path == s.Path || !strings.Contains(r.Command, r.ID) {
					t.Fatalf("not a new target session: %+v", r)
				}
				raw := sum(t, r.Path)
				for _, want := range []string{"My dog is called Biscuit", "wc -l calc.py", "6 calc.py", "[Write]", "[Read]", "[Edit]", "Inspect this picture", "iVBORw0KGgo="} {
					if !strings.Contains(raw, want) {
						t.Errorf("%s conversion lost %q", to, want)
					}
				}
				for _, unwanted := range []string{"The user wants me to", `"thinking"`, `"signature"`, `"reasoning"`} {
					if strings.Contains(raw, unwanted) {
						t.Errorf("%s conversion retained %q", to, unwanted)
					}
				}
				Reset()
				converted, ok := Find(to, r.ID)
				if !ok || converted.Cwd != s.Cwd || len(converted.Models) != 0 || converted.Cost != 0 {
					t.Fatalf("target is not a zero-usage resumed session: %+v, %v", converted, ok)
				}
				if to == "claude" && !strings.Contains(raw, `"name":"Bash"`) || to == "codex" && !strings.Contains(raw, `"name":"exec_command"`) {
					t.Fatal("shell call not converted to target's tool")
				}
				if again, err := CarryOver(s, to); err != nil || again != r {
					t.Fatalf("repeat made another session: %+v %v", again, err)
				}
			}
			if sum(t, s.Path) != before {
				t.Fatal("source history changed")
			}
			if kind != "native" {
				if !s.ReadOnly || s.Resume != "" {
					t.Fatalf("imported original gained direct resume: %+v", s)
				}
				if _, err := Delete(s.Agent, s.ID); err == nil {
					t.Fatal("imported original became deletable")
				}
			}
		})
	}
}

func TestCarryDesktopRestrictions(t *testing.T) {
	s := desktopCarryFixture(t, true, "claude-desktop")
	for _, change := range []func(*Session){
		func(s *Session) { s.Gateway = true },
		func(s *Session) { s.WSL = "Ubuntu" },
		func(s *Session) { s.Cwd = "" },
		func(s *Session) { s.Path = "" },
		func(s *Session) { s.Path = "/some/session.db" },
		func(s *Session) { s.Agent = "pi" },
		func(s *Session) { s.Agent = "codex" },
	} {
		blocked := s
		change(&blocked)
		if got := carries(blocked); len(got) != 0 {
			t.Errorf("unsafe source offered: %+v -> %+v", blocked, got)
		}
		if _, err := CarryOver(blocked, "claude"); err == nil {
			t.Error("unsafe conversion accepted")
		}
	}
	agentThere = func(a string) bool { return a == "claude" }
	if got := carries(s); len(got) != 1 || got[0].Agent != "claude" {
		t.Fatalf("uninstalled target offered: %+v", got)
	}
	if _, err := CarryOver(s, "codex"); err == nil {
		t.Fatal("uninstalled target accepted")
	}
	if err := SetReads(nil, map[string]bool{"claude-desktop": false}); err != nil {
		t.Fatal(err)
	}
	if _, err := CarryOver(s, "claude"); !errors.Is(err, ErrNotRead) {
		t.Fatalf("source read consent bypassed: %v", err)
	}
}

func TestCarryIntoImportedRootIsRefused(t *testing.T) {
	for _, to := range []string{"claude", "codex"} {
		t.Run(to, func(t *testing.T) {
			s := desktopCarryFixture(t, true, "claude-desktop")
			root := filepath.Join(ClaudeDir(), "projects")
			if to == "codex" {
				root = filepath.Join(CodexDir(), "sessions")
			}
			// A custom projects tree may live inside the target store too.
			writeLines(t, filepath.Join(root, "external-project", sourceTestID+".jsonl"), strings.TrimSpace(sum(t, s.Path)))
			if _, err := ImportSource(SessionSource{Path: root, Agent: "claude-desktop"}); err != nil {
				t.Fatal(err)
			}
			before := carryFilesUnder(t, root)
			if _, err := CarryOver(s, to); err == nil || !strings.Contains(err.Error(), "read only") {
				t.Fatalf("write into imported %s target: %v", to, err)
			}
			if got := carryFilesUnder(t, root); got != before {
				t.Fatal("refused target still changed")
			}
		})
	}
}

func carryFilesUnder(t *testing.T, root string) string {
	t.Helper()
	var paths []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return strings.Join(paths, "\n")
}

func TestCarryCachedTargetCannotResumeImportedOriginal(t *testing.T) {
	s := desktopCarryFixture(t, true, "claude-desktop")
	r, err := CarryOver(s, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ImportSource(SessionSource{Path: filepath.Dir(r.Path), Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	before := sum(t, r.Path)
	if _, err := CarryOver(s, "claude"); err == nil || !strings.Contains(err.Error(), "read only") {
		t.Fatalf("cached copy now imported gained resume: %v", err)
	}
	if sum(t, r.Path) != before {
		t.Fatal("cached target changed")
	}
}

func TestCarryCachedTargetFollowsHome(t *testing.T) {
	for _, to := range []string{"claude", "codex"} {
		t.Run(to, func(t *testing.T) {
			s := desktopCarryFixture(t, true, "claude-desktop")
			first, err := CarryOver(s, to)
			if err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			if to == "claude" {
				t.Setenv("CLAUDE_CONFIG_DIR", home)
			} else {
				t.Setenv("CODEX_HOME", home)
			}
			second, err := CarryOver(s, to)
			if err != nil || second.ID == first.ID || !sourceWithin(home, second.Path) {
				t.Fatalf("old target home reused: first %+v, second %+v, %v", first, second, err)
			}
		})
	}
}

func TestCarryTargetGuardPhysicalAndUnknown(t *testing.T) {
	s := desktopCarryFixture(t, true, "claude-desktop")
	real, err := sourceRealPath(filepath.Dir(s.Path))
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(real, alias); err == nil {
		path := filepath.Join(alias, "new-child", "fresh.jsonl")
		if err := writeNew(path, [][]byte{[]byte(`{}`)}); err == nil || !strings.Contains(err.Error(), "read only") {
			t.Fatalf("physical alias accepted: %v", err)
		}
		if _, err := os.Stat(filepath.Join(real, "new-child")); !os.IsNotExist(err) {
			t.Fatal("created inside read-only source")
		}
	}
	if err := os.WriteFile(sourcesPath(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CarryOver(s, "claude"); err == nil || !strings.Contains(err.Error(), "cannot verify") {
		t.Fatalf("unreadable registry allowed output: %v", err)
	}
}

func TestCarryAgentAvailabilityFollowsHome(t *testing.T) {
	setup(t)
	thereMu.Lock()
	thereAt = map[string]there{}
	thereMu.Unlock()
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CODEX_HOME", first)
	if err := os.Mkdir(first, 0o700); err != nil {
		t.Fatal(err)
	}
	if !agentThere("codex") {
		t.Fatal("installed target folder not seen")
	}
	t.Setenv("CODEX_HOME", second)
	if agentThere("codex") {
		t.Fatal("target availability reused another home's installed folder")
	}
	t.Cleanup(func() {
		thereMu.Lock()
		thereAt = map[string]there{}
		thereMu.Unlock()
	})
}
