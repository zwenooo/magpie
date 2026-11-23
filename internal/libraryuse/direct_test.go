package libraryuse

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDirectSnapshot(t *testing.T) {
	file := filepath.Join(t.TempDir(), "ops-direct.db")
	now := time.Date(2026, 10, 30, 2, 3, 4, 0, time.UTC)
	d := now.Unix() / 86400
	if v, e := directSnapshot(file, now); e != nil || len(v) != 0 {
		t.Fatal(v, e)
	}
	if _, e := os.Stat(file); !os.IsNotExist(e) {
		t.Fatal("reading created the database")
	}
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE direct_daily(item TEXT,day INTEGER,tool TEXT,n INTEGER,failed INTEGER,last INTEGER,PRIMARY KEY(item,day,tool))`); err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct {
		day, n, f int64
		tool      string
	}{{d, 2, 1, "target_list"}, {d, 1, 0, "ssh_exec"}, {d - 29, 1, 0, "ssh_exec"}, {d - 30, 10, 8, "target_list"}} {
		if _, e := db.Exec(`INSERT INTO direct_daily VALUES(?,?,?,?,?,?)`, "ops", r.day, r.tool, r.n, r.f, r.day*86400); e != nil {
			t.Fatal(e)
		}
	}
	for range 2 {
		v, e := directSnapshot(file, now)
		if e != nil || v["ops"].Calls30 != 4 || v["ops"].Failed30 != 1 || v["ops"].LastUsed != d*86400 || v["ops"].ByAgent["other"] != (DirectAgentUsage{4, 1, d * 86400}) {
			t.Fatalf("snapshot: %+v %v", v, e)
		}
	}
	// New workers record a second margin; residual old-worker calls still belong to other.
	if _, err = db.Exec(`CREATE TABLE direct_agent_daily(item TEXT,day INTEGER,agent TEXT,n INTEGER,failed INTEGER,last INTEGER,PRIMARY KEY(item,day,agent))`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO direct_agent_daily VALUES('ops',?,'cursor',1,0,?)`, d-29, (d-29)*86400); err != nil {
		t.Fatal(err)
	}
	v, err := directSnapshot(file, now)
	if err != nil || v["ops"].Calls30 != 4 || v["ops"].ByAgent["other"] != (DirectAgentUsage{3, 1, d * 86400}) || v["ops"].ByAgent["cursor"] != (DirectAgentUsage{1, 0, (d - 29) * 86400}) {
		t.Fatalf("mixed snapshot: %+v %v", v, err)
	}
	if _, err = db.Exec(`UPDATE direct_daily SET failed=n+1`); err != nil {
		t.Fatal(err)
	}
	if _, e := directSnapshot(file, now); e == nil {
		t.Fatal("invalid totals accepted")
	}
}
