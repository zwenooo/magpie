package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/gateway"
)

// reclaudeDir makes ReClaude's folder for account id, Claude Code having
// run there (a .claude.json, holding claudeJSON).
func reclaudeDir(t *testing.T, h, id, claudeJSON string) string {
	t.Helper()
	d := filepath.Join(h, ".rec", "claude", "accounts", id, "config")
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

func TestClaudeDirsGetWhatClaudeCodeIsGiven(t *testing.T) {
	h := sandbox(t)
	// an account with a server of its own, and ops by hand (another copy)
	acc := reclaudeDir(t, h, "54687", `{"numStartups": 7, "mcpServers": {
		"mine": {"type": "stdio", "command": "node", "args": ["mine.js"]},
		"ops": {"type": "stdio", "command": "python", "args": ["D:/old/ops/server.py"]}}}`)
	// a folder Claude Code never ran in isn't one
	never := filepath.Join(h, ".rec", "claude", "accounts", "48142", "config")
	os.MkdirAll(never, 0o755)

	keptServer(t, "print('v1')\n", "targets: {}\n")
	src := filepath.Join(h, "src/skills")
	skill(t, filepath.Join(src, "pdf"), "pdf", "Read PDFs")
	ok(t)(InstallSkills(src, []string{"pdf"}, []string{"claude"}))

	here := mcpServersOf(t, filepath.Join(h, ".claude.json"))["ops"]
	got := mcpServersOf(t, filepath.Join(acc, ".claude.json"))
	if here == nil || !reflect.DeepEqual(got["ops"], here) {
		t.Fatalf("the account's ops is %v, Claude Code here's %v", got["ops"], here)
	}
	if got["mine"] == nil {
		t.Fatal("the account's own server was taken out")
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
	// what was given is recorded, and listed
	v, err := ClaudeDirsView()
	if err != nil || len(v) != 1 || !sameDir(v[0].Dir, acc) || !slices.Equal(v[0].MCP, []string{"ops"}) || !slices.Equal(v[0].Skills, []string{"pdf"}) {
		t.Fatalf("view %+v %v", v, err)
	}
	// the library's own file holds nothing of the account (carried by a sync)
	if b := read(t, path()); strings.Contains(b, "54687") || strings.Contains(b, "claude@dir") {
		t.Fatalf("library.json names the account:\n%s", b)
	}
	// a second sync changes nothing
	if res := ok(t)(Sync()); slices.ContainsFunc(res.Changed, func(id string) bool { return strings.HasPrefix(id, "claude@dir") }) {
		t.Fatalf("a sync with nothing new changed %v", res.Changed)
	}
}

func TestClaudeDirsTakeOutWhatGoes(t *testing.T) {
	h := sandbox(t)
	acc := reclaudeDir(t, h, "1", `{"mcpServers": {"mine": {"command": "node"}}}`)
	keptServer(t, "print('v1')\n", "targets: {}\n")
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
	if v, _ := ClaudeDirsView(); len(v) != 1 || len(v[0].MCP)+len(v[0].Skills) != 0 {
		t.Fatalf("view %+v", v)
	}
}

func TestClaudeDirsLeaveTheAccountsOwnSkill(t *testing.T) {
	h := sandbox(t)
	acc := reclaudeDir(t, h, "1", `{}`)
	// a skill folder of the account's own, and a link to an old checkout
	write(t, filepath.Join(acc, "skills", "pdf", "SKILL.md"), "the account's own\n")
	old := filepath.Join(h, "old", "docx")
	write(t, filepath.Join(old, "SKILL.md"), "an old copy\n")
	if err := dirLink(old, filepath.Join(acc, "skills", "docx")); err != nil {
		t.Skip("no folder links here:", err)
	}
	src := filepath.Join(h, "src/skills")
	skill(t, filepath.Join(src, "pdf"), "pdf", "Read PDFs")
	skill(t, filepath.Join(src, "docx"), "docx", "Write Word files")
	res, err := InstallSkills(src, []string{"pdf", "docx"}, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(res.Problems, func(p Problem) bool {
		return p.What == "skill:pdf" && strings.HasPrefix(p.Agent, "claude@dir") && strings.Contains(p.Error, "of its own")
	}) {
		t.Fatalf("the account's own pdf wasn't reported: %+v", res.Problems)
	}
	if b := read(t, filepath.Join(acc, "skills", "pdf", "SKILL.md")); b != "the account's own\n" {
		t.Fatalf("the account's own pdf became %q", b)
	}
	if !ours(filepath.Join(acc, "skills", "docx"), "docx") {
		t.Fatal("the link to the old copy wasn't replaced with the library's")
	}
	if b := read(t, filepath.Join(old, "SKILL.md")); b != "an old copy\n" {
		t.Fatal("the old copy itself was touched")
	}
}

func TestClaudeDirsInstructionsPart(t *testing.T) {
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

func TestClaudeDirsAddedByHand(t *testing.T) {
	h := sandbox(t)
	keptServer(t, "print('v1')\n", "targets: {}\n")
	other := filepath.Join(h, "work", "claude")
	os.MkdirAll(other, 0o755)
	ok(t)(AddClaudeDir(other))
	if mcpServersOf(t, filepath.Join(other, ".claude.json"))["ops"] == nil {
		t.Fatal("a folder added by hand wasn't given ops")
	}
	if _, err := AddClaudeDir(claudeDir()); err == nil {
		t.Fatal("Claude Code's own folder was taken as another")
	}
	ok(t)(RemoveClaudeDir(other))
	if mcpServersOf(t, filepath.Join(other, ".claude.json"))["ops"] != nil {
		t.Fatal("ops stayed in a folder taken off")
	}
	if v, _ := ClaudeDirsView(); len(v) != 0 {
		t.Fatalf("still listed: %+v", v)
	}
}

func TestClaudeDirsKeepInstructionsOnAutomaticSync(t *testing.T) {
	homeDir := sandbox(t)
	account := reclaudeDir(t, homeDir, "1", `{}`)
	file := filepath.Join(account, "CLAUDE.md")
	shared := "Keep this instruction."
	ok(t)(SaveInstructions(InstructionsChange{Shared: &shared, Agents: []string{"claude"}}))
	before := []byte(read(t, file))
	lostLibrary(t, nil, nil)
	ok(t)(Sync())
	applyWiped(t, before, file)
	ok(t)(SaveInstructions(InstructionsChange{Shared: &shared, Agents: []string{"claude", "codex"}}))
	l := loaded(t)
	l.Instructions.Agents = []string{"codex"}
	if err := l.save(); err != nil {
		t.Fatal(err)
	}
	ok(t)(Sync())
	if got := read(t, file); got != "" {
		t.Fatalf("automatic take-back left %q, want an empty file", got)
	}
}

func TestClaudeDirsMirrorSkillRouter(t *testing.T) {
	homeDir := sandbox(t)
	account := reclaudeDir(t, homeDir, "1", `{}`)
	other := reclaudeDir(t, homeDir, "2", `{}`)
	private := filepath.Join(other, "commands", "pdf.md")
	write(t, private, "my pdf command\n")
	source := filepath.Join(homeDir, "source")
	skill(t, filepath.Join(source, "pdf"), "pdf", "Read PDFs")
	ok(t)(InstallSkills(source, []string{"pdf"}, []string{"claude"}))
	ok(t)(SetSkillRouter(true))
	entry := filepath.Join(account, "skills", RouterSkill)
	if !routerOurs(entry) || !strings.Contains(read(t, filepath.Join(entry, "SKILL.md")), "## pdf") {
		t.Fatal("the account cannot see the routed pdf skill")
	}
	command := filepath.Join(account, "commands", "pdf.md")
	if body := read(t, command); !strings.Contains(body, filepath.Join(entry, ".skills", "pdf", "SKILL.md")) || !strings.Contains(body, "$ARGUMENTS") {
		t.Fatalf("account command points outside its router: %s", body)
	}
	if res := ok(t)(Sync()); slices.Contains(res.Changed, claudeDirID(account)) {
		t.Fatalf("second sync changed %s", account)
	}
	ok(t)(SetSkillRouter(false))
	gone(t, command)
	if read(t, private) != "my pdf command\n" {
		t.Fatal("the account's own command was changed")
	}
	ok(t)(SetSkillRouter(true))
	write(t, command, "my replacement\n")
	ok(t)(SkillRouterHow("pdf", true))
	if read(t, command) != "my replacement\n" {
		t.Fatal("edited command removed with the stub")
	}
	if _, err := os.Lstat(entry); !os.IsNotExist(err) || !ours(filepath.Join(account, "skills", "pdf"), "pdf") {
		t.Fatal("turning off the router did not restore the direct skill")
	}
}

func TestClaudeDirsProjectScope(t *testing.T) {
	home, project := scopeProject(t)
	root, err := json.Marshal(claudeRoot(project))
	if err != nil {
		t.Fatal(err)
	}
	first := reclaudeDir(t, home, "1", `{"projects":{`+string(root)+`:{"private":42,"disabledMcpServers":["fs","mine"],"mcpServers":{"mine":{"command":"local"}}}}}`)
	second := t.TempDir()
	ok(t)(AddClaudeDir(second))
	ok(t)(ProjectServerOff(project, "fs", []string{"claude"}))
	disabled := func(dir string, want ...string) {
		t.Helper()
		got, _ := claudeRecord(t, dir, project)["disabledMcpServers"].([]any)
		if !slices.Equal(anyStrs(got), want) {
			t.Fatalf("%s disabled %v, want %v", dir, got, want)
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
	if res := ok(t)(Sync()); slices.Contains(res.Changed, claudeDirID(first)) || slices.Contains(res.Changed, claudeDirID(second)) {
		t.Fatalf("second sync changed accounts: %v", res.Changed)
	}
	ok(t)(RemoveClaudeDir(second))
	disabled(second)
	ok(t)(RemoveProject(project, false))
	disabled(first, "fs", "mine")
	for _, dir := range []string{first, second} {
		if got := claudeRecord(t, dir, project)["mcpServers"].(map[string]any); got[HubEntry] != nil {
			t.Fatalf("hub left in %s", dir)
		}
	}
	record := claudeRecord(t, first, project)
	if record["private"] != float64(42) || record["mcpServers"].(map[string]any)["mine"] == nil {
		t.Fatalf("private fields lost: %v", record)
	}
}

func TestClaudeDirsKeepProjectFiles(t *testing.T) {
	home, project := scopeProject(t)
	account := reclaudeDir(t, home, "1", `{}`)
	ok(t)(ProjectServerOff(project, "fs", []string{"claude"}))
	ok(t)(RemoveProject(project, true))
	ok(t)(Sync())
	if got := anyStrs(claudeRecord(t, account, project)["disabledMcpServers"]); !slices.Equal(got, []string{"fs"}) {
		t.Fatalf("keep removed the project's disable: %v", got)
	}
	ok(t)(AddProject(project))
	ok(t)(ProjectServerOff(project, "fs", []string{"claude"}))
	ok(t)(RemoveProject(project, false))
	if got := anyStrs(claudeRecord(t, account, project)["disabledMcpServers"]); !slices.Equal(got, []string{"fs"}) {
		t.Fatalf("re-added project reclaimed the user's disable: %v", got)
	}
}

func TestClaudeDirsForgetAFolderGone(t *testing.T) {
	h := sandbox(t)
	acc := reclaudeDir(t, h, "1", `{}`)
	keptServer(t, "print('v1')\n", "targets: {}\n")
	if st, _ := readClaudeDirs(); st.Given[dirKey(acc)] == nil {
		t.Fatal("nothing recorded for the account")
	}
	if err := os.RemoveAll(filepath.Dir(acc)); err != nil {
		t.Fatal(err)
	}
	ok(t)(Sync())
	if st, _ := readClaudeDirs(); len(st.Given) != 0 {
		t.Fatalf("a removed account is still recorded: %+v", st.Given)
	}
}
