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
type DirectUsage struct {
	Calls30, Failed30, LastUsed int64
	ByAgent                     map[string]DirectAgentUsage
}
type DirectAgentUsage struct{ Calls30, Failed30, LastUsed int64 }

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
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	day := now.UTC().Unix() / 86400
	rows, err := tx.QueryContext(ctx, `SELECT item,SUM(CASE WHEN day>=? THEN n ELSE 0 END),SUM(CASE WHEN day>=? THEN failed ELSE 0 END),MAX(last),MIN(n),MIN(failed),MIN(n-failed) FROM direct_daily WHERE day<=? GROUP BY item LIMIT ?`, day-29, day-29, day, maxItems+1)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	var hasAgents bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='direct_agent_daily')`).Scan(&hasAgents); err != nil {
		return nil, err
	}
	if hasAgents {
		ar, err := tx.QueryContext(ctx, `SELECT item,agent,SUM(CASE WHEN day>=? THEN n ELSE 0 END),SUM(CASE WHEN day>=? THEN failed ELSE 0 END),MAX(last),MIN(n),MIN(failed),MIN(n-failed) FROM direct_agent_daily WHERE day<=? GROUP BY item,agent`, day-29, day-29, day)
		if err != nil {
			return nil, err
		}
		defer ar.Close()
		for ar.Next() {
			var item, client string
			var a DirectAgentUsage
			var minN, minF, minOK int64
			if err := ar.Scan(&item, &client, &a.Calls30, &a.Failed30, &a.LastUsed, &minN, &minF, &minOK); err != nil {
				return nil, err
			}
			v, ok := result[item]
			if !ok || client != product(client) || a.LastUsed <= 0 || a.LastUsed > v.LastUsed || minN < 0 || minF < 0 || minOK < 0 {
				return nil, errors.New("invalid direct agent history")
			}
			if v.ByAgent == nil {
				v.ByAgent = map[string]DirectAgentUsage{}
			}
			v.ByAgent[client] = a
			result[item] = v
		}
		if err := ar.Err(); err != nil {
			return nil, err
		}
		ar.Close()
	}
	// Older workers write only the total. Preserve the residual in an unnamed
	// bucket. Its timestamp is knowable only if no attributed event accounts
	// for the total's most recent timestamp (or the entire item is legacy).
	for item, v := range result {
		var calls, failed, last int64
		for _, a := range v.ByAgent {
			calls += a.Calls30
			failed += a.Failed30
			last = max(last, a.LastUsed)
		}
		n, f := v.Calls30-calls, v.Failed30-failed
		if n < 0 || f < 0 || f > n {
			return nil, errors.New("inconsistent direct agent counts")
		}
		if n > 0 || last < v.LastUsed {
			if v.ByAgent == nil {
				v.ByAgent = map[string]DirectAgentUsage{}
			}
			a := v.ByAgent["other"]
			a.Calls30 += n
			a.Failed30 += f
			if last < v.LastUsed {
				a.LastUsed = v.LastUsed
			} else if n > 0 {
				a.LastUsed = 0
			}
			v.ByAgent["other"] = a
			result[item] = v
		}
	}
	return result, tx.Commit()
}
