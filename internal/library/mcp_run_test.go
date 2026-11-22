package library

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/libraryuse"
	"github.com/yetone/magpie/internal/proc"
)

// The fake server magpie runs for Codex: this test binary again, with
// MAGPIE_FAKE_RUN naming a log it writes a line to for each start (its
// pid and the client named in the initialize it was given).
func TestFakeRunServer(t *testing.T) {
	logf := os.Getenv("MAGPIE_FAKE_RUN")
	if logf == "" {
		t.Skip("run by the run tests")
	}
	out := bufio.NewWriter(os.Stdout)
	say := func(v any) {
		b, _ := json.Marshal(v)
		out.Write(append(b, '\n'))
		out.Flush()
	}
	sc := bufio.NewScanner(os.Stdin)
	var asked json.RawMessage // the tools/call waiting for the client's roots
	for sc.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name       string `json:"name"`
				ClientInfo struct {
					Name string `json:"name"`
				} `json:"clientInfo"`
			} `json:"params"`
			Result json.RawMessage `json:"result"`
		}
		json.Unmarshal(sc.Bytes(), &m)
		switch {
		case m.Method == "initialize":
			l, _ := os.OpenFile(logf, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(l, "%d %s\n", os.Getpid(), m.Params.ClientInfo.Name)
			l.Close()
			say(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{"listChanged": true}}, "serverInfo": map[string]any{"name": "fake", "version": "1"}}})
		case m.Method == "tools/list":
			say(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"tools": []any{map[string]any{"name": "echo"}, map[string]any{"name": fmt.Sprint("pid", os.Getpid())}}}})
		case m.Method == "tools/call" && m.Params.Name == "tool-error":
			say(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "private error detail"}}}})
		case m.Method == "tools/call" && m.Params.Name == "rpc-error":
			say(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32603, "message": "private RPC detail"}})
		case m.Method == "tools/call" && m.Params.Name == "ask":
			asked = m.ID
			say(map[string]any{"jsonrpc": "2.0", "id": 77, "method": "roots/list"})
		case m.Method == "tools/call" && m.Params.Name == "change":
			say(map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"})
			say(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"content": []any{}}})
		case m.Method == "tools/call":
			say(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": fmt.Sprint(os.Getpid())}}}})
		case m.Method == "" && string(m.ID) == "77":
			say(map[string]any{"jsonrpc": "2.0", "id": asked, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "roots " + string(m.Result)}}}})
		}
	}
	os.Exit(0)
}

// runFixture is the fake server given as "fake", served as the gateway
// serves it, and the log of its starts.
func runFixture(t *testing.T) (*httptest.Server, string, runSpec) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logf := filepath.Join(t.TempDir(), "starts")
	spec := runSpec{Command: self, Args: []string{"-test.run=^TestFakeRunServer$"}, Env: map[string]string{"MAGPIE_FAKE_RUN": logf}}
	resetRuns(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	giveRunFixture(t, &spec)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeRun(w, r, strings.TrimPrefix(r.URL.Path, RunPath))
	}))
	t.Cleanup(srv.Close)
	return srv, logf, spec
}

// giveRunFixture writes what a sync that gave (or withdrew) the fake records.
func giveRunFixture(t *testing.T, spec *runSpec) {
	t.Helper()
	l := &Library{CodexMCPOnDemand: true}
	given := map[string]runSpec{}
	if spec != nil {
		l.MCP = []*Server{{Name: "fake", Transport: "stdio", Command: spec.Command, Args: spec.Args, Env: spec.Env, Agents: []string{"codex"}}}
		l.Applied = map[string]*Applied{"codex": {MCP: []string{"fake"}, MCPRun: []string{"fake"}}}
		given["fake"] = *spec
	}
	if err := l.save(); err != nil {
		t.Fatal(err)
	}
	runGiven(given)
}

// resetRuns ends every session's server, before the test and after it.
func resetRuns(t *testing.T) {
	end := func() {
		runs.Lock()
		all := runs.sessions
		runs.sessions, runs.given, runs.said = nil, nil, nil
		runs.Unlock()
		for _, s := range all {
			s.mu.Lock()
			c := s.child
			s.mu.Unlock()
			s.stop()
			if c != nil {
				<-c.done
			}
		}
	}
	end()
	t.Cleanup(end)
}

func starts(t *testing.T, logf string) []string {
	t.Helper()
	b, _ := os.ReadFile(logf)
	return strings.Fields(strings.ReplaceAll(string(b), "\n", " | "))
}

func startCount(logf string) int {
	b, _ := os.ReadFile(logf)
	return strings.Count(string(b), "\n")
}

// call posts one message as Codex does, and is the reply's body and the
// session id it was given.
func call(t *testing.T, srv *httptest.Server, sid, body string) (string, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", srv.URL+RunPath+"fake", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 && resp.StatusCode != 202 {
		t.Fatalf("%s: %d %s", body, resp.StatusCode, b)
	}
	got := string(b)
	// an event stream's last message is the reply
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		var last string
		for _, l := range strings.Split(got, "\n") {
			if d, ok := strings.CutPrefix(l, "data: "); ok {
				last = d
			}
		}
		got = last
	}
	return got, resp.Header.Get("Mcp-Session-Id")
}

const runInit = `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"%s","version":"1"}}}`

// The HTTP and event-stream branches see the same final reply. A notification
// mid-call (change), or a failed call's HTTP 200, is not another success.
func TestRunRecordsToolUse(t *testing.T) {
	sandbox(t)
	_, _, spec := runFixture(t)
	post := func(method, params, accept, sid string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", RunPath+"fake", strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":12,"method":%q,"params":%s}`, method, params)))
		req.Header.Set("Accept", accept)
		req.Header.Set("Mcp-Session-Id", sid)
		w := httptest.NewRecorder()
		ServeRun(w, req, "fake")
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
		}
		return w
	}
	w := post("initialize", `{"protocolVersion":"2025-06-18","clientInfo":{"name":"private-client-name"}}`, "application/json", "")
	sid := w.Header().Get("Mcp-Session-Id")
	post("tools/list", `{}`, "application/json", sid)
	post("ping", `{}`, "application/json", sid)
	checkMCPCount(t, "fake", 0, 0)
	// Only echo and pid were actually advertised. Preserve totals for other
	// calls without persisting arbitrary names submitted by the client.
	post("tools/call", `{"name":"echo"}`, "application/json", sid)
	count, failed := int64(1), int64(0)
	for _, accept := range []string{"application/json", "text/event-stream"} {
		for _, tool := range []string{"change", "tool-error", "rpc-error"} {
			post("tools/call", fmt.Sprintf(`{"name":%q,"arguments":{"text":"private request"}}`, tool), accept, sid)
			count++
			if tool != "change" {
				failed++
			}
			checkMCPCount(t, "fake", count, failed)
		}
	}
	// A valid call whose command can't start never reaches the reply reader.
	spec.Command = filepath.Join(t.TempDir(), "missing-program")
	giveRunFixture(t, &spec)
	post("tools/call", `{"name":"change"}`, "application/json", sid)
	use := checkMCPCount(t, "fake", count+1, failed+1)
	if len(use.Tools) != 2 || use.Tools["echo"].Days30.Count != 1 || use.Tools["(other)"].Days30.Count != 7 || use.Tools["(other)"].Days30.Failed != 5 {
		t.Fatalf("run tools: %+v", use.Tools)
	}
	if len(use.ByClient) != 1 || use.ByClient["codex"].Days30.Count != 8 {
		t.Fatalf("run clients: %+v", use.ByClient)
	}
}

func checkMCPCount(t *testing.T, server string, calls, failed int64) libraryuse.Summary {
	t.Helper()
	s, err := libraryuse.Report("mcp", server)
	if err != nil {
		t.Fatal(err)
	}
	if w := s.Days30; w.Count != calls || w.Failed != failed {
		t.Fatalf("%s: got %d calls, %d failures; want %d, %d", server, w.Count, w.Failed, calls, failed)
	}
	return s
}

// A second thread's session is initialized and listed from what the first
// one's server said, with no server of its own; its server starts on its
// first call, initialized as that thread itself asked.
func TestRunStartsOnCall(t *testing.T) {
	srv, logf, _ := runFixture(t)
	r, a := call(t, srv, "", fmt.Sprintf(runInit, "thread-a"))
	if a == "" || !strings.Contains(r, `"serverInfo"`) || !strings.Contains(r, `"id":0`) {
		t.Fatalf("initialize: %s (session %q)", r, a)
	}
	call(t, srv, a, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	tools, _ := call(t, srv, a, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if !strings.Contains(tools, `"echo"`) || !strings.Contains(tools, `"id":1`) {
		t.Fatalf("tools/list: %s", tools)
	}
	if n := startCount(logf); n != 1 {
		t.Fatalf("%d starts for the first session: %v", n, starts(t, logf))
	}
	for i := range 5 {
		r, b := call(t, srv, "", fmt.Sprintf(runInit, fmt.Sprint("idle-", i)))
		if b == "" || b == a || !strings.Contains(r, `"serverInfo"`) {
			t.Fatalf("initialize %d: %s (session %q)", i, r, b)
		}
		call(t, srv, b, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
		if got, _ := call(t, srv, b, `{"jsonrpc":"2.0","id":"x","method":"tools/list"}`); !strings.Contains(got, `"id":"x"`) || !strings.Contains(got, `"echo"`) {
			t.Fatalf("tools/list %d: %s", i, got)
		}
		if got, _ := call(t, srv, b, `{"jsonrpc":"2.0","id":2,"method":"ping"}`); !strings.Contains(got, `"result":{}`) {
			t.Fatalf("ping: %s", got)
		}
	}
	if n := startCount(logf); n != 1 {
		t.Fatalf("idle sessions started servers: %v", starts(t, logf))
	}
	r, b := call(t, srv, "", fmt.Sprintf(runInit, "thread-b"))
	call(t, srv, b, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	got, _ := call(t, srv, b, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"echo","arguments":{}}}`)
	if !strings.Contains(got, `"id":9`) || !strings.Contains(got, `"text"`) {
		t.Fatalf("tools/call: %s (init %s)", got, r)
	}
	if s := starts(t, logf); startCount(logf) != 2 || !strings.Contains(strings.Join(s, " "), "thread-b") {
		t.Fatalf("starts: %v", s)
	}
	// the first session keeps its own server
	got2, _ := call(t, srv, a, `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"echo","arguments":{}}}`)
	if startCount(logf) != 2 || got2 == got {
		t.Fatalf("session a: %s, b: %s, starts %v", got2, got, starts(t, logf))
	}
}

// A session's server ends once it has been idle runIdle, and the next call
// starts it again, initialized as the session was.
func TestRunIdleEnds(t *testing.T) {
	srv, logf, _ := runFixture(t)
	_, a := call(t, srv, "", fmt.Sprintf(runInit, "thread-a"))
	first, _ := call(t, srv, a, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo"}}`)
	runs.Lock()
	s := runs.sessions[a]
	runs.Unlock()
	c := s.running()
	if c == nil {
		t.Fatal("no server after a call")
	}
	runSweep(time.Now().Add(runIdle / 2))
	if s.running() == nil {
		t.Fatal("ended before it was idle")
	}
	runSweep(time.Now().Add(runIdle + time.Minute))
	select {
	case <-c.done:
	case <-time.After(30 * time.Second):
		t.Fatal("an idle server wasn't ended")
	}
	again, _ := call(t, srv, a, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo"}}`)
	if !strings.Contains(again, `"id":2`) || again == first {
		t.Fatalf("after idle: %s (was %s)", again, first)
	}
	if got := starts(t, logf); startCount(logf) != 2 || strings.Count(strings.Join(got, " "), "thread-a") != 2 {
		t.Fatalf("starts: %v", got)
	}
	// DELETE ends the session and its server
	c = s.running()
	req, _ := http.NewRequest("DELETE", srv.URL+RunPath+"fake", nil)
	req.Header.Set("Mcp-Session-Id", a)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	select {
	case <-c.done:
	case <-time.After(30 * time.Second):
		t.Fatal("DELETE left the server running")
	}
}

// What the server asks the client mid-call comes on the call's stream, and
// the client's answer goes to the server.
func TestRunServerAsks(t *testing.T) {
	srv, _, _ := runFixture(t)
	_, a := call(t, srv, "", fmt.Sprintf(runInit, "thread-a"))
	req, _ := http.NewRequest("POST", srv.URL+RunPath+"fake", strings.NewReader(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"ask"}}`))
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", a)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rd := bufio.NewReader(resp.Body)
	next := func() string {
		for {
			l, err := rd.ReadString('\n')
			if err != nil {
				t.Fatalf("stream: %v", err)
			}
			if d, ok := strings.CutPrefix(strings.TrimSpace(l), "data: "); ok {
				return d
			}
		}
	}
	if q := next(); !strings.Contains(q, `"roots/list"`) || !strings.Contains(q, `"id":77`) {
		t.Fatalf("asked: %s", q)
	}
	call(t, srv, a, `{"jsonrpc":"2.0","id":77,"result":{"roots":[]}}`)
	if r := next(); !strings.Contains(r, `"id":5`) || !strings.Contains(r, `roots {\"roots\":[]}`) {
		t.Fatalf("reply: %s", r)
	}
}

// A server saying its tools changed has the next session's tools/list go
// to a server again.
func TestRunListChanged(t *testing.T) {
	srv, _, _ := runFixture(t)
	_, a := call(t, srv, "", fmt.Sprintf(runInit, "a"))
	before, _ := call(t, srv, a, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	_, b := call(t, srv, "", fmt.Sprintf(runInit, "b"))
	call(t, srv, b, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"change"}}`)
	// the notification is read before the reply after it
	after, _ := call(t, srv, b, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if before == after {
		t.Fatalf("tools/list answered from before the change: %s", after)
	}
}

// A server given no longer, or with another command, has its sessions'
// servers ended; a new session of one gone is refused.
func TestRunGivenChanged(t *testing.T) {
	srv, logf, spec := runFixture(t)
	_, a := call(t, srv, "", fmt.Sprintf(runInit, "a"))
	call(t, srv, a, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo"}}`)
	runs.Lock()
	s := runs.sessions[a]
	runs.Unlock()
	c := s.running()
	giveRunFixture(t, &spec) // the same: kept
	if s.running() != c {
		t.Fatal("a sync giving the same server ended it")
	}
	changed := spec
	changed.Args = append([]string{"-test.v"}, spec.Args...)
	giveRunFixture(t, &changed)
	<-c.done
	call(t, srv, a, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo"}}`)
	if startCount(logf) != 2 {
		t.Fatalf("starts after the change: %v", starts(t, logf))
	}
	c = s.running()
	giveRunFixture(t, nil)
	<-c.done
	req, _ := http.NewRequest("POST", srv.URL+RunPath+"fake", strings.NewReader(fmt.Sprintf(runInit, "c")))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a server given no longer: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest("POST", srv.URL+RunPath+"fake", strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo"}}`))
	req.Header.Set("Mcp-Session-Id", a)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || s.running() != nil {
		t.Fatalf("a session of a server given no longer: %d", resp.StatusCode)
	}
}

// A session magpie didn't see start (Codex kept it over a restart of
// magpie's) is taken up as it is.
func TestRunUnknownSession(t *testing.T) {
	srv, logf, _ := runFixture(t)
	got, sid := call(t, srv, "old-session", `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo"}}`)
	if sid != "old-session" || !strings.Contains(got, `"id":4`) || strings.Contains(got, `"error"`) {
		t.Fatalf("%s (session %q)", got, sid)
	}
	if startCount(logf) != 1 {
		t.Fatalf("starts: %v", starts(t, logf))
	}
}

// Codex on magpie is given a command's server as the gateway's, and a
// sync after reads it back as magpie's own; off magpie it gets the
// command again.
func TestRunCodexEntry(t *testing.T) {
	h := sandbox(t)
	resetRuns(t)
	runOn = true
	wired := true
	was := runWired
	runWired = func(a *agent.Agent) bool { return wired }
	t.Cleanup(func() { runOn, runWired = false, was })
	if _, err := SaveServer("", Server{Name: "mem", Transport: "stdio", Command: "node", Args: []string{"server.js"}, Agents: []string{"codex", "claude"}}); err != nil {
		t.Fatal(err)
	}
	if codex := read(t, filepath.Join(h, ".codex/config.toml")); strings.Contains(codex, RunPath) || !strings.Contains(codex, `"server.js"`) {
		t.Fatalf("wired with no opt-in:\n%s", codex)
	}
	if runWiringChanged("codex") {
		t.Fatal("a rewire would sync with the option off")
	}
	ok(t)(SetCodexMCPOnDemand(true))
	if runWiringChanged("codex") {
		t.Fatal("a sync didn't remember its wiring")
	}
	local := targetByID("codex")
	if l, err := load(); err != nil || l.runsFor(local, "http://192.0.2.10:3425") {
		t.Fatalf("remote gateway eligible: %v", err)
	}
	if _, err := SaveServer("", Server{Name: "keyed", Transport: "stdio", Command: "node", Env: map[string]string{"MAGPIE_TEST_UNSET_VAR": "${MAGPIE_TEST_UNSET_VAR}"}, Agents: []string{"codex"}}); err != nil {
		t.Fatal(err)
	}
	codex := read(t, filepath.Join(h, ".codex/config.toml"))
	if !strings.Contains(codex, RunPath+"mem") || strings.Contains(codex, `"server.js"`) {
		t.Fatalf("codex on magpie:\n%s", codex)
	}
	if !strings.Contains(codex, `MAGPIE_TEST_UNSET_VAR`) || strings.Contains(codex, RunPath+"keyed") {
		t.Fatalf("a server with a variable magpie hasn't:\n%s", codex)
	}
	if claude := read(t, filepath.Join(h, ".claude.json")); !strings.Contains(claude, `"server.js"`) || strings.Contains(claude, RunPath) {
		t.Fatalf("claude:\n%s", claude)
	}
	runs.Lock()
	_, given := runs.given["mem"]
	runs.Unlock()
	if !given {
		t.Fatal("the sync didn't record the server it gave")
	}
	if r, err := Sync(); err != nil || len(r.Changed) != 0 || len(r.Problems) != 0 {
		t.Fatalf("a second sync: %+v %v", r, err)
	}
	if v, err := Read(nil); err != nil {
		t.Fatal(err)
	} else {
		for _, f := range v.FoundServers {
			t.Errorf("found as the user's: %+v", f)
		}
	}
	wired = false
	if !runWiringChanged("codex") {
		t.Fatal("a disconnect wouldn't resync the opted-in route")
	}
	if _, err := Sync(); err != nil {
		t.Fatal(err)
	}
	if runWiringChanged("codex") {
		t.Fatal("a sync didn't remember the disconnect")
	}
	if got := inHome(&Server{Name: "mem", Transport: "stdio", Agents: []string{"codex"}}); got {
		t.Fatal("disconnected command still runs in home")
	}
	codex = read(t, filepath.Join(h, ".codex/config.toml"))
	if strings.Contains(codex, RunPath) || !strings.Contains(codex, `"server.js"`) {
		t.Fatalf("codex off magpie:\n%s", codex)
	}
	runs.Lock()
	_, given = runs.given["mem"]
	runs.Unlock()
	if given {
		t.Fatal("a server given by its command again is still served")
	}
}

// A previous magpie gave Codex a URL without asking. Its Library has no
// on-demand preference or applied route receipt; Sync alone must undo it.
func TestRunOldEntryAndLocalPreference(t *testing.T) {
	h := sandbox(t)
	resetRuns(t)
	was := runWired
	runOn, runWired = true, func(*agent.Agent) bool { return true }
	t.Cleanup(func() { runOn, runWired = false, was })
	cfg := filepath.Join(h, ".codex", "config.toml")
	write(t, cfg, "[mcp_servers.mem]\nurl = \"http://127.0.0.1:3425/mcp/run/mem\"\ntool_timeout_sec = 90\n")
	write(t, path(), `{"mcp":[{"name":"mem","transport":"stdio","command":"node","args":["server.js"],"agents":["codex"]}],"applied":{"codex":{"mcp":["mem"]}}}`)
	if _, ok := runSpecOf("mem"); ok {
		t.Fatal("legacy URL authorized before sync")
	}
	ok(t)(Sync())
	if e := codexLoads(t, cfg, "mem"); e["url"] != nil || e["command"] != "node" || !isNumber(e["tool_timeout_sec"], 90) {
		t.Fatal(e)
	}
	if err := libraryuse.Observe([]string{"mem"}, nil); err != nil {
		t.Fatal(err)
	}
	check := func(on bool, coverage string) {
		t.Helper()
		v, err := Glance(nil)
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(v)
		if err != nil || !strings.Contains(string(b), fmt.Sprintf(`"codexMCPOnDemand":%t`, on)) {
			t.Fatalf("view missing preference %t: %v", on, err)
		}
		for _, s := range v.Servers {
			if s.Name == "mem" {
				if s.UseStats.Coverage != coverage || s.UseStats.Candidate {
					t.Fatalf("coverage: %+v", s.UseStats)
				}
				return
			}
		}
		t.Fatal("no mem in view")
	}
	check(false, "unknown")
	ok(t)(SetCodexMCPOnDemand(true))
	check(true, "partial")
	bundle, err := Collect()
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(bundle)
	if err != nil || strings.Contains(string(b), `"codexMCPOnDemand"`) || strings.Contains(string(b), `"mcpRun"`) {
		t.Fatalf("bundle contains local settings: %s, %v", b, err)
	}
	ok(t)(Put(bundle))
	check(true, "partial")
	ok(t)(SetCodexMCPOnDemand(false))
	ok(t)(Put(bundle))
	check(false, "unknown")
}

// The second copy of the test binary plays the CLI using exactly the same
// library and agent files. It cannot invalidate runs.given in the server.
func TestRunSwitchInAnotherProcess(t *testing.T) {
	const mode = "MAGPIE_TEST_RUN_CLI"
	if action := os.Getenv(mode); action != "" {
		runOn, runWired = true, func(*agent.Agent) bool { return true }
		ok(t)(SetCodexMCPOnDemand(action == "on"))
		return
	}
	sandbox(t)
	srv, logf, _ := runFixture(t)
	_, sid := call(t, srv, "", fmt.Sprintf(runInit, "cached"))
	call(t, srv, sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	set := func(value string) {
		t.Helper()
		cmd := proc.Command(self, "-test.run=^TestRunSwitchInAnotherProcess$")
		cmd.Env = append(os.Environ(), mode+"="+value)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("child %s: %v: %s", value, err, b)
		}
	}
	set("off")
	runs.Lock()
	_, stale := runs.given["fake"]
	runs.Unlock()
	if !stale {
		t.Fatal("did not exercise a stale gateway cache")
	}
	for _, method := range []string{"initialize", "tools/call", "GET"} {
		req := httptest.NewRequest("POST", RunPath+"fake", strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":%q}`, method)))
		req.Header.Set("Mcp-Session-Id", sid)
		if method == "GET" {
			req.Method = method
			req.Header.Set("Accept", "text/event-stream")
			ctx, cancel := context.WithCancel(req.Context())
			cancel()
			req = req.WithContext(ctx)
		}
		rec := httptest.NewRecorder()
		ServeRun(rec, req, "fake")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s through stale URL: %d %s", method, rec.Code, rec.Body.String())
		}
	}
	if startCount(logf) != 1 {
		t.Fatal("old URL launched a process")
	}
	set("on")
	call(t, srv, sid, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo"}}`)
}

// A server the user keeps direct (#4382, cuipengcx90: cocoindex reads its
// project from the folder it starts in) is given to Codex on magpie as its
// command, as to every other agent, with the hub on or off: Codex starts it
// in the thread's folder, where magpie's /mcp/run would start it from home.
// No cwd is written, which is what makes Codex use the thread's.
func TestRunDirectServerIsCodexsCommand(t *testing.T) {
	h := sandbox(t)
	t.Cleanup(hubStopAll)
	resetRuns(t)
	runOn = true
	was := runWired
	runWired = func(a *agent.Agent) bool { return true }
	t.Cleanup(func() { runOn, runWired = false, was })
	ok(t)(SetCodexMCPOnDemand(true))
	if _, err := SaveServer("", Server{Name: "cocoindex", Transport: "stdio", Command: "ccc", Args: []string{"mcp"}, Env: map[string]string{"K": "v"}, Agents: []string{"codex", "claude"}, Hub: HubDirect}); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveServer("", Server{Name: "mem", Transport: "stdio", Command: "node", Args: []string{"server.js"}, Agents: []string{"codex"}}); err != nil {
		t.Fatal(err)
	}
	check := func(when string, wantShared bool) {
		t.Helper()
		e, err := targetByID("codex").MCP.entries()
		if err != nil {
			t.Fatal(err)
		}
		codex := read(t, filepath.Join(h, ".codex/config.toml"))
		c := e["cocoindex"]
		if c["command"] != "ccc" || c["url"] != nil || c["cwd"] != nil || fmt.Sprint(c["args"]) != "[mcp]" || fmt.Sprint(c["env"]) != "map[K:v]" {
			t.Fatalf("%s: codex's direct entry %v\n%s", when, c, codex)
		}
		runs.Lock()
		_, given := runs.given["cocoindex"]
		runs.Unlock()
		if given {
			t.Fatalf("%s: a direct server is served at %scocoindex", when, RunPath)
		}
		// before a sync has recorded what it gave, too
		runs.Lock()
		given2 := runs.given
		runs.given = nil
		runs.Unlock()
		_, before := runSpecOf("cocoindex")
		_, shared := runSpecOf("mem")
		runs.Lock()
		runs.given = given2
		runs.Unlock()
		if before || shared != wantShared {
			t.Fatalf("%s: before a sync /mcp/run would start the direct server %v, the shared one %v", when, before, shared)
		}
	}
	check("hub off", true)
	if codex := read(t, filepath.Join(h, ".codex/config.toml")); !strings.Contains(codex, RunPath+"mem") {
		t.Fatalf("a shared server isn't run by magpie any more:\n%s", codex)
	}
	if _, err := SetMCPHub(true); err != nil {
		t.Fatal(err)
	}
	check("hub on", false)
	// shared again: magpie runs it for Codex, as before
	if _, err := ServerHub("cocoindex", false); err != nil {
		t.Fatal(err)
	}
	if _, err := SetMCPHub(false); err != nil {
		t.Fatal(err)
	}
	if codex := read(t, filepath.Join(h, ".codex/config.toml")); !strings.Contains(codex, RunPath+"cocoindex") {
		t.Fatalf("cocoindex shared again:\n%s", codex)
	}
}
