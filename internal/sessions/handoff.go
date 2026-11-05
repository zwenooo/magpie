package sessions

// Handing a conversation over to another agent: magpie writes what was said,
// condensed to what an agent reads whole, and the whole of it beside, for
// the user to give any agent with a prompt (the Sessions page's Continue in,
// "with a prompt"). A 7 MB Claude Code transcript held 32 KB of what the user
// and the model said; the rest was tool output, request records and the
// agent's own lines, more than any agent's context holds, and an agent given
// a file reads it whole. Every agent magpie reads is written the same way,
// from its parts (transcript.go), so the agent carrying it on needs to know
// no other agent's files. No model is asked: it is the same every time.

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tidwall/gjson"

	"github.com/yetone/magpie/internal/settings"
)

// handoffMax is the bytes of a condensed record: 15–20k tokens, read whole.
const handoffMax = 60_000

// What a condensed record keeps, in characters: of one part said; of all the
// user said in a turn; of a turn shortened, its user's words and each reply;
// of a turn cut to its first words; of the last tool output; of a tool
// call's argument shown.
const (
	hoPartMax    = 6000
	hoUserMax    = 12000
	hoShortUser  = 1000
	hoShortReply = 300
	hoMinUser    = 300
	hoLastOutput = 1500
	hoArgMax     = 80
)

// handoffKeep is how long a handoff's files are kept: they are written
// again each time one is asked for.
const handoffKeep = 14 * 24 * time.Hour

// Handoff is where a session's condensed record and its whole conversation
// were written, for another agent to carry it on.
type Handoff struct {
	Path      string `json:"path"`                // the condensed record
	Full      string `json:"full"`                // the whole conversation, as Markdown
	Size      int    `json:"size"`                // bytes of the condensed record
	FullSize  int    `json:"fullSize"`            // and of the whole conversation
	Folder    string `json:"folder,omitempty"`    // the folder it ran in
	Here      bool   `json:"here"`                // that folder is on this computer
	Compacted bool   `json:"compacted,omitempty"` // it starts at the agent's own summary
	Shortened bool   `json:"shortened,omitempty"` // turns were shortened or left out
}

// HandoffDir is the folder handoffs are written in, magpie's own.
func HandoffDir() string { return filepath.Join(settings.Dir(), "handoffs") }

// SaveHandoff writes s's condensed record and its whole conversation as
// Markdown in HandoffDir, anew each time (the session may have gone on);
// name is the agent as the reader knows it. The session's files are only
// read; handoffs older than handoffKeep are taken out.
func SaveHandoff(s Session, name string) (Handoff, error) {
	if !Reads(s.Agent) {
		return Handoff{}, ErrNotRead
	}
	if !readable(s) || s.Path == "" {
		return Handoff{}, ErrNoTranscript
	}
	dir := HandoffDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Handoff{}, err
	}
	pruneHandoffs(dir, time.Now())
	stem := handoffStem(s)
	h := Handoff{Path: filepath.Join(dir, stem+".md"), Full: filepath.Join(dir, stem+".full.md"), Folder: s.Cwd}
	if st, err := os.Stat(s.Cwd); s.Cwd != "" && err == nil && st.IsDir() {
		h.Here = true
	}
	var full, short bytes.Buffer
	if err := WriteMarkdown(&full, s, name); err != nil {
		return Handoff{}, err
	}
	h.FullSize = full.Len()
	r, err := writeHandoff(&short, s, name, h.Full, h.FullSize, h.Here)
	if err != nil {
		return Handoff{}, err
	}
	h.Size, h.Compacted, h.Shortened = short.Len(), r.compacted, r.shortened
	if err := writeHandoffFile(h.Full, full.Bytes()); err != nil {
		return Handoff{}, err
	}
	if err := writeHandoffFile(h.Path, short.Bytes()); err != nil {
		return Handoff{}, err
	}
	return h, nil
}

// handoffStem is a session's handoff files' name: its agent and id, in
// characters every system takes.
func handoffStem(s Session) string {
	stem := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, s.Agent+"-"+s.ID)
	if len(stem) > 120 {
		stem = stem[:120]
	}
	return stem
}

// writeHandoffFile writes a handoff file whole, or leaves the one before:
// an agent may be reading it.
func writeHandoffFile(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".handoff-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// pruneHandoffs takes out the handoff files not written for handoffKeep.
func pruneHandoffs(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > handoffKeep {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// claudeFormat says whether readTranscriptOf reads an agent's file as Claude
// Code's lines (its default case).
func claudeFormat(agent string) bool {
	switch agent {
	case "opencode", "cursor", "codex", "pi", "raft", "cindy", "omp", "kimi", "agy":
		return false
	}
	return true
}

var ccCompactSummary = []byte(`"isCompactSummary":true`)

// lastCompaction is where the last compaction summary of a Claude Code file
// starts: the user line Claude Code marks isCompactSummary, whose words
// stand for all that came before it.
func lastCompaction(path string) (int64, bool, error) {
	at, found := int64(0), false
	_, err := scanAt(path, 0, nil, func(b []byte, start, _ int64) bool {
		if bytes.Contains(b, ccCompactSummary) && bytes.Contains(b, ccUserLine) {
			r := gjson.GetManyBytes(b, "type", "isCompactSummary", "isSidechain")
			if r[0].String() == "user" && r[1].Bool() && !r[2].Bool() {
				at, found = start, true
			}
		}
		return true
	})
	return at, found, err
}

// hoTurn is one turn: what the user said, and all the assistant did after
// it until the user spoke again.
type hoTurn struct {
	user    []string // the user's words; "[image]" for an image
	summary bool     // the agent's own summary of the conversation before it
	items   []hoItem // the assistant's replies and runs of tool calls, in order
}

// hoItem is one reply, or one run of tool calls between replies (tools not
// nil).
type hoItem struct {
	reply  string
	notice bool     // the reply is the agent's own line, an error it reported, not the model's
	tools  []hoTool // in the order first called
}

// hoTool is one tool of a run: how often it was called, and up to three of
// its arguments.
type hoTool struct {
	name string
	n    int
	args []string
	more bool // it was called with arguments other than these
}

func (it hoItem) calls() int {
	n := 0
	for _, t := range it.tools {
		n += t.n
	}
	return n
}

// hoRead is a conversation read for its handoff.
type hoRead struct {
	compacted  bool // it is read from the agent's last compaction summary
	notices    bool // its file marks the agent's own lines (Claude Code's): a reply no model wrote
	first      bool // a part has been read
	turns      []*hoTurn
	files      []string // changed by tool calls, in order
	seen       map[string]bool
	end        string // what it ended with: user, reply, notice or tools
	lastCall   string
	lastOutput string
}

// turn is the turn the assistant's parts go into.
func (r *hoRead) turn() *hoTurn {
	if len(r.turns) == 0 {
		r.turns = append(r.turns, &hoTurn{})
	}
	return r.turns[len(r.turns)-1]
}

func (r *hoRead) add(p Part) {
	text := strings.TrimSpace(p.Text)
	if text == "" {
		return
	}
	first := !r.first
	r.first = true
	switch {
	case r.compacted && first && p.Role == "user" && (p.Kind == "text" || p.Kind == "context"):
		r.turns = append(r.turns, &hoTurn{user: []string{text}, summary: true})
	case p.Role == "user" && (p.Kind == "text" || p.Kind == "image" || p.Kind == "context" && userWords(text) != ""):
		switch p.Kind {
		case "image":
			text = "[image]"
		case "context":
			text = userWords(text)
		}
		n := len(r.turns)
		if n == 0 || len(r.turns[n-1].items) > 0 || r.turns[n-1].summary {
			r.turns = append(r.turns, &hoTurn{})
		}
		t := r.turns[len(r.turns)-1]
		t.user = append(t.user, text)
		r.end = "user"
	case p.Role == "assistant" && p.Kind == "text":
		// Claude Code's own line, "<synthetic>" (read as no model), is an
		// error it reported, an API's or a sign-in's, in place of a reply
		notice := r.notices && p.Model == ""
		t := r.turn()
		t.items = append(t.items, hoItem{reply: text, notice: notice})
		r.end = "reply"
		if notice {
			r.end = "notice"
		}
	case p.Kind == "tool_use":
		t := r.turn()
		if n := len(t.items); n == 0 || t.items[n-1].tools == nil {
			t.items = append(t.items, hoItem{tools: []hoTool{}})
		}
		run := &t.items[len(t.items)-1]
		name := cmp.Or(strings.TrimSpace(p.Name), "tool")
		arg := toolArg(text)
		i := slices.IndexFunc(run.tools, func(x hoTool) bool { return x.name == name })
		if i < 0 {
			run.tools = append(run.tools, hoTool{name: name})
			i = len(run.tools) - 1
		}
		tool := &run.tools[i]
		tool.n++
		if arg != "" && !slices.Contains(tool.args, arg) {
			if len(tool.args) < 3 {
				tool.args = append(tool.args, arg)
			} else {
				tool.more = true
			}
		}
		for _, f := range changedFiles(name, text) {
			if !r.seen[f] {
				r.seen[f] = true
				r.files = append(r.files, f)
			}
		}
		r.lastCall, r.lastOutput, r.end = toolLine(name, arg), "", "tools"
	case p.Kind == "tool_result":
		r.lastOutput, r.end = text, "tools"
	}
}

var hoTagOpen = regexp.MustCompile(`^<([A-Za-z][\w.-]*)(?:\s[^>]*)?>`)

// userWords is what the user said in a part that starts with what the
// agent put in (textPart's context): the words after its leading tagged
// blocks, as Claude Desktop puts its <system-reminder> before the prompt a
// session was started with, or "" when the agent put in all of it (a
// command's tags, its output, AGENTS.md).
func userWords(text string) string {
	for {
		text = strings.TrimSpace(text)
		m := hoTagOpen.FindStringSubmatch(text)
		if m == nil {
			break
		}
		end := strings.Index(text, "</"+m[1]+">")
		if end < 0 {
			return ""
		}
		text = text[end+len(m[1])+3:]
	}
	if strings.HasPrefix(text, "<") || strings.HasPrefix(text, "# AGENTS.md") {
		return ""
	}
	return text
}

// hoArgKeys are the arguments that say what a tool call was about, the
// first one a call has.
var hoArgKeys = []string{"command", "cmd", "file_path", "filePath", "notebook_path", "path", "pattern", "query", "url", "description", "prompt"}

// toolArg is what a tool call was about, on one short line: its first
// telling argument, or the first line of a call that isn't JSON.
func toolArg(text string) string {
	if strings.HasPrefix(text, "{") {
		var m map[string]any
		if json.Unmarshal([]byte(text), &m) == nil {
			for _, k := range hoArgKeys {
				switch v := m[k].(type) {
				case string:
					if v = strings.TrimSpace(v); v != "" {
						return shortArg(v, strings.Contains(strings.ToLower(k), "path"))
					}
				case []any: // Codex's shell command, word by word
					var words []string
					for _, x := range v {
						if s, ok := x.(string); ok {
							words = append(words, s)
						}
					}
					if len(words) > 0 {
						return shortArg(strings.Join(words, " "), false)
					}
				}
			}
			return ""
		}
	}
	line, _, _ := strings.Cut(text, "\n")
	return shortArg(line, false)
}

// shortArg is an argument on one line of at most hoArgMax characters: a
// path keeps its end, anything else its start.
func shortArg(s string, path bool) string {
	s = strings.ReplaceAll(oneLine(s), "`", "'")
	r := []rune(s)
	if len(r) <= hoArgMax {
		return s
	}
	if path {
		return "…" + string(r[len(r)-hoArgMax+1:])
	}
	return string(r[:hoArgMax-1]) + "…"
}

func toolLine(name, arg string) string {
	if arg == "" {
		return "`" + name + "`"
	}
	return "`" + name + "` — `" + arg + "`"
}

// hoEdits are the tools whose calls change files, by their names in lower
// case (an MCP server's after its prefix): Claude Code's, Codex's patch,
// and other agents' alike.
var hoEdits = map[string]bool{
	"edit": true, "multiedit": true, "write": true, "notebookedit": true, "apply_patch": true,
	"create_file": true, "write_file": true, "edit_file": true, "str_replace_editor": true,
	"str_replace_based_edit_tool": true,
}

var hoPatchFile = regexp.MustCompile(`(?m)^\*\*\* (?:Add|Update|Delete) File: (.+?)\s*$`)

// changedFiles are the files a tool call changes: the path it is given, or
// those a patch names.
func changedFiles(name, text string) []string {
	if i := strings.LastIndex(name, "__"); i >= 0 {
		name = name[i+2:]
	}
	if !hoEdits[strings.ToLower(name)] {
		return nil
	}
	patch := text
	if strings.HasPrefix(text, "{") {
		var m map[string]any
		if json.Unmarshal([]byte(text), &m) == nil {
			for _, k := range []string{"file_path", "filePath", "notebook_path", "path", "target_file"} {
				if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
					return []string{strings.TrimSpace(v)}
				}
			}
			patch = ""
			for _, k := range []string{"input", "patch"} {
				if v, ok := m[k].(string); ok {
					patch = v
					break
				}
			}
		}
	}
	var out []string
	for _, m := range hoPatchFile.FindAllStringSubmatch(patch, -1) {
		out = append(out, m[1])
	}
	return out
}

// hoResult is what writeHandoff says of what it wrote.
type hoResult struct {
	compacted, shortened bool
}

// writeHandoff writes s's condensed record: a heading that says what the
// session was and how it ended, then each turn, the user's words and the
// assistant's replies whole, each run of tool calls one line, and, when it
// ended among tool calls, where it stopped. Thinking, tool output and what
// the agent put in itself are left out; full names the whole conversation's
// file, fullSize its bytes. When it is longer than handoffMax, the first
// turn (what was asked) and the newest are kept whole and the ones between
// shortened, then cut to their first words, then left out, oldest first.
func writeHandoff(w io.Writer, s Session, name, full string, fullSize int, here bool) (hoResult, error) {
	from, compacted := int64(0), false
	if claudeFormat(s.Agent) && s.BranchOf == "" {
		var err error
		if from, compacted, err = lastCompaction(s.Path); err != nil {
			return hoResult{}, err
		}
	}
	r := &hoRead{compacted: compacted, notices: claudeFormat(s.Agent), seen: map[string]bool{}}
	each := func(_ bool, p Part) bool { r.add(p); return true }
	var err error
	if compacted {
		err = claudeContentOf(Call{Agent: s.Agent, File: s.Path, From: from, To: math.MaxInt64}, nil, each)
	} else {
		err = readTranscript(s, each)
	}
	if err != nil {
		return hoResult{}, err
	}

	pieces := make([]hoPieces, len(r.turns))
	for i, t := range r.turns {
		pieces[i] = t.pieces()
	}
	stop := r.stopped()
	b := hoBudget{pieces: pieces, level: make([]int, len(pieces))}
	head := func(shortened bool) string { return r.heading(s, name, full, fullSize, here, shortened) }
	b.fixed = len(head(false)) + len(stop)
	shortened := b.fit(handoffMax)
	if shortened {
		b.fixed = len(head(true)) + len(stop)
		b.fit(handoffMax)
	}

	var out strings.Builder
	out.WriteString(head(shortened))
	out.WriteString(b.render())
	out.WriteString(stop)
	_, err = io.WriteString(w, out.String())
	return hoResult{compacted: compacted, shortened: shortened}, err
}

// heading is what the record is, and what is known of the session.
func (r *hoRead) heading(s Session, name, full string, fullSize int, here, shortened bool) string {
	var b strings.Builder
	title := oneLine(cmp.Or(strings.TrimSpace(s.Title), "(no prompt)"))
	fmt.Fprintf(&b, "# Handoff: %s\n\n", title)
	fmt.Fprintf(&b, "A condensed record of a conversation held in %s, written by magpie for another agent to carry the work on. "+
		"The user's words and the assistant's replies are kept as they were, their own headings two levels down; each run of tool calls is one line; "+
		"tool output, thinking and what the agent added on its own are left out.\n", name)
	if r.compacted {
		b.WriteString("\nThe agent had compacted the conversation before this point: it starts with the agent's own summary of what came earlier.\n")
	}
	if shortened {
		b.WriteString("\nTo keep it short enough to read whole, some turns between the first and the newest are shortened or left out; the newest are whole.\n")
	}
	b.WriteString("\n")
	meta := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "- **%s:** %s\n", k, v)
		}
	}
	meta("Agent", name)
	meta("Session ID", "`"+s.ID+"`")
	if s.Cwd != "" {
		folder := s.Cwd
		if !here {
			folder += " (not on this computer)"
		}
		meta("Folder", folder)
	}
	meta("Started", when(s.Start))
	meta("Last active", when(s.Last))
	var models []string
	for _, m := range s.Models {
		models = append(models, m.Model)
	}
	meta("Models", strings.Join(models, ", "))
	users, replies, calls := 0, 0, 0
	for _, t := range r.turns {
		if len(t.user) > 0 && !t.summary {
			users++
		}
		for _, it := range t.items {
			if it.tools == nil && !it.notice {
				replies++
			}
			calls += it.calls()
		}
	}
	meta("Turns", fmt.Sprintf("%d from the user, %d replies, %d tool calls", users, replies, calls))
	if len(r.files) > 0 {
		shown := r.files[:min(len(r.files), 30)]
		list := "`" + strings.Join(shown, "`, `") + "`"
		if n := len(r.files) - len(shown); n > 0 {
			list += fmt.Sprintf(" (and %d more)", n)
		}
		meta("Files changed", list)
	}
	switch r.end {
	case "reply":
		meta("How it ended", "with the assistant's reply, the last below")
	case "user":
		meta("How it ended", "with the user's last message, which has no reply")
	case "notice":
		meta("How it ended", "with an error the agent reported in place of a reply: the user's last message wasn't answered")
	case "tools":
		meta("How it ended", "while the assistant was using tools, with no reply after them: it may have been cut off (see Where it stopped)")
	default:
		meta("How it ended", "nothing was said")
	}
	meta("Whole conversation", fmt.Sprintf("`%s` (%s): every tool call and its output; too long to read whole, so search it for what you need", full, byteSize(fullSize)))
	b.WriteString("\n---\n")
	return b.String()
}

// stopped is where a conversation that ended among tool calls stopped: its
// last call, and the end of what that call answered.
func (r *hoRead) stopped() string {
	if r.end != "tools" || r.lastCall == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Where it stopped\n\nThe last tool call: " + r.lastCall + "\n\n")
	if r.lastOutput == "" {
		b.WriteString("It has no output: the call didn't finish.\n")
	} else {
		b.WriteString("The end of its output:\n\n")
		b.WriteString(fence(tailRunes(r.lastOutput, hoLastOutput)))
	}
	return b.String()
}

// hoPieces are a turn rendered each way the budget may pick.
type hoPieces struct {
	head, headShort, headMin string   // the user's words: whole, shortened, first words
	items, short             []string // each reply or run: whole, shortened
	tail                     string   // what the turn cut to its first words left out
	replies, calls           []int    // of each item
}

func (t *hoTurn) pieces() hoPieces {
	var p hoPieces
	words := func(max, each int) string {
		var parts []string
		for _, u := range t.user {
			parts = append(parts, cutRunes(nested(u), each))
		}
		return cutRunes(strings.Join(parts, "\n\n"), max)
	}
	switch {
	case t.summary:
		p.head = "\n## Summary of the conversation before, written by the agent\n\n" + words(hoUserMax, hoUserMax) + "\n"
		p.headShort = "\n## Summary of the conversation before, written by the agent (shortened)\n\n" + words(hoShortUser, hoShortUser) + "\n"
		p.headMin = "\n## Summary of the conversation before, written by the agent (shortened)\n\n" + words(hoMinUser, hoMinUser) + "\n"
	case len(t.user) > 0:
		p.head = "\n## User\n\n" + words(hoUserMax, hoPartMax) + "\n"
		p.headShort = "\n## User (shortened)\n\n" + words(hoShortUser, hoShortUser) + "\n"
		p.headMin = "\n## User (shortened)\n\n" + words(hoMinUser, hoMinUser) + "\n"
	}
	if len(t.items) > 0 {
		p.head += "\n## Assistant\n\n"
		p.headShort += "\n## Assistant\n\n"
	}
	replies, calls := 0, 0
	for _, it := range t.items {
		if it.notice {
			line := "> The agent's own notice, not a reply: " + oneLine(cutRunes(it.reply, 500)) + "\n\n"
			p.items, p.short = append(p.items, line), append(p.short, line)
			p.replies, p.calls = append(p.replies, 0), append(p.calls, 0)
			continue
		}
		if it.tools == nil {
			reply := nested(it.reply)
			p.items = append(p.items, cutRunes(reply, hoPartMax)+"\n\n")
			p.short = append(p.short, cutRunes(reply, hoShortReply)+"\n\n")
			p.replies, p.calls = append(p.replies, 1), append(p.calls, 0)
			replies++
			continue
		}
		var whole, brief []string
		for _, tool := range it.tools {
			n := tool.name
			if tool.n > 1 {
				n += fmt.Sprintf(" ×%d", tool.n)
			}
			brief = append(brief, n)
			if len(tool.args) > 0 {
				args := "`" + strings.Join(tool.args, "`; `") + "`"
				if tool.more {
					args += "; …"
				}
				n += " (" + args + ")"
			}
			whole = append(whole, n)
		}
		p.items = append(p.items, "> Tools: "+strings.Join(whole, " · ")+"\n\n")
		p.short = append(p.short, "> Tools: "+strings.Join(brief, " · ")+"\n\n")
		p.replies, p.calls = append(p.replies, 0), append(p.calls, it.calls())
		calls += it.calls()
	}
	if len(t.items) > 0 {
		p.tail = fmt.Sprintf("\n*(%d replies and %d tool calls of this turn left out)*\n", replies, calls)
	}
	return p
}

// hoBudget picks how each turn is written: whole (0), shortened (1), cut to
// its first words (2) or left out (3); the last turn written whole may leave
// out its first items (from).
type hoBudget struct {
	pieces []hoPieces
	level  []int
	from   int // the last turn's first item written
	fixed  int // bytes of the heading and the end
}

func (b *hoBudget) turnSize(i int) int {
	p := b.pieces[i]
	switch b.level[i] {
	case 0:
		n := len(p.head)
		from := 0
		if i == len(b.pieces)-1 {
			from = b.from
			n += len(b.skipped(from))
		}
		for _, s := range p.items[from:] {
			n += len(s)
		}
		return n
	case 1:
		n := len(p.headShort)
		for _, s := range p.short {
			n += len(s)
		}
		return n
	case 2:
		return len(p.headMin) + len(p.tail)
	}
	return 0
}

// skipped is what the last turn says of its first items left out.
func (b *hoBudget) skipped(from int) string {
	if from == 0 {
		return ""
	}
	p := b.pieces[len(b.pieces)-1]
	replies, calls := 0, 0
	for i := range from {
		replies += p.replies[i]
		calls += p.calls[i]
	}
	return fmt.Sprintf("*… %d earlier replies and %d tool calls of this turn left out here.*\n\n", replies, calls)
}

// leftOut is what stands for n turns in a row left out.
func leftOut(n int) string {
	return fmt.Sprintf("\n*… %d turns left out here (see the whole conversation).*\n", n)
}

func (b *hoBudget) size() int {
	n, gap := b.fixed, 0
	for i := range b.pieces {
		if b.level[i] == 3 {
			gap++
			continue
		}
		if gap > 0 {
			n += len(leftOut(gap))
			gap = 0
		}
		n += b.turnSize(i)
	}
	if gap > 0 {
		n += len(leftOut(gap))
	}
	return n
}

// fit picks each turn's way so that all is at most limit bytes, and says
// whether any turn isn't whole.
func (b *hoBudget) fit(limit int) bool {
	n := len(b.pieces)
	for i := range b.level {
		b.level[i] = 0
	}
	b.from = 0
	if n == 0 || b.size() <= limit {
		return false
	}
	last := n - 1
	// the first and the last turn first, the ones between left out: the
	// first shortened, then cut to its first words, and the last leaving
	// out its first items, as far as they must
	for i := 1; i < last; i++ {
		b.level[i] = 3
	}
	for _, lv := range []int{1, 2} {
		if n > 1 && b.size() > limit {
			b.level[0] = lv
		}
	}
	for b.size() > limit && b.from < len(b.pieces[last].items) {
		b.from++
	}
	// then the turns between, newest first: each as whole as fits and no
	// more whole than the newer one; the older ones from the first that
	// doesn't fit at all stay left out
	best := 0
	for i := last - 1; i >= 1; i-- {
		for lv := best; lv < 3; lv++ {
			if b.level[i] = lv; b.size() <= limit {
				best = lv
				break
			}
			b.level[i] = 3
		}
		if b.level[i] == 3 {
			break
		}
	}
	return true
}

func (b *hoBudget) render() string {
	var out strings.Builder
	gap := 0
	for i, p := range b.pieces {
		if b.level[i] == 3 {
			gap++
			continue
		}
		if gap > 0 {
			out.WriteString(leftOut(gap))
			gap = 0
		}
		switch b.level[i] {
		case 0:
			out.WriteString(p.head)
			from := 0
			if i == len(b.pieces)-1 {
				from = b.from
				out.WriteString(b.skipped(from))
			}
			for _, s := range p.items[from:] {
				out.WriteString(s)
			}
		case 1:
			out.WriteString(p.headShort)
			for _, s := range p.short {
				out.WriteString(s)
			}
		case 2:
			out.WriteString(p.headMin + p.tail)
		}
	}
	if gap > 0 {
		out.WriteString(leftOut(gap))
	}
	return out.String()
}

var hoHeading = regexp.MustCompile(`^#{1,6}(?:\s|$)`)

// nested is what was said with its own Markdown headings two levels down,
// below the record's own (its title, each turn's), so none reads as a
// turn; a code block's lines are left as they are.
func nested(text string) string {
	lines := strings.Split(text, "\n")
	fence := ""
	for i, l := range lines {
		t := strings.TrimLeft(l, " ")
		if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = t[:3]
			continue
		}
		if hoHeading.MatchString(l) {
			n := len(l) - len(strings.TrimLeft(l, "#"))
			lines[i] = strings.Repeat("#", min(n+2, 6)) + l[n:]
		}
	}
	return strings.Join(lines, "\n")
}

// cutRunes is s cut to n characters, saying how many more there were.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + fmt.Sprintf(" … (%d more characters)", len(r)-n)
}

// tailRunes is the last n characters of s, saying how many came before.
func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return fmt.Sprintf("(%d characters before) …\n", len(r)-n) + string(r[len(r)-n:])
}

// byteSize is a size as a person reads it.
func byteSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}
