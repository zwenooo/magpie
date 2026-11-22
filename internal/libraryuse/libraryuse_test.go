package libraryuse

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func sandbox(t *testing.T) (*time.Time, func()) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "folder with # and ?")
	// Windows prohibits '?' in a filename; '#' still exercises URI escaping.
	dir = strings.TrimSuffix(dir, " and ?")
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	was := local
	t.Cleanup(func() { local = was })
	restart := func() { local = &store{dir: func() string { return dir }, now: func() time.Time { return clock }} }
	restart()
	return &clock, restart
}
func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func report(t *testing.T, kind, name string) Summary {
	t.Helper()
	s, e := Report(kind, name)
	ok(t, e)
	return s
}

func TestExplicitEventsDatesAndRestart(t *testing.T) {
	clock, restart := sandbox(t)
	dir := local.dir()
	s := report(t, "skill", "readme")
	if !s.ObservedSince.IsZero() || s.Days90.Complete {
		t.Fatal(s)
	}
	if _, e := os.Stat(dir); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("read created state: %v", e)
	}
	first := *clock
	ok(t, Observe([]string{"docs"}, nil))
	ok(t, RecordMCP("docs", "search", "codex", false))
	ok(t, RecordMCP("docs", "fetch", "claude", true))
	*clock = first.AddDate(0, 0, 1)
	ok(t, RecordMCP("docs", "search", "claude", true))
	ok(t, RecordSkill("readme", "claude", true))
	ok(t, RecordSkill("readme", "claude", false))
	later := *clock
	*clock = first.AddDate(0, 0, 30)
	restart()
	v, e := Snapshot()
	ok(t, e)
	s = v.Report("mcp", "docs")
	if !s.ObservedSince.Equal(first) || !s.LastUsed.Equal(later) || s.Days30.Count != 1 || s.Days90.Count != 3 || s.Days90.Failed != 1 || s.Days90.Complete || s.Tools["fetch"].Days30.Count != 0 || s.Tools["search"].Days90.Count != 2 || s.ByClient["codex"].Days90.Failed != 1 {
		t.Fatal(s)
	}
	skill := v.Report("skill", "readme")
	if !skill.ObservedSince.Equal(later) || skill.Days30.Count != 2 || skill.Days30.Reads != 1 || skill.Days30.Failed != 0 {
		t.Fatal(skill)
	}
	*clock = first.AddDate(0, 0, 90)
	s = report(t, "mcp", "docs")
	if s.Days30.Count != 0 || s.Days90.Count != 1 || s.Days90.Failed != 0 {
		t.Fatal(s)
	}
	*clock = clock.AddDate(0, 0, 1)
	ok(t, RecordSkill("next", "codex", true))
	s = report(t, "mcp", "docs")
	if s.Days90.Count != 0 || !s.Tools["fetch"].LastUsed.Equal(first) || !s.LastUsed.Equal(later) {
		t.Fatal(s)
	}
	if v.Report("mcp", "docs").Days30.Count != 1 {
		t.Fatal("snapshot changed")
	}
}

func TestEligibilityAndInventoryLifetimes(t *testing.T) {
	clock, _ := sandbox(t)
	a := Item{"mcp", "docs", true}
	items := []Item{a}
	first := *clock
	ok(t, ReconcileInventory(items))
	s := report(t, a.Kind, a.Name)
	if !s.ObservedSince.IsZero() {
		t.Fatal(s)
	}
	ok(t, ObserveInventory(items))
	*clock = clock.Add(15 * time.Minute)
	ok(t, ObserveInventory(items))
	if s = report(t, a.Kind, a.Name); !s.EligibleSince.Equal(first) {
		t.Fatal(s)
	}
	*clock = clock.Add(maxSilence + time.Millisecond)
	if s = report(t, a.Kind, a.Name); !s.EligibleSince.IsZero() {
		t.Fatal(s)
	}
	ok(t, ObserveInventory(items))
	if s = report(t, a.Kind, a.Name); !s.EligibleSince.Equal(*clock) || !s.ObservedSince.Equal(first) {
		t.Fatal(s)
	}
	a.Eligible = false
	ok(t, ReconcileInventory([]Item{a}))
	a.Eligible = true
	ok(t, ReconcileInventory([]Item{a}))
	if s = report(t, a.Kind, a.Name); !s.EligibleSince.IsZero() {
		t.Fatal(s)
	}
	ok(t, RecordMCP(a.Name, "fetch", "codex", true))
	ok(t, ReconcileInventory([]Item{{"mcp", "renamed", true}}))
	*clock = clock.Add(time.Minute)
	ok(t, ObserveInventory(items))
	s = report(t, a.Kind, a.Name)
	if !s.LastUsed.IsZero() || !s.ObservedSince.Equal(*clock) || len(s.Tools) > 0 {
		t.Fatal(s)
	}
}

func TestCompleteWindowsRequireContinuousHeartbeat(t *testing.T) {
	clock, _ := sandbox(t)
	first := *clock
	items := []Item{{"mcp", "eligible", true}, {"skill", "partial", false}}
	ok(t, ObserveInventory(items))
	// Resume a real persistent store just before the last heartbeat of a long
	// interval. Eligibility, loss and resumption are exercised through the API.
	jump := func(d int) {
		*clock = first.AddDate(0, 0, d)
		db, e := openDB(local.dir(), true)
		ok(t, e)
		_, e = db.Exec(`UPDATE item SET seen=? WHERE name='eligible'`, clock.Add(-15*time.Minute).UnixMilli())
		ok(t, e)
		ok(t, db.Close())
		ok(t, ObserveInventory(items))
	}
	jump(29)
	s := report(t, "mcp", "eligible")
	if !s.Days30.Complete || s.Days90.Complete || s.Days30.Count != 0 {
		t.Fatal(s)
	}
	jump(89)
	s = report(t, "mcp", "eligible")
	if !s.Days90.Complete || report(t, "skill", "partial").Days90.Complete {
		t.Fatal(s)
	}
	*clock = clock.Add(maxSilence + time.Second)
	ok(t, ReconcileInventory(items))
	if s = report(t, "mcp", "eligible"); s.Days30.Complete || s.Days90.Complete {
		t.Fatal(s)
	}
}

func TestIndependentConcurrentWriters(t *testing.T) {
	_, restart := sandbox(t)
	ok(t, ObserveInventory([]Item{{"mcp", "docs", true}}))
	const workers = 6
	const calls = 12
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := range workers {
		s := &store{dir: local.dir, now: local.now}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range calls {
				if e := s.record("mcp", "docs", fmt.Sprintf("t%d", i), "codex", false, j%2 == 0); e != nil {
					errs <- e
					return
				}
				if j == 4 {
					v, e := s.snapshot()
					if e != nil {
						errs <- e
						return
					}
					r := v.Report("mcp", "docs")
					var tools int64
					for _, t := range r.Tools {
						tools += t.Days30.Count
					}
					if tools != r.Days30.Count {
						errs <- errors.New("torn snapshot")
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		ok(t, e)
	}
	restart()
	s := report(t, "mcp", "docs")
	if s.Days30.Count != workers*calls || s.Days30.Failed != workers*calls/2 || len(s.Tools) != workers || s.ByClient["codex"].Days30 != s.Days30 {
		t.Fatal(s)
	}
}

func TestPrivacyAndLimits(t *testing.T) {
	sandbox(t)
	secret := `C:\Users\private\file.json`
	session := "session-private-XXXXXXXX"
	if err := RecordMCP(secret, "read", session, true); err == nil {
		t.Fatal("accepted path as server")
	}
	ok(t, RecordMCP("docs", secret, session, true))
	ok(t, RecordMCP("docs", strings.Repeat("x", 1<<20), strings.Repeat("z", 1<<20), false))
	for i := 0; i <= maxTools; i++ {
		ok(t, RecordMCP("docs", fmt.Sprintf("tool_%02d", i), "claude", true))
	}
	s := report(t, "mcp", "docs")
	if len(s.Tools) != maxTools+1 || s.Tools[otherTools].Days30.Count != 3 || s.ByClient["other"].Days30.Count != 2 || s.Days30.Count != maxTools+3 {
		t.Fatal(s)
	}
	b, e := os.ReadFile(filepath.Join(local.dir(), "use.db"))
	ok(t, e)
	if bytes.Contains(b, []byte("private")) || bytes.Contains(b, []byte("session-")) {
		t.Fatal("persisted a sensitive input")
	}
	if e := ObserveInventory(make([]Item, maxItems+1)); e == nil {
		t.Fatal("unbounded inventory")
	}
	if e := ObserveInventory([]Item{{"mcp", "docs", true}, {"mcp", "docs", false}}); e == nil {
		t.Fatal("ambiguous inventory")
	}
	if s = report(t, "mcp", "docs"); s.Days30.Count != maxTools+3 {
		t.Fatal("invalid input replaced good rows")
	}
}

func TestReadAndWriteErrorsFailClosed(t *testing.T) {
	_, restart := sandbox(t)
	ok(t, ObserveInventory([]Item{{"mcp", "docs", true}}))
	p := filepath.Join(local.dir(), "use.db")
	db, e := openDB(local.dir(), true)
	ok(t, e)
	_, e = db.Exec(`CREATE TRIGGER fail_daily BEFORE INSERT ON daily BEGIN SELECT raise(ABORT,'forced failure'); END`)
	ok(t, e)
	ok(t, db.Close())
	if e = RecordMCP("docs", "read", "codex", false); e == nil {
		t.Fatal("write error hidden")
	}
	restart()
	s := report(t, "mcp", "docs")
	if s.Days90.Complete || !s.EligibleSince.IsZero() || !s.LastUsed.IsZero() || len(s.Tools) > 0 {
		t.Fatal("failed write left partial counts or intact coverage", s)
	}
	bad := []byte("not a database")
	ok(t, os.WriteFile(p, bad, 0600))
	if v, e := Snapshot(); e == nil || len(v.items) != 0 {
		t.Fatal("invalid database became empty success")
	}
	if e := RecordSkill("other", "codex", true); e == nil {
		t.Fatal("overwrote corrupt database")
	}
	b, e := os.ReadFile(p)
	ok(t, e)
	if !bytes.Equal(b, bad) {
		t.Fatal("destroyed invalid database")
	}
}
