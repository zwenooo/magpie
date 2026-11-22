package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/proc"
)

func TestMaintenanceLibraryWriteTargets(t *testing.T) {
	h := sandbox(t)
	write(t, path(), `{"skills":[{"name":"notes"}],"mcp":[{"name":"notes","transport":"stdio","command":"{{program}}/run"}]}`)
	root := skillDir("notes")
	write(t, filepath.Join(root, "SKILL.md"), "test")
	write(t, filepath.Join(programDir("notes"), "run"), "never execute")
	if _, err := Where("skill", "notes", ""); err == nil {
		t.Fatal("used a same-name directory without registration")
	}
	loc, err := Maintain("skill", "notes", "library", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Where("mcp", "notes", ""); err == nil {
		t.Fatal("skill registration authorized same-name MCP")
	}
	if _, err := Maintain("mcp", "notes", "library", programDir("notes")); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(h, ".codex", "skills", "notes")
	if err := os.MkdirAll(filepath.Dir(alias), 0700); err != nil {
		t.Fatal(err)
	}
	if err := dirLink(root, alias); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"SKILL.md", "new.md"} {
		for _, request := range []string{file, filepath.Join(root, file), filepath.Join(alias, file)} {
			v, err := Where("skill", "notes", request)
			if err != nil || v.WritePath != filepath.Join(loc.MaintenancePath, file) {
				t.Fatalf("%s: %+v, %v", request, v, err)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(root, "new.md")); !os.IsNotExist(err) {
		t.Fatal("where created new file")
	}
	out := t.TempDir()
	write(t, filepath.Join(out, "outside"), "not source")
	if err := dirLink(out, filepath.Join(root, "away")); err != nil {
		t.Fatal(err)
	}
	broken := t.TempDir()
	if err := dirLink(broken, filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, ".git", "config"), "data")
	if err := dirLink(filepath.Join(root, ".git"), filepath.Join(root, "metadata")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(out, "outside"), filepath.Join(root, "hard")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"away/outside", "away/new", "dangling", "dangling/new", "missing/new", "SKILL.md/new", "hard", ".Git/config", "metadata/new", "../other", "away/../new", filepath.Join(out, "outside"), filepath.Join(out, "new")} {
		if v, err := Where("skill", "notes", p); err == nil {
			t.Errorf("authorized %s: %+v", p, v)
		}
	}
	if err := os.Rename(root, root+"-moved"); err != nil {
		t.Fatal(err)
	}
	if _, err := Where("skill", "notes", ""); err == nil {
		t.Fatal("missing registered root accepted")
	}
	if err := dirLink(out, root); err != nil {
		t.Fatal(err)
	}
	if _, err := Where("skill", "notes", ""); err == nil {
		t.Fatal("redirected registered root accepted")
	}
	write(t, maintenanceFile(), "null")
	if _, err := Where("mcp", "notes", ""); err == nil {
		t.Fatal("null record accepted")
	}
	if _, err := Maintain("mcp", "notes", "library", ""); err == nil || read(t, maintenanceFile()) != "null" {
		t.Fatal("corrupt registry overwritten")
	}
}

func TestMaintenanceCheckoutIsNotTheInstallation(t *testing.T) {
	env := os.Getenv("PATH")
	h := sandbox(t)
	t.Setenv("PATH", env)
	const libraryJSON = `{"skills":[{"name":"notes"}],"mcp":[{"name":"notes","transport":"stdio","command":"{{program}}/run","repoSource":{"repo":"https://github.com/example/tools","path":"src/mcp"}}]}`
	write(t, path(), libraryJSON)
	installed := programDir("notes")
	write(t, filepath.Join(installed, "run"), "not executable")
	repo := filepath.Join(h, "work")
	src := filepath.Join(repo, "tool")
	write(t, filepath.Join(src, "run"), "do not run")
	git, err := proc.Look("git")
	if err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		if b, err := proc.Command(git, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, b, err)
		}
	}
	runGit("init", "--template=", repo)
	runGit("init", "--template=", installed)
	if _, err := Maintain("mcp", "notes", "library", ""); err == nil {
		t.Fatal("authorized managed snapshot as library source")
	}
	if _, err := Maintain("mcp", "notes", "checkout", installed); err == nil {
		t.Fatal("installed program treated as checkout")
	}
	bad := filepath.Join(h, "copy")
	write(t, filepath.Join(bad, ".git", "made-up"), "")
	t.Setenv("GIT_DIR", filepath.Join(repo, ".git"))
	t.Setenv("GIT_WORK_TREE", repo)
	if _, err := Maintain("mcp", "notes", "checkout", bad); err == nil {
		t.Fatal("ambient repository vouched for fake worktree")
	}
	realSrc, err := proc.RealPath(src)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Maintain("mcp", "notes", "checkout", src)
	if err != nil || v.RepoSource == nil || v.RepoSource.Path != "src/mcp" || !sameDir(v.MaintenancePath, realSrc) {
		t.Fatalf("register: %+v %v", v, err)
	}
	if _, err := Maintain("mcp", "notes", "checkout", bad); err == nil {
		t.Fatal("bad replacement accepted")
	}
	if _, err := Where("mcp", "notes", "new"); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(src, marker), "notes")
	if _, err := Where("mcp", "notes", ""); err == nil {
		t.Fatal("accepted agent copy marker")
	}
	if err := os.Remove(filepath.Join(src, marker)); err != nil {
		t.Fatal(err)
	}
	if read(t, path()) != libraryJSON {
		t.Fatal("registration changed the synchronized library")
	}
	bundle, err := Collect()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `maintenancePath`) {
		t.Fatal("registration carried with library")
	}
	write(t, path(), strings.Replace(libraryJSON, "example/tools", "another/tools", 1))
	if _, err := Where("mcp", "notes", ""); err == nil {
		t.Fatal("changed repoSource inherited registration")
	}
	write(t, path(), libraryJSON)
	if err := os.Rename(filepath.Join(repo, ".git"), filepath.Join(repo, "old-metadata")); err != nil {
		t.Fatal(err)
	}
	if _, err := Where("mcp", "notes", ""); err == nil {
		t.Fatal("checkout no longer under Git accepted")
	}
}
