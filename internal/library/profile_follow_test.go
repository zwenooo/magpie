package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/gateway"
)

// reclaudeDir makes ReClaude's folder for account id, Claude Code having
// run there: its settings.json, and a .claude.json holding claudeJSON.
func reclaudeDir(t *testing.T, h, id, claudeJSON string) string {
	t.Helper()
	d := filepath.Join(h, ".rec", "claude", "accounts", id, "config")
	write(t, filepath.Join(d, "settings.json"), "{}\n")
	write(t, filepath.Join(d, ".claude.json"), claudeJSON)
	return d
}

// mcpServersOf is the mcpServers of the .claude.json at p.
func mcpServersOf(t *testing.T, p string) map[string]any {
	t.Helper()
	var cfg struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(read(t, p)), &cfg); err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	return cfg.MCPServers
}

func followView(t *testing.T, id string) *FollowView {
	t.Helper()
	views, err := FollowViews()
	if err != nil {
		t.Fatal(err)
	}
	for i := range views {
		if views[i].ID == id {
			return &views[i]
		}
	}
	return nil
}

// opsServer is a command server Claude Code is given.
func opsServer(t *testing.T) {
	t.Helper()
	ok(t)(SaveServer("", Server{Name: "ops", Transport: "stdio", Command: "python", Args: []string{"server.py"}, Agents: []string{"claude"}}))
}

// A ReClaude account's folder is a claude# agent that follows Claude Code:
// what Claude Code is given, it is given, written as its own agent record,
// and what the account has of its own stays.
func TestAccountFoldersFollowClaudeCode(t *testing.T) {
	h := sandbox(t)
	acc := reclaudeDir(t, h, "54687", `{"numStartups": 7, "mcpServers": {
		"mine": {"type": "stdio", "command": "node", "args": ["mine.js"]},
		"ops": {"type": "stdio", "command": "python", "args": ["D:/old/ops/server.py"]}}}`)
	// a folder Claude Code never ran in isn't one
	never := filepath.Join(h, ".rec", "claude", "accounts", "48142", "config")
	os.MkdirAll(never, 0o755)
	const id = "claude#rec-54687"

	opsServer(t)
	ok(t)(SaveServer("", Server{Name: "codex-only", Transport: "stdio", Command: "x", Agents: []string{"codex"}}))
	src := filepath.Join(h, "src/skills")
	skill(t, filepath.Join(src, "pdf"), "pdf", "Read PDFs")
	ok(t)(InstallSkills(src, []string{"pdf"}, []string{"claude"}))

	here := mcpServersOf(t, filepath.Join(h, ".claude.json"))["ops"]
	got := mcpServersOf(t, filepath.Join(acc, ".claude.json"))
	if here == nil || !reflect.DeepEqual(got["ops"], here) {
		t.Fatalf("the account's ops is %v, Claude Code here's %v", got["ops"], here)
	}
	if got["mine"] == nil || got["codex-only"] != nil {
		t.Fatalf("the account has %v: its own mine taken out, or Codex's server given", got)
	}
	if c := read(t, filepath.Join(acc, ".claude.json")); !strings.Contains(c, `"numStartups": 7`) {
		t.Fatalf("the rest of the account's .claude.json changed:\n%s", c)
	}
	if !ours(filepath.Join(acc, "skills", "pdf"), "pdf") {
		t.Fatal("the account hasn't the library's pdf")
	}
	if _, err := os.Lstat(filepath.Join(never, "skills")); !os.IsNotExist(err) {
		t.Fatal("a folder Claude Code never ran in was given skills")
	}
	l := loaded(t)
	if a := l.Applied[id]; a == nil || !slices.Contains(a.MCP, "ops") || !slices.Equal(a.Skills, []string{"pdf"}) {
		t.Fatalf("the account's record: %+v", a)
	}
	if !slices.Contains(l.server("ops").Agents, id) || slices.Contains(l.server("codex-only").Agents, id) || !slices.Contains(l.skill("pdf").Agents, id) {
		t.Fatalf("ticks: ops %v, codex-only %v, pdf %v", l.server("ops").Agents, l.server("codex-only").Agents, l.skill("pdf").Agents)
	}
	if v := followView(t, id); v == nil || !v.Follows || v.Chosen || v.Parent != "claude" || !sameDir(v.Dir, acc) {
		t.Fatalf("view %+v", v)
	}
	// a second sync changes nothing
	if res := ok(t)(Sync()); slices.Contains(res.Changed, id) {
		t.Fatalf("a sync with nothing new changed %v", res.Changed)
	}
}

func TestAccountFolderLosesWhatClaudeCodeLoses(t *testing.T) {
	h := sandbox(t)
	acc := reclaudeDir(t, h, "1", `{"mcpServers": {"mine": {"command": "node"}}}`)
	opsServer(t)
	src := filepath.Join(h, "src/skills")
	skill(t, filepath.Join(src, "pdf"), "pdf", "Read PDFs")
	ok(t)(InstallSkills(src, []string{"pdf"}, []string{"claude"}))

	ok(t)(RemoveServer("ops"))
	ok(t)(SkillAgents("pdf", nil))
	got := mcpServersOf(t, filepath.Join(acc, ".claude.json"))
	if got["ops"] != nil || got["mine"] == nil {
		t.Fatalf("after ops is removed the account has %v", got)
	}
	if _, err := os.Lstat(filepath.Join(acc, "skills", "pdf")); !os.IsNotExist(err) {
		t.Fatal("pdf is still linked in the account")
	}
	if v := followView(t, "claude#rec-1"); v == nil || len(v.MCP)+len(v.Skills) != 0 {
		t.Fatalf("view %+v", v)
	}
}

func TestAccountFolderKeepsItsOwnSkill(t *testing.T) {
	h := sandbox(t)
	acc := reclaudeDir(t, h, "1", `{}`)
	write(t, filepath.Join(acc, "skills", "pdf", "SKILL.md"), "the account's own\n")
	src := filepath.Join(h, "src/skills")
	skill(t, filepath.Join(src, "pdf"), "pdf", "Read PDFs")
	res, err := InstallSkills(src, []string{"pdf"}, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(res.Problems, func(p Problem) bool { return p.What == "skill:pdf" && p.Agent == "claude#rec-1" }) {
		t.Fatalf("the account's own pdf wasn't reported: %+v", res.Problems)
	}
	if b := read(t, filepath.Join(acc, "skills", "pdf", "SKILL.md")); b != "the account's own\n" {
		t.Fatalf("the account's own pdf became %q", b)
	}
}

func TestAccountFolderInstructionsPart(t *testing.T) {
	h := sandbox(t)
	acc := reclaudeDir(t, h, "1", `{}`)
	write(t, filepath.Join(acc, "CLAUDE.md"), "the account's own words\n")
	text := "Answer in Chinese."
	ok(t)(SaveInstructions(InstructionsChange{Shared: &text, Agents: []string{"claude"}}))
	c := read(t, filepath.Join(acc, "CLAUDE.md"))
	if !strings.Contains(c, "the account's own words") || !strings.Contains(c, blockBegin) || !strings.Contains(c, text) {
		t.Fatalf("the account's CLAUDE.md:\n%s", c)
	}
	none := ""
	ok(t)(SaveInstructions(InstructionsChange{Shared: &none, Agents: []string{"claude"}}))
	if c := read(t, filepath.Join(acc, "CLAUDE.md")); strings.Contains(c, blockBegin) || !strings.Contains(c, "the account's own words") {
		t.Fatalf("after the text is emptied:\n%s", c)
	}
}

func TestAccountFolderKeepsInstructionsOnAutomaticSync(t *testing.T) {
	h := sandbox(t)
	acc := reclaudeDir(t, h, "1", `{}`)
	file := filepath.Join(acc, "CLAUDE.md")
	shared := "Keep this instruction."
	ok(t)(SaveInstructions(InstructionsChange{Shared: &shared, Agents: []string{"claude"}}))
	before := []byte(read(t, file))
	lostLibrary(t, nil, nil)
	ok(t)(Sync())
	applyWiped(t, before, file)
}

// A Claude Code folder added by hand is a row that follows Claude Code,
// and taken off again it loses what magpie put there.
func TestClaudeFolderAddedByHand(t *testing.T) {
	h := sandbox(t)
	opsServer(t)
	other := filepath.Join(h, "work", "claude")
	os.MkdirAll(other, 0o755)
	if _, err := AddClaudeDir(other); err == nil {
		t.Fatal("a folder with no Claude Code's files was added")
	}
	write(t, filepath.Join(other, "settings.json"), "{}\n")
	write(t, filepath.Join(other, ".claude.json"), "{}\n")
	ok(t)(AddClaudeDir(other))
	if mcpServersOf(t, filepath.Join(other, ".claude.json"))["ops"] == nil {
		t.Fatal("a folder added by hand wasn't given ops")
	}
	if v := followView(t, "claude#claude"); v == nil || !v.ByHand || !v.Follows {
		t.Fatalf("view %+v", v)
	}
	if _, err := AddClaudeDir(claudeDir()); err == nil {
		t.Fatal("Claude Code's own folder was taken as another")
	}
	ok(t)(RemoveClaudeDir(other))
	if mcpServersOf(t, filepath.Join(other, ".claude.json"))["ops"] != nil {
		t.Fatal("ops stayed in a folder taken off")
	}
	if v := followView(t, "claude#claude"); v != nil {
		t.Fatalf("still listed: %+v", v)
	}
	if slices.Contains(loaded(t).server("ops").Agents, "claude#claude") {
		t.Fatal("still ticked for the folder taken off")
	}
}

// The skill router's entry reaches a following folder as Claude Code's
// does; the router's slash commands don't, as no claude# row takes them.
func TestAccountFolderGetsTheSkillRouter(t *testing.T) {
	h := sandbox(t)
	acc := reclaudeDir(t, h, "1", `{}`)
	src := filepath.Join(h, "source")
	skill(t, filepath.Join(src, "pdf"), "pdf", "Read PDFs")
	ok(t)(InstallSkills(src, []string{"pdf"}, []string{"claude"}))
	ok(t)(SetSkillRouter(true))
	entry := filepath.Join(acc, "skills", RouterSkill)
	if !routerOurs(entry) || !strings.Contains(read(t, filepath.Join(entry, "SKILL.md")), "## pdf") {
		t.Fatal("the account cannot see the routed pdf skill")
	}
	if res := ok(t)(Sync()); slices.Contains(res.Changed, "claude#rec-1") {
		t.Fatalf("second sync changed the account: %v", res.Changed)
	}
	ok(t)(SetSkillRouter(false))
	if _, err := os.Lstat(entry); !os.IsNotExist(err) || !ours(filepath.Join(acc, "skills", "pdf"), "pdf") {
		t.Fatal("turning off the router did not restore the direct skill")
	}
}

// Claude Code keeps a project's servers turned off and its hub entry in
// its folder's record: a following folder's record gets Claude Code's,
// and its own names there stay.
func TestAccountFolderProjectScope(t *testing.T) {
	home, project := scopeProject(t)
	root, err := json.Marshal(claudeRoot(project))
	if err != nil {
		t.Fatal(err)
	}
	first := reclaudeDir(t, home, "1", `{"projects":{`+string(root)+`:{"private":42,"disabledMcpServers":["fs","mine"],"mcpServers":{"mine":{"command":"local"}}}}}`)
	second := filepath.Join(home, "second")
	write(t, filepath.Join(second, "settings.json"), "{}\n")
	write(t, filepath.Join(second, ".claude.json"), "{}\n")
	ok(t)(AddClaudeDir(second))
	ok(t)(ProjectServerOff(project, "fs", []string{"claude"}))
	disabled := func(dir string, want ...string) {
		t.Helper()
		got, _ := claudeRecord(t, dir, project)["disabledMcpServers"].([]any)
		var have []string
		for _, x := range got {
			have = append(have, x.(string))
		}
		if !slices.Equal(have, want) {
			t.Fatalf("%s disabled %v, want %v", dir, have, want)
		}
	}
	disabled(first, "fs", "mine")
	disabled(second, "fs")
	ok(t)(ProjectServerOff(project, "fs", nil))
	disabled(first, "fs", "mine")
	disabled(second)
	ok(t)(SetMCPHub(true))
	ok(t)(ProjectServerOff(project, "docs", []string{"claude"}))
	for _, dir := range []string{first, second} {
		servers, _ := claudeRecord(t, dir, project)["mcpServers"].(map[string]any)
		hub, _ := servers[HubEntry].(map[string]any)
		address, _ := hub["url"].(string)
		if !strings.HasSuffix(address, gateway.HubMCPPath+"claude"+gateway.HubProjectSep+projectKey(project)) {
			t.Fatalf("wrong project hub in %s: %v", dir, hub)
		}
	}
	ok(t)(RemoveClaudeDir(second))
	disabled(second)
	ok(t)(RemoveProject(project, false))
	disabled(first, "fs", "mine")
	if got, _ := claudeRecord(t, first, project)["mcpServers"].(map[string]any); got[HubEntry] != nil {
		t.Fatal("hub left in the account")
	}
	record := claudeRecord(t, first, project)
	if record["private"] != float64(42) || record["mcpServers"].(map[string]any)["mine"] == nil {
		t.Fatalf("private fields lost: %v", record)
	}
}

func TestAccountFolderProjectPluginHub(t *testing.T) {
	home, project, _ := pluginProjects(t)
	write(t, filepath.Join(home, ".claude.json"), `{}`)
	acc := reclaudeDir(t, home, "1", `{}`)
	ok(t)(SetMCPHub(true))
	ok(t)(ProjectPlugin(project, "acme-kit", []string{"claude"}))
	hubURL := func(dir string) string {
		servers, _ := claudeRecord(t, dir, project)["mcpServers"].(map[string]any)
		hub, _ := servers[HubEntry].(map[string]any)
		url, _ := hub["url"].(string)
		return url
	}
	mainURL := hubURL(home)
	if !strings.HasSuffix(mainURL, gateway.HubMCPPath+"claude"+gateway.HubProjectSep+projectKey(project)) || hubURL(acc) != mainURL {
		t.Fatalf("plugin-only project hub: main %q, account %q", mainURL, hubURL(acc))
	}
	ok(t)(ProjectPlugin(project, "acme-kit", nil))
	if url := hubURL(acc); url != "" {
		t.Fatalf("project hub left in account: %s", url)
	}
}

// A project taken off with keep leaves what was written, the user's.
func TestAccountFolderKeepsProjectFiles(t *testing.T) {
	home, project := scopeProject(t)
	acc := reclaudeDir(t, home, "1", `{}`)
	ok(t)(ProjectServerOff(project, "fs", []string{"claude"}))
	ok(t)(RemoveProject(project, true))
	ok(t)(Sync())
	if got := anyStrs(claudeRecord(t, acc, project)["disabledMcpServers"]); !slices.Equal(got, []string{"fs"}) {
		t.Fatalf("keep removed the project's disable: %v", got)
	}
	ok(t)(AddProject(project))
	ok(t)(ProjectServerOff(project, "fs", []string{"claude"}))
	ok(t)(RemoveProject(project, false))
	if got := anyStrs(claudeRecord(t, acc, project)["disabledMcpServers"]); !slices.Equal(got, []string{"fs"}) {
		t.Fatalf("re-added project reclaimed the user's disable: %v", got)
	}
}

// An account removed takes its ticks with it.
func TestAccountFolderGoneLosesItsTicks(t *testing.T) {
	h := sandbox(t)
	acc := reclaudeDir(t, h, "1", `{}`)
	opsServer(t)
	if !slices.Contains(loaded(t).server("ops").Agents, "claude#rec-1") {
		t.Fatal("the account isn't ticked")
	}
	if err := os.RemoveAll(filepath.Dir(acc)); err != nil {
		t.Fatal(err)
	}
	ok(t)(Sync())
	if a := loaded(t).server("ops").Agents; slices.Contains(a, "claude#rec-1") {
		t.Fatalf("a removed account is still ticked: %v", a)
	}
	if st, _ := readFollow(); st.Followers["claude#rec-1"] != nil {
		t.Fatalf("a removed account is still a follower: %+v", st.Followers)
	}
}

// Following is the user's to turn off and on: off keeps the ticks it had,
// its own from then on, and what Claude Code gets later doesn't reach it.
func TestFollowOffAndOn(t *testing.T) {
	h := sandbox(t)
	acc := reclaudeDir(t, h, "1", `{}`)
	const id = "claude#rec-1"
	src := filepath.Join(h, "src/skills")
	skill(t, filepath.Join(src, "pdf"), "pdf", "Read PDFs")
	skill(t, filepath.Join(src, "docx"), "docx", "Write Word files")
	ok(t)(InstallSkills(src, []string{"pdf"}, []string{"claude"}))
	ok(t)(Follow(id, false))
	if v := followView(t, id); v == nil || v.Follows || !v.Chosen {
		t.Fatalf("view %+v", v)
	}
	ok(t)(InstallSkills(src, []string{"docx"}, []string{"claude"}))
	if !ours(filepath.Join(acc, "skills", "pdf"), "pdf") {
		t.Fatal("turning following off took pdf out")
	}
	if _, err := os.Lstat(filepath.Join(acc, "skills", "docx")); !os.IsNotExist(err) {
		t.Fatal("a skill ticked for Claude Code later reached an account that stopped following")
	}
	// ticked on its own now
	ok(t)(SkillAgents("pdf", []string{"claude"}))
	if _, err := os.Lstat(filepath.Join(acc, "skills", "pdf")); !os.IsNotExist(err) {
		t.Fatal("unticked on its own, the account kept pdf")
	}
	ok(t)(Follow(id, true))
	if !ours(filepath.Join(acc, "skills", "pdf"), "pdf") || !ours(filepath.Join(acc, "skills", "docx"), "docx") {
		t.Fatal("following again didn't give the account Claude Code's skills")
	}
	if _, err := Follow("claude", true); err == nil {
		t.Fatal("an agent that is no profile was made to follow")
	}
}

// Any agent's profile can follow its agent, not Claude Code's alone; one
// upstream finds itself (~/.claude-work, a Hermes profile) is ticked on its
// own until the user says otherwise.
func TestProfilesOfAnyAgentFollowByChoice(t *testing.T) {
	h := sandbox(t)
	work := filepath.Join(h, ".claude-work")
	write(t, filepath.Join(work, "settings.json"), "{}\n")
	write(t, filepath.Join(work, ".claude.json"), "{}\n")
	t.Setenv("HERMES_HOME", "")
	root := appdir.HermesHomeIn(h)
	write(t, filepath.Join(root, "config.yaml"), hermesConfig)
	coder := filepath.Join(root, "profiles", "coder", "config.yaml")
	write(t, coder, "model:\n  default: kimi-k3\n  provider: moonshot\n")
	holdCLI(t, h, "hermes")
	ok(t)(SaveServer("", Server{Name: "fs", Transport: "stdio", Command: "npx", Args: []string{"-y", "@mcp/fs"}, Agents: []string{"claude", "hermes"}}))
	if mcpServersOf(t, filepath.Join(work, ".claude.json"))["fs"] != nil || strings.Contains(read(t, coder), "fs:") {
		t.Fatal("a profile followed without being asked to")
	}
	ok(t)(Follow("hermes#coder", true))
	ok(t)(Follow("claude#work", true))
	if mcpServersOf(t, filepath.Join(work, ".claude.json"))["fs"] == nil {
		t.Fatal("claude#work, following, wasn't given fs")
	}
	if !strings.Contains(read(t, coder), "fs:") || !strings.Contains(read(t, coder), "provider: moonshot") {
		t.Fatalf("hermes#coder, following, has no fs, or lost its own settings:\n%s", read(t, coder))
	}
	ok(t)(ServerAgents("fs", []string{"claude"}))
	if strings.Contains(read(t, coder), "fs:") {
		t.Fatal("fs stays in hermes#coder after Hermes lost it")
	}
}

// What the fork's earlier sync gave an account folder becomes that
// folder's agent record, nothing written twice or taken out; what it gave
// a folder that is no row (a saved account the gateway runs) comes out,
// the folder's own entries staying.
func TestClaudeDirsGivenIsMigrated(t *testing.T) {
	h := sandbox(t)
	src := filepath.Join(h, "src/skills")
	skill(t, filepath.Join(src, "pdf"), "pdf", "Read PDFs")
	ok(t)(InstallSkills(src, []string{"pdf"}, []string{"claude"}))
	ok(t)(SaveServer("", Server{Name: "ops", Transport: "stdio", Command: "python", Args: []string{"new.py"}, Agents: []string{"claude"}}))
	// as the earlier sync left them: ops as it was before its update, and
	// old, which Claude Code has lost since
	ops := map[string]any{"type": "stdio", "command": "python", "args": []any{"server.py"}}
	given, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"ops": ops, "old": ops, "mine": map[string]any{"command": "node"}}})
	acc := reclaudeDir(t, h, "1", string(given))
	saved := filepath.Join(h, ".config", "magpie", "claude-accounts", "0123456789abcdef")
	write(t, filepath.Join(saved, ".claude.json"), string(given))
	for _, d := range []string{acc, saved} {
		if err := link(filepath.Join(d, "skills", "pdf"), "pdf"); err != nil {
			t.Fatal(err)
		}
	}
	legacy, _ := json.Marshal(map[string]any{"given": map[string]any{
		dirKeyOf(acc):   map[string]any{"mcp": []string{"ops", "old"}, "skills": []string{"pdf"}},
		dirKeyOf(saved): map[string]any{"mcp": []string{"ops"}, "skills": []string{"pdf"}},
	}})
	write(t, agent.ClaudeDirsFile(), string(legacy))

	res := ok(t)(Sync())
	if slices.ContainsFunc(res.Problems, func(p Problem) bool { return p.Agent == "claude#rec-1" }) {
		t.Fatalf("the migrated account has problems: %+v", res.Problems)
	}
	if a := loaded(t).Applied["claude#rec-1"]; a == nil || !slices.Equal(a.Skills, []string{"pdf"}) || !slices.Equal(a.MCP, []string{"ops"}) {
		t.Fatalf("the account's record after the migration: %+v", a)
	}
	here := mcpServersOf(t, filepath.Join(h, ".claude.json"))["ops"]
	got := mcpServersOf(t, filepath.Join(acc, ".claude.json"))
	if here == nil || !reflect.DeepEqual(got["ops"], here) || got["old"] != nil || got["mine"] == nil {
		t.Fatalf("the account's servers after the migration: %v (Claude Code's ops %v)", got, here)
	}
	if !ours(filepath.Join(acc, "skills", "pdf"), "pdf") {
		t.Fatal("the account lost pdf")
	}
	got = mcpServersOf(t, filepath.Join(saved, ".claude.json"))
	if got["ops"] != nil || got["mine"] == nil {
		t.Fatalf("the saved account's servers after the migration: %v", got)
	}
	if _, err := os.Lstat(filepath.Join(saved, "skills", "pdf")); !os.IsNotExist(err) {
		t.Fatal("the saved account kept the library's pdf")
	}
	if _, err := os.Stat(agent.ClaudeDirsFile()); !os.IsNotExist(err) {
		t.Fatalf("claude-dirs.json stays with nothing left in it: %s", read(t, agent.ClaudeDirsFile()))
	}
}

// dirKeyOf is a folder as the fork's claude-dirs.json recorded it.
func dirKeyOf(d string) string {
	d = filepath.Clean(d)
	if filepath.Separator == '\\' {
		return strings.ToLower(d)
	}
	return d
}

// Every list of agents the library gives something to is reached: a field
// added to an item, or an item added to the library, is ticked for a
// follower without a change to forAgentLists. A field naming agents that
// isn't such a list is classed here, so a new one is looked at.
func TestFollowReachesEveryAgentsList(t *testing.T) {
	notLists := map[string]bool{
		// what magpie wrote, by agent; a project's agents read the
		// project's files; other computers and repositories
		"Library.Applied": true, "Library.Projects": true, "Library.Borrowed": true, "Library.SeenSkills": true,
		// the parent's choice is copied by keepFollowers itself
		"Library.SkillHow": true,
		// imported from an agent's own files: the agent's, not given
		"Command.Taken": true,
		// a skill's folder's agents before it came in
		"HomeEntry.Agent": true,
	}
	var walk func(t reflect.Type, seen map[reflect.Type]bool)
	walk = func(typ reflect.Type, seen map[reflect.Type]bool) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || seen[typ] {
			return
		}
		seen[typ] = true
		for i := range typ.NumField() {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			name := typ.Name() + "." + f.Name
			if notLists[name] || typ == reflect.TypeFor[Library]() && followSkip[f.Name] {
				continue
			}
			if lower := strings.ToLower(f.Name); strings.Contains(strings.ReplaceAll(lower, "subagent", ""), "agent") && !(followLists[f.Name] && f.Type == stringsType) {
				t.Errorf("%s (%s) names agents but isn't a list forAgentLists reaches: make it one, or class it here", name, f.Type)
			}
			walk(f.Type, seen)
		}
	}
	walk(reflect.TypeFor[Library](), map[reflect.Type]bool{})

	// and each list is reached, wherever it is
	l := &Library{
		Instructions: Instructions{Agents: []string{"claude"}, Sets: []InstrSet{{}}},
		MCP:          []*Server{{Name: "a", Agents: []string{"claude"}}, {Name: "b", Hidden: true, Paused: []string{"claude", "codex"}}},
		Skills:       []*Skill{{Name: "s", Agents: []string{"codex"}}},
		Hooks:        []*Hook{{Agents: []string{"claude"}}},
		Env:          []*EnvVar{{Agents: []string{"claude"}}},
		Subagents:    []*Subagent{{Agents: []string{"claude"}}},
		Commands:     []*Command{{Agents: []string{"claude"}}},
		Rules:        []*Rule{{Agents: []string{"claude"}}},
		Plugins:      []*Plugin{{Agents: []string{"claude"}}},
	}
	n := 0
	l.forAgentLists(func(list *[]string) {
		n++
		*list = set(*list, "claude#x", slices.Contains(*list, "claude"))
	})
	if n < 10 {
		t.Fatalf("reached %d lists, want every one of the 10", n)
	}
	if !slices.Contains(l.MCP[1].Paused, "claude#x") || slices.Contains(l.Skills[0].Agents, "claude#x") ||
		!slices.Contains(l.Plugins[0].Agents, "claude#x") || !slices.Contains(l.Instructions.Agents, "claude#x") {
		t.Fatalf("lists after: %+v %+v %+v %+v", l.MCP[1].Paused, l.Skills[0].Agents, l.Plugins[0].Agents, l.Instructions.Agents)
	}
}
