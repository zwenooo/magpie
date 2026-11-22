package library

import (
	"context"
	"slices"
	"time"

	"github.com/yetone/magpie/internal/libraryuse"
)

// UseStats is local evidence. A partial count is a lower bound, not evidence
// that an item was unused on other clients or computers.
type UseStats struct {
	DirectCalls30 int64  `json:"directCalls30,omitempty"`
	Calls30       int64  `json:"calls30"`
	Failed30      int64  `json:"failed30"`
	Reads30       int64  `json:"reads30"`
	LastUsed      int64  `json:"lastUsed"`
	Coverage      string `json:"coverage"`
	Complete30    bool   `json:"complete30"`
	Candidate     bool   `json:"candidate"`
	ObservedDays  int    `json:"observedDays"`
}

func useInventory(l *Library) []libraryuse.Item {
	project := map[string]bool{}
	for _, p := range l.Projects {
		_, servers, _ := l.withPlugins(p)
		for name, as := range servers {
			if len(as) > 0 {
				project[name] = true
			}
		}
	}
	items := make([]libraryuse.Item, 0, len(l.Skills)+len(l.MCP))
	for _, s := range l.Skills {
		items = append(items, libraryuse.Item{Kind: "skill", Name: s.Name})
	}
	hubAvailable := l.MCPHub && l.server(HubEntry) == nil
	for _, s := range l.MCP {
		eligible := hubAvailable && !s.Hidden && !project[s.Name] && s.Bundle == "" && s.Bridge == "" && len(s.Agents) > 0
		for _, id := range s.Agents {
			a := l.Applied[id]
			if a == nil || !inHub(s, id) || !slices.Contains(a.MCP, HubEntry) || slices.Contains(a.MCP, s.Name) {
				eligible = false
				break
			}
		}
		items = append(items, libraryuse.Item{Kind: "mcp", Name: s.Name, Eligible: eligible})
	}
	return items
}

// Only a serving gateway observes coverage; opening a GUI on a computer
// without a gateway must not certify a day of monitoring.
func KeepUseStats(ctx context.Context) {
	tick := time.NewTicker(15 * time.Minute)
	defer tick.Stop()
	for {
		observeUseInventory()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func observeUseInventory() { updateUseInventory(true) }

// A configuration change can invalidate existing coverage but cannot earn
// additional observation time just by writing the configuration from a CLI.
func reconcileUseInventory() { updateUseInventory(false) }

func updateUseInventory(serving bool) {
	mu.Lock()
	l, err := load()
	var items []libraryuse.Item
	if err == nil {
		items = useInventory(l)
	}
	mu.Unlock()
	if err != nil {
		return
	} // unknown is never an empty inventory
	if serving {
		_ = libraryuse.ObserveInventory(items)
	} else {
		_ = libraryuse.ReconcileInventory(items)
	}
}

func rowUse(s libraryuse.Summary, coverage string, now time.Time) UseStats {
	u := UseStats{Coverage: coverage, Calls30: s.Days30.Count - s.Days30.Reads, Reads30: s.Days30.Reads, Failed30: s.Days30.Failed}
	if !s.LastUsed.IsZero() {
		u.LastUsed = s.LastUsed.Unix()
		if coverage == "unknown" {
			u.Coverage = "partial"
		}
	}
	if !s.ObservedSince.IsZero() && !now.Before(s.ObservedSince) {
		u.ObservedDays = int(now.Sub(s.ObservedSince).Hours() / 24)
	}
	if s.ObservedSince.IsZero() && s.LastUsed.IsZero() {
		u.Coverage = "unknown"
	}
	u.Complete30 = u.Coverage == "observed" && s.Days30.Complete
	u.Candidate = u.Coverage == "observed" && s.Days90.Complete && s.Days90.Count == 0 && u.ObservedDays >= 90 && (s.LastUsed.IsZero() || !s.LastUsed.After(now.Add(-90*24*time.Hour)))
	return u
}
func withDirectUse(u UseStats, d libraryuse.DirectUsage, err error) UseStats {
	// Other native clients may still run an older snapshot or environment.
	// No native observations is never evidence for removing this server.
	u.Candidate, u.Complete30 = false, false
	if u.Coverage == "observed" || d.LastUsed > 0 {
		u.Coverage = "partial"
	}
	if err != nil {
		return u
	}
	u.DirectCalls30 = d.Calls30
	u.Calls30 += d.Calls30
	u.Failed30 += d.Failed30
	u.LastUsed = max(u.LastUsed, d.LastUsed)
	return u
}

func attachUseStats(v *View, items []libraryuse.Item) {
	snap, err := libraryuse.Snapshot()
	now := time.Now()
	eligible := map[string]bool{}
	for _, i := range items {
		if i.Kind == "mcp" {
			eligible[i.Name] = i.Eligible
		}
	}
	for i := range v.Skills {
		s := &v.Skills[i]
		s.UseStats.Coverage = "unknown"
		if err != nil {
			continue
		}
		coverage := "unknown"
		if slices.Contains(s.Agents, "claude") || slices.Contains(s.Always, "claude") {
			coverage = "partial"
		}
		s.UseStats = rowUse(snap.Report("skill", s.Name), coverage, now)
	}
	var direct map[string]libraryuse.DirectUsage
	var directErr error
	for _, s := range v.Servers {
		if supportsDirectUse(s.Server) {
			direct, directErr = libraryuse.DirectSnapshot(now)
			break
		}
	}
	for i := range v.Servers {
		s := &v.Servers[i]
		s.UseStats.Coverage = "unknown"
		if err == nil {
			coverage := "unknown"
			if eligible[s.Name] && len(s.Problems) == 0 {
				coverage = "observed"
			} else if v.MCPHub && s.Hub != HubDirect || slices.Contains(s.Agents, "codex") && s.Transport == "stdio" && !s.Hidden && s.Hub != HubDirect {
				coverage = "partial"
			}
			s.UseStats = rowUse(snap.Report("mcp", s.Name), coverage, now)
		}
		if supportsDirectUse(s.Server) {
			s.UseStats = withDirectUse(s.UseStats, direct[s.Name], directErr)
		}
	}
}
