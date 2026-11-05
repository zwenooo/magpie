package sessions

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// handoffOf is s's condensed record, written as SaveHandoff writes it.
func handoffOf(t *testing.T, s Session) (string, hoResult) {
	t.Helper()
	var b bytes.Buffer
	r, err := writeHandoff(&b, s, "Claude Code", "/magpie/handoffs/whole.md", 123456, true)
	if err != nil {
		t.Fatal(err)
	}
	return b.String(), r
}

// inOrder fails unless each of parts is in md, one after the other.
func inOrder(t *testing.T, md string, parts ...string) {
	t.Helper()
	at := 0
	for _, p := range parts {
		i := strings.Index(md[at:], p)
		if i < 0 {
			t.Fatalf("no %q after byte %d in\n%s", p, at, md[:min(len(md), 4000)])
		}
		at += i + len(p)
	}
}

func notIn(t *testing.T, md string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(md, p) {
			t.Fatalf("%q should be left out of\n%s", p, md[:min(len(md), 4000)])
		}
	}
}

// A conversation handed over keeps what the user and the model said, each
// run of tool calls on one line with the files they changed, and leaves out
// the thinking, the tools' output and what the agent put in itself; the
// prompt Claude Desktop puts after its own system reminder is the user's.
func TestHandoffKeepsWhatWasSaid(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sess1.jsonl")
	output := strings.Repeat("tool output line\n", 2000)
	writeLines(t, file,
		claudeUser(1, `"<system-reminder>\nThe user started this session without a folder.\n<recent-project-folders>\n/a\n</recent-project-folders>\n</system-reminder>\n\nBackground: make the login work again"`),
		claudeUser(2, `"<command-name>/init</command-name>\n<command-message>init</command-message>"`),
		claudeBlocks("m1", 3, `{"type":"thinking","thinking":"secret reasoning"}`),
		claudeBlocks("m1", 4, `{"type":"text","text":"## Plan\nRead, then fix.\n`+"```"+`\n## not a heading\n`+"```"+`"}`),
		claudeBlocks("m1", 5, `{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}`),
		claudeUser(6, `[{"type":"tool_result","tool_use_id":"t1","content":`+fmt.Sprintf("%q", output)+`}]`),
		claudeBlocks("m1", 7, `{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"git status"}}`),
		claudeBlocks("m1", 8, `{"type":"tool_use","id":"t3","name":"Edit","input":{"file_path":"/work/app/login.go","old_string":"a","new_string":"b"}}`),
		claudeBlocks("m1", 9, `{"type":"tool_use","id":"t4","name":"Bash","input":{"command":"go test ./..."}}`),
		claudeBlocks("m2", 12, `{"type":"text","text":"Fixed: the check was inverted."}`),
		claudeUser(20, `"Now add a test"`),
		claudeBlocks("m3", 21, `{"type":"text","text":"Added TestLogin."}`),
	)
	before, _ := os.ReadFile(file)
	start := time.Date(2026, 11, 5, 9, 0, 0, 0, time.Local)
	s := Session{Agent: "claude-desktop", ID: "sess1", Path: file, Cwd: "/work/app", Title: "Fix the login",
		Start: start, Last: start.Add(time.Hour), Models: []Model{{Model: "claude-opus-5-5"}}}
	md, r := handoffOf(t, s)
	inOrder(t, md,
		"# Handoff: Fix the login\n",
		"- **Agent:** Claude Code\n",
		"- **Session ID:** `sess1`\n",
		"- **Folder:** /work/app\n",
		"- **Turns:** 2 from the user, 3 replies, 4 tool calls\n",
		"- **Files changed:** `/work/app/login.go`\n",
		"- **How it ended:** with the assistant's reply",
		"- **Whole conversation:** `/magpie/handoffs/whole.md` (120 KB)",
		"## User\n\nBackground: make the login work again\n",
		"## Assistant\n\n",
		"#### Plan\nRead, then fix.\n```\n## not a heading\n```",
		"> Tools: Bash ×3 (`go test ./...`; `git status`) · Edit (`/work/app/login.go`)\n",
		"Fixed: the check was inverted.",
		"## User\n\nNow add a test\n",
		"Added TestLogin.",
	)
	notIn(t, md, "without a folder", "<system-reminder>", "recent-project-folders", "/init", "secret reasoning", "tool output line", "Where it stopped", "shortened")
	if r.compacted || r.shortened {
		t.Fatalf("result %+v: neither compacted nor shortened", r)
	}
	if after, _ := os.ReadFile(file); !bytes.Equal(after, before) {
		t.Fatal("the session file was changed")
	}
}

// A Codex conversation is written the same way: its patches name the files
// they change, its shell calls their command, and what it put in itself
// (the environment, AGENTS.md) is left out.
func TestHandoffCodex(t *testing.T) {
	file := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeLines(t, file,
		`{"type":"session_meta","payload":{"id":"x"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /w\n\nbe brief"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n<cwd>/w</cwd>\n</environment_context>"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Rename foo to bar"}]}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{\"command\":[\"rg\",\"foo\"]}","call_id":"c1"}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"a.go: foo()"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch\n*** Update File: a.go\n@@\n-foo()\n+bar()\n*** Add File: b.go\n+package b\n*** End Patch","call_id":"c2"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c2","output":"Done"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Renamed in a.go; b.go added."}]}}`,
	)
	md, _ := handoffOf(t, Session{Agent: "codex", ID: "x", Path: file, Title: "Rename foo"})
	inOrder(t, md,
		"- **Turns:** 1 from the user, 1 replies, 2 tool calls\n",
		"- **Files changed:** `a.go`, `b.go`\n",
		"## User\n\nRename foo to bar\n",
		"> Tools: shell (`rg foo`) · apply_patch (`*** Begin Patch`)\n",
		"Renamed in a.go; b.go added.",
	)
	notIn(t, md, "be brief", "environment_context", "a.go: foo()")
}

// After Claude Code compacted a conversation, its own summary stands for
// all before it: the record starts there.
func TestHandoffFromCompaction(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sess1.jsonl")
	writeLines(t, file,
		claudeUser(1, `"the old plan"`),
		claudeBlocks("m1", 2, `{"type":"text","text":"working on the old plan"}`),
		`{"type":"system","subtype":"compact_boundary","content":"Conversation compacted"}`,
		`{"type":"user","isCompactSummary":true,"isVisibleInTranscriptOnly":true,"sessionId":"sess1","message":{"role":"user","content":"This session is being continued from a previous conversation.\n\n## Done\nThe login is fixed."}}`,
		claudeUser(10, `"Next: the signup page"`),
		claudeBlocks("m2", 11, `{"type":"text","text":"Signup done."}`),
	)
	md, r := handoffOf(t, Session{Agent: "claude", ID: "sess1", Path: file, Title: "Login"})
	if !r.compacted {
		t.Fatal("not marked compacted")
	}
	inOrder(t, md,
		"it starts with the agent's own summary",
		"- **Turns:** 1 from the user, 1 replies, 0 tool calls\n",
		"## Summary of the conversation before, written by the agent\n\nThis session is being continued from a previous conversation.\n\n#### Done\nThe login is fixed.\n",
		"## User\n\nNext: the signup page\n",
		"Signup done.",
	)
	notIn(t, md, "the old plan")
}

// A conversation that ended among tool calls says where it stopped: the
// last call and the end of what it answered.
func TestHandoffEndedAmongTools(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sess1.jsonl")
	output := strings.Repeat("x", hoLastOutput*2) + "THE END"
	writeLines(t, file,
		claudeUser(1, `"Deploy it"`),
		claudeBlocks("m1", 2, `{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"make deploy"}}`),
		claudeUser(3, `[{"type":"tool_result","tool_use_id":"t1","content":"`+output+`"}]`),
	)
	md, _ := handoffOf(t, Session{Agent: "claude", ID: "sess1", Path: file})
	inOrder(t, md,
		"- **How it ended:** while the assistant was using tools",
		"## Where it stopped\n\nThe last tool call: `Bash` — `make deploy`\n",
		"(1507 characters before) …\n",
		"THE END\n```\n",
	)
}

// What Claude Code says itself in place of a reply (an API's error, a
// sign-in it needs) is no reply: the record says the last message wasn't
// answered, so the agent carrying it on doesn't take it as done.
func TestHandoffEndedOnAnError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sess1.jsonl")
	writeLines(t, file,
		claudeUser(1, `"Fix it"`),
		claudeBlocks("m1", 2, `{"type":"text","text":"Fixed."}`),
		claudeUser(3, `"Push and open a PR"`),
		`{"type":"assistant","isApiErrorMessage":true,"sessionId":"sess1","message":{"id":"e1","model":"<synthetic>","content":[{"type":"text","text":"API Error: 400 the account is unavailable"}]}}`,
	)
	md, _ := handoffOf(t, Session{Agent: "claude", ID: "sess1", Path: file})
	inOrder(t, md,
		"- **Turns:** 2 from the user, 1 replies, 0 tool calls\n",
		"- **How it ended:** with an error the agent reported in place of a reply: the user's last message wasn't answered\n",
		"## User\n\nPush and open a PR\n",
		"> The agent's own notice, not a reply: API Error: 400 the account is unavailable\n",
	)
}

// A long conversation is cut to what an agent reads whole: what was first
// asked and the newest turns whole, the ones between shortened, then cut to
// their first words, then left out, oldest first. It is shaped as a real
// one measured: a few words said, much tool output.
func TestHandoffLongConversationFits(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sess1.jsonl")
	var lines []string
	sec := 0
	output := fmt.Sprintf("%q", strings.Repeat("o", 30_000))
	for turn := range 40 {
		sec++
		lines = append(lines, claudeUser(sec, fmt.Sprintf(`"prompt %02d %s"`, turn, strings.Repeat("u", 1500))))
		for call := range 5 {
			sec++
			lines = append(lines, claudeBlocks(fmt.Sprintf("m%d-%d", turn, call), sec, fmt.Sprintf(`{"type":"tool_use","id":"t%d-%d","name":"Bash","input":{"command":"step %d"}}`, turn, call, call)))
			sec++
			lines = append(lines, claudeUser(sec, `[{"type":"tool_result","tool_use_id":"t","content":`+output+`}]`))
		}
		sec++
		lines = append(lines, claudeBlocks(fmt.Sprintf("r%d", turn), sec, fmt.Sprintf(`{"type":"text","text":"reply %02d %s"}`, turn, strings.Repeat("r", 2500))))
	}
	writeLines(t, file, lines...)
	if st, _ := os.Stat(file); st.Size() < 5<<20 {
		t.Fatalf("the fixture is %d bytes, not a long conversation", st.Size())
	}
	md, r := handoffOf(t, Session{Agent: "claude", ID: "sess1", Path: file})
	if len(md) > handoffMax {
		t.Fatalf("%d bytes, more than %d", len(md), handoffMax)
	}
	if !r.shortened {
		t.Fatal("not marked shortened")
	}
	whole := func(turn int) string {
		return fmt.Sprintf("## User\n\nprompt %02d %s\n", turn, strings.Repeat("u", 1500))
	}
	inOrder(t, md,
		"some turns between the first and the newest are shortened or left out",
		"- **Turns:** 40 from the user, 40 replies, 200 tool calls\n",
		whole(0),
		"turns left out here",
		whole(38),
		fmt.Sprintf("reply 38 %s", strings.Repeat("r", 2500)),
		whole(39),
		fmt.Sprintf("reply 39 %s", strings.Repeat("r", 2500)),
	)
	notIn(t, md, "ooooo")
	// the turns whole are the newest, one after the other
	seen := false
	for turn := 1; turn < 40; turn++ {
		in := strings.Contains(md, whole(turn))
		if seen && !in {
			t.Fatalf("turn %d isn't whole after a newer one before it was", turn)
		}
		seen = seen || in
	}
}

// One turn longer than a record alone (an agent left to work for hours)
// keeps what was asked and its newest replies, saying how many came before.
func TestHandoffOneLongTurn(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sess1.jsonl")
	lines := []string{claudeUser(1, `"Port the whole app"`)}
	for i := range 300 {
		lines = append(lines,
			claudeBlocks(fmt.Sprintf("m%d", i), i+2, fmt.Sprintf(`{"type":"text","text":"step %03d %s"}`, i, strings.Repeat("w", 900))),
			claudeBlocks(fmt.Sprintf("c%d", i), i+2, fmt.Sprintf(`{"type":"tool_use","id":"t%d","name":"Edit","input":{"file_path":"/w/f%d.go"}}`, i, i)))
	}
	writeLines(t, file, lines...)
	md, r := handoffOf(t, Session{Agent: "claude", ID: "sess1", Path: file})
	if len(md) > handoffMax || !r.shortened {
		t.Fatalf("%d bytes (at most %d), shortened %v", len(md), handoffMax, r.shortened)
	}
	inOrder(t, md, "(and 270 more)", "## User\n\nPort the whole app\n", "earlier replies and", "tool calls of this turn left out here", "step 299 ")
	notIn(t, md, "step 000 ")
}

// What follows an agent's tags in a part it put in is the user's; a part
// of tags alone is the agent's.
func TestUserWords(t *testing.T) {
	for in, want := range map[string]string{
		"<system-reminder>\nx\n</system-reminder>\n\nDo it":                           "Do it",
		"<a>1</a>\n<b k=\"v\">2</b>\nDo it":                                           "Do it",
		"<command-name>/init</command-name>\n<command-message>init</command-message>": "",
		"<local-command-stdout>ok</local-command-stdout>":                             "",
		"<unclosed>\nDo it": "",
		"<environment_context>\n<cwd>/w</cwd>\n</environment_context>": "",
	} {
		if got := userWords(in); got != want {
			t.Errorf("userWords(%q) = %q, want %q", in, got, want)
		}
	}
}

// SaveHandoff writes the condensed record and the whole conversation into
// magpie's folder, says whether the session's folder is here, and takes out
// handoffs older than two weeks.
func TestSaveHandoff(t *testing.T) {
	work := t.TempDir()
	file := filepath.Join(t.TempDir(), "sess1.jsonl")
	writeLines(t, file, claudeUser(1, `"Fix it"`), claudeBlocks("m1", 2, `{"type":"text","text":"Fixed."}`))
	if err := os.MkdirAll(HandoffDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(HandoffDir(), "claude-gone.md")
	if err := os.WriteFile(old, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	long := time.Now().Add(-handoffKeep - time.Hour)
	os.Chtimes(old, long, long)

	h, err := SaveHandoff(Session{Agent: "claude", ID: "sess1", Path: file, Cwd: work, Title: "Fix it"}, "Claude Code")
	if err != nil {
		t.Fatal(err)
	}
	if h.Path != filepath.Join(HandoffDir(), "claude-sess1.md") || h.Full != filepath.Join(HandoffDir(), "claude-sess1.full.md") || !h.Here || h.Folder != work {
		t.Fatalf("handoff %+v", h)
	}
	short, _ := os.ReadFile(h.Path)
	full, _ := os.ReadFile(h.Full)
	if len(short) != h.Size || len(full) != h.FullSize {
		t.Fatalf("sizes %d/%d, files %d/%d", h.Size, h.FullSize, len(short), len(full))
	}
	inOrder(t, string(short), "# Handoff: Fix it\n", "`"+h.Full+"`", "Fixed.")
	inOrder(t, string(full), "# Fix it\n", "Fix it", "Fixed.")
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("a handoff two weeks old was kept")
	}

	h, err = SaveHandoff(Session{Agent: "claude", ID: "sess1", Path: file, Cwd: filepath.Join(work, "gone")}, "Claude Code")
	if err != nil || h.Here {
		t.Fatalf("a folder not here: %+v, %v", h, err)
	}
	if got, _ := os.ReadFile(h.Path); !strings.Contains(string(got), "gone (not on this computer)") {
		t.Fatalf("the record doesn't say the folder isn't here:\n%s", got)
	}
	if _, err := SaveHandoff(Session{Agent: "cline", ID: "x", Path: file}, "Cline"); err != ErrNoTranscript {
		t.Fatalf("an agent whose conversation isn't read: %v", err)
	}
}
