package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/libraryuse"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// Only direct, local Claude Code Messages requests report this particular
// tool protocol. A model proposing a call is not evidence the tool ran.
var claudeSkillEvents skillEventWindow

func observeSkillUse(r *http.Request, from provider.Protocol, body []byte) {
	if from != provider.Anthropic || !fromClaudeCode(r.Header) || !local(r) || isClaudeDesktop(r) || isClaudeScience(r) {
		return
	}
	// Even a trusted reverse proxy doesn't establish a local filesystem.
	if r.Header.Get(HopsHeader) != "" || slices.ContainsFunc(forwardHeaders, func(h string) bool { return r.Header.Get(h) != "" }) {
		return
	}
	claudeSkillEvents.observe(body, filepath.Join(settings.Dir(), "library", "skills"), filepath.Join(appdir.ClaudeDir(), "skills"), func(name, client string, read bool) {
		// A failed local stats write must not change the client's request.
		libraryuse.RecordSkill(name, client, read)
	})
}

// Claude's tool_use id identifies the same execution across HTTP retries
// (and within a replay). Keep only bounded ids, never request slices.
// A replay after eviction or a gateway restart is indistinguishable.
type skillEventWindow struct {
	mu    sync.Mutex
	seen  map[string]bool
	queue [4096]string
	next  int
}

func (w *skillEventWindow) first(id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seen[id] {
		return false
	}
	if w.seen == nil {
		w.seen = make(map[string]bool)
	}
	delete(w.seen, w.queue[w.next])
	w.queue[w.next] = id
	w.next = (w.next + 1) % len(w.queue)
	w.seen[id] = true
	return true
}

func skillField(obj []byte, key string, max int) string {
	raw := memberOrNil(obj, key)
	if len(raw) > max {
		return ""
	}
	v, _ := stringBytes(raw)
	return string(v)
}

// skillFile recognizes only the Library's actual entry, directly or at
// the two paths Claude receives. Compare directory identity (junctions too)
// before accepting SKILL.md; no stat of arbitrary requested paths and no
// open/read of the skill. Copies and unrecognized paths are not evidence.
func skillFile(path, lib, claude string) string {
	path = filepath.FromSlash(path)
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.HasPrefix(filepath.VolumeName(path), `\\`) || filepath.Base(path) != "SKILL.md" {
		return ""
	}
	dir := filepath.Dir(path)
	name := filepath.Base(dir)
	if len(name) > 64 || !safeID([]byte(name)) || name[0] == '-' || name[0] == '_' || name == "magpie-skills" || !filepath.IsLocal(name) {
		return ""
	}
	parent := filepath.Dir(dir)
	if parent != lib && parent != claude && parent != filepath.Join(claude, "magpie-skills", ".skills") {
		return ""
	}
	want := filepath.Join(lib, name)
	if dir != want {
		a, err := os.Stat(want)
		if err != nil || !a.IsDir() {
			return ""
		}
		b, err := os.Stat(dir)
		if err != nil || !os.SameFile(a, b) {
			return ""
		}
	}
	f, err := os.Lstat(filepath.Join(want, "SKILL.md"))
	if err != nil || !f.Mode().IsRegular() {
		return ""
	}
	return name
}

// The first line of Claude's text block says which directory was loaded.
// Unquote that line alone, never the SKILL.md text that follows it.
func skillBase(block []byte) string {
	raw := memberOrNil(block, "text")
	const prefix = `"Base directory for this skill: `
	if !hasPrefix(raw, prefix) {
		return ""
	}
	raw = raw[len(prefix):min(len(raw), len(prefix)+32768)]
	end := bytes.Index(raw, []byte(`\n\n`))
	if end < 1 {
		return ""
	}
	var path string
	if json.Unmarshal(append(append([]byte{'"'}, raw[:end]...), '"'), &path) != nil || !filepath.IsAbs(path) || filepath.Clean(filepath.FromSlash(path)) != filepath.FromSlash(path) {
		return ""
	}
	return path
}

// A normal Read of text returns numbered lines. Look only at its numeric
// prefix and separator, not its payload: cache notices, denials, empty
// files and other result forms aren't counted as reads.
func numberedSkillRead(raw []byte) bool {
	if len(raw) < 4 || raw[0] != '"' || raw[1] < '1' || raw[1] > '9' {
		return false
	}
	i := 2
	for i < len(raw) && raw[i] >= '0' && raw[i] <= '9' {
		i++
	}
	return hasPrefix(raw[i:], `\t`)
}

func (w *skillEventWindow) observe(body []byte, lib, claude string, record func(string, string, bool)) {
	if !bodyHas(body, `"tool_result"`) {
		return
	}
	msgs, _, ok := topMember(body, "messages")
	if !ok || len(msgs) == 0 || msgs[0] != '[' {
		return
	}
	var prev, last []byte
	eachElement(msgs, func(m []byte) bool { prev, last = last, m; return true })
	if skillField(last, "role", 16) != "user" || skillField(prev, "role", 16) != "assistant" {
		return
	}
	results := elementsOf(memberOrNil(last, "content"))
	if len(results) == 0 {
		return
	}
	calls := map[string][]byte{}
	for _, b := range elementsOf(memberOrNil(prev, "content")) {
		if skillField(b, "type", 24) != "tool_use" {
			continue
		}
		id := skillField(b, "id", 128)
		if !safeID([]byte(id)) {
			continue
		}
		if _, dup := calls[id]; dup {
			calls[id] = nil
		} else {
			calls[id] = b
		}
	}
	for _, b := range results {
		if skillField(b, "type", 24) != "tool_result" {
			continue
		}
		id := skillField(b, "tool_use_id", 128)
		c := calls[id]
		if c == nil {
			continue
		}
		if e := memberOrNil(b, "is_error"); e != nil && string(e) != "false" {
			continue
		}
		input := memberOrNil(c, "input")
		content := memberOrNil(b, "content")
		name := ""
		read := false
		switch skillField(c, "name", 16) {
		case "Read":
			if !numberedSkillRead(content) {
				continue
			}
			name = skillFile(skillField(input, "file_path", 32768), lib, claude)
			read = true
		case "Skill":
			n := skillField(input, "skill", 128)
			if !safeID([]byte(n)) {
				continue
			}
			launch := `"Launching skill: ` + n
			// Claude 2.1.330 appends a reminder after a blank line; the
			// launch receipt is its complete first line, never a substring.
			if string(content) != launch+`"` && !hasPrefix(content, launch+`\n\n`) {
				continue
			}
			for _, text := range results {
				if skillField(text, "type", 24) != "text" {
					continue
				}
				if base := skillBase(text); base != "" && filepath.Base(base) == n && skillFile(filepath.Join(base, "SKILL.md"), lib, claude) == n {
					name = n
					break
				}
			}
		}
		if name != "" && w.first(id) {
			record(name, "claude", read)
		}
	}
}
