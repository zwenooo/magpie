package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyManagedMCPRequiresLocalFilesAndFreshHandshake(t *testing.T) {
	h := sandbox(t)
	initMarker := filepath.Join(h, "initialized")
	s := fakeStdio(t, "ok", map[string]string{"MAGPIE_FAKE_MCP_INIT": initMarker})
	s.Args = append(s.Args, "{{program}}/entry")
	ok(t)(SaveServer("", *s))
	if err := VerifyManagedMCP(s.Name); err == nil {
		t.Fatal("an unbound command was run")
	}
	root := programDir(s.Name)
	entry := filepath.Join(root, "entry")
	write(t, entry, "fixture")
	digest, err := repoProgramHash(root)
	if err != nil {
		t.Fatal(err)
	}
	l, err := load()
	if err != nil {
		t.Fatal(err)
	}
	target := l.server(s.Name)
	target.RepoSource = &MCPRepoSource{Repo: "https://github.com/example/fixture"}
	target.RepoLocalCommit = strings.Repeat("a", 40)
	target.RepoLocalHash = digest
	if err = l.save(); err != nil {
		t.Fatal(err)
	}
	if err := VerifyManagedMCP(s.Name); err == nil {
		t.Fatal("verified without a local source receipt")
	}
	target.RepoLocalSource = target.RepoSource
	if err := l.save(); err != nil {
		t.Fatal(err)
	}
	runs.Lock()
	previous := runs.given
	runs.given = nil
	runs.Unlock()
	t.Cleanup(func() { runs.Lock(); runs.given = previous; runs.Unlock() })
	if got := ManagedGatewayState(s.Name); got != "unknown" {
		t.Fatalf("no gateway registry: %s", got)
	}
	spec, _ := specOf(target)
	runs.Lock()
	runs.given = map[string]runSpec{s.Name: spec}
	runs.Unlock()
	if got := ManagedGatewayState(s.Name); got != "matching" {
		t.Fatalf("same startup configuration: %s", got)
	}
	spec.Revision = strings.Repeat("b", 40)
	runs.Lock()
	runs.given[s.Name] = spec
	runs.Unlock()
	if got := ManagedGatewayState(s.Name); got != "stale" {
		t.Fatalf("older registered revision with identical path: %s", got)
	}
	for range 2 {
		if err := VerifyManagedMCP(s.Name); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(initMarker); err != nil {
			t.Fatal("no fresh initialize:", err)
		}
	}
	write(t, entry, "edited")
	if err := VerifyManagedMCP(s.Name); err == nil {
		t.Fatal("ran edited program")
	}
	if _, err := os.Stat(initMarker); !os.IsNotExist(err) {
		t.Fatal("started process despite local change", err)
	}
}
