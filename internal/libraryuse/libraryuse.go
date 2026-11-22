// Package libraryuse counts explicit local Library events. It never infers use
// from sign-ins, inventory, listings or time spent online. Counts describe
// this computer only. A window with incomplete coverage is a lower bound.
package libraryuse

import (
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/appdir"
)

const (
	maxItems   = 1024
	maxTools   = 64               // per server, plus one anonymous overflow bucket
	maxSilence = 30 * time.Minute // the gateway samples its complete inventory every 15 minutes
)

// Item is a canonical library name, never a display label, path or session ID.
// Eligible certifies that every use on this computer passes a counted route.
type Item struct {
	Kind, Name string
	Eligible   bool
}

type Window struct {
	Count, Reads, Failed int64
	Complete             bool
}
type ToolSummary struct {
	LastUsed       time.Time
	Days30, Days90 Window
}
type Summary struct {
	ObservedSince, EligibleSince, LastUsed time.Time
	Days30, Days90                         Window
	Tools, ByClient                        map[string]ToolSummary
}

type itemKey struct{ kind, name string }

// View is a point-in-time snapshot. Treat its returned maps as read-only.
type View struct{ items map[itemKey]*Summary }

// Report is a memory-only lookup. An absent entry has unknown coverage.
func (v View) Report(kind, name string) Summary {
	if s := v.items[itemKey{kind, name}]; s != nil {
		return *s
	}
	return Summary{}
}

var local = &store{dir: func() string { return filepath.Join(appdir.Config(), "library-use") }, now: time.Now}

type store struct {
	dir      func() string
	now      func() time.Time
	mu       sync.Mutex // coordinate a failed write and the same process's next read
	faultDir string
	faultDay int64
}

// Observe records first sight of names; not evidence that any route was monitored.
func Observe(mcp, skills []string) error {
	if len(mcp) > maxItems || len(skills) > maxItems-len(mcp) {
		return errInventory
	}
	items := make([]Item, 0, len(mcp)+len(skills))
	for _, n := range mcp {
		items = append(items, Item{"mcp", n, false})
	}
	for _, n := range skills {
		items = append(items, Item{"skill", n, false})
	}
	return local.inventory(items, observeNames)
}

// ObserveInventory samples the complete authoritative inventory. An absent name
// loses its history. A new name, lost eligibility or a missed heartbeat breaks
// the coverage interval; re-observing an eligible item is not a use.
func ObserveInventory(items []Item) error { return local.inventory(items, heartbeat) }

// ReconcileInventory only removes missing names and invalidates lost coverage.
// It does not certify uptime or start observing a new name.
func ReconcileInventory(items []Item) error { return local.inventory(items, reconcile) }

// RecordMCP counts one completed attempt (also on failure). Unknown clients
// and unsafe/excess tool names go into fixed buckets, never raw payloads.
func RecordMCP(server, tool, client string, success bool) error {
	return local.record("mcp", server, tool, client, false, !success)
}

// RecordSkill counts one explicit invocation or read. A read is included in
// Count and Reads, so Count-Reads is the number of invocations.
func RecordSkill(name, client string, read bool) error {
	return local.record("skill", name, "", client, read, false)
}

// Snapshot reads all retained entries in one consistent read transaction.
func Snapshot() (View, error) { return local.snapshot() }
func Report(kind, name string) (Summary, error) {
	if !validItem(kind, name) {
		return Summary{}, errName
	}
	v, err := Snapshot()
	return v.Report(kind, name), err
}

var errInventory = errors.New("libraryuse: inventory exceeds the limit or has invalid or duplicate names")
var errName = errors.New("libraryuse: expected a canonical library name")

func validItem(kind, name string) bool {
	return (kind == "mcp" || kind == "skill") && identifier(name, 64, false)
}
func identifier(name string, max int, tool bool) bool {
	if len(name) == 0 || len(name) > max {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		if (tool || i > 0) && (c == '-' || c == '_' || tool && c == '.') {
			continue
		}
		return false
	}
	return name != "." && name != ".."
}
func product(s string) string {
	switch s {
	case "claude", "codex", "cursor", "gemini", "opencode", "claude-desktop", "kiro-cli", "copilot", "droid", "pi", "magpie":
		return s
	}
	return "other"
}

const otherTools = "(other)" // not a legal tool identifier, so cannot collide
func stamp(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

const dayMS = int64(24 * time.Hour / time.Millisecond)
