package gateway

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/libraryuse"
	"github.com/yetone/magpie/internal/settings"
)

type skillBlock = map[string]any

func skillCall(id, tool, key, value string) skillBlock {
	return skillBlock{"type": "tool_use", "id": id, "name": tool, "input": skillBlock{key: value}}
}
func skillResult(id, content string) skillBlock {
	return skillBlock{"type": "tool_result", "tool_use_id": id, "content": content}
}
func skillWire(t *testing.T, calls, results []skillBlock, later ...skillBlock) []byte {
	t.Helper()
	msgs := []skillBlock{{"role": "assistant", "content": calls}, {"role": "user", "content": results}}
	raw, err := json.Marshal(skillBlock{"model": "test/missing", "max_tokens": 10, "messages": append(msgs, later...)})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func skillEntry(t *testing.T, root, n string) string {
	t.Helper()
	dir := filepath.Join(root, n)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(p, []byte("not collected"), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestForkSkillUseLaunch(t *testing.T) {
	// Actual Claude Code 2.1.316 request in the tree. Only substitute the
	// skill identity and directory for one in the isolated Library.
	raw, err := os.ReadFile("testdata/claude-code-skill-launch.json")
	if err != nil {
		t.Fatal(err)
	}
	lib := t.TempDir()
	claude := t.TempDir()
	dir := filepath.Dir(skillEntry(t, lib, "greet"))
	quoted, _ := json.Marshal(dir)
	var w skillEventWindow
	count := 0
	record := func(n, c string, read bool) {
		if n != "greet" || c != "claude" || read {
			t.Errorf("wrong launch: %q, %q, %v", n, c, read)
		}
		count++
	}
	w.observe(raw, lib, claude, record)
	if count != 0 {
		t.Fatal("router catalog counted")
	}
	raw = bytes.ReplaceAll(raw, []byte("magpie-skills"), []byte("greet"))
	raw = bytes.ReplaceAll(raw, []byte("/Users/u/.claude/skills/greet"), quoted[1:len(quoted)-1])
	w.observe(raw, lib, claude, record)
	w.observe(raw, lib, claude, record)
	if count != 1 {
		t.Fatalf("recorded %d launches for one successful tool id", count)
	}
}

func TestForkSkillUseProof(t *testing.T) {
	lib := t.TempDir()
	claude := t.TempDir()
	p := skillEntry(t, lib, "greet")
	copyPath := skillEntry(t, claude, "greet")
	other := skillEntry(t, t.TempDir(), "greet")
	call := skillCall("r", "Read", "file_path", p)
	read := skillResult("r", "1\t---\n2\tsecret")
	launch := skillCall("s", "Skill", "skill", "greet")
	receipt := skillResult("s", "Launching skill: greet\n\n<system-reminder>budget</system-reminder>")
	base := func(p string) skillBlock {
		return skillBlock{"type": "text", "text": "Base directory for this skill: " + filepath.Dir(p) + "\n\nprivate skill text"}
	}
	bad := skillResult("r", "1\t---")
	bad["is_error"] = true
	type proofCase struct {
		name           string
		calls, results []skillBlock
		want           string
	}
	cases := []proofCase{
		{"both", []skillBlock{call, launch}, []skillBlock{receipt, read, base(p)}, "CR"},
		{"same name not same skill", []skillBlock{launch}, []skillBlock{receipt, base(copyPath)}, ""},
		{"no base", []skillBlock{launch}, []skillBlock{receipt}, ""},
		{"no launch", []skillBlock{launch}, []skillBlock{skillResult("s", "Unknown skill"), base(p)}, ""},
		{"wrong launch", []skillBlock{launch}, []skillBlock{skillResult("s", "Launching skill: greet-next"), base(p)}, ""},
		{"call only", []skillBlock{call}, nil, ""}, {"orphan", nil, []skillBlock{read}, ""},
		{"different id", []skillBlock{call}, []skillBlock{skillResult("other", "1\t---")}, ""},
		{"failed", []skillBlock{call}, []skillBlock{bad}, ""},
		{"cache notice", []skillBlock{call}, []skillBlock{skillResult("r", "File unchanged since last read")}, ""},
		{"listing", []skillBlock{skillCall("r", "Glob", "file_path", p)}, []skillBlock{read}, ""},
		{"mcp namesake", []skillBlock{skillCall("r", "mcp__fs__Read", "file_path", p)}, []skillBlock{read}, ""},
		{"duplicate call id", []skillBlock{call, call}, []skillBlock{read}, ""},
		{"duplicate result id", []skillBlock{call}, []skillBlock{read, read}, "R"},
	}
	for _, path := range []string{copyPath, other, filepath.Dir(p), p + ".bak", filepath.Join(lib, "greet", "ref", "SKILL.md"), filepath.Join("greet", "SKILL.md"), lib + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(lib) + string(filepath.Separator) + "greet" + string(filepath.Separator) + "SKILL.md"} {
		cases = append(cases, proofCase{"path " + path, []skillBlock{skillCall("r", "Read", "file_path", path)}, []skillBlock{read}, ""})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var w skillEventWindow
			got := ""
			w.observe(skillWire(t, tc.calls, tc.results), lib, claude, func(n, c string, r bool) {
				if n != "greet" || c != "claude" {
					t.Errorf("unexpected identity %q/%q", n, c)
				}
				if r {
					got += "R"
				} else {
					got += "C"
				}
			})
			if got != tc.want {
				t.Errorf("events %q, want %q", got, tc.want)
			}
		})
	}
	var w skillEventWindow
	never := func(string, string, bool) { t.Fatal("counted old or malformed exchange") }
	w.observe(skillWire(t, []skillBlock{call}, []skillBlock{read}, skillBlock{"role": "user", "content": "next prompt"}), lib, claude, never)
	w.observe(append(skillWire(t, []skillBlock{call}, []skillBlock{read}), '?'), lib, claude, never)
}

func TestForkSkillUseRoutedRead(t *testing.T) {
	lib := t.TempDir()
	claude := t.TempDir()
	p := skillEntry(t, lib, "greet")
	for _, dir := range []string{claude, filepath.Join(claude, "magpie-skills", ".skills")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "greet")
		if err := os.Symlink(filepath.Dir(p), link); err != nil {
			t.Fatal(err)
		}
		var w skillEventWindow
		got := 0
		w.observe(skillWire(t, []skillBlock{skillCall("r", "Read", "file_path", filepath.Join(link, "SKILL.md"))}, []skillBlock{skillResult("r", "2\tdata")}), lib, claude, func(n, c string, r bool) {
			if n != "greet" || !r {
				t.Fatal(n, r)
			}
			got++
		})
		if got != 1 {
			t.Fatalf("link %s: %d reads", dir, got)
		}
	}
}

func TestForkSkillUseGateway(t *testing.T) {
	fresh(t)
	name := "gateway-probe"
	if err := libraryuse.Observe(nil, []string{name}); err != nil {
		t.Fatal(err)
	}
	p := skillEntry(t, filepath.Join(settings.Dir(), "library", "skills"), name)
	id := "toolu_" + rand.Text()
	// Exercise the Messages entry once per request, not provider attempts.
	handler := New().Handler()
	send := func(addr, ua, forward string) {
		raw := skillWire(t, []skillBlock{skillCall(id, "Read", "file_path", p)}, []skillBlock{skillResult(id, "1\t---")})
		r := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(raw))
		r.RemoteAddr = addr
		r.Header.Set("User-Agent", ua)
		r.Header.Set("X-Forwarded-For", forward)
		r.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(httptest.NewRecorder(), r)
	}
	total := func() int64 {
		snap, err := libraryuse.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		return snap.Report("skill", name).Days30.Reads
	}
	send("192.0.2.1:1234", "claude-cli/2.1.330", "")
	send("127.0.0.1:1234", "claude-cli/2.1.330", "192.0.2.2")
	send("127.0.0.1:1234", "other", "")
	if n := total(); n != 0 {
		t.Fatalf("remote/other agent recorded %d reads", n)
	}
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() { send("127.0.0.1:1234", "claude-cli/2.1.330", "") })
	}
	group.Wait()
	if n := total(); n != 1 {
		t.Fatalf("got %d reads after concurrent replay, want 1", n)
	}
}
