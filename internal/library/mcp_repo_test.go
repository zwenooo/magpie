package library

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func repoFixtureServer() Server {
	return Server{Name: "notes", Transport: "stdio", Command: "python", Args: []string{programRef + "/main.py"}, Agents: []string{}}
}

func TestMCPRepoInstallAndCarry(t *testing.T) {
	sandbox(t)
	src := &MCPRepoSource{Repo: "https://github.com/example/project"}
	server := repoFixtureServer()
	server.RepoSource, server.RepoLocalHash = src, "from an ordinary save"
	ok(t)(SaveServer("", server))
	calls := 0
	archive := repoTestArchive(t, repoTestEntry{"main.py", 0644, "version one"})
	repoTestHTTP(t, func(req *http.Request) *http.Response {
		calls++
		data := []byte(repoTestSHA)
		if req.URL.Host == "codeload.github.com" {
			data = archive
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data))}
	})
	if _, err := CheckMCPRepo(server.Name, nil); err == nil || calls != 0 {
		t.Fatalf("unregistered check: %v, calls %d", err, calls)
	}
	if _, err := ApplyMCPRepo(server.Name, nil, repoTestSHA); err == nil || calls != 0 {
		t.Fatalf("unregistered apply: %v, calls %d", err, calls)
	}
	before := read(t, path())
	check, err := CheckMCPRepo(server.Name, src)
	if err != nil || check.Current != "" || check.Latest != repoTestSHA || before != read(t, path()) || calls != 1 {
		t.Fatalf("preview %+v, %v, calls %d", check, err, calls)
	}
	program := filepath.Join(programDir(server.Name), "main.py")
	write(t, program, "own existing file")
	if _, err := ApplyMCPRepo(server.Name, src, repoTestSHA); err == nil || read(t, program) != "own existing file" || before != read(t, path()) {
		t.Fatal("first install replaced an unmanaged file or registered its source")
	}
	if err := os.Remove(program); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(privateDir(server.Name), "secret")
	state := filepath.Join(stateDir(server.Name), "run")
	write(t, private, "key")
	write(t, state, "state")
	ok(t)(ApplyMCPRepo(server.Name, src, repoTestSHA))
	if read(t, program) != "version one" || read(t, private) != "key" || read(t, state) != "state" {
		t.Fatal("wrong installation folders")
	}
	server = repoFixtureServer()
	server.Note = "ordinary edit"
	ok(t)(SaveServer(server.Name, server))
	check, err = CheckMCPRepo(server.Name, nil)
	if err != nil || check.Current != repoTestSHA {
		t.Fatalf("lost registration after editing: %+v %v", check, err)
	}
	recordedCalls := calls
	bundle, err := Collect()
	if err != nil {
		t.Fatal(err)
	}
	bundle = carriedOver(t, bundle)
	remote := bundle.MCP[0]
	if remote.RepoLocalSource != nil {
		t.Fatal("local repository receipt exported")
	}
	remote.RepoLocalSource = src // incoming claims do not grant local trust
	if remote.RepoSource == nil || *remote.RepoSource != *src || remote.RepoCommit != repoTestSHA || remote.RepoHash == "" || remote.RepoLocalHash != "" || len(bundle.Folders) != 0 {
		t.Fatalf("wrong carried metadata or program files: %+v", remote)
	}
	// Even a bundle supplying bytes and claiming a local installation cannot install it.
	remote.RepoLocalHash, remote.RepoLocalCommit = remote.RepoHash, repoTestSHA
	bundle.Folders = []*CarriedFolders{{Name: server.Name, Program: map[string][]byte{"main.py": []byte("not confirmed")}}}
	sandbox(t)
	ok(t)(Put(bundle))
	if server, _, err := repoTarget(server.Name, nil); err != nil || server.RepoLocalSource != nil {
		t.Fatal("incoming bundle granted local trust", err)
	}
	if _, err := os.Lstat(programDir(server.Name)); !errors.Is(err, fs.ErrNotExist) || calls != recordedCalls {
		t.Fatalf("restore installed the program: %v, calls %d", err, calls)
	}
	check, err = CheckMCPRepo(server.Name, nil)
	if err != nil || check.Current != "" || check.Latest != repoTestSHA {
		t.Fatalf("restored preview: %+v %v", check, err)
	}
	ok(t)(ApplyMCPRepo(server.Name, nil, repoTestSHA))
	if read(t, filepath.Join(programDir(server.Name), "main.py")) != "version one" {
		t.Fatal("explicit install did not use the registered source")
	}
	sandbox(t)
	ok(t)(SaveServer("", repoFixtureServer()))
	localFile := filepath.Join(programDir(server.Name), "main.py")
	write(t, localFile, "plain server")
	ok(t)(Put(bundle))
	if _, err := CheckMCPRepo(server.Name, nil); err == nil || read(t, localFile) != "plain server" {
		t.Fatal("restoring a same-named server migrated a plain installation")
	}
}

func TestMCPRepoUpdateTransaction(t *testing.T) {
	sandbox(t)
	src := &MCPRepoSource{Repo: "https://github.com/example/project"}
	server := repoFixtureServer()
	ok(t)(SaveServer("", server))
	version := "first"
	var during func()
	fetch := func(actual MCPRepoSource, expected string) (map[string][]byte, []string, error) {
		if actual != *src || expected != version {
			t.Fatal("incorrect source or commit")
		}
		if during != nil {
			during()
		}
		return map[string][]byte{"main.py": []byte(version)}, nil, nil
	}
	ok(t)(applyMCPRepo(server.Name, src, version, fetch, (*Library).save))
	program := filepath.Join(programDir(server.Name), "main.py")
	version = "second"
	during = func() { server.Note = "edited while downloading"; ok(t)(SaveServer(server.Name, server)) }
	if _, err := applyMCPRepo(server.Name, nil, version, fetch, (*Library).save); err == nil || read(t, program) != "first" {
		t.Fatal("accepted concurrent configuration change")
	}
	during = nil
	before := read(t, path())
	failed := errors.New("save failed")
	if _, err := applyMCPRepo(server.Name, nil, version, fetch, func(*Library) error { return failed }); !errors.Is(err, failed) || read(t, program) != "first" || read(t, path()) != before {
		t.Fatalf("rollback: %v", err)
	}
	during = func() { write(t, program, "changed here") }
	if _, err := applyMCPRepo(server.Name, nil, version, fetch, (*Library).save); err == nil || read(t, program) != "changed here" || read(t, path()) != before {
		t.Fatal("overwrote local edits")
	}
	during = nil
	write(t, program, "first")
	ok(t)(applyMCPRepo(server.Name, nil, version, fetch, (*Library).save))
	if read(t, program) != "second" {
		t.Fatal("did not replace the clean program")
	}
}

func TestMCPRepoOnlyInstallsIntoItsProgram(t *testing.T) {
	sandbox(t)
	source := &MCPRepoSource{Repo: "https://github.com/example/project"}
	server := repoFixtureServer()
	for _, args := range [][]string{{"other.py"}, {programRef + "/../private/secret"}} {
		server.Args, server.Env = args, map[string]string{"DATA": programRef}
		if repoEntry(&server) != "" {
			t.Fatalf("accepted non-entry %v", args)
		}
	}
	server = repoFixtureServer()
	ok(t)(SaveServer("", server))
	files := map[string][]byte{"other.py": []byte("not the entry")}
	fetch := func(MCPRepoSource, string) (map[string][]byte, []string, error) { return files, nil, nil }
	if _, err := applyMCPRepo(server.Name, source, repoTestSHA, fetch, (*Library).save); err == nil {
		t.Fatal("installed snapshot without the entry")
	}
	files["main.py"] = []byte("from repo")
	private := privateDir(server.Name)
	write(t, filepath.Join(private, "secret"), "not program data")
	if err := dirLink(private, programDir(server.Name)); err != nil {
		t.Fatal(err)
	}
	before := read(t, path())
	if _, err := applyMCPRepo(server.Name, source, repoTestSHA, fetch, (*Library).save); err == nil || read(t, filepath.Join(private, "secret")) != "not program data" || before != read(t, path()) {
		t.Fatal("installed through a program directory link")
	}
}
