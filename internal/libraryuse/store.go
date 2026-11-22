package libraryuse

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/yetone/magpie/internal/sqlitedb"
)

// Daily series are independent margins, not a tool × client cross product.
// dim=0 is an item, dim=1 a tool and dim=2 a fixed client product.
const schema = `CREATE TABLE meta(id INTEGER PRIMARY KEY CHECK(id=1), latest INTEGER NOT NULL);
INSERT INTO meta VALUES(1,0);
CREATE TABLE item(kind TEXT NOT NULL,name TEXT NOT NULL,first INTEGER NOT NULL,start INTEGER NOT NULL DEFAULT 0,seen INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(kind,name)) WITHOUT ROWID;
CREATE TABLE series(kind TEXT NOT NULL,name TEXT NOT NULL,dim INTEGER NOT NULL,label TEXT NOT NULL,last INTEGER NOT NULL,PRIMARY KEY(kind,name,dim,label),FOREIGN KEY(kind,name) REFERENCES item ON DELETE CASCADE) WITHOUT ROWID;
CREATE TABLE daily(kind TEXT NOT NULL,name TEXT NOT NULL,dim INTEGER NOT NULL,label TEXT NOT NULL,day INTEGER NOT NULL,n INTEGER NOT NULL,r INTEGER NOT NULL,f INTEGER NOT NULL,PRIMARY KEY(kind,name,dim,label,day),FOREIGN KEY(kind,name,dim,label) REFERENCES series ON DELETE CASCADE) WITHOUT ROWID;
CREATE INDEX expire_daily ON daily(day);
PRAGMA user_version=1;`

func openDB(dir string, write bool) (*sql.DB, error) {
	path := filepath.Join(dir, "use.db")
	if !filepath.IsAbs(path) {
		return nil, errors.New("libraryuse: database directory must be absolute")
	}
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	q := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(2000)", "foreign_keys(ON)"}}
	if write {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		q.Set("mode", "rwc")
		q.Set("_txlock", "immediate")
		q.Add("_pragma", "max_page_count(16384)")
	} else if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err == nil {
		db.SetMaxOpenConns(1)
	}
	return db, err
}

func version(tx *sql.Tx, init bool) error {
	var v int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v == 0 && init {
		_, err := tx.Exec(schema)
		return err
	}
	if v != 1 {
		return fmt.Errorf("libraryuse: unrecognized database version %d", v)
	}
	return nil
}

// Failed observations must not turn into complete zero windows, including for
// other processes. A one-byte sidecar also works when SQLite cannot commit.
// Its filesystem modification time dates the latest gap. If the filesystem
// itself won't take a mark, retain the gap in this process and return both errors.
func (s *store) fault(dir string, err error) error {
	now := s.now().UTC()
	d := now.UnixMilli() / dayMS
	if s.faultDir != dir || s.faultDay < d {
		s.faultDir, s.faultDay = dir, d
	}
	p := filepath.Join(dir, "gap")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return errors.Join(err, e)
	}
	f, e := os.OpenFile(p, os.O_WRONLY|os.O_CREATE, 0600)
	if e == nil {
		_, e = f.WriteAt([]byte{1}, 0)
		e = errors.Join(e, f.Sync(), f.Close())
	}
	return errors.Join(err, e)
}

func (s *store) write(fn func(*sql.Tx, int64) error) (result error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.dir()
	defer func() {
		if result != nil {
			result = s.fault(dir, result)
		}
	}()
	db, err := openDB(dir, true)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = version(tx, true); err != nil {
		return err
	}
	now := s.now().UTC().UnixMilli()
	var latest int64
	if err = tx.QueryRow(`SELECT latest FROM meta WHERE id=1`).Scan(&latest); err != nil {
		return err
	}
	if now <= 0 || now < latest {
		return errors.New("libraryuse: clock moved backwards")
	}
	if now/dayMS != latest/dayMS {
		if _, err = tx.Exec(`DELETE FROM daily WHERE day<?`, now/dayMS-89); err != nil {
			return err
		}
	}
	if err = fn(tx, now); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE meta SET latest=? WHERE id=1`, now); err != nil {
		return err
	}
	return tx.Commit()
}

type inventoryMode int

const (
	observeNames inventoryMode = iota
	heartbeat
	reconcile
)

func (s *store) inventory(items []Item, mode inventoryMode) error {
	// Validate the entire inventory before deleting anything. Do not retain raw
	// invalid input in a database, log message, or error string.
	valid := len(items) <= maxItems
	if valid {
		seen := make(map[itemKey]bool, len(items))
		for _, i := range items {
			k := itemKey{i.Kind, i.Name}
			if !validItem(i.Kind, i.Name) || seen[k] {
				valid = false
				break
			}
			seen[k] = true
		}
	}
	if !valid {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.fault(s.dir(), errInventory)
	}
	// One packed value goes into a connection-local indexed table. N items do
	// not mean N opens/queries/fsyncs. Nil is the authoritative empty inventory.
	packed, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return s.write(func(tx *sql.Tx, now int64) error {
		if _, err := tx.Exec(`CREATE TEMP TABLE inventory(kind TEXT,name TEXT,eligible INTEGER,PRIMARY KEY(kind,name)) WITHOUT ROWID`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO inventory SELECT value->>'Kind',value->>'Name',value->>'Eligible' FROM json_each(?) WHERE type='object'`, string(packed)); err != nil {
			return err
		}
		if mode != observeNames {
			if _, err := tx.Exec(`DELETE FROM item WHERE (kind,name) NOT IN (SELECT kind,name FROM inventory)`); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE item SET start=0,seen=0 WHERE start!=0 AND (kind,name) IN (SELECT kind,name FROM inventory WHERE NOT eligible)`); err != nil {
				return err
			}
		}
		if mode == reconcile {
			return nil
		}
		if _, err := tx.Exec(`INSERT INTO item(kind,name,first) SELECT kind,name,? FROM inventory WHERE true ON CONFLICT DO NOTHING`, now); err != nil {
			return err
		}
		if err := checkItemCap(tx); err != nil {
			return err
		}
		if mode == heartbeat {
			_, err := tx.Exec(`UPDATE item SET start=CASE WHEN seen>=? AND start>0 THEN start ELSE ? END,seen=? WHERE (kind,name) IN (SELECT kind,name FROM inventory WHERE eligible)`, now-maxSilence.Milliseconds(), now, now)
			return err
		}
		return nil
	})
}
func checkItemCap(tx *sql.Tx) error {
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM item`).Scan(&n); err != nil {
		return err
	}
	if n > maxItems {
		return errInventory
	}
	return nil
}

func (s *store) record(kind, name, tool, client string, read, failed bool) error {
	if !validItem(kind, name) {
		return errName
	}
	client = product(client)
	if kind == "mcp" && !identifier(tool, 128, true) {
		tool = otherTools
	}
	return s.write(func(tx *sql.Tx, now int64) error {
		res, err := tx.Exec(`INSERT INTO item(kind,name,first) VALUES(?,?,?) ON CONFLICT DO NOTHING`, kind, name, now)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 0 {
			if err := checkItemCap(tx); err != nil {
				return err
			}
		}
		add := func(dim int, label string) error {
			if _, err := tx.Exec(`INSERT INTO series VALUES(?,?,?,?,?) ON CONFLICT DO UPDATE SET last=excluded.last`, kind, name, dim, label, now); err != nil {
				return err
			}
			_, err := tx.Exec(`INSERT INTO daily VALUES(?,?,?,?,?,1,?,?) ON CONFLICT DO UPDATE SET n=n+1,r=r+excluded.r,f=f+excluded.f`, kind, name, dim, label, now/dayMS, read, failed)
			return err
		}
		if err = add(0, ""); err != nil {
			return err
		}
		if err = add(2, client); err != nil {
			return err
		}
		if kind != "mcp" {
			return nil
		}
		if tool != otherTools {
			var full bool
			err = tx.QueryRow(`SELECT NOT EXISTS(SELECT 1 FROM series WHERE kind='mcp' AND name=? AND dim=1 AND label=?) AND (SELECT count(*) FROM series WHERE kind='mcp' AND name=? AND dim=1 AND label!=?)>=?`, name, tool, name, otherTools, maxTools).Scan(&full)
			if err != nil {
				return err
			}
			if full {
				tool = otherTools
			}
		}
		return add(1, tool)
	})
}
