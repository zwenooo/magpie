package sessions

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const resumeSourceID = "22222222-bbbb-4444-8888-999999999999"

// resumeSourceFixture writes one Claude transcript at
// <projects>/project/<id>.jsonl that ran in cwd, and imports projects.
func resumeSourceFixture(t *testing.T, projects, cwd string) string {
	t.Helper()
	at, err := json.Marshal(cwd)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projects, "project", resumeSourceID+".jsonl")
	writeLines(t, path,
		`{"type":"user","timestamp":"2026-11-05T10:00:00Z","message":{"role":"user","content":"go on from here"},"cwd":`+string(at)+`,"sessionId":"`+resumeSourceID+`"}`,
		claudeMsg("resume-response", "claude-opus-5-5", 100, 12, 0, 0, 1))
	if _, err := ImportSource(SessionSource{Path: projects, Agent: "claude-desktop"}); err != nil {
		t.Fatal(err)
	}
	Reset()
	return path
}

func listedSource(t *testing.T) Managed {
	t.Helper()
	for _, s := range ListAgent("claude-desktop") {
		if s.ID == resumeSourceID {
			return s
		}
	}
	t.Fatal("imported session not listed")
	return Managed{}
}

// An imported Claude config folder's session resumes in Claude Code from
// that folder, and stays one magpie neither deletes nor moves.
func TestSourceResumeInItsConfigFolder(t *testing.T) {
	setup(t)
	config := filepath.Join(t.TempDir(), "account", "config")
	cwd := t.TempDir()
	path := resumeSourceFixture(t, filepath.Join(config, "projects"), cwd)
	before, _ := os.ReadFile(path)
	s := listedSource(t)
	for _, want := range []string{"claude --resume " + resumeSourceID, "CLAUDE_CONFIG_DIR", config, cwd} {
		if !strings.Contains(s.Resume, want) {
			t.Fatalf("resume %q lacks %q", s.Resume, want)
		}
	}
	if found, ok := Find("claude-desktop", resumeSourceID); !ok || found.Resume != s.Resume {
		t.Fatalf("Find's resume %q, the listing's %q", found.Resume, s.Resume)
	}
	if !s.ReadOnly || s.Deletable {
		t.Fatalf("imported session became writable: %+v", s)
	}
	if _, err := Delete("claude-desktop", resumeSourceID); err == nil {
		t.Fatal("imported session became deletable")
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("listing changed the imported transcript")
	}
}

// Nothing is offered when the command couldn't run: the folder it ran in
// isn't on this computer, or the transcript isn't in a config folder's
// projects, so there is no CLAUDE_CONFIG_DIR to name.
func TestSourceResumeNeedsItsFolderAndLayout(t *testing.T) {
	t.Run("folder gone", func(t *testing.T) {
		setup(t)
		gone := filepath.Join(t.TempDir(), "gone")
		resumeSourceFixture(t, filepath.Join(t.TempDir(), "config", "projects"), gone)
		if s := listedSource(t); s.Resume != "" {
			t.Fatalf("resume offered into a missing folder: %q", s.Resume)
		}
	})
	t.Run("not a config folder", func(t *testing.T) {
		setup(t)
		resumeSourceFixture(t, filepath.Join(t.TempDir(), "histories"), t.TempDir())
		if s := listedSource(t); s.Resume != "" {
			t.Fatalf("resume offered outside a config folder: %q", s.Resume)
		}
	})
}

// Claude Code's own folder imported needs no CLAUDE_CONFIG_DIR.
func TestSourceResumeInClaudesOwnFolder(t *testing.T) {
	claude, _ := setup(t)
	cwd := t.TempDir()
	resumeSourceFixture(t, filepath.Join(claude, "projects"), cwd)
	s := listedSource(t)
	if want := ResumeCommand("claude", resumeSourceID, cwd); s.Resume != want {
		t.Fatalf("resume %q, want %q", s.Resume, want)
	}
}

// A Cowork session's own .claude folder resumes when its folder is on this
// computer, never from its VM's folder, a gateway's row or a WSL distro.
func TestDesktopResumeOfItsOwnFolder(t *testing.T) {
	home := filepath.Join(t.TempDir(), "local-agent-mode-sessions", "account", "org", "local_session", ".claude")
	cwd := t.TempDir()
	s := Session{Agent: "claude-desktop", ID: resumeSourceID, Cwd: cwd, Path: filepath.Join(home, "projects", "p", resumeSourceID+".jsonl")}
	got := resumeOf(s)
	if !strings.Contains(got, "CLAUDE_CONFIG_DIR") || !strings.Contains(got, home) || !strings.Contains(got, "claude --resume "+resumeSourceID) {
		t.Fatalf("Cowork resume = %q", got)
	}
	for name, change := range map[string]func(*Session){
		"VM folder": func(s *Session) { s.Cwd = "/sessions/vm-only-folder" },
		"gateway":   func(s *Session) { s.Gateway = true },
		"WSL":       func(s *Session) { s.WSL = "Ubuntu" },
		"other id":  func(s *Session) { s.ID = "33333333-bbbb-4444-8888-999999999999" },
	} {
		c := s
		change(&c)
		if got := resumeOf(c); got != "" {
			t.Errorf("%s: resume %q", name, got)
		}
	}
	// any other read-only session still has none
	if got := resumeOf(Session{Agent: "codex", ID: resumeSourceID, Cwd: cwd, Path: s.Path, ReadOnly: true}); got != "" {
		t.Fatalf("read-only Codex session resumed: %q", got)
	}
}
