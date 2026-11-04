package sessions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/filememo"
)

const sourceTestID = "11111111-aaaa-4444-8888-999999999999"

func sourceFixture(t *testing.T) (SessionSource, string) {
	t.Helper()
	setup(t)
	s := SessionSource{Path: filepath.Join(t.TempDir(), "profile", "config", "projects"), Agent: "claude-desktop"}
	path := filepath.Join(s.Path, "C--work", sourceTestID+".jsonl")
	writeLines(t, path,
		`{"type":"permission-mode","permissionMode":"auto","sessionId":"`+sourceTestID+`"}`,
		`{"type":"user","timestamp":"2026-09-20T10:00:00Z","message":{"role":"user","content":"custom directory conversation"},"cwd":"C:\\work","sessionId":"`+sourceTestID+`"}`,
		claudeMsg("custom-response", "claude-opus-5-5", 100, 12, 0, 0, 1),
		`{"type":"ai-title","aiTitle":"Imported conversation"}`)
	return s, path
}

func TestSessionSourceScanImport(t *testing.T) {
	s, path := sourceFixture(t)
	before, _ := os.ReadFile(path)
	preview, err := ScanSource(s)
	if err != nil || preview.Count != 1 || len(preview.Sessions) != 1 || preview.Sessions[0].Title != "Imported conversation" {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if rows, err := Sources(); err != nil || len(rows) != 0 {
		t.Fatalf("preview registered source: %+v, %v", rows, err)
	}
	if got := ListAgent("claude-desktop"); len(got) != 0 {
		t.Fatalf("preview imported sessions: %+v", got)
	}
	if _, err := ImportSource(s); err != nil {
		t.Fatal(err)
	}
	Reset()
	got := ListAgent("claude-desktop")
	if len(got) != 1 || got[0].ID != sourceTestID || !got[0].ReadOnly || got[0].Deletable || !got[0].Transcript {
		t.Fatalf("imported sessions = %+v", got)
	}
	tr, err := TranscriptOf(got[0].Session)
	if err != nil || len(tr.Parts) == 0 || !strings.Contains(tr.Parts[0].Text, "custom directory") {
		t.Fatalf("transcript = %+v, %v", tr, err)
	}
	if calls := AgentCalls(time.Time{}, "claude-desktop"); len(calls) != 1 {
		t.Fatalf("imported calls = %+v", calls)
	}
	if !slices.Contains(Found(), "claude-desktop") || !slices.Contains(Dirs(), preview.Source.Path) {
		t.Fatal("source missing from consent or directory listing")
	}
	if _, err := ImportSource(s); err != nil {
		t.Fatal(err)
	}
	rows, _ := Sources()
	if len(rows) != 1 {
		t.Fatalf("repeated import = %+v", rows)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("import rewrote source")
	}
	if err := RemoveSource(preview.Source); err != nil {
		t.Fatal(err)
	}
	if got := ListAgent("claude-desktop"); len(got) != 0 {
		t.Fatalf("removed source remains listed: %+v", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("removing source removed transcript", err)
	}
}

func TestSessionSourceValidation(t *testing.T) {
	s, path := sourceFixture(t)
	for _, bad := range []SessionSource{
		{Path: "projects", Agent: s.Agent},
		{Path: path, Agent: s.Agent},
		{Path: s.Path, Agent: "unknown"},
		{Path: filepath.Join(s.Path, "missing"), Agent: s.Agent},
	} {
		if _, err := ScanSource(bad); err == nil {
			t.Errorf("accepted invalid source %+v", bad)
		}
	}
	if err := SetReads(nil, map[string]bool{"claude-desktop": false}); err != nil {
		t.Fatal(err)
	}
	if _, err := ScanSource(s); !errors.Is(err, ErrNotRead) {
		t.Fatalf("consent bypassed: %v", err)
	}
	if _, err := ImportSource(s); !errors.Is(err, ErrNotRead) {
		t.Fatalf("import bypassed consent: %v", err)
	}
}

func TestSessionSourceProjectAndUnrelatedFiles(t *testing.T) {
	s, path := sourceFixture(t)
	writeLines(t, filepath.Join(s.Path, "C--work", "journal.jsonl"), `{"type":"user","message":{"role":"user","content":"unrelated"}}`)
	writeLines(t, filepath.Join(s.Path, "C--work", "22222222-aaaa-4444-8888-999999999999.jsonl"), `{"type":"metric","value":1}`)
	writeLines(t, filepath.Join(filepath.Dir(path), sourceTestID, "subagents", "agent-sub.jsonl"),
		`{"type":"user","message":{"role":"user","content":"child task"}}`)
	s.Path = filepath.Dir(path)
	preview, err := ScanSource(s)
	if err != nil || preview.Count != 1 {
		t.Fatalf("project preview = %+v, %v", preview, err)
	}
	if _, err := ImportSource(s); err != nil {
		t.Fatal(err)
	}
	got := ListAgent(s.Agent)
	if len(got) != 1 || len(got[0].Subagents) != 1 {
		t.Fatalf("subagent should stay with its parent: %+v", got)
	}
	parent := s
	parent.Path = filepath.Dir(s.Path)
	imported, err := ImportSource(parent)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := Sources(); err != nil || len(rows) != 1 || rows[0] != imported.Source {
		t.Fatalf("overlapping roots not merged into %+v: %+v, %v", imported.Source, rows, err)
	}
	other := s
	other.Agent = "claude"
	if _, err := ImportSource(other); err == nil {
		t.Fatal("overlap allowed conflicting type")
	}
}

func TestSessionSourceCanonicalAlias(t *testing.T) {
	s, path := sourceFixture(t)
	alias := s
	alias.Path = filepath.Join(t.TempDir(), "linked-projects")
	if err := os.Symlink(s.Path, alias.Path); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	preview, err := ScanSource(alias)
	if err != nil || preview.Count != 1 {
		t.Fatalf("alias preview: %+v, %v", preview, err)
	}
	for _, source := range []SessionSource{alias, s} {
		imported, err := ImportSource(source)
		if err != nil || imported.Source != preview.Source {
			t.Fatalf("alias did not share canonical source: %+v, %v", imported, err)
		}
	}
	if rows, err := Sources(); err != nil || len(rows) != 1 || rows[0] != preview.Source {
		t.Fatalf("alias imported twice: %+v, %v", rows, err)
	}
	if err := RemoveSource(preview.Source); err != nil {
		t.Fatal(err)
	}
	if rows, err := Sources(); err != nil || len(rows) != 0 {
		t.Fatalf("canonical registration not removed: %+v, %v", rows, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("removing alias registration removed its transcript", err)
	}
}

func TestSessionSourceKeepsUnreadableRegistry(t *testing.T) {
	s, _ := sourceFixture(t)
	if _, err := ImportSource(s); err != nil {
		t.Fatal(err)
	}
	broken := []byte(`[{"path":`)
	if err := os.WriteFile(sourcesPath(), broken, 0o600); err != nil {
		t.Fatal(err)
	}
	filememo.Forget()
	if _, err := Sources(); err == nil {
		t.Fatal("invalid registry accepted")
	}
	if got := ListAgent(s.Agent); len(got) != 1 {
		t.Fatalf("registry error discarded existing sessions: %+v", got)
	}
	if _, err := ImportSource(s); err == nil {
		t.Fatal("invalid registry overwritten on import")
	}
	if err := RemoveSource(s); err == nil {
		t.Fatal("invalid registry overwritten on removal")
	}
	got, _ := os.ReadFile(sourcesPath())
	if !bytes.Equal(got, broken) {
		t.Fatal("invalid registry changed")
	}
}

func TestSessionSourceWriteReadsPastHeldMemo(t *testing.T) {
	s, _ := sourceFixture(t)
	if _, err := ImportSource(s); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(sourcesPath(), old, old); err != nil {
		t.Fatal(err)
	}
	filememo.Forget()
	Sources()
	done := filememo.Hold()
	defer done()
	Sources()
	other := SessionSource{Path: filepath.Join(t.TempDir(), "projects"), Agent: "claude"}
	b, _ := json.Marshal([]SessionSource{s, other})
	if err := os.WriteFile(sourcesPath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSource(s); err != nil {
		t.Fatal(err)
	}
	rows, err := Sources()
	if err != nil || len(rows) != 1 || rows[0] != other {
		t.Fatalf("external registration lost: %+v, %v", rows, err)
	}
}

func TestSessionSourceMalformedSiblingKeepsImportedSessions(t *testing.T) {
	s, _ := sourceFixture(t)
	if _, err := ImportSource(s); err != nil {
		t.Fatal(err)
	}
	writeLines(t, filepath.Join(s.Path, "C--work", "22222222-aaaa-4444-8888-999999999999.jsonl"), `{"broken`)
	if _, err := ScanSource(s); err == nil {
		t.Fatal("malformed JSONL silently counted as an empty source")
	}
	if got := ListAgent(s.Agent); len(got) != 1 || got[0].ID != sourceTestID {
		t.Fatalf("valid imported sibling disappeared: %+v", got)
	}
}

func TestSessionSourceDefaultOverlapIsReadOnly(t *testing.T) {
	claude, _ := setup(t)
	s := SessionSource{Path: filepath.Join(claude, "projects"), Agent: "claude-desktop"}
	if _, err := ImportSource(s); err != nil {
		t.Fatal(err)
	}
	for _, f := range allFiles() {
		if sourceWithin(s.Path, f.path) && (f.agent != s.Agent || !f.readOnly) {
			t.Fatalf("default root escaped explicit source: %+v", f)
		}
	}
	if got := ListAgent("claude"); len(got) != 0 {
		t.Fatalf("source duplicated as default agent: %+v", got)
	}
}

func TestSessionSourceDoesNotWakeColdWSL(t *testing.T) {
	s, _ := sourceFixture(t)
	if _, err := ImportSource(s); err != nil {
		t.Fatal(err)
	}
	old := sourceRealPath
	defer func() { sourceRealPath = old }()
	sourceRealPath = func(path string) (string, error) {
		if strings.Contains(path, "StoppedDistro") {
			t.Fatal("resolved a stopped WSL distribution")
		}
		return old(path)
	}
	cold := file{agent: "claude", path: `\\wsl.localhost\StoppedDistro\home\u\.claude\projects\p\session.jsonl`, cold: true}
	got := withCustomSources([][]file{{cold}})
	if len(got) < 1 || len(got[0]) != 1 || got[0][0].path != cold.path || !got[0][0].cold {
		t.Fatalf("cold session not preserved: %+v", got)
	}
}

func TestSessionSourceColdUNC(t *testing.T) {
	old := WSLRunning
	defer func() { WSLRunning = old }()
	WSLRunning = func(name string) bool { return name == "RunningDistro" }
	if !sourceCold(`\\wsl.localhost\StoppedDistro\home\u\projects`) || !sourceCold(`\\wsl$\StoppedDistro\projects`) {
		t.Fatal("stopped custom WSL source would be scanned")
	}
	if sourceCold(`\\wsl.localhost\RunningDistro\home\u\projects`) || sourceCold(`C:\work\projects`) {
		t.Fatal("running or local source skipped")
	}
}

func TestSessionSourceImportsWholeDirectory(t *testing.T) {
	setup(t)
	s := SessionSource{Path: filepath.Join(t.TempDir(), "projects"), Agent: "claude-desktop"}
	add := func(i int) {
		id := fmt.Sprintf("%08d-aaaa-4444-8888-999999999999", i)
		writeLines(t, filepath.Join(s.Path, fmt.Sprintf("project-%d", i%2), id+".jsonl"),
			`{"type":"user","timestamp":"2026-09-20T10:00:00Z","message":{"role":"user","content":"directory session"},"cwd":"/work","sessionId":"`+id+`"}`)
	}
	for i := range 55 {
		add(i)
	}
	preview, err := ScanSource(s)
	if err != nil || preview.Count != 55 || len(preview.Sessions) != 50 {
		t.Fatalf("bounded preview = %+v, %v", preview, err)
	}
	if _, err := ImportSource(s); err != nil {
		t.Fatal(err)
	}
	if got := ListAgent(s.Agent); len(got) != 55 {
		t.Fatalf("imported %d sessions; want the whole directory, not the 50-row preview", len(got))
	}
	add(55)
	Reset()
	if got := ListAgent(s.Agent); len(got) != 56 {
		t.Fatalf("new session not discovered: %d", len(got))
	}
}

func TestSessionSourceCopiesCountOnce(t *testing.T) {
	s, path := sourceFixture(t)
	b, _ := os.ReadFile(path)
	copy := filepath.Join(s.Path, "another-project", filepath.Base(path))
	writeLines(t, copy, strings.TrimSpace(string(b)))
	preview, err := ImportSource(s)
	if err != nil || preview.Count != 1 {
		t.Fatalf("copy preview = %+v, %v", preview, err)
	}
	got := ListAgent(s.Agent)
	if len(got) != 1 || got[0].Files != 1 || got[0].Input != 100 || got[0].Output != 12 {
		t.Fatalf("copied session counted twice: %+v", got)
	}
}
