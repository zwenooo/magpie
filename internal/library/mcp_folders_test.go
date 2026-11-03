package library

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// keptServer saves a server whose program and private files are kept in
// the library, as ops-mcp is: python running its script, its config
// named by the environment, its logs in its state folder.
func keptServer(t *testing.T, program, config string) Server {
	t.Helper()
	write(t, filepath.Join(programDir("ops"), "server.py"), program)
	write(t, filepath.Join(programDir("ops"), "lib", "util.py"), "X = 1\n")
	write(t, filepath.Join(programDir("ops"), "__pycache__", "util.cpython-312.pyc"), "compiled")
	write(t, filepath.Join(programDir("ops"), ".git", "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(privateDir("ops"), "config.yaml"), config)
	write(t, filepath.Join(privateDir("ops"), "secrets", "a.password"), "hunter2\n")
	s := Server{Name: "ops", Transport: "stdio", Command: "python", Args: []string{"{{program}}/server.py"},
		Env: map[string]string{"OPS_MCP_CONFIG": "{{private}}/config.yaml", "OPS_MCP_STATE_DIR": "{{state}}"}, Agents: []string{"claude"}}
	ok(t)(SaveServer("", s))
	return s
}

func carriedOver(t *testing.T, b *Bundle) *Bundle {
	t.Helper()
	var in Bundle
	if err := json.Unmarshal([]byte(mustJSON(t, b)), &in); err != nil {
		t.Fatal(err)
	}
	return &in
}

func TestServerFoldersGivenAsThisComputersPaths(t *testing.T) {
	h := sandbox(t)
	keptServer(t, "print('v1')\n", "targets: {}\n")
	c := read(t, filepath.Join(h, ".claude.json"))
	var cfg struct {
		MCPServers map[string]struct {
			Args []string          `json:"args"`
			Env  map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(c), &cfg); err != nil {
		t.Fatal(err)
	}
	ops := cfg.MCPServers["ops"]
	if strings.Contains(c, "{{") || len(ops.Args) != 1 || ops.Args[0] != programDir("ops")+"/server.py" ||
		ops.Env["OPS_MCP_CONFIG"] != privateDir("ops")+"/config.yaml" || ops.Env["OPS_MCP_STATE_DIR"] != stateDir("ops") {
		t.Fatalf("claude was given the names, not this computer's paths:\n%s", c)
	}
	// the library keeps them as written, which is what is carried
	l, err := load()
	if err != nil {
		t.Fatal(err)
	}
	if s := l.server("ops"); s.Args[0] != "{{program}}/server.py" || s.Env["OPS_MCP_CONFIG"] != "{{private}}/config.yaml" {
		t.Fatalf("the library holds %+v", s)
	}
	// the state folder is outside the library: never carried, never a change to it
	if strings.HasPrefix(stateDir("ops"), Dir()) {
		t.Fatalf("state folder %s is in the library's", stateDir("ops"))
	}
	if spec, ok := specOf(l.server("ops")); !ok || spec.Args[0] != programDir("ops")+"/server.py" {
		t.Fatalf("magpie would run %+v", spec)
	}
	// an agent in WSL can't run a Windows program: not given, said why
	_, _, _, _, _, bad := startServer(context.Background(), l.server("ops"), "Ubuntu")
	if bad == nil || !strings.Contains(bad.Detail, "WSL") {
		t.Fatalf("a check in WSL: %+v", bad)
	}
}

func TestServerFoldersCarried(t *testing.T) {
	sandbox(t)
	keptServer(t, "print('v2')\n", "targets: {a: {}}\n")
	b, err := Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Folders) != 1 || b.Folders[0].Name != "ops" {
		t.Fatalf("folders: %+v", b.Folders)
	}
	f := b.Folders[0]
	if got := strings.Join(slices.Sorted(maps.Keys(f.Program)), ","); got != "lib/util.py,server.py" {
		t.Fatalf("program carried %s: what running it wrote, or .git, went too", got)
	}
	if string(f.Private["secrets/a.password"]) != "hunter2\n" || string(f.Private["config.yaml"]) != "targets: {a: {}}\n" {
		t.Fatalf("private: %v", keysOf(f.Private))
	}
	withKeys := carriedOver(t, b)
	keyless := carriedOver(t, b)
	keyless.WithoutSecrets(func(string) bool { return false })
	if keyless.Folders[0].Private != nil || withKeys.Folders[0].Private == nil {
		t.Fatal("a bundle without keys carried the private files, or stripping one stripped the other")
	}
	if strings.Contains(mustJSON(t, keyless), "hunter2") {
		t.Fatal("a keyless bundle has the password in it")
	}

	// another computer, with its own older program and its own config
	sandbox(t)
	write(t, filepath.Join(programDir("ops"), "server.py"), "print('v1')\n")
	write(t, filepath.Join(programDir("ops"), "old.py"), "gone\n")
	write(t, filepath.Join(privateDir("ops"), "config.yaml"), "mine\n")
	ok(t)(Put(keyless))
	if read(t, filepath.Join(programDir("ops"), "server.py")) != "print('v2')\n" || read(t, filepath.Join(programDir("ops"), "lib", "util.py")) != "X = 1\n" {
		t.Fatal("the program wasn't brought in")
	}
	if _, err := os.Stat(filepath.Join(programDir("ops"), "old.py")); err == nil {
		t.Fatal("a file the other computer doesn't have stayed in the program")
	}
	kept, _ := filepath.Glob(filepath.Join(BackupDir(), "*", "mcp", "ops", "program", "old.py"))
	if len(kept) != 1 {
		t.Fatal("the program replaced wasn't kept with the backups")
	}
	if read(t, filepath.Join(privateDir("ops"), "config.yaml")) != "mine\n" {
		t.Fatal("a bundle without keys took this computer's config")
	}
	// the bundle with keys brings them, the user's alone
	ok(t)(Put(withKeys))
	if read(t, filepath.Join(privateDir("ops"), "secrets", "a.password")) != "hunter2\n" {
		t.Fatal("the private files weren't brought in")
	}
	if _, err := os.Stat(filepath.Join(privateDir("ops"), "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{privateDir("ops"), filepath.Join(privateDir("ops"), "secrets", "a.password")} {
			if fi, _ := os.Stat(p); fi.Mode().Perm()&0o077 != 0 {
				t.Errorf("%s is %v: others can read it", p, fi.Mode().Perm())
			}
		}
	}
	if old, _ := filepath.Glob(filepath.Join(BackupDir(), "*", "mcp", "ops", "private")); len(old) != 0 {
		t.Fatal("the config replaced was copied into the backups")
	}
	// the same bundle again changes nothing
	before, _ := filepath.Glob(filepath.Join(BackupDir(), "*"))
	ok(t)(Put(withKeys))
	if after, _ := filepath.Glob(filepath.Join(BackupDir(), "*", "mcp")); len(after) != 1 || len(before) == 0 {
		t.Fatalf("an unchanged program was set aside again: %v", after)
	}
}

func TestServerFoldersGoWithTheServer(t *testing.T) {
	sandbox(t)
	keptServer(t, "print('v1')\n", "x\n")
	// renamed, its folders follow it
	s := Server{Name: "ops2", Transport: "stdio", Command: "python", Args: []string{"{{program}}/server.py"}, Agents: []string{"claude"}}
	ok(t)(SaveServer("ops", s))
	if read(t, filepath.Join(programDir("ops2"), "server.py")) != "print('v1')\n" {
		t.Fatal("the program didn't follow the rename")
	}
	// taken out on another computer, its folders go here at the next sync:
	// the program to the backups, the keys away
	b, err := Collect()
	if err != nil {
		t.Fatal(err)
	}
	b.MCP = nil
	b.Folders = nil
	ok(t)(Put(carriedOver(t, b)))
	if _, err := os.Stat(serverFolder("ops2")); err == nil {
		t.Fatal("the folders of a server taken out stayed")
	}
	kept, _ := filepath.Glob(filepath.Join(BackupDir(), "*", "mcp", "ops2", "program", "server.py"))
	if len(kept) != 1 {
		t.Fatal("the program of a server taken out wasn't kept")
	}
	if p, _ := filepath.Glob(filepath.Join(BackupDir(), "*", "mcp", "ops2", "private")); len(p) != 0 {
		t.Fatal("its keys were copied into the backups")
	}
	// newer changes' backups past keepBackups: this one is the oldest
	for i := range keepBackups + 5 {
		write(t, filepath.Join(BackupDir(), fmt.Sprintf("2999-01-01_00-00-%02d.000", i), "claude", "x"), "x")
	}
	pruneBackups()
	if kept, _ := filepath.Glob(filepath.Join(BackupDir(), "*", "mcp", "ops2", "program")); len(kept) != 1 {
		t.Fatal("pruning took the only copy of the program")
	}
}

func TestServerFoldersRemovedWithIt(t *testing.T) {
	sandbox(t)
	keptServer(t, "print('v1')\n", "x\n")
	ok(t)(RemoveServer("ops"))
	if _, err := os.Stat(serverFolder("ops")); err == nil {
		t.Fatal("a server removed left its folders in the library")
	}
	if kept, _ := filepath.Glob(filepath.Join(BackupDir(), "*", "mcp", "ops", "program", "server.py")); len(kept) != 1 {
		t.Fatal("the program of a server removed wasn't kept")
	}
	if p, _ := filepath.Glob(filepath.Join(BackupDir(), "*", "mcp", "ops", "private")); len(p) != 0 {
		t.Fatal("its keys were copied into the backups")
	}
}

func TestServerFoldersKeptToThisComputer(t *testing.T) {
	sandbox(t)
	s := keptServer(t, "print('mine')\n", "mine\n")
	s.Local = true
	ok(t)(SaveServer("ops", s))
	b, err := Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Folders) != 0 {
		t.Fatal("a server kept to this computer had its folders carried")
	}
	// another computer's server by that name brings nothing in here
	other := carriedOver(t, b)
	other.MCP = []*Server{{Name: "ops", Transport: "stdio", Command: "python", Args: []string{"{{program}}/server.py"}, Agents: []string{}}}
	other.Folders = []*CarriedFolders{{Name: "ops", Program: map[string][]byte{"server.py": []byte("print('theirs')\n")}, Private: map[string][]byte{"config.yaml": []byte("theirs\n")}}}
	ok(t)(Put(other))
	if read(t, filepath.Join(programDir("ops"), "server.py")) != "print('mine')\n" || read(t, filepath.Join(privateDir("ops"), "config.yaml")) != "mine\n" {
		t.Fatal("another computer's folders replaced this computer's own")
	}
}

func TestServerFoldersRefuseAPathOut(t *testing.T) {
	sandbox(t)
	for _, rel := range []string{"../x", "a/../../x", "/etc/x", "C:/x", `a\..\x`} {
		b := &Bundle{MCP: []*Server{{Name: "ops", Transport: "stdio", Command: "python"}},
			Folders: []*CarriedFolders{{Name: "ops", Private: map[string][]byte{rel: []byte("x")}}}}
		if _, err := Put(b); err == nil {
			t.Errorf("put %q", rel)
		}
	}
	if _, err := os.Stat(serverFolder("ops")); err == nil {
		t.Fatal("a refused bundle wrote a folder")
	}
}
