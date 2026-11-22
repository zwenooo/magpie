package libraryuse

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
)

func (s *store) snapshot() (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.dir()
	db, err := openDB(dir, false)
	if errors.Is(err, os.ErrNotExist) {
		return View{}, nil
	}
	if err != nil {
		return View{}, err
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return View{}, err
	}
	defer tx.Rollback()
	if err = version(tx, false); err != nil {
		return View{}, err
	}
	var latest int64
	if err = tx.QueryRow(`SELECT latest FROM meta WHERE id=1`).Scan(&latest); err != nil {
		return View{}, err
	}
	now := s.now().UTC().UnixMilli()
	if now <= 0 || now < latest {
		return View{}, errors.New("libraryuse: history is ahead of the clock")
	}
	day := now / dayMS
	var gap int64
	if s.faultDir == dir {
		gap = s.faultDay
	}
	st, err := os.Stat(filepath.Join(dir, "gap"))
	if err == nil {
		if !st.Mode().IsRegular() {
			return View{}, errors.New("libraryuse: invalid gap marker")
		}
		gap = max(gap, st.ModTime().UnixMilli()/dayMS)
	} else if !errors.Is(err, os.ErrNotExist) {
		return View{}, err
	}
	// One result per retained series, including unused inventory entries and
	// tools whose last use predates both windows. Never read individual events.
	rows, err := tx.Query(`SELECT i.kind,i.name,i.first,i.start,i.seen,coalesce(s.dim,0),coalesce(s.label,''),coalesce(s.last,0),
 coalesce(sum(d.n) FILTER(WHERE d.day>=?),0),coalesce(sum(d.r) FILTER(WHERE d.day>=?),0),coalesce(sum(d.f) FILTER(WHERE d.day>=?),0),
 coalesce(sum(d.n),0),coalesce(sum(d.r),0),coalesce(sum(d.f),0)
 FROM item i LEFT JOIN series s USING(kind,name) LEFT JOIN daily d ON (d.kind,d.name,d.dim,d.label)=(s.kind,s.name,s.dim,s.label) AND d.day BETWEEN ? AND ?
 GROUP BY i.kind,i.name,s.dim,s.label`, day-29, day-29, day-29, day-89, day)
	if err != nil {
		return View{}, err
	}
	defer rows.Close()
	v := View{items: make(map[itemKey]*Summary)}
	for rows.Next() {
		var k itemKey
		var first, start, seen, last int64
		var dim int
		var label string
		var t ToolSummary
		if err := rows.Scan(&k.kind, &k.name, &first, &start, &seen, &dim, &label, &last, &t.Days30.Count, &t.Days30.Reads, &t.Days30.Failed, &t.Days90.Count, &t.Days90.Reads, &t.Days90.Failed); err != nil {
			return View{}, err
		}
		if !validItem(k.kind, k.name) || first <= 0 || first > now || seen > now || start > seen || start < 0 || last > now {
			return View{}, errors.New("libraryuse: invalid item history")
		}
		if seen/dayMS != day || seen < now-maxSilence.Milliseconds() {
			start = 0
		} else if start != 0 && gap >= start/dayMS {
			start = (gap + 1) * dayMS
			if start > seen {
				start = 0
			}
		}
		t.LastUsed = stamp(last)
		t.Days30.Complete = start > 0 && start <= (day-29)*dayMS
		t.Days90.Complete = start > 0 && start <= (day-89)*dayMS
		r := v.items[k]
		if r == nil {
			r = &Summary{ObservedSince: stamp(first), EligibleSince: stamp(start)}
			v.items[k] = r
		}
		switch dim {
		case 0:
			r.LastUsed, r.Days30, r.Days90 = t.LastUsed, t.Days30, t.Days90
		case 1:
			if k.kind != "mcp" || label != otherTools && !identifier(label, 128, true) {
				return View{}, errors.New("libraryuse: invalid tool dimension")
			}
			if r.Tools == nil {
				r.Tools = make(map[string]ToolSummary)
			}
			r.Tools[label] = t
		case 2:
			if product(label) != label {
				return View{}, errors.New("libraryuse: invalid client dimension")
			}
			if r.ByClient == nil {
				r.ByClient = make(map[string]ToolSummary)
			}
			r.ByClient[label] = t
		default:
			return View{}, errors.New("libraryuse: invalid dimension")
		}
	}
	if err := rows.Err(); err != nil {
		return View{}, err
	}
	if err := tx.Commit(); err != nil {
		return View{}, err
	}
	return v, nil
}
