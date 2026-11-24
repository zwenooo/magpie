package library

// Claude Code folders added by hand, and what the fork's own sync of
// Claude Code's other folders gave them before they were claude# agents,
// zwenooo/magpie fork. The folders are rows of their own
// (agent.addAccountClaudeDirs) following Claude Code's Library
// (profile_follow.go); claude-dirs.json keeps the ones added by hand, and,
// until the first sync after the change, what each folder was given:
// migrateClaudeDirs hands that to the folder's own agent record, so
// nothing is written twice or taken out, and takes it out of a folder
// that is no row (magpie's saved accounts, which the gateway runs Claude
// Code in).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/claudelock"
	"github.com/yetone/magpie/internal/edit"
)

type claudeDirsFile struct {
	// Dirs are the folders added by hand, as given
	Dirs []string `json:"dirs,omitempty"`
	// Given is what the fork's earlier sync put in each folder, by its
	// cleaned path (lower case on Windows): gone once migrated
	Given map[string]*legacyGiven `json:"given,omitempty"`
}

type legacyGiven struct {
	Projects       map[string]*claudeLocal `json:"projects,omitempty"`
	RouterCommands []SubagentFile          `json:"routerCommands,omitempty"`
	MCP            []string                `json:"mcp,omitempty"`
	Skills         []string                `json:"skills,omitempty"`
	Instructions   bool                    `json:"instructions,omitempty"`
}

func readClaudeDirsFile() (*claudeDirsFile, error) {
	st := &claudeDirsFile{}
	b, err := os.ReadFile(agent.ClaudeDirsFile())
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("%s: %w", agent.ClaudeDirsFile(), err)
	}
	return st, nil
}

func (st *claudeDirsFile) save() error {
	if len(st.Dirs) == 0 && len(st.Given) == 0 {
		if err := os.Remove(agent.ClaudeDirsFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return edit.WriteAtomic(agent.ClaudeDirsFile(), append(b, '\n'))
}

// migrateClaudeDirs hands what the earlier sync gave each Claude Code
// folder to that folder's agent (all being the sync's agents): its
// record says the entries, links and files are magpie's, as if its own
// sync had written them. A folder that is no agent here has them taken
// out. Each is done once; one that fails is tried again next sync.
func (l *Library) migrateClaudeDirs(all []*Target, follow *followState, res *Result) {
	st, err := readClaudeDirsFile()
	if err != nil {
		res.fail("library", "claude-dirs", err)
		return
	}
	if len(st.Given) == 0 {
		return
	}
	b := l.kept
	if b == nil {
		b = newBackups()
	}
	for _, dir := range sortedKeys(st.Given) {
		g := st.Given[dir]
		i := slices.IndexFunc(all, func(t *Target) bool {
			_, ok := profileOf(t.Agent)
			return ok && t.Agent.Dir != "" && sameDir(t.Agent.Dir, dir)
		})
		if i < 0 {
			if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) || takeOutLegacy(dir, g, b, res) {
				delete(st.Given, dir)
			}
			continue
		}
		id := all[i].Agent.ID
		a := l.applied(id)
		for _, name := range g.MCP {
			a.MCP = set(a.MCP, name, true)
		}
		for _, name := range g.Skills {
			if name == RouterSkill {
				a.Router = true
				continue
			}
			a.Skills = set(a.Skills, name, true)
		}
		if len(a.RouterCommands) == 0 {
			a.RouterCommands = g.RouterCommands
		}
		a.Instructions = a.Instructions || g.Instructions
		path := filepath.Join(dir, ".claude.json")
		if all[i].MCP != nil {
			path = all[i].MCP.Path
		}
		for root, rec := range g.Projects {
			p := l.projectAt(root)
			if p == nil {
				// a project no longer listed: what was written for it goes
				if err := clearLegacyProject(path, rec); err != nil {
					res.fail(id, "project:claude", err)
				}
				continue
			}
			if follow.Projects == nil {
				follow.Projects = map[string]map[string]*claudeLocal{}
			}
			if follow.Projects[id] == nil {
				follow.Projects[id] = map[string]*claudeLocal{}
			}
			follow.Projects[id][projectKey(p.Dir)] = rec
		}
		delete(st.Given, dir)
	}
	if err := st.save(); err != nil {
		res.fail("library", "claude-dirs", err)
	}
}

// projectAt is the listed project whose Claude Code record is root.
func (l *Library) projectAt(root string) *Project {
	for _, p := range l.Projects {
		if claudeRoot(p.Dir) == root {
			return p
		}
	}
	return nil
}

func clearLegacyProject(path string, rec *claudeLocal) error {
	if rec == nil || !exists(path) {
		return nil
	}
	release, err := claudelock.Config(path)
	if err != nil {
		return err
	}
	defer release()
	return clearClaudeLocal(path, rec)
}

// takeOutLegacy takes what the earlier sync gave dir back out of it:
// whatever the user changed since stays. It says whether all of it went.
func takeOutLegacy(dir string, g *legacyGiven, b *backups, res *Result) bool {
	id := "claude-dirs"
	ok := true
	fail := func(what string, err error) {
		res.fail(id, what, fmt.Errorf("%s: %w", dir, err))
		ok = false
	}
	f := &mcpFile{Path: filepath.Join(dir, ".claude.json"), Format: fmtClaude}
	for _, rec := range g.Projects {
		if err := clearLegacyProject(f.Path, rec); err != nil {
			fail("project:claude", err)
		}
	}
	if len(g.MCP) > 0 && exists(f.Path) {
		if have, err := f.entries(); err != nil {
			fail("mcp", err)
		} else {
			var del []string
			for _, name := range g.MCP {
				if _, there := have[name]; there {
					del = append(del, f.key()+"."+name)
				}
			}
			if len(del) > 0 {
				if err := takeOutEntries(f.Path, del, b); err != nil {
					fail("mcp", err)
				}
			}
		}
	}
	for _, name := range g.Skills {
		p := filepath.Join(dir, "skills", name)
		if !ours(p, name) && !(name == RouterSkill && routerOurs(p)) {
			continue // the user's since
		}
		var err error
		if _, isLink := linkTarget(p); isLink {
			err = os.Remove(p)
		} else {
			err = os.RemoveAll(p)
		}
		if err != nil {
			fail("skill:"+name, err)
		}
	}
	if left := syncOwnFiles(&Target{Agent: &agent.Agent{ID: id, Name: dir}}, "command", g.RouterCommands, nil, b, res); len(left) > 0 {
		ok = false
	}
	if g.Instructions {
		p := filepath.Join(dir, "CLAUDE.md")
		raw, err := os.ReadFile(p)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			fail("instructions", err)
		default:
			was := strings.ReplaceAll(string(raw), "\r\n", "\n")
			if want := withBlock(was, ""); strings.TrimSpace(want) != strings.TrimSpace(was) {
				if err := b.keep(id, p); err != nil {
					fail("instructions", err)
				} else if err := writeText(p, want); err != nil {
					fail("instructions", err)
				}
			}
		}
	}
	return ok
}

// takeOutEntries deletes keys from Claude Code's file at path, under its
// lock, the file as it was kept with the backups first.
func takeOutEntries(path string, keys []string, b *backups) error {
	release, err := claudelock.Config(path)
	if err != nil {
		return err
	}
	defer release()
	if err := b.keep("claude-dirs", path); err != nil {
		return err
	}
	return edit.DelJSON(path, keys...)
}

// AddClaudeDir makes Claude Code kept in folder d a row of its own that
// follows Claude Code's Library.
func AddClaudeDir(d string) (*Result, error) {
	abs, err := filepath.Abs(d)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a folder", abs)
	}
	if sameDir(abs, claudeDir()) {
		return nil, fmt.Errorf("%s is Claude Code's own folder, which the library is given to already", abs)
	}
	if !agent.IsClaudeConfigDir(abs) {
		return nil, fmt.Errorf("%s holds no Claude Code's files (settings.json, .credentials.json or projects): run Claude Code with CLAUDE_CONFIG_DIR there first", abs)
	}
	mu.Lock()
	st, err := readClaudeDirsFile()
	if err == nil && !slices.ContainsFunc(st.Dirs, func(o string) bool { return sameDir(o, abs) }) {
		st.Dirs = append(st.Dirs, abs)
		err = st.save()
	}
	mu.Unlock()
	if err != nil {
		return nil, err
	}
	return Sync()
}

// RemoveClaudeDir makes a folder added by hand no row again, first
// taking out everything magpie put there, while it is one.
func RemoveClaudeDir(d string) (*Result, error) {
	abs, err := filepath.Abs(d)
	if err != nil {
		return nil, err
	}
	st, err := readClaudeDirsFile()
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(st.Dirs, func(o string) bool { return sameDir(o, abs) })
	if i < 0 {
		return nil, fmt.Errorf("%s wasn't added by hand", abs)
	}
	var id string
	for _, t := range Targets() {
		if _, ok := profileOf(t.Agent); ok && sameDir(t.Agent.Dir, abs) {
			id = t.Agent.ID
		}
	}
	res := &Result{Changed: []string{}, Problems: []Problem{}}
	if id != "" {
		// it follows no more, and is ticked for nothing: the sync takes
		// out what it holds of magpie's
		if res, err = change(func(l *Library) error {
			follow, err := readFollow()
			if err != nil {
				return err
			}
			if follow.Follow == nil {
				follow.Follow = map[string]bool{}
			}
			follow.Follow[id] = false
			if err := follow.clearProjects(id); err != nil {
				return err
			}
			if err := follow.save(); err != nil {
				return err
			}
			l.forAgentLists(func(list *[]string) { *list = set(*list, id, false) })
			delete(l.SkillHow, id)
			return nil
		}); err != nil {
			return res, err
		}
		if slices.ContainsFunc(res.Problems, func(p Problem) bool { return p.Agent == id }) {
			return res, nil // tried again on the next rm
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if st, err = readClaudeDirsFile(); err != nil {
		return res, err
	}
	st.Dirs = slices.DeleteFunc(st.Dirs, func(o string) bool { return sameDir(o, abs) })
	if err := st.save(); err != nil {
		return res, err
	}
	if id != "" {
		follow, err := readFollow()
		if err != nil {
			return res, err
		}
		delete(follow.Follow, id)
		return res, follow.save()
	}
	return res, nil
}
