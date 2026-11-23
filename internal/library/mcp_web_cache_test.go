package library

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/proc"
)

func TestWebCacheListsOnlyAllowedAddresses(t *testing.T) {
	sandbox(t)
	s := &Server{Name: "secret-name", Transport: "http", URL: "https://service.example/mcp?key=secret-endpoint", Headers: map[string]string{"Authorization": "secret-header"}, OAuth: &OAuthApp{ClientID: "test", ClientSecret: "secret-oauth"}}
	if err := (&Library{MCP: []*Server{s}}).save(); err != nil {
		t.Fatal(err)
	}
	want := []MCPWebEntry{{"website", webSite}, {"console", "https://service.example/admin"}}
	bad := []MCPWebEntry{{"custom", webSite}, {"logs", "https://service.example/" + strings.Repeat("a", maxWebURL)}, {"logs", "https://user:secret-password@service.example/logs"}, {"logs", "https://service.example/logs?key=secret-query"}, {"logs", "http://127.0.0.1:9911"}, {"logs", webSite}}
	rememberWebEntries(s, append(slices.Clone(bad), want...))
	b := read(t, webCacheFile())
	if strings.Contains(b, "secret-") || strings.Contains(b, "9911") || strings.Contains(b, "custom") || strings.Contains(b, "Authorization") {
		t.Fatal("cache contains unapproved data")
	}
	if got := rememberedWebEntries(s); !slices.Equal(got, want) {
		t.Fatalf("persisted: %v", got)
	}
	// Even a locally altered cache is not an authority on the URL policy.
	blob, err := json.Marshal(webRecords{{Key: webEntryKey(s), Entries: append(bad, want...)}})
	if err != nil {
		t.Fatal(err)
	}
	write(t, webCacheFile(), string(blob))
	for _, list := range []func([]Problem) (*View, error){Glance, Read} {
		v, err := list(nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(v.Servers) != 1 || v.Servers[0].WebEntryKey != webEntryKey(s) || !slices.Equal(v.Servers[0].RememberedWebEntries, want) {
			t.Fatalf("Library rows: %+v", v.Servers)
		}
	}
	bundle, err := Collect()
	if err != nil {
		t.Fatal(err)
	}
	setup, err := Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, outgoing := range []any{bundle, setup} {
		b, err := json.Marshal(outgoing)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte(webSite)) || bytes.Contains(b, []byte("webEntryKey")) {
			t.Fatal("local advertisement traveled with the library")
		}
	}
	rememberWebEntries(s, nil)
	if got := rememberedWebEntries(s); got != nil {
		t.Fatalf("empty result kept %v", got)
	}
}

func TestRememberedToolsLatestObservationAndIsolation(t *testing.T) {
	sandbox(t)
	s := &Server{Name: "books", Transport: "stdio", Command: "node", RepoLocalCommit: "v1"}
	l := &Library{MCP: []*Server{s}}
	if err := l.save(); err != nil {
		t.Fatal(err)
	}
	secret := "PRIVATE_DESCRIPTION_MUST_NOT_TRAVEL"
	want := &listedTools{Names: []string{"private_tool_name", "payload: " + secret}, Count: maxToolList + 2}
	first, later := beginToolList(s), beginToolList(s)
	finishToolList(s, later, want, nil)
	finishToolList(s, first, nil, nil)                       // old failure must not erase a success
	rememberHandshake(s, s, json.RawMessage(webInitFixture)) // initialize does not list tools
	for _, view := range []func([]Problem) (*View, error){Glance, Read} {
		v, err := view(nil)
		if err != nil {
			t.Fatal(err)
		}
		row := v.Servers[0]
		if !slices.Equal(row.RememberedTools, []string{"private_tool_name"}) || row.RememberedToolCount != want.Count || len(row.RememberedWebEntries) == 0 {
			t.Fatalf("wrong remembered metadata: %+v", row)
		}
	}
	if strings.Contains(read(t, webCacheFile()), secret) {
		t.Fatal("cached non-identifier body")
	}
	bundle, err := Collect()
	if err != nil {
		t.Fatal(err)
	}
	setup, err := Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []any{bundle, setup, l} {
		b, err := json.Marshal(out)
		if err != nil || bytes.Contains(b, []byte("private_tool_name")) || bytes.Contains(b, []byte("rememberedTool")) {
			t.Fatal("remembered metadata appeared in carried Library", err)
		}
	}
	for _, empty := range []*listedTools{{}, nil} {
		old, newer := beginToolList(s), beginToolList(s)
		finishToolList(s, newer, empty, nil)
		finishToolList(s, old, want, nil)
		v, err := Glance(nil)
		if err != nil {
			t.Fatal(err)
		}
		row := v.Servers[0]
		if (row.RememberedTools == nil) != (empty == nil) || len(row.RememberedTools) != 0 || row.RememberedToolCount != 0 || len(row.RememberedWebEntries) == 0 {
			t.Fatalf("stale or ambiguous list: %+v", row)
		}
		b, _ := json.Marshal(row)
		wire := `"rememberedTools":[]`
		if empty == nil {
			wire = `"rememberedTools":null`
		}
		if !bytes.Contains(b, []byte(wire)) || !bytes.Contains(b, []byte(`"rememberedToolCount":0`)) {
			t.Fatalf("missing %s or count", wire)
		}
	}
	pending := beginToolList(s)
	if _, err := RemoveServer(s.Name); err != nil {
		t.Fatal(err)
	}
	if err := l.save(); err != nil {
		t.Fatal(err)
	}
	finishToolList(s, pending, want, nil)
	r, err := readWebRecords()
	if err != nil || r.record(webEntryKey(s), s).Tools != nil {
		t.Fatal("old in-flight response revived identical re-created server", err)
	}
	old := *s
	pending = beginToolList(&old)
	s.RepoLocalCommit = "v2"
	if err := l.save(); err != nil {
		t.Fatal(err)
	}
	finishToolList(&old, pending, want, nil)
	v, err := Read(nil)
	if err != nil || v.Servers[0].RememberedTools != nil {
		t.Fatal("old revision supplied tools", err)
	}
}

func TestWebCacheRejectsOtherNamespaceLoopback(t *testing.T) {
	w := fakeWSLCheck(t, "Debian")
	for _, s := range []*Server{
		{Name: "books", Transport: "stdio", Command: "node", Agents: []string{"claude@wsl:Debian"}},
		{Name: "books", Transport: "stdio", Command: "docker"},
	} {
		if err := (&Library{MCP: []*Server{s}}).save(); err != nil {
			t.Fatal(err)
		}
		site := []MCPWebEntry{{"website", webSite}}
		all := append(slices.Clone(site), MCPWebEntry{"console", "http://localhost:7711/"})
		rememberWebEntries(s, all)
		if b := read(t, webCacheFile()); !strings.Contains(b, webSite) || strings.Contains(b, "localhost") {
			t.Fatal("invalid namespace on write")
		}
		b, err := json.Marshal(webRecords{{Key: webEntryKey(s), Entries: all}})
		if err != nil {
			t.Fatal(err)
		}
		write(t, webCacheFile(), string(b))
		if got := rememberedWebEntries(s); !slices.Equal(got, site) {
			t.Fatalf("unsafe cached entry: %v", got)
		}
	}
	if len(w.probes) != 0 || len(w.runs) != 0 {
		t.Fatal("reading or writing a cache probed WSL")
	}
}

func TestWebCacheLateHandshakeAndRecreation(t *testing.T) {
	sandbox(t)
	old := &Server{Name: "books", Transport: "stdio", Command: "node", Args: []string{"{{program}}/server.js"}, RepoSource: &MCPRepoSource{Repo: "a/b", Ref: "main"}, RepoCommit: "revision-a", RepoLocalCommit: "revision-a"}
	l := &Library{MCP: []*Server{old}}
	if err := l.save(); err != nil {
		t.Fatal(err)
	}
	want := []MCPWebEntry{{"console", "http://localhost:8181/"}}
	rememberWebEntries(old, want)
	for _, change := range []func(*Server){
		func(s *Server) { s.Command = "ssh" },
		func(s *Server) { s.Env = map[string]string{"A": "different"} },
		func(s *Server) { s.RepoSource = &MCPRepoSource{Repo: "c/d"} },
		func(s *Server) { s.RepoCommit = "revision-b" },
		func(s *Server) { s.RepoLocalCommit = "revision-b" },
		func(s *Server) { s.RepoHash = "new hash" },
		func(s *Server) { s.RepoLocalSource = s.RepoSource },
		func(s *Server) { s.Headers = map[string]string{"key": "changed"} },
		func(s *Server) { s.Agents = []string{"codex@wsl:Debian"} },
	} {
		cur := *old
		change(&cur)
		if webEntryKey(&cur) == webEntryKey(old) {
			t.Fatalf("unchanged identity for %+v", cur)
		}
	}
	cur := *old
	cur.RepoLocalCommit = "revision-b"
	l.MCP[0] = &cur
	if err := l.save(); err != nil {
		t.Fatal(err)
	}
	if got := rememberedWebEntries(&cur); got != nil {
		t.Fatalf("new revision reused %v", got)
	}
	want[0].URL = "http://localhost:9191/"
	rememberWebEntries(&cur, want)
	for _, stale := range []*Server{old, here(&cur)} {
		rememberWebEntries(stale, []MCPWebEntry{{"website", webSite}})
		rememberWebEntries(stale, nil)
		if got := rememberedWebEntries(&cur); !slices.Equal(got, want) {
			t.Fatalf("late result changed current entry: %v", got)
		}
	}
	if _, err := RemoveServer(cur.Name); err != nil {
		t.Fatal(err)
	}
	rememberWebEntries(&cur, want) // reply after removal
	if err := l.save(); err != nil {
		t.Fatal(err)
	}
	if got := rememberedWebEntries(&cur); got != nil {
		t.Fatalf("deleted server revived %v", got)
	}
}

func TestWebCacheBounds(t *testing.T) {
	sandbox(t)
	l := &Library{}
	records := webRecords{}
	for i := 0; i <= maxWebServers; i++ {
		s := &Server{Name: fmt.Sprintf("server%d", i), Transport: "stdio", Command: "node"}
		l.MCP = append(l.MCP, s)
		if i < maxWebServers {
			records = append(records, webRecord{Key: webEntryKey(s), Entries: []MCPWebEntry{{"website", webSite}}})
		}
	}
	first, last := l.MCP[0], l.MCP[maxWebServers]
	if err := l.save(); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	write(t, webCacheFile(), string(b))
	rememberWebEntries(last, []MCPWebEntry{{"website", webSite}})
	if records, err := readWebRecords(); err != nil || len(records) != maxWebServers || records.entries(webEntryKey(first), first) != nil || records.entries(webEntryKey(last), last) == nil {
		t.Fatal("cache did not bound records by recency", err)
	}
	for _, invalid := range []string{"{", strings.Repeat(" ", maxWebFile+1)} {
		write(t, webCacheFile(), invalid)
		rememberWebEntries(last, nil)
		if rememberedWebEntries(last) != nil || read(t, webCacheFile()) != invalid {
			t.Fatal("an unreadable cache was trusted or overwritten")
		}
	}
}

func TestWebCacheProcessWriters(t *testing.T) {
	const marker = "MAGPIE_WEB_CACHE_TEST_SERVER"
	if name := os.Getenv(marker); name != "" {
		l, err := load()
		if err != nil {
			t.Fatal(err)
		}
		s := l.server(name)
		if s == nil {
			t.Fatal("missing child fixture")
		}
		rememberWebEntries(s, []MCPWebEntry{{"website", webSite}})
		return
	}
	sandbox(t)
	l := &Library{}
	for i := 0; i < 4; i++ {
		l.MCP = append(l.MCP, &Server{Name: fmt.Sprintf("worker%d", i), Transport: "stdio", Command: "node"})
	}
	if err := l.save(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, s := range l.MCP {
		wg.Go(func() {
			cmd := proc.CommandContext(t.Context(), os.Args[0], "-test.run=^TestWebCacheProcessWriters$")
			cmd.Env = append(os.Environ(), marker+"="+s.Name)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("cache writer %s: %v: %s", s.Name, err, out)
			}
		})
	}
	wg.Wait()
	for _, s := range l.MCP {
		if !slices.Equal(rememberedWebEntries(s), []MCPWebEntry{{"website", webSite}}) {
			t.Errorf("lost %s", s.Name)
		}
	}
}
