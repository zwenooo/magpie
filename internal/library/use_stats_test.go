package library

import (
	"testing"
	"time"

	"github.com/yetone/magpie/internal/libraryuse"
)

func TestLibraryUseCandidateRequiresObservedIdleWindow(t *testing.T) {
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	s := libraryuse.Summary{ObservedSince: now.AddDate(0, 0, -91)}
	s.Days90.Complete = true
	if !rowUse(s, "observed", now).Candidate {
		t.Fatal("observed idle item missing")
	}
	for _, coverage := range []string{"partial", "unknown"} {
		if rowUse(s, coverage, now).Candidate {
			t.Fatal("incomplete route called unused")
		}
	}
	s.Days90.Count = 2
	s.Days30.Count = 2
	s.Days30.Reads = 1
	s.Days30.Failed = 1
	s.LastUsed = now
	u := rowUse(s, "observed", now)
	if u.Candidate || u.Calls30 != 1 || u.Reads30 != 1 || u.Failed30 != 1 || u.LastUsed != now.Unix() {
		t.Fatalf("failed call or read misclassified: %+v", u)
	}
	s.Days90.Count = 0
	s.Days90.Complete = false
	if rowUse(s, "observed", now).Candidate {
		t.Fatal("gap accepted")
	}
}
func TestDirectUseIsAddedOnceAndCannotProveInactivity(t *testing.T) {
	gateway := UseStats{Calls30: 3, Failed30: 1, Coverage: "observed", Candidate: true, Complete30: true, LastUsed: 10}
	d := libraryuse.DirectUsage{Calls30: 4, Failed30: 2, LastUsed: 20}
	for range 2 { // a view refresh reuses the snapshot, not a cumulative import
		u := withDirectUse(gateway, d, nil)
		if u.Calls30 != 7 || u.Failed30 != 3 || u.DirectCalls30 != 4 || u.LastUsed != 20 || u.Coverage != "partial" || u.Candidate || u.Complete30 {
			t.Fatalf("misleading combined report: %+v", u)
		}
	}
}

func TestLibraryUseAgentBreakdown(t *testing.T) {
	now := time.Date(2026, 11, 22, 15, 16, 37, 0, time.UTC)
	gateway := rowUse(libraryuse.Summary{ByClient: map[string]libraryuse.ToolSummary{"claude": {LastUsed: now, Days30: libraryuse.Window{Count: 4, Reads: 1, Failed: 1}}}}, "partial", now)
	d := libraryuse.DirectUsage{Calls30: 3, Failed30: 1, LastUsed: now.Add(time.Second).Unix(), ByAgent: map[string]libraryuse.DirectAgentUsage{"claude": {Calls30: 1, LastUsed: now.Add(time.Second).Unix()}, "cursor": {Calls30: 2, Failed30: 1, LastUsed: now.Unix()}}}
	u := withDirectUse(gateway, d, nil)
	if len(u.ByAgent) != 2 || u.ByAgent[0] != (AgentUseStats{"claude", 4, 1, 1, now.Add(time.Second).Unix()}) || u.ByAgent[1] != (AgentUseStats{"cursor", 2, 1, 0, now.Unix()}) || gateway.ByAgent[0].Calls30 != 3 {
		t.Fatalf("wrong agent rows: %+v", u.ByAgent)
	}
}

func TestLibraryUseScopeExcludesDirectAndProjectRoutes(t *testing.T) {
	l := &Library{MCPHub: true, MCP: []*Server{{Name: "weather", Agents: []string{"claude"}}}, Applied: map[string]*Applied{"claude": {MCP: []string{HubEntry}}}}
	eligible := func() bool { return useInventory(l)[0].Eligible }
	if !eligible() {
		t.Fatal("only registered hub route not observed")
	}
	l.MCP[0].Agents = append(l.MCP[0].Agents, "other")
	if eligible() {
		t.Fatal("agent without a recorded hub counted as covered")
	}
	l.MCP[0].Agents = l.MCP[0].Agents[:1]
	l.MCP[0].Hub = HubDirect
	if eligible() {
		t.Fatal("direct server counted as covered")
	}
	l.MCP[0].Hub = ""
	l.Projects = []*Project{{Servers: map[string][]string{"weather": {"claude"}}}}
	if eligible() {
		t.Fatal("project-specific server counted as covered")
	}
}
