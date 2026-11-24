package library

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/settings"
)

func TestOpsDirectUseDistribution(t *testing.T) {
	h := sandbox(t)
	resetRuns(t)
	wasOn, wasWired := runOn, runWired
	runOn, runWired = true, func(*agent.Agent) bool { return true }
	t.Cleanup(func() { runOn, runWired = wasOn, wasWired })
	s := bundleServer()
	s.Name = "operations"
	s.Agents = []string{"claude", "cursor", "codex", "claude#private-profile", agent.DesktopTwinID, "deveco"}
	raw := &Library{MCP: []*Server{&s}, Applied: map[string]*Applied{}}
	// An older library or a hand-edited import can contain spoofed markers.
	s.Env["magpie_direct_usage_item"] = "wrong"
	s.Env[directUseDB] = "wrong-db"
	s.Env[strings.ToLower(directUseAgent)] = "private-label"
	write(t, path(), mustJSON(t, raw))
	apply := func() {
		t.Helper()
		_, err := applyMCPRepo(s.Name, s.RepoSource, repoTestSHA, func(MCPRepoSource, string) (map[string][]byte, []string, error) {
			return map[string][]byte{"server.py": []byte("# fixture\n")}, nil, nil
		}, (*Library).save)
		if err != nil {
			t.Fatal(err)
		}
	}
	apply()
	loadLib := func() *Library {
		t.Helper()
		l, err := load()
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	l := loadLib()
	// The pre-source-receipt installation can be confirmed without replacing
	// its program or fetching from GitHub in the test.
	l.server(s.Name).RepoLocalSource = nil
	if err := l.save(); err != nil {
		t.Fatal(err)
	}
	if supportsDirectUse(loadLib().server(s.Name)) {
		t.Fatal("legacy receipt attributed by name")
	}
	apply()
	l = loadLib()
	if !supportsDirectUse(l.server(s.Name)) {
		t.Fatal("explicit reapply not recognized")
	}
	db := filepath.Join(settings.Dir(), "library-use", "ops-direct.db")
	file := func(id string, format mcpFormat) *mcpFile {
		return &mcpFile{Path: filepath.Join(h, id, "config"), Format: format}
	}
	sync := func(id, base string, f *mcpFile) *Result {
		t.Helper()
		res := &Result{}
		l.syncMCP(&Target{Agent: &agent.Agent{ID: id, Gateway: func() string { return base }}, MCP: f}, newBackups(), res)
		if len(res.Problems) > 0 {
			t.Fatalf("%s: %+v", id, res.Problems)
		}
		return res
	}
	readEntry := func(f *mcpFile, name string) *Server {
		t.Helper()
		es, err := f.entries()
		if err != nil {
			t.Fatal(err)
		}
		got, ok := f.decode(name, es[name])
		if !ok {
			t.Fatalf("no %q in %v", name, es)
		}
		return got
	}
	check := func(got *Server, product string) {
		t.Helper()
		if got.Transport != "stdio" || got.Command != s.Command || len(got.Args) != 1 || got.Args[0] != programDir(s.Name)+"/server.py" || got.Env["OPS_MCP_STATE_DIR"] != stateDir(s.Name) {
			t.Fatalf("launch changed: %+v", got)
		}
		for k := range got.Env {
			if directUseKey(k) && (product == "" || k != directUseDB && k != directUseItem && k != directUseAgent) {
				t.Fatalf("unexpected %s", k)
			}
		}
		if product != "" && (got.Env[directUseDB] != db || got.Env[directUseItem] != s.Name || got.Env[directUseAgent] != product) {
			t.Fatalf("native env: %v", got.Env)
		}
	}
	const base = "http://127.0.0.1:4000"
	claude, cursor, codex := file("claude", fmtClaude), file("cursor", fmtCursor), file("codex", fmtCodex)
	for _, a := range []struct {
		id, product string
		f           *mcpFile
	}{
		{"claude", "claude", claude}, {"cursor", "cursor", cursor},
		{"claude#private-profile", "claude", file("profile", fmtClaude)},
		{agent.DesktopTwinID, "claude-desktop", file("desktop", fmtDesktop)},
		{"deveco", "other", file("deveco", fmtOpenCode)},
	} {
		sync(a.id, base, a.f)
		check(readEntry(a.f, s.Name), a.product)
	}
	// An account switcher's folder is a claude# profile that follows Claude
	// Code (profile_follow.go) and gets the native launch. Its id and the
	// account's name must never become product labels.
	acc := reclaudeDir(t, h, "private-account", `{}`)
	follower := &Target{Agent: &agent.Agent{ID: "claude#rec-private-account", Dir: acc, Gateway: func() string { return base }},
		MCP: &mcpFile{Path: filepath.Join(acc, ".claude.json"), Format: fmtClaude}}
	mirrored := &Result{}
	l.keepFollowers([]*Target{{Agent: &agent.Agent{ID: "claude"}, MCP: claude}, follower}, mirrored)
	l.syncMCP(follower, newBackups(), mirrored)
	if len(mirrored.Problems) != 0 {
		t.Fatal(mirrored.Problems)
	}
	check(readEntry(follower.MCP, s.Name), "claude")
	res := sync("codex", base, codex)
	if got := readEntry(codex, s.Name); got.URL != base+RunPath+s.Name || got.Env != nil {
		t.Fatalf("Codex run route: %+v", got)
	}
	spec, ok := res.run[s.Name]
	if !ok || spec.Env[directUseDB] != "" || spec.Env[directUseItem] != "" || spec.Env[directUseAgent] != "" {
		t.Fatalf("run spec: %+v", spec)
	}
	l.MCPHub = true
	sync("claude", base, claude)
	if got := readEntry(claude, HubEntry); !got.Remote() || len(got.Env) != 0 {
		t.Fatalf("Hub: %+v", got)
	}
	l.server(s.Name).Hub = HubDirect
	sync("codex", base, codex)
	check(readEntry(codex, s.Name), "codex")
	sync("cursor", "http://192.0.2.1:4000", cursor)
	check(readEntry(cursor, s.Name), "")
	for _, ids := range [][]string{{"cursor", "claude"}, {"cursor", "claude", "codebuddy"}, {"cursor", "codebuddy", "claude"}} {
		p := &Project{Dir: t.TempDir(), Servers: map[string][]string{s.Name: ids}}
		l.syncProjectMCP(p, nil, func(what string, err error) { t.Fatal(what, err) })
		check(readEntry(&mcpFile{Path: filepath.Join(p.Dir, ProjectMCPFile("cursor")), Format: fmtCursor}, s.Name), "cursor")
		check(readEntry(&mcpFile{Path: filepath.Join(p.Dir, ProjectMCPFile("claude")), Format: fmtClaude}, s.Name), "other")
	}
	copy, err := ConfigOfServers("cursor", []string{s.Name})
	if err != nil || strings.Contains(strings.ToUpper(copy.Text), "MAGPIE_DIRECT_USAGE_") {
		t.Fatalf("portable snippet: %+v %v", copy, err)
	}
	bundle, err := Collect()
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{read(t, path()), mustJSON(t, bundle)} {
		if strings.Contains(strings.ToUpper(text), "MAGPIE_DIRECT_USAGE_") || strings.Contains(text, "ops-direct.db") {
			t.Fatal("markers in library or sync")
		}
	}
	// A different computer keeps the synced source, but not a local receipt.
	other := &Library{}
	other.putServers(bundle.MCP)
	if supportsDirectUse(other.server(s.Name)) {
		t.Fatal("synced receipt authorized a direct client")
	}
}

func TestOpsDirectUseExclusions(t *testing.T) {
	h := sandbox(t)
	s := bundleServer()
	s.RepoHash, s.RepoLocalHash = "local-hash", "local-hash"
	write(t, filepath.Join(programDir(s.Name), "server.py"), "fixture")
	for name, modify := range map[string]func(*Server){
		"same name":  func(s *Server) { s.RepoSource = nil },
		"other repo": func(s *Server) { s.RepoSource.Repo = "https://github.com/other/ops-mcp" },
		"subfolder":  func(s *Server) { s.RepoSource.Path = "ops" },
		"changed binding": func(s *Server) {
			s.RepoLocalSource = &MCPRepoSource{Repo: "https://github.com/zwenooo/magpie-agent-toolkit"}
		},
		"uninstalled":       func(s *Server) { s.RepoLocalCommit = "" },
		"different commit":  func(s *Server) { s.RepoCommit = strings.Repeat("d", 40) },
		"different hash":    func(s *Server) { s.RepoHash = "different" },
		"remote":            func(s *Server) { s.Transport = "http" },
		"different command": func(s *Server) { s.Command = "ssh" },
		"replaced script":   func(s *Server) { s.Args = []string{"other.py"} },
		"missing program":   func(s *Server) { s.Name = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			c := s
			src := *s.RepoSource
			c.RepoSource, c.RepoLocalSource = &src, &src
			modify(&c)
			if supportsDirectUse(&c) {
				t.Fatal("unsupported entry attributed")
			}
		})
	}
	s.Env[directUseDB] = "spoof"
	s.Env[strings.ToLower(directUseItem)] = "wrong"
	s.Env[directUseAgent] = "spoof"
	want := maps.Clone(s.Env)
	for _, f := range []*mcpFile{{WSL: true, Path: filepath.Join(h, "wsl")}, {Path: `\\server\share\agent.json`}, {Path: `\\wsl.localhost\Ubuntu\agent.json`}} {
		g := nativeDirectUse(&s, here(&s), "http://localhost", f, "claude")
		for k := range g.Env {
			if directUseKey(k) {
				t.Fatalf("nonlocal file got %s", k)
			}
		}
	}
	if !maps.Equal(want, s.Env) {
		t.Fatal("mutated caller env")
	}
	if _, _, _, _, _, bad := startServer(context.Background(), &s, "unopened-distro", ""); bad == nil || !strings.Contains(bad.Detail, "WSL") {
		t.Fatal("expected pre-launch refusal")
	}
}

// The child fails its handshake if either a parent or an explicit marker
// survived. It also requires an unrelated entry env to survive unchanged.
func TestOpsDirectUseChild(t *testing.T) {
	if os.Getenv("DIRECT_USE_FIXTURE") != "child" {
		t.Skip("subprocess only")
	}
	for _, k := range []string{directUseDB, directUseItem, directUseAgent} {
		if _, ok := os.LookupEnv(k); ok {
			os.Exit(19)
		}
	}
	if os.Getenv("OTHER_ENTRY_ENV") != "preserved" {
		os.Exit(20)
	}
	TestFakeRunServer(t)
}

func TestOpsDirectUseProcessEnvironment(t *testing.T) {
	sandbox(t)
	t.Setenv(directUseDB, "from-parent")
	t.Setenv(strings.ToLower(directUseItem), "from-parent")
	t.Setenv(directUseAgent, "from-parent")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Name: "fake", Transport: "stdio", Command: self, Args: []string{"-test.run=^TestOpsDirectUseChild$"}, Env: map[string]string{"DIRECT_USE_FIXTURE": "child", "MAGPIE_FAKE_RUN": filepath.Join(t.TempDir(), "starts"), "OTHER_ENTRY_ENV": "preserved", directUseItem: "from-entry", strings.ToLower(directUseDB): "from-entry", strings.ToLower(directUseAgent): "from-entry"}}
	if health := checkStdioIn(context.Background(), s, "", ""); health.State != "ok" {
		t.Fatalf("probe/hub launch: %+v", health)
	}
	_, spec, ok := runVia(s, "http://localhost")
	if !ok || len(spec.Env) != 3 {
		t.Fatalf("run spec: %+v", spec)
	}
	spec.Env[directUseItem] = "from-spec"
	spec.Env[strings.ToLower(directUseAgent)] = "from-spec"
	child, _, err := runStart(&runSession{name: s.Name}, spec, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	child.end()
	<-child.tree.Stop(0)
	<-child.done
}
