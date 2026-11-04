package sessions

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func importedRelocationFixture(t *testing.T) (string, string, ClaudeRelocation) {
	t.Helper()
	setup(t)
	root, source, in := relocationFixture(t)
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	p := filepath.Join(source, mgCC+".jsonl")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	writeLines(t, p, string(b))
	ageRelocation(t, source)
	return root, source, in
}

func TestSessionSourceRelocationRefusesImportedSources(t *testing.T) {
	for _, agent := range []string{"claude", "claude-desktop"} {
		for _, shape := range []string{"projects", "project", "nested"} {
			t.Run(agent+"/"+shape, func(t *testing.T) {
				root, source, in := importedRelocationFixture(t)
				preview, err := RelocateClaudeProject(in)
				if err != nil {
					t.Fatal(err)
				}
				dir := filepath.Dir(source)
				switch shape {
				case "project":
					dir = source
				case "nested":
					dir = filepath.Join(source, "nested", "projects")
					b, _ := os.ReadFile(filepath.Join(source, mgCC+".jsonl"))
					writeLines(t, filepath.Join(dir, "p", mgCC+".jsonl"), strings.TrimSpace(string(b)))
					ageRelocation(t, source)
				}
				if _, err := ImportSource(SessionSource{Path: dir, Agent: agent}); err != nil {
					t.Fatal(err)
				}
				before, _ := os.ReadFile(filepath.Join(source, mgCC+".jsonl"))
				for _, token := range []string{"", preview.Token} {
					in.Token = token
					if _, err := RelocateClaudeProject(in); err == nil || !strings.Contains(err.Error(), "read-only") {
						t.Fatalf("imported %s accepted relocation: %v", shape, err)
					}
				}
				after, _ := os.ReadFile(filepath.Join(source, mgCC+".jsonl"))
				if !bytes.Equal(before, after) {
					t.Fatal("read-only transcript changed")
				}
				if _, err := os.Stat(filepath.Join(root, "magpie-relocations")); !os.IsNotExist(err) {
					t.Fatal("read-only relocation staged files")
				}
			})
		}
	}
}

func TestSessionSourceRelocationKeepsUnrelatedImportedProject(t *testing.T) {
	root, source, in := importedRelocationFixture(t)
	unrelated := filepath.Join(root, "projects", "unrelated")
	b, _ := os.ReadFile(filepath.Join(source, mgCC+".jsonl"))
	path := filepath.Join(unrelated, mgCC+".jsonl")
	writeLines(t, path, strings.TrimSpace(string(b)))
	if _, err := ImportSource(SessionSource{Path: unrelated, Agent: "claude-desktop"}); err != nil {
		t.Fatal(err)
	}
	preview, err := RelocateClaudeProject(in)
	if err != nil {
		t.Fatal("unrelated registration blocked preview", err)
	}
	in.Token = preview.Token
	if _, err := RelocateClaudeProject(in); err != nil {
		t.Fatal("unrelated registration blocked relocation", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, b) {
		t.Fatal("unrelated imported project changed", err)
	}
}

func TestSessionSourceRelocationRefusesRegisteredDestination(t *testing.T) {
	root, source, in := importedRelocationFixture(t)
	name, _ := claudeProjectName(in.To)
	dest := filepath.Join(root, "projects", name)
	b, _ := os.ReadFile(filepath.Join(source, mgCC+".jsonl"))
	writeLines(t, filepath.Join(dest, "nested", "p", mgCC+".jsonl"), strings.TrimSpace(string(b)))
	if _, err := ImportSource(SessionSource{Path: filepath.Join(dest, "nested"), Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dest, filepath.Join(root, "detached-source")); err != nil {
		t.Fatal(err)
	}
	if _, err := RelocateClaudeProject(in); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("registered destination accepted: %v", err)
	}
}

func TestSessionSourceRelocationRefusesUnreadableSourceRegistry(t *testing.T) {
	_, source, in := importedRelocationFixture(t)
	preview, err := RelocateClaudeProject(in)
	if err != nil {
		t.Fatal(err)
	}
	broken := []byte("[{broken")
	if err := os.WriteFile(sourcesPath(), broken, 0o600); err != nil {
		t.Fatal(err)
	}
	in.Token = preview.Token
	if _, err := RelocateClaudeProject(in); err == nil || !strings.Contains(err.Error(), "cannot verify") {
		t.Fatalf("unreadable registry allowed relocation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(source, mgCC+".jsonl")); err != nil {
		t.Fatal("original disappeared", err)
	}
	got, _ := os.ReadFile(sourcesPath())
	if !bytes.Equal(got, broken) {
		t.Fatal("unreadable registry rewritten")
	}
}

func TestSessionSourceRelocationResolvesOnlyTouchedPaths(t *testing.T) {
	_, source, in := importedRelocationFixture(t)
	real := filepath.Join(t.TempDir(), "physical-project")
	stopped := `\\wsl.localhost\StoppedDistro\projects`
	if !filepath.IsAbs(stopped) {
		stopped = filepath.Join(t.TempDir(), "StoppedDistro", "projects")
	}
	if err := changeSources(func([]SessionSource) ([]SessionSource, error) {
		return []SessionSource{{Path: real, Agent: "claude"}, {Path: stopped, Agent: "claude"}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	old := sourceRealPath
	defer func() { sourceRealPath = old }()
	sourceRealPath = func(path string) (string, error) {
		if strings.Contains(path, "StoppedDistro") {
			t.Fatal("opened unrelated stopped WSL source")
		}
		if path == source {
			return real, nil
		}
		return old(path)
	}
	if _, err := RelocateClaudeProject(in); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("physical source alias accepted: %v", err)
	}
}
