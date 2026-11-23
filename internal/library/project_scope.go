package library

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/jsonc"
	"github.com/yetone/magpie/internal/claudelock"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/gateway"
)

// A project is the library's global set, the servers and skills given to
// each agent, with its own added (Project.Servers, Project.Skills) and
// some of the global ones turned off there (Project.ServersOff,
// Project.SkillsOff). Turning one off is done the way each agent reads it
// for a folder:
//
//   - Claude Code keeps, for each project, a record in ~/.claude.json
//     under projects[<the repository's main top folder>] (the git root,
//     the main worktree's for a worktree; the folder itself outside git:
//     Claude Code 2.1.316's project key). Its disabledMcpServers turns a
//     server off there, whatever scope it is from, and its mcpServers are
//     the local scope, which wins a name over the user scope. That record
//     is not in the project, so nothing goes into its git.
//   - Codex reads .codex/config.toml in a trusted project over its
//     ~/.codex/config.toml, table by table (config layers): a server's
//     table with enabled = false turns it off there.
//   - Claude Code's skillOverrides, in the project's
//     .claude/settings.local.json, turns a skill off ("off": neither the
//     model nor the user sees it).
//
// With the router on, an agent's servers are one entry (HubEntry). A
// project that adds or turns off a server the router serves gives Claude
// Code and Codex its own hub entry, at the hub's address with the
// project's key after the agent's id (gateway.HubProjectSep): the router
// serves that address the project's set, starting each server once
// however many projects use it. Claude Code's goes in the local scope
// above, Codex's in the project's .codex/config.toml.

// projectOffAgents are the agents a global server can be turned off for
// in one project, and projectSkillOffAgents those a skill can.
var (
	projectOffAgents      = []string{"claude", "codex"}
	projectSkillOffAgents = []string{"claude"}
)

// ProjectOffAgents are the agents a project can turn a global server off
// for, and ProjectSkillOffAgents a global skill.
func ProjectOffAgents() []string      { return slices.Clone(projectOffAgents) }
func ProjectSkillOffAgents() []string { return slices.Clone(projectSkillOffAgents) }

// claudeLocal is what magpie wrote in Claude Code's record of a project.
type claudeLocal struct {
	// Root is the record's key (claudeRoot)
	Root string `json:"root"`
	// Servers are the entries magpie wrote in its mcpServers
	Servers []string `json:"servers,omitempty"`
	// Off are the names magpie put in its disabledMcpServers
	Off []string `json:"off,omitempty"`
}

// claudeRoot is the key Claude Code keeps dir's project under: the main
// worktree's top folder in a git repository, else dir itself, with
// forward slashes on Windows.
func claudeRoot(dir string) string {
	r := dir
	if g, ok := gitOf(dir); ok {
		if m := g.mainTop(); m != "" {
			r = m
		} else {
			r = g.Top
		}
	}
	return filepath.ToSlash(filepath.Clean(r))
}

// jsonKey is a key as one part of a gjson path; ok is false for one that
// a path can't name plainly.
func jsonKey(k string) (string, bool) {
	if strings.ContainsAny(k, `*?|#@\"`) {
		return "", false
	}
	return strings.ReplaceAll(k, ".", `\.`), true
}

// claudeDefaults are the fields Claude Code gives a project's record when
// it makes one (2.1.316), for a record magpie makes first.
func claudeDefaults() map[string]any {
	return map[string]any{
		"allowedTools": []any{}, "mcpContextUris": []any{}, "mcpServers": map[string]any{},
		"enabledMcpjsonServers": []any{}, "disabledMcpjsonServers": []any{},
		"hasTrustDialogAccepted": false, "hasClaudeMdExternalIncludesApproved": false,
		"hasClaudeMdExternalIncludesWarningShown": false,
	}
}

// offFor are the servers (or skills) of names turned off in the project
// for agent, sorted.
func offFor(m map[string][]string, agent string) []string {
	var out []string
	for name, ids := range m {
		if slices.Contains(ids, agent) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// projectHubs are the hub entries the project gives Claude Code and Codex
// (by agent): with the router on, one for an agent the project adds a
// server the router serves to, or turns one off for.
func (l *Library) projectHubs(p *Project) map[string]*Server {
	if !l.MCPHub || l.server(HubEntry) != nil {
		return nil
	}
	out := map[string]*Server{}
	for _, id := range projectOffAgents {
		if !slices.ContainsFunc(l.MCP, func(s *Server) bool {
			return inHub(s, id) && (!s.Hidden && slices.Contains(p.Servers[s.Name], id) || slices.Contains(p.ServersOff[s.Name], id))
		}) {
			continue
		}
		t := targetByID(id)
		if t == nil || t.MCP == nil {
			continue
		}
		hs := l.hubServerFor(t)
		if t.MCP.supports(hs) != nil {
			continue
		}
		// an entry called magpie of the user's own is left to win
		if es, err := t.MCP.entries(); err != nil {
			continue
		} else if e := es[HubEntry]; e != nil {
			if u, _ := e["url"].(string); !strings.Contains(u, gateway.HubMCPPath) {
				continue
			}
		}
		hs.URL += url.PathEscape(gateway.HubProjectSep + projectKey(p.Dir))
		out[id] = hs
	}
	return out
}

// projectByKey is the project the router knows by key.
func (l *Library) projectByKey(key string) *Project {
	for _, p := range l.Projects {
		if projectKey(p.Dir) == key {
			return p
		}
	}
	return nil
}

// syncClaudeLocal keeps Claude Code's record of the project holding the
// project's hub entry (hub, or nil) in its local scope and the servers it
// turns off in its disabledMcpServers; what magpie wrote there before and
// no longer wants comes out, and nothing else of the record changes.
func (l *Library) syncClaudeLocal(p *Project, hub *Server, fail func(what string, err error)) {
	want := l.claudeOff(p)
	if p.Claude == nil && hub == nil && len(want) == 0 {
		return
	}
	path := claudeJSON()
	if !exists(path) {
		if hub != nil || len(want) > 0 {
			fail("claude", fmt.Errorf("Claude Code hasn't run on this computer yet: %s isn't there", path))
		}
		return
	}
	release, err := claudelock.Config(path)
	if err != nil {
		fail("claude", err)
		return
	}
	defer release()
	root := claudeRoot(p.Dir)
	if p.Claude != nil && p.Claude.Root != root {
		if _, _, err := writeClaudeLocal(path, p.Claude.Root, p.Claude, nil, nil, nil); err != nil {
			fail("claude", err)
			return
		}
		p.Claude = nil
	}
	p.Claude, _, err = writeClaudeLocal(path, root, p.Claude, hub, want, nil)
	if err != nil {
		fail("claude", err)
	}
}

func (l *Library) claudeOff(p *Project) []string {
	var want []string
	for _, name := range offFor(p.ServersOff, "claude") {
		if l.server(name) != nil {
			want = append(want, name)
		}
	}
	return want
}

// writeClaudeLocal reconciles one project under the target file's Claude lock.
// Receipts never cross account boundaries, even for the same project root.
func writeClaudeLocal(path, root string, prev *claudeLocal, hub *Server, want []string, backup func() error) (*claudeLocal, bool, error) {
	k, ok := jsonKey(root)
	if !ok {
		return prev, false, fmt.Errorf("Claude Code's record of %s can't be written by magpie", root)
	}
	base := "projects." + k
	raw, err := edit.Read(path)
	if err != nil {
		return prev, false, err
	}
	plain := jsonc.ToJSON(raw)
	if len(raw) > 0 && (!json.Valid(plain) || !gjson.ParseBytes(plain).IsObject()) {
		return prev, false, fmt.Errorf("%s: expected a JSON object", path)
	}
	for _, key := range []string{"projects", base, base + ".mcpServers"} {
		if value := gjson.GetBytes(plain, key); value.Exists() && !value.IsObject() {
			return prev, false, fmt.Errorf("%s: expected an object at %s", path, key)
		}
	}
	var put []edit.KV
	var del []string
	if !gjson.GetBytes(plain, base).Exists() && (hub != nil || len(want) > 0) {
		put = append(put, edit.KV{Path: base, Value: claudeDefaults()})
	}
	rec := &claudeLocal{Root: root}
	if prev != nil {
		rec.Servers, rec.Off = prev.Servers, prev.Off
	}
	// the hub's entry
	var servers []string
	for _, name := range rec.Servers {
		if hub != nil && name == hub.Name {
			continue
		}
		key := base + ".mcpServers." + name
		if gjson.GetBytes(plain, key).Exists() {
			del = append(del, key)
		}
	}
	var conflict error
	if hub != nil {
		var old map[string]any
		key := base + ".mcpServers." + hub.Name
		value := gjson.GetBytes(plain, key)
		raw, exists := value.Raw, value.Exists()
		if exists && !slices.Contains(rec.Servers, hub.Name) {
			conflict = fmt.Errorf("Claude Code has a server called %s for %s already that isn't magpie's: it's left as it is", hub.Name, root)
		} else {
			if exists {
				if err := json.Unmarshal([]byte(raw), &old); err != nil {
					return prev, false, err
				}
			}
			body, err := json.Marshal((&mcpFile{Format: fmtClaude}).merged(hub, old))
			if err != nil {
				return prev, false, err
			}
			if !exists || !sameJSON(raw, string(body)) {
				put = append(put, edit.KV{Path: key, Value: json.RawMessage(body)})
			}
			servers = append(servers, hub.Name)
		}
	}
	rec.Servers = servers
	// the servers turned off: the user's own names there stay theirs
	var cur []string
	if value := gjson.GetBytes(plain, base+".disabledMcpServers"); value.Exists() {
		if err := json.Unmarshal([]byte(value.Raw), &cur); err != nil {
			return prev, false, err
		}
	}
	next, mine := offList(cur, rec.Off, want)
	if !slices.Equal(next, cur) {
		put = append(put, edit.KV{Path: base + ".disabledMcpServers", Value: next})
	}
	rec.Off = mine
	changed := len(put)+len(del) > 0
	if changed {
		body, err := edit.PatchJSON(raw, put, del)
		if err != nil {
			return prev, false, err
		}
		if backup != nil {
			if err := backup(); err != nil {
				return prev, false, err
			}
		}
		if err := edit.WriteAtomic(path, body); err != nil {
			return prev, false, err
		}
	}
	if len(rec.Servers) == 0 && len(rec.Off) == 0 {
		rec = nil
	}
	return rec, changed, conflict
}

// codexOffRel is Codex's file in a project.
var codexOffRel = projectMCPFiles["codex"].rel

// syncCodexOff keeps enabled = false in the project's .codex/config.toml
// for each server the project turns off for Codex that Codex's own
// config.toml has: a table naming a server Codex doesn't have would be
// one without a command, which Codex refuses to start with. A server the
// router serves isn't in it, and is left out of the project's hub entry
// instead.
func (l *Library) syncCodexOff(p *Project, fail func(what string, err error)) {
	want := offFor(p.ServersOff, "codex")
	if len(want) == 0 && len(p.CodexOff) == 0 {
		return
	}
	var have map[string]map[string]any
	if len(want) > 0 {
		if t := targetByID("codex"); t != nil && t.MCP != nil {
			have, _ = t.MCP.entries()
		}
	}
	want = slices.DeleteFunc(want, func(n string) bool { return have[n] == nil })
	abs := filepath.Join(p.Dir, filepath.FromSlash(codexOffRel))
	if err := p.inside(abs); err != nil {
		fail("mcp", err)
		return
	}
	var mine []string
	for _, name := range p.CodexOff {
		if slices.Contains(want, name) {
			continue
		}
		if err := edit.DelTOMLKey(abs, "mcp_servers."+name, "enabled"); err != nil {
			fail("mcp:"+name, err)
			mine = append(mine, name)
		}
	}
	for _, name := range want {
		if !exists(abs) {
			if err := p.mkdirs(filepath.Dir(abs)); err != nil {
				fail("mcp:"+name, err)
				continue
			}
			if !slices.Contains(p.MadeFiles, codexOffRel) {
				p.MadeFiles = append(p.MadeFiles, codexOffRel)
			}
		}
		if err := edit.SetTOMLKey(abs, "mcp_servers."+name, "enabled", false); err != nil {
			fail("mcp:"+name, err)
			continue
		}
		mine = append(mine, name)
	}
	sort.Strings(mine)
	p.CodexOff = mine
}

// claudeSettingsRel is the file in a project Claude Code reads the user's
// own settings for it from, which it keeps out of git itself.
const claudeSettingsRel = ".claude/settings.local.json"

// syncSkillsOff keeps skillOverrides.<skill> = "off" in the project's
// .claude/settings.local.json for each skill the project turns off for
// Claude Code; one the user set there themselves stays theirs.
func (l *Library) syncSkillsOff(p *Project, fail func(what string, err error)) {
	want := offFor(p.SkillsOff, "claude")
	if len(want) == 0 && len(p.Overrides) == 0 {
		return
	}
	abs := filepath.Join(p.Dir, filepath.FromSlash(claudeSettingsRel))
	if err := p.inside(abs); err != nil {
		fail("skills", err)
		return
	}
	var mine []string
	for _, name := range p.Overrides {
		if slices.Contains(want, name) {
			continue
		}
		k, _ := jsonKey(name)
		if v, ok := edit.GetJSON(abs, "skillOverrides."+k); ok && v != "off" {
			continue // the user's own by now
		}
		if err := edit.DelJSON(abs, "skillOverrides."+k); err != nil {
			fail(name, err)
			mine = append(mine, name)
		}
	}
	for _, name := range want {
		k, _ := jsonKey(name)
		if v, ok := edit.GetJSON(abs, "skillOverrides."+k); ok && !slices.Contains(p.Overrides, name) {
			if v != "off" {
				fail(name, fmt.Errorf("%s sets %s for this project already: it's left as it is", claudeSettingsRel, name))
			}
			continue
		}
		if !exists(abs) {
			if err := p.mkdirs(filepath.Dir(abs)); err != nil {
				fail(name, err)
				continue
			}
			if !slices.Contains(p.MadeFiles, claudeSettingsRel) {
				p.MadeFiles = append(p.MadeFiles, claudeSettingsRel)
			}
		}
		if err := edit.SetJSON(abs, edit.KV{Path: "skillOverrides." + k, Value: "off"}); err != nil {
			fail(name, err)
			continue
		}
		mine = append(mine, name)
	}
	sort.Strings(mine)
	p.Overrides = mine
	// the file holds the project's plugins' hooks too (project_plugins.go)
	if len(mine) == 0 && len(p.HookRecs["claude"]) == 0 && len(p.ClaudeWrote) == 0 {
		p.dropMadeFile(claudeSettingsRel)
	}
}

// dropMadeFile takes away a file magpie made once nothing is left in it;
// one with anything of the user's in it is theirs from then on.
func (p *Project) dropMadeFile(rel string) {
	i := slices.Index(p.MadeFiles, rel)
	if i < 0 {
		return
	}
	abs := filepath.Join(p.Dir, filepath.FromSlash(rel))
	if p.inside(abs) != nil {
		return
	}
	format := fmtClaude
	if strings.HasSuffix(rel, ".toml") {
		format = fmtCodex
	}
	if blankFile(abs, format) {
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return
		}
	}
	p.MadeFiles = slices.Delete(p.MadeFiles, i, i+1)
}

// ---- worktrees -------------------------------------------------------------

// syncTrees gives the project's folder in each other worktree of its
// repository the project's set, as its own project magpie keeps records
// of (Project.Trees), and takes it back out of a folder that is no longer
// one of them. A worktree gone from disk takes its records with it:
// nothing of magpie's is left there to take back. Claude Code keeps a
// worktree's project under the main worktree's record (claudeRoot), so
// that one serves them all.
func (l *Library) syncTrees(p *Project, res *Result, hubs map[string]*Server) {
	dirs := p.treeDirs()
	var trees []*Project
	for _, d := range dirs {
		t := p.tree(d)
		if t == nil {
			t = &Project{Dir: d}
		}
		t.Copy, t.Skills, t.Servers, t.ServersOff, t.SkillsOff, t.Subagents = p.Copy, p.Skills, p.Servers, p.ServersOff, p.SkillsOff, p.Subagents
		t.Instr, t.InstrAgents, t.Plugins, t.ClaudePlugins, t.Hooks, t.Pi = p.Instr, p.InstrAgents, p.Plugins, p.ClaudePlugins, p.Hooks, p.Pi
		l.syncFolder(t, res, hubs)
		trees = append(trees, t)
	}
	for _, t := range p.Trees {
		if slices.ContainsFunc(trees, func(x *Project) bool { return x.Dir == t.Dir }) {
			continue
		}
		if fi, err := os.Stat(t.Dir); err != nil || !fi.IsDir() {
			continue
		}
		// no longer one of the repository's worktrees, or no longer the
		// project's: what magpie put there comes out
		t.Skills, t.Servers, t.ServersOff, t.SkillsOff, t.Subagents = nil, nil, nil, nil, nil
		t.Instr, t.InstrAgents, t.Plugins, t.ClaudePlugins, t.Hooks, t.Pi = "", nil, nil, nil, nil, nil
		l.syncFolder(t, res, nil)
		if t.holds() {
			trees = append(trees, t)
		}
	}
	for _, t := range trees {
		t.Trees, t.Claude = nil, nil
	}
	p.Trees = trees
}

func (p *Project) tree(dir string) *Project {
	for _, t := range p.Trees {
		if t.Dir == dir {
			return t
		}
	}
	return nil
}

// holds says whether magpie has anything of its own in the folder still.
func (p *Project) holds() bool {
	return len(p.Placed) > 0 || len(p.Wrote) > 0 || len(p.CodexOff) > 0 || len(p.Overrides) > 0 || len(p.MadeFiles) > 0 || len(p.AgentFiles) > 0 || len(p.InstrFiles) > 0 || len(p.HookRecs) > 0 || len(p.ClaudeWrote) > 0 || len(p.PiRecs) > 0
}

// ignored are the files and folders of the project magpie put there, which
// are kept out of git.
func (p *Project) ignored() []string {
	out := slices.Concat(p.Placed, p.MadeFiles)
	for _, f := range p.AgentFiles {
		out = append(out, f.Path)
	}
	// a file magpie made for the project's instructions; one of the
	// user's, in git or not, stays as git has it
	for _, f := range p.InstrFiles {
		if f.Made {
			out = append(out, f.Path)
		}
	}
	return out
}

// treesChanged says whether a project's repository has a worktree its
// records don't, or lost one: the library is synced again then
// (keepProjectTrees), for a worktree made with git to follow its project.
func (l *Library) treesChanged() bool {
	for _, p := range l.Projects {
		// a folder made a repository since (git init): its entries go
		// into the new info/exclude
		if len(p.ignored()) > 0 {
			if g, ok := gitOf(p.Dir); ok {
				b, _ := os.ReadFile(filepath.Join(g.Common, "info", "exclude"))
				if !strings.Contains(string(b), excludeBegin(p.Dir)) {
					return true
				}
			}
		}
		if len(p.Skills)+len(p.Servers)+len(p.ServersOff)+len(p.SkillsOff)+len(p.Subagents)+len(p.InstrAgents)+len(p.Plugins)+len(p.Hooks)+len(p.ClaudePlugins)+len(p.Pi) == 0 && len(p.Trees) == 0 {
			continue
		}
		dirs := p.treeDirs()
		if len(dirs) != len(p.Trees) {
			return true
		}
		for _, d := range dirs {
			if p.tree(d) == nil {
				return true
			}
		}
	}
	return false
}

// keepProjectTrees syncs the library again when a project's repository
// has a new worktree, or lost one, or the project's folder became a
// repository.
func keepProjectTrees() {
	mu.Lock()
	l, err := load()
	mu.Unlock()
	if err != nil || !l.treesChanged() {
		return
	}
	if _, err := change(func(*Library) error { return nil }); err != nil {
		log.Println("library: a project's worktrees:", err)
	}
}

// ---- changing -------------------------------------------------------------

// ProjectServerOff sets which agents have a global server turned off in a
// project; none gives it back to the project as the global set has it.
func ProjectServerOff(dir, name string, agents []string) (*Result, error) {
	return change(func(l *Library) error {
		p := l.project(dir)
		if p == nil {
			return fmt.Errorf("no project %s", dir)
		}
		if l.server(name) == nil {
			return fmt.Errorf("no server called %s", name)
		}
		var ids []string
		for _, id := range agents {
			if !slices.Contains(projectOffAgents, id) {
				return fmt.Errorf("magpie can't turn a server off in one project for %s", id)
			}
			ids = set(ids, id, true)
		}
		if len(ids) == 0 {
			delete(p.ServersOff, name)
			return nil
		}
		if p.ServersOff == nil {
			p.ServersOff = map[string][]string{}
		}
		p.ServersOff[name] = ids
		return nil
	})
}

// ProjectSkillsOff sets which agents have each of some global skills
// turned off in a project; none gives them back.
func ProjectSkillsOff(dir string, names, agents []string) (*Result, error) {
	return change(func(l *Library) error {
		p := l.project(dir)
		if p == nil {
			return fmt.Errorf("no project %s", dir)
		}
		if len(names) == 0 {
			return fmt.Errorf("no skills given")
		}
		for _, name := range names {
			if l.skill(name) == nil {
				return fmt.Errorf("no skill called %s", name)
			}
		}
		var ids []string
		for _, id := range agents {
			if !slices.Contains(projectSkillOffAgents, id) {
				return fmt.Errorf("magpie can't turn a skill off in one project for %s", id)
			}
			ids = set(ids, id, true)
		}
		for _, name := range names {
			if len(ids) == 0 {
				delete(p.SkillsOff, name)
				continue
			}
			if p.SkillsOff == nil {
				p.SkillsOff = map[string][]string{}
			}
			p.SkillsOff[name] = slices.Clone(ids)
		}
		return nil
	})
}
