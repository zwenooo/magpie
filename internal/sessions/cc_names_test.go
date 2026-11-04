package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// TestCCListingKeepsItsNames: each Sessions poll made every Claude Code
// file's path and key again, ~10 MB of a poll over ~15k sessions, though
// the listing is the same but for a file or two. A listing like the last
// makes none of them; a file new, gone or moved since is listed as a
// fresh listing lists it.
func TestCCListingKeepsItsNames(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string) {
		t.Helper()
		p := filepath.Join(dir, "projects", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const n = 3000
	for i := range n {
		write(fmt.Sprintf("-Users-someone-work-a-project-p%d/%08d-1111-2222-3333-444444444444.jsonl", i%3, i))
	}
	// a session's folder, sorted between files, with its subagents
	write("-Users-someone-work-a-project-p1/00000500-aaaa/subagents/agent-1.jsonl")
	write("-Users-someone-work-a-project-p1/00000500-aaaa/subagents/workflows/run-1/agent-2.jsonl")
	write("-Users-someone-work-a-project-p1/00000500-aaaa/subagents/workflows/run-1/note.jsonl")
	// Both measurements must use settled directory listings: crossing
	// readDirectory's recent-write window would measure another cache too.
	old := time.Now().Add(-time.Minute)
	if err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		return os.Chtimes(p, old, old)
	}); err != nil {
		t.Fatal(err)
	}
	ccListed.Clear()
	t.Cleanup(func() { ccListed.Clear() })
	fresh := func() []file {
		ccListed.Clear()
		return listCCFiles("claude", dir)
	}
	same := func(what string, got []file) {
		t.Helper()
		want := fresh()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %d files, a fresh listing %d", what, len(got), len(want))
		}
	}

	first := listCCFiles("claude", dir)
	if len(first) != n+2 {
		t.Fatalf("listed %d files, want %d", len(first), n+2)
	}
	var again []file
	kept := testing.AllocsPerRun(5, func() { again = listCCFiles("claude", dir) })
	if !reflect.DeepEqual(again, first) {
		t.Fatal("an unchanged folder listed otherwise")
	}
	uncached := testing.AllocsPerRun(5, func() { again = fresh() })
	// Stat and directory allocations differ by OS. Reusing paths and keys
	// saves about two allocations per file; require at least one, rather
	// than bounding the filesystem work both listings must still do.
	if saved := uncached - kept; saved < n {
		t.Fatalf("listing %d unchanged files saved %.0f allocations with kept names (kept %.0f, fresh %.0f), want at least %d", n, saved, kept, uncached, n)
	}
	t.Logf("allocations per listing: kept %.0f, fresh %.0f", kept, uncached)

	write("-p0/00000001-new.jsonl")
	same("a file new", listCCFiles("claude", dir))
	listCCFiles("claude", dir)
	os.Remove(filepath.Join(dir, "projects", "-Users-someone-work-a-project-p0", "00000003-1111-2222-3333-444444444444.jsonl"))
	os.Remove(filepath.Join(dir, "projects", "-Users-someone-work-a-project-p2", "00000005-1111-2222-3333-444444444444.jsonl"))
	os.Remove(filepath.Join(dir, "projects", "-Users-someone-work-a-project-p2", "00000008-1111-2222-3333-444444444444.jsonl"))
	same("files gone", listCCFiles("claude", dir))
	listCCFiles("claude", dir)
	os.RemoveAll(filepath.Join(dir, "projects", "-Users-someone-work-a-project-p1"))
	write("-p9/00000500-aaaa/subagents/agent-1.jsonl")
	same("a folder gone and one new", listCCFiles("claude", dir))
	listCCFiles("claude", dir)
	// the same names under another agent's key
	got := listCCFiles("qoder", dir)
	for _, f := range got {
		if f.key[:6] != "qoder:" {
			t.Fatalf("key %q under qoder", f.key)
		}
	}
}
