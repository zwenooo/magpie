package libraryuse

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DirectUsage is a point-in-time total from the cooperating local ops-mcp
// server. Do not copy it into use.db: opening the page again must be idempotent.
type DirectUsage struct{ Calls30, Failed30, LastUsed int64 }

func DirectSnapshot(now time.Time) (map[string]DirectUsage, error) {
	return directSnapshot(filepath.Join(local.dir(), "ops-direct.db"), now)
}

func directSnapshot(path string, now time.Time) (map[string]DirectUsage, error) {
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || !filepath.IsAbs(path) {
		return nil, errors.New("invalid direct usage database")
	}
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	q := url.Values{"mode": {"ro"}, "_pragma": {"query_only(ON)", "trusted_schema(OFF)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	day := now.UTC().Unix() / 86400
	rows, err := db.QueryContext(ctx, `SELECT item,SUM(CASE WHEN day>=? THEN n ELSE 0 END),SUM(CASE WHEN day>=? THEN failed ELSE 0 END),MAX(last),MIN(n),MIN(failed),MIN(n-failed) FROM direct_daily WHERE day<=? GROUP BY item LIMIT ?`, day-29, day-29, day, maxItems+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]DirectUsage{}
	for rows.Next() {
		var name string
		var v DirectUsage
		var minN, minF, minOK int64
		if err := rows.Scan(&name, &v.Calls30, &v.Failed30, &v.LastUsed, &minN, &minF, &minOK); err != nil {
			return nil, err
		}
		if !validItem("mcp", name) || len(result) >= maxItems || minN < 0 || minF < 0 || minOK < 0 || v.LastUsed <= 0 || v.LastUsed > now.Unix() {
			return nil, errors.New("invalid direct use history")
		}
		result[name] = v
	}
	return result, rows.Err()
}
