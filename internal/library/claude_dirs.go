package library

// Claude Code kept in config folders of its own (zwenooo/magpie fork).
//
// Claude Code reads everything from $CLAUDE_CONFIG_DIR when it is set, and
// the library is given to one such folder: the one Claude Code here uses
// (claudeDir). An account switcher runs each account's Claude Code in a
// folder of its own (ReClaude's ~/.rec/claude/accounts/<id>/config, magpie's
// saved accounts under claude-accounts), and those got nothing of the
// library: each kept its own hand-made servers and skills, or none.
//
// Each sync now gives every other Claude Code folder on this computer what
// Claude Code here was given, as it was written there: the same MCP
// servers (this computer's paths, the router's address when it is on),
// links to the same skills, and the same instructions part. What magpie
// put in each folder is recorded in claude-dirs.json in magpie's folder, on
// this computer alone and never carried, so a server or skill no longer
// given is taken out of it again; nothing the user put there is touched.
// A server of the same name already there is replaced with the library's
// (it is the same server, by hand); a skill folder of the account's own by
// a skill's name is left and reported. Hooks, subagents and tool deny rules
// aren't given: they are Claude Code here's alone.
//
// The folders are ReClaude's and magpie's own accounts' that Claude Code
// has run in (a .claude.json there), and any listed in claude-dirs.json by
// hand (AddClaudeDir); never claudeDir itself.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/claudelock"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/settings"
)

// claudeDirsFile is this computer's record of the other Claude Code
// folders: those added by hand, and what magpie put in each.
func claudeDirsFile() string { return filepath.Join(settings.Dir(), "claude-dirs.json") }

type claudeDirsState struct {
	// Dirs are the folders added by hand, as given.
	Dirs []string `json:"dirs,omitempty"`
	// Given is what magpie put in each folder, by its cleaned path.
	Given map[string]*claudeDirGiven `json:"given,omitempty"`
}

type claudeDirGiven struct {
	MCP          []string `json:"mcp,omitempty"`
	Skills       []string `json:"skills,omitempty"`
	Instructions bool     `json:"instructions,omitempty"`
}

func (g *claudeDirGiven) empty() bool {
	return len(g.MCP) == 0 && len(g.Skills) == 0 && !g.Instructions
}

func readClaudeDirs() (*claudeDirsState, error) {
	st := &claudeDirsState{}
	b, err := os.ReadFile(claudeDirsFile())
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("%s: %w", claudeDirsFile(), err)
	}
	return st, nil
}

func (st *claudeDirsState) save() error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return edit.WriteAtomic(claudeDirsFile(), append(b, '\n'))
}

// sameDir is whether two folders are one: case aside on Windows.
func sameDir(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// dirKey is a folder as claude-dirs.json records it.
func dirKey(d string) string {
	d = filepath.Clean(d)
	if runtime.GOOS == "windows" {
		return strings.ToLower(d)
	}
	return d
}

// claudeDirID names a folder's part of a sync: in its problems, its
// changes and its backups.
func claudeDirID(d string) string {
	sum := sha256.Sum256([]byte(dirKey(d)))
	return "claude@dir." + hex.EncodeToString(sum[:])[:8]
}

// foundClaudeDirs are the account switchers' folders Claude Code has run in.
func foundClaudeDirs() []string {
	var out []string
	for _, pattern := range []string{
		filepath.Join(home(), ".rec", "claude", "accounts", "*", "config"), // ReClaude
		filepath.Join(settings.Dir(), "claude-accounts", "*"),              // magpie's saved accounts
	} {
		ms, _ := filepath.Glob(pattern)
		for _, d := range ms {
			if strings.HasPrefix(filepath.Base(d), "signin-") {
				continue // a sign-in being made
			}
			if fi, err := os.Stat(filepath.Join(d, ".claude.json")); err == nil && fi.Mode().IsRegular() {
				out = append(out, d)
			}
		}
	}
	return out
}

// ClaudeDirs are the other Claude Code folders the library is given to:
// those found and those added by hand, never Claude Code here's own.
func ClaudeDirs() ([]string, error) {
	st, err := readClaudeDirs()
	if err != nil {
		return nil, err
	}
	return st.dirs(), nil
}

// ClaudeDirView is another Claude Code folder and what magpie put in it.
type ClaudeDirView struct {
	Dir          string   `json:"dir"`
	ByHand       bool     `json:"byHand,omitempty"`
	MCP          []string `json:"mcp"`
	Skills       []string `json:"skills"`
	Instructions bool     `json:"instructions,omitempty"`
}

// ClaudeDirsView are the other Claude Code folders, each with what magpie
// put there at the last sync.
func ClaudeDirsView() ([]ClaudeDirView, error) {
	st, err := readClaudeDirs()
	if err != nil {
		return nil, err
	}
	var out []ClaudeDirView
	for _, d := range st.dirs() {
		v := ClaudeDirView{Dir: d, MCP: []string{}, Skills: []string{},
			ByHand: slices.ContainsFunc(st.Dirs, func(o string) bool { return sameDir(o, d) })}
		if g := st.Given[dirKey(d)]; g != nil {
			v.MCP, v.Skills, v.Instructions = slices.Clone(g.MCP), slices.Clone(g.Skills), g.Instructions
		}
		out = append(out, v)
	}
	return out, nil
}

func (st *claudeDirsState) dirs() []string {
	var out []string
	for _, d := range append(foundClaudeDirs(), st.Dirs...) {
		if sameDir(d, claudeDir()) || slices.ContainsFunc(out, func(o string) bool { return sameDir(o, d) }) {
			continue
		}
		out = append(out, filepath.Clean(d))
	}
	return out
}

// AddClaudeDir gives the library to Claude Code kept in folder d too.
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
	mu.Lock()
	st, err := readClaudeDirs()
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

// RemoveClaudeDir stops giving the library to folder d, added by hand,
// taking out what magpie put there.
func RemoveClaudeDir(d string) (*Result, error) {
	abs, err := filepath.Abs(d)
	if err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	st, err := readClaudeDirs()
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(st.Dirs, func(o string) bool { return sameDir(o, abs) })
	if i < 0 {
		return nil, fmt.Errorf("%s wasn't added by hand", abs)
	}
	st.Dirs = slices.Delete(st.Dirs, i, i+1)
	res := &Result{Changed: []string{}, Problems: []Problem{}}
	l, err := load()
	if err != nil {
		return nil, err
	}
	if g := st.Given[dirKey(abs)]; g != nil {
		l.giveClaudeDir(abs, nil, nil, "", g, newBackups(), res)
		if g.empty() {
			delete(st.Given, dirKey(abs))
		}
	}
	return res, st.save()
}

// syncClaudeDirs gives each other Claude Code folder what Claude Code here
// was given (all is the sync's targets): the last step of a sync.
func (l *Library) syncClaudeDirs(all []*Target, b *backups, res *Result) {
	var here *Target
	for _, t := range all {
		if t.Agent.ID == "claude" && t.Agent.WSL == "" {
			here = t
		}
	}
	st, err := readClaudeDirs()
	if err != nil {
		res.fail("claude@dirs", "claude-dirs", err)
		return
	}
	dirs := st.dirs()
	if st.Given == nil {
		st.Given = map[string]*claudeDirGiven{}
	}
	if here == nil {
		// Claude Code isn't here (or not now): what each folder was given
		// stays as it is, as an agent gone keeps its own
		return
	}
	a := l.Applied["claude"]
	if a == nil {
		a = &Applied{}
	}
	servers := map[string]map[string]any{}
	if here.MCP != nil && len(a.MCP) > 0 {
		entries, err := here.MCP.entries()
		if err != nil {
			res.fail("claude@dirs", "mcp", err)
			return
		}
		for _, name := range a.MCP {
			if e, ok := entries[name]; ok {
				servers[name] = e
			}
		}
	}
	text := l.wanted("claude")
	// a folder gone since (an account removed) is forgotten
	for k := range st.Given {
		if slices.ContainsFunc(dirs, func(d string) bool { return dirKey(d) == k }) {
			continue
		}
		if _, err := os.Stat(k); errors.Is(err, os.ErrNotExist) {
			delete(st.Given, k)
		}
	}
	for _, d := range dirs {
		g := st.Given[dirKey(d)]
		if g == nil {
			g = &claudeDirGiven{}
		}
		l.giveClaudeDir(d, servers, a.Skills, text, g, b, res)
		if g.empty() {
			delete(st.Given, dirKey(d))
		} else {
			st.Given[dirKey(d)] = g
		}
	}
	if err := st.save(); err != nil {
		res.fail("claude@dirs", "claude-dirs", err)
	}
}

// giveClaudeDir makes folder d hold servers, links to skills and the
// instructions part text (nil and "" for none), taking out what magpie
// put there before (g) that isn't among them; g becomes what d holds of
// magpie's now.
func (l *Library) giveClaudeDir(d string, servers map[string]map[string]any, skills []string, text string, g *claudeDirGiven, b *backups, res *Result) {
	id := claudeDirID(d)
	fail := func(what string, err error) { res.fail(id, what, fmt.Errorf("%s: %w", d, err)) }

	// MCP servers, in its .claude.json: read and written under Claude
	// Code's own lock, as Claude Code here's is (#4747)
	f := &mcpFile{Path: filepath.Join(d, ".claude.json"), Format: fmtClaude}
	if have, err := f.entries(); err != nil {
		fail("mcp", err)
	} else {
		var put, del []string
		for name, e := range servers {
			if !reflect.DeepEqual(have[name], e) {
				put = append(put, name)
			}
		}
		for _, name := range g.MCP {
			if _, keep := servers[name]; !keep {
				if _, there := have[name]; there {
					del = append(del, name)
				}
			}
		}
		slices.Sort(put)
		slices.Sort(del)
		if len(put)+len(del) > 0 {
			if err := l.editClaudeJSON(id, f, servers, put, del, b); err != nil {
				fail("mcp", err)
			} else {
				res.changed(id)
				g.MCP = sortedKeys(servers)
			}
		} else {
			g.MCP = sortedKeys(servers)
		}
	}

	// skills: links to the library's, in its skills folder
	sd := filepath.Join(d, "skills")
	var now []string
	for _, name := range skills {
		p := filepath.Join(sd, name)
		if ours(p, name) {
			now = append(now, name)
			continue
		}
		if fi, err := os.Lstat(p); err == nil {
			if !linkEntry(fi) {
				fail("skill:"+name, fmt.Errorf("its skills folder has a %s of its own; the library's isn't linked over it", name))
				continue
			}
			// a link to another copy (a project's, an old checkout's)
			if err := os.Remove(p); err != nil {
				fail("skill:"+name, err)
				continue
			}
		}
		if err := link(p, name); err != nil {
			fail("skill:"+name, err)
			continue
		}
		res.changed(id)
		now = append(now, name)
	}
	for _, name := range g.Skills {
		if slices.Contains(skills, name) {
			continue
		}
		p := filepath.Join(sd, name)
		if !ours(p, name) {
			continue // replaced by the user since
		}
		var err error
		if _, isLink := linkTarget(p); isLink {
			err = os.Remove(p)
		} else {
			err = os.RemoveAll(p) // magpie's marked copy
		}
		if err != nil {
			fail("skill:"+name, err)
			now = append(now, name)
			continue
		}
		res.changed(id)
	}
	slices.Sort(now)
	g.Skills = now

	// instructions: magpie's part of its CLAUDE.md
	if text != "" || g.Instructions {
		p := filepath.Join(d, "CLAUDE.md")
		raw, err := os.ReadFile(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			fail("instructions", err)
			return
		}
		was := strings.ReplaceAll(string(raw), "\r\n", "\n")
		want := withBlock(was, text)
		if strings.TrimSpace(want) != strings.TrimSpace(was) {
			if err := b.keep(id, p); err != nil {
				fail("instructions", err)
				return
			}
			if err := writeText(p, want); err != nil {
				fail("instructions", err)
				return
			}
			res.changed(id)
		}
		g.Instructions = text != ""
	}
}

// editClaudeJSON puts servers' entries named in put into f, and takes the
// ones named in del out, at once, under Claude Code's lock on the file;
// the file as it was is kept with the sync's backups.
func (l *Library) editClaudeJSON(id string, f *mcpFile, servers map[string]map[string]any, put, del []string, b *backups) error {
	release, err := claudelock.Config(f.Path)
	if err != nil {
		return err
	}
	defer release()
	if err := b.keep(id, f.Path); err != nil {
		return err
	}
	return edit.Atomically(func() error {
		for _, name := range put {
			if err := edit.SetJSON(f.Path, edit.KV{Path: f.key() + "." + name, Value: servers[name]}); err != nil {
				return err
			}
		}
		if len(del) > 0 {
			paths := make([]string, len(del))
			for i, name := range del {
				paths[i] = f.key() + "." + name
			}
			return edit.DelJSON(f.Path, paths...)
		}
		return nil
	}, f.Path)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
