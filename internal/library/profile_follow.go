package library

// A profile agent that follows its agent's Library, zwenooo/magpie fork.
// An agent kept in more than one folder is an agent of its own per folder,
// agent#profile (claude#work, hermes#work, omp#work, claude#rec-48142 for
// a ReClaude account: agent.claudeDirAgents), ticked on its own. One that
// follows is given what its agent is given instead: each sync first ticks
// it wherever its agent is ticked, and nowhere else, in every agents list
// of the library (forAgentLists), so every part of a sync gives it the same
// as any agent, for any agent that has profiles. An account switcher's
// Claude Code folder, and one added by hand, follows by default
// (agent.AccountClaudeDir); the user turns any profile's following on or
// off (Follow), and one turned off keeps its ticks as they are, its own
// from then on. Claude Code also keeps a project's servers turned off and
// its hub entry in its own folder's record, not the project's, so a
// following Claude Code folder gets those as Claude Code's
// (syncFollowersClaudeLocal). What this computer's profiles follow, and
// what magpie wrote in their records, is kept in library-follow.json in
// magpie's folder: never carried, as the folders are this computer's.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/claudelock"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/settings"
)

func followFile() string { return filepath.Join(settings.Dir(), "library-follow.json") }

type followState struct {
	// Follow is the user's own choice for a profile, by its id; one not
	// named follows by default when it is an account switcher's Claude
	// Code folder or one added by hand
	Follow map[string]bool `json:"follow,omitempty"`
	// Followers are the profiles the last sync kept in step, by id
	Followers map[string]*follower `json:"followers,omitempty"`
	// Projects are, by follower and by project (projectKey), what magpie
	// wrote in its Claude Code record of the project, as Project.Claude is
	// for Claude Code's own
	Projects map[string]map[string]*claudeLocal `json:"projects,omitempty"`
}

type follower struct {
	Parent string `json:"parent"`
	// ClaudeJSON is, for a Claude Code folder, the file holding its record
	// of each project
	ClaudeJSON string `json:"claudeJSON,omitempty"`
}

func readFollow() (*followState, error) {
	st := &followState{}
	b, err := os.ReadFile(followFile())
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("%s: %w", followFile(), err)
	}
	return st, nil
}

func (st *followState) save() error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return edit.WriteAtomic(followFile(), append(b, '\n'))
}

// profileOf is the agent a profile agent is one of: claude for claude#work.
// A WSL distro's agents have no profiles here.
func profileOf(a *agent.Agent) (string, bool) {
	parent, _, ok := strings.Cut(a.ID, "#")
	return parent, ok && a.WSL == "" && parent != ""
}

// follows says whether t follows its agent, and which agent that is.
func (st *followState) follows(t *Target) (string, bool) {
	parent, ok := profileOf(t.Agent)
	if !ok {
		return "", false
	}
	if on, set := st.Follow[t.Agent.ID]; set {
		return parent, on
	}
	return parent, parent == "claude" && agent.AccountClaudeDir(t.Agent.Dir)
}

// followersIn are the agents among all that follow theirs.
func (st *followState) followersIn(all []*Target) map[string]*follower {
	out := map[string]*follower{}
	for _, t := range all {
		parent, ok := st.follows(t)
		if !ok {
			continue
		}
		f := &follower{Parent: parent}
		if parent == "claude" && t.MCP != nil {
			f.ClaudeJSON = t.MCP.Path
		}
		out[t.Agent.ID] = f
	}
	return out
}

// followSkip are the library's fields that aren't what the agents are
// given: what magpie wrote into each (Applied), a project's own agents,
// which read the project's files whatever folder they run from, and
// records of other computers and repositories.
var followSkip = map[string]bool{"Applied": true, "Projects": true, "Borrowed": true, "SeenSkills": true, "SkillHow": true}

// followLists are the names of the agents lists forAgentLists reaches.
var followLists = map[string]bool{"Agents": true, "Paused": true}

// forAgentLists calls f with every list of agents the library gives
// something to (an item's Agents, a hidden server's Paused), wherever it
// is: a field added to an item later is reached without a change here.
func (l *Library) forAgentLists(f func(*[]string)) {
	v := reflect.ValueOf(l).Elem()
	for i := range v.NumField() {
		if sf := v.Type().Field(i); sf.IsExported() && !followSkip[sf.Name] {
			walkAgentLists(v.Field(i), f)
		}
	}
}

var stringsType = reflect.TypeFor[[]string]()

func walkAgentLists(v reflect.Value, f func(*[]string)) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			walkAgentLists(v.Elem(), f)
		}
	case reflect.Slice:
		for i := range v.Len() {
			walkAgentLists(v.Index(i), f)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			sf, fv := v.Type().Field(i), v.Field(i)
			if !sf.IsExported() {
				continue
			}
			if followLists[sf.Name] && fv.Type() == stringsType && fv.CanAddr() {
				f(fv.Addr().Interface().(*[]string))
				continue
			}
			walkAgentLists(fv, f)
		}
	}
}

// keepFollowers ticks each profile that follows its agent wherever that
// agent is ticked, and nowhere else: a sync's first step, all being its
// agents. A follower gone from this computer has its ticks taken back; one
// the user stopped keeps them.
func (l *Library) keepFollowers(all []*Target, res *Result) {
	st, err := readFollow()
	if err != nil {
		// unknown: every list stays as it is
		res.fail("library", "follow", err)
		return
	}
	now := st.followersIn(all)
	l.migrateClaudeDirs(all, st, res)
	here := map[string]bool{}
	for _, t := range all {
		here[t.Agent.ID] = true
	}
	for id := range st.Followers {
		if now[id] == nil && !here[id] {
			l.forAgentLists(func(list *[]string) { *list = set(*list, id, false) })
			delete(l.SkillHow, id)
			delete(st.Projects, id)
		}
	}
	for id, f := range now {
		l.forAgentLists(func(list *[]string) { *list = set(*list, id, slices.Contains(*list, f.Parent)) })
		if how, ok := l.SkillHow[f.Parent]; ok {
			if l.SkillHow == nil {
				l.SkillHow = map[string]string{}
			}
			l.SkillHow[id] = how
		} else {
			delete(l.SkillHow, id)
		}
	}
	st.Followers = now
	if err := st.save(); err != nil {
		res.fail("library", "follow", err)
	}
}

// syncFollowersClaudeLocal gives each Claude Code folder that follows
// Claude Code the project's record syncClaudeLocal gives Claude Code's.
func (l *Library) syncFollowersClaudeLocal(p *Project, hub *Server, fail func(what string, err error)) {
	st, err := readFollow()
	if err != nil {
		fail("claude", err)
		return
	}
	key := projectKey(p.Dir)
	changed := false
	for _, id := range sortedKeys(st.Followers) {
		f := st.Followers[id]
		if f.Parent != "claude" || f.ClaudeJSON == "" {
			continue
		}
		was := st.Projects[id][key]
		kept := was
		l.syncClaudeLocalIn(f.ClaudeJSON, &kept, p, hub, func(what string, err error) {
			fail(what, fmt.Errorf("%s: %w", id, err))
		})
		if reflect.DeepEqual(kept, was) {
			continue
		}
		changed = true
		if kept == nil {
			delete(st.Projects[id], key)
			if len(st.Projects[id]) == 0 {
				delete(st.Projects, id)
			}
			continue
		}
		if st.Projects == nil {
			st.Projects = map[string]map[string]*claudeLocal{}
		}
		if st.Projects[id] == nil {
			st.Projects[id] = map[string]*claudeLocal{}
		}
		st.Projects[id][key] = kept
	}
	if changed {
		if err := st.save(); err != nil {
			fail("claude", err)
		}
	}
}

// forgetFollowersProject forgets what magpie wrote in the following Claude
// Code folders' records of a project taken off the list with keep: it
// stays there, the user's, as it does in Claude Code's own.
func forgetFollowersProject(dir string) error {
	st, err := readFollow()
	if err != nil {
		return err
	}
	key, changed := projectKey(dir), false
	for id, recs := range st.Projects {
		if recs[key] != nil {
			delete(recs, key)
			if len(recs) == 0 {
				delete(st.Projects, id)
			}
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return st.save()
}

// FollowView is a profile agent here and whether it follows its agent.
type FollowView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Parent  string `json:"parent"`
	Dir     string `json:"dir"`
	Follows bool   `json:"follows"`
	// Chosen: the user turned it on or off, rather than its default
	Chosen bool `json:"chosen,omitempty"`
	// ByHand: a Claude Code folder added by hand (AddClaudeDir)
	ByHand bool `json:"byHand,omitempty"`
	// MCP and Skills are what magpie last wrote into it
	MCP    []string `json:"mcp"`
	Skills []string `json:"skills"`
}

// FollowViews are the profile agents here, by id.
func FollowViews() ([]FollowView, error) {
	mu.Lock()
	defer mu.Unlock()
	st, err := readFollow()
	if err != nil {
		return nil, err
	}
	l, err := load()
	if err != nil {
		return nil, err
	}
	hand := agent.HandAddedClaudeDirs()
	out := []FollowView{}
	for _, t := range Targets() {
		parent, ok := profileOf(t.Agent)
		if !ok {
			continue
		}
		_, follows := st.follows(t)
		_, chosen := st.Follow[t.Agent.ID]
		v := FollowView{ID: t.Agent.ID, Name: t.Agent.Name, Parent: parent, Dir: t.Agent.Dir, Follows: follows, Chosen: chosen,
			ByHand: parent == "claude" && slices.ContainsFunc(hand, func(d string) bool { return sameDir(d, t.Agent.Dir) }),
			MCP:    []string{}, Skills: []string{}}
		if a := l.Applied[t.Agent.ID]; a != nil {
			v.MCP, v.Skills = slices.Clone(a.MCP), slices.Clone(a.Skills)
		}
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b FollowView) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// profileTarget is the profile agent here called id.
func profileTarget(id string) (*Target, error) {
	for _, t := range Targets() {
		if _, ok := profileOf(t.Agent); ok && t.Agent.ID == id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("%s is no agent's profile here (magpie library follow lists them)", id)
}

// Follow has the profile agent id follow its agent's Library, or not. One
// stopped keeps what it is ticked for, its own from then on, and what
// magpie wrote in its records of projects for its agent comes out.
func Follow(id string, on bool) (*Result, error) {
	if err := checkAgent(id); err != nil {
		return nil, err
	}
	if _, err := profileTarget(id); err != nil {
		return nil, err
	}
	return change(func(l *Library) error {
		st, err := readFollow()
		if err != nil {
			return err
		}
		if st.Follow == nil {
			st.Follow = map[string]bool{}
		}
		st.Follow[id] = on
		if !on {
			if err := st.clearProjects(id); err != nil {
				return err
			}
		}
		return st.save()
	})
}

// clearProjects takes what magpie wrote out of id's records of projects.
func (st *followState) clearProjects(id string) error {
	f := st.Followers[id]
	if f == nil || f.ClaudeJSON == "" || len(st.Projects[id]) == 0 {
		delete(st.Projects, id)
		return nil
	}
	if !exists(f.ClaudeJSON) {
		delete(st.Projects, id)
		return nil
	}
	release, err := claudelock.Config(f.ClaudeJSON)
	if err != nil {
		return err
	}
	defer release()
	for key, rec := range st.Projects[id] {
		if err := clearClaudeLocal(f.ClaudeJSON, rec); err != nil {
			return err
		}
		delete(st.Projects[id], key)
	}
	delete(st.Projects, id)
	return nil
}

// sameDir is whether two folders are one: case aside on Windows.
func sameDir(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
