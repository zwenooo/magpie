package agent

// cursor-byok is installed and initialized separately. Its desktop service
// owns its SQLite database and Cursor setup; magpie reads only the service
// port and uses its model CRUD API. The schema and hash below are from
// leookun/cursor-byok 1.0.1 (31e9f5c), server/src/model/configuration.rs.
// A journal in magpie's folder records only models this integration creates.
// An intent is saved before each request, so a lost reply can be recovered
// without adopting a user's pre-existing model or creating a duplicate.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/filelock"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/sessions"
	_ "github.com/yetone/magpie/internal/sqlitedb"
)

const CursorBYOKID = "cursor-byok"
const cursorBYOKModelsPath = "/__byok-api__/api/models"

var cursorBYOKMu sync.Mutex
var cursorBYOKClient = &http.Client{
	Timeout:       2 * time.Second,
	Transport:     &http.Transport{Proxy: nil},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

var noticeCursorBYOK = newNotice("Install and initialize cursor-byok separately, then keep it running. magpie manages only its model configurations; Cursor setup and the local service stay in cursor-byok. Restart Cursor and start a new conversation to refresh its model list.")

var errCursorBYOKChanged = errors.New("cursor-byok's managed model was changed or removed outside magpie; disconnect and connect again to keep that model as your own")

type cursorBYOKModel map[string]json.RawMessage

type cursorBYOKEntry struct {
	Hash    string          `json:"hash"`
	Last    cursorBYOKModel `json:"last"`
	Pending cursorBYOKModel `json:"pending,omitempty"`
	Before  cursorBYOKModel `json:"before,omitempty"`
	After   cursorBYOKModel `json:"after,omitempty"`
}

type cursorBYOKState struct {
	Active bool                        `json:"active"`
	Models map[string]*cursorBYOKEntry `json:"models"`
}

func cursorBYOKStatePath() string { return filepath.Join(appdir.Config(), "cursor-byok.json") }

func cursorBYOKLoad() (cursorBYOKState, error) {
	var s cursorBYOKState
	b, err := os.ReadFile(cursorBYOKStatePath())
	if errors.Is(err, os.ErrNotExist) {
		return cursorBYOKState{Models: map[string]*cursorBYOKEntry{}}, nil
	}
	if err != nil || json.Unmarshal(b, &s) != nil || s.Models == nil {
		return s, errors.New("cursor-byok's magpie connection record cannot be read; it has been left unchanged")
	}
	for _, e := range s.Models {
		if e == nil || e.Last == nil || e.Hash != "" && !cursorBYOKHashValid(e.Hash) ||
			e.Pending != nil && e.After == nil {
			return s, errors.New("cursor-byok's magpie connection record is invalid; it has been left unchanged")
		}
	}
	return s, nil
}

func cursorBYOKSave(s cursorBYOKState) error {
	p := cursorBYOKStatePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	// WriteAtomic preserves a file's mode; establish private permissions
	// before it writes the journal, which includes the gateway's key.
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err = f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return edit.WriteAtomic(p, b)
}

func cursorBYOK(home string) *Agent {
	dir := filepath.Join(home, ".cursor-byok-v3")
	db := filepath.Join(dir, "cursor-byok.db")
	return &Agent{
		ID: CursorBYOKID, Name: "Cursor (via cursor-byok)", Icon: "cursor", Dir: dir,
		// No editable Path: DisconnectPreview must never contact the live
		// service from its copied HOME. The API has no file diff to show.
		detect:      func() bool { return isFile(db) },
		ListsModels: true,
		Notice:      func() string { return noticeCursorBYOK.String() },
		Check:       func() string { return cursorBYOKCheck(db) },
		Sync:        func() error { return cursorBYOKWrite(db, true, false) },
		Fields: []Field{{
			Key: "provider", Label: "provider",
			Get: func() string {
				s, err := cursorBYOKLoad()
				if err != nil || s.Active || len(s.Models) > 0 {
					return magpieID
				}
				return ""
			},
			Set: func(v string) error { return cursorBYOKWrite(db, v != "", true) },
			Options: func(map[string]string) []Option {
				return []Option{{Value: magpieID, Label: "magpie", Icon: "magpie", Note: "magpie's models through cursor-byok"}}
			},
		}},
	}
}

// cursorBYOKAddress reads the port desktop persists at bind, including a
// fallback port. OpenUntouched reads WAL without creating sidecars. Never
// scan ports or follow an address supplied by another process.
func cursorBYOKAddress(path string) (string, error) {
	if disconnectDryRun {
		return "", errors.New("cursor-byok's live service has no disconnect preview")
	}
	if !isFile(path) {
		return "", errors.New("open cursor-byok once and keep it running before connecting")
	}
	db, closeDB, err := sessions.OpenUntouched(path)
	if err != nil {
		return "", errors.New("cursor-byok's service port cannot be read")
	}
	defer closeDB()
	var raw string
	var ports struct {
		Service int `json:"service_port"`
	}
	if db.QueryRow(`SELECT value_json FROM service_settings WHERE setting_key = 'network_ports'`).Scan(&raw) != nil ||
		json.Unmarshal([]byte(raw), &ports) != nil || ports.Service < 1 || ports.Service > 65535 {
		return "", errors.New("cursor-byok has no valid saved service port; open it and try again")
	}
	return "http://127.0.0.1:" + strconv.Itoa(ports.Service), nil
}

func cursorBYOKAsk(addr, method, path string, body, out any) error {
	if disconnectDryRun {
		return errors.New("cursor-byok's live service has no disconnect preview")
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, addr+path, rd)
	if err != nil {
		return errors.New("cursor-byok's local API address is invalid")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := cursorBYOKClient.Do(req)
	if err != nil {
		return errors.New("cursor-byok isn't answering; open it and try again")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		// API errors can echo an upstream key or the submitted model.
		return fmt.Errorf("cursor-byok's model API returned HTTP %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
	if err != nil || len(b) > 16<<20 || json.Unmarshal(b, out) != nil {
		return errors.New("cursor-byok returned an unreadable model configuration")
	}
	return nil
}

func cursorBYOKList(addr string) (map[string]cursorBYOKModel, error) {
	var list []cursorBYOKModel
	if err := cursorBYOKAsk(addr, "GET", cursorBYOKModelsPath, nil, &list); err != nil {
		return nil, err
	}
	if list == nil {
		return nil, errors.New("cursor-byok returned no model list")
	}
	out := map[string]cursorBYOKModel{}
	for _, m := range list {
		h := cursorBYOKString(m, "model_hash")
		if !cursorBYOKHashValid(h) || out[h] != nil {
			return nil, errors.New("cursor-byok returned an invalid model identity")
		}
		for _, k := range []string{"display_name", "type", "base_url", "api_key", "tooltip_data", "model_id"} {
			if cursorBYOKString(m, k) == "" {
				return nil, errors.New("cursor-byok returned an incomplete model configuration")
			}
		}
		out[h] = m
	}
	return out, nil
}

func cursorBYOKString(m cursorBYOKModel, k string) string {
	var s string
	json.Unmarshal(m[k], &s)
	return s
}

func cursorBYOKHashValid(h string) bool {
	b, err := hex.DecodeString(h)
	return err == nil && len(b) == 8
}

// Every generated model uses a full URL. This is cursor-byok's own hash,
// which includes its display name and changes when the gateway port moves.
func cursorBYOKHash(m cursorBYOKModel) string {
	parts := []string{cursorBYOKString(m, "base_url"), cursorBYOKString(m, "model_id"), cursorBYOKString(m, "api_key"), cursorBYOKString(m, "display_name")}
	if cursorBYOKString(m, "type") == "openai" {
		parts = append(parts, cursorBYOKString(m, "openai_endpoint"))
	}
	h := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(h[:8])
}

func cursorBYOKDesired(m catalog.Model, gw string) cursorBYOKModel {
	typ, endpoint := "openai", "/v1/chat/completions"
	if slices.Contains(m.APIs, "responses") {
		endpoint = "/v1/responses"
	}
	if slices.Contains(m.APIs, "anthropic") {
		typ, endpoint = "anthropic", "/v1/messages"
	}
	v := map[string]any{"display_name": strings.TrimSpace(m.Name), "group_name": "magpie", "type": typ,
		"base_url": strings.TrimRight(gw, "/") + endpoint, "use_full_url": true,
		"api_key": agentKeyAt(CursorBYOKID, gw), "tooltip_data": "Managed by magpie", "model_id": m.ID,
		"openai_endpoint": endpoint, "context_window_tokens": nil, "max_completion_tokens": nil, "anthropic_max_tokens": nil}
	if v["display_name"] == "" {
		v["display_name"] = m.ID
	}
	if m.Context > 0 {
		v["context_window_tokens"] = m.Context
	}
	if typ == "anthropic" {
		v["openai_endpoint"] = ""
		if n := maxTokens(m); n > 0 {
			v["anthropic_max_tokens"] = n
		}
	} else if n := maxTokens(m); n > 0 {
		v["max_completion_tokens"] = n
	}
	b, _ := json.Marshal(v)
	var out cursorBYOKModel
	json.Unmarshal(b, &out)
	return out
}

// Connection edits transfer a row out of magpie's control. Tuning, headers,
// labels and token limits may be edited in cursor-byok and are kept.
func cursorBYOKSameConnection(a, b cursorBYOKModel) bool {
	for _, k := range []string{"base_url", "api_key", "model_id", "type", "use_full_url", "openai_endpoint"} {
		if !cursorBYOKEqual(a[k], b[k]) {
			return false
		}
	}
	return true
}

func cursorBYOKEqual(a, b json.RawMessage) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

// A reply (including one found after a lost reply) must confirm every
// submitted field, not just the hash: limits and tuning are not hashed.
func cursorBYOKConfirmed(got, pending cursorBYOKModel) bool {
	if got == nil {
		return false
	}
	for k, v := range pending {
		if !cursorBYOKEqual(got[k], v) {
			return false
		}
	}
	return true
}

// cursor-byok normalizes fields belonging to the other protocol. Send that
// normalized shape too, so acknowledgement and recovery compare like with
// like even when a catalog model changes its preferred protocol.
func cursorBYOKNormalize(m cursorBYOKModel) {
	if cursorBYOKString(m, "type") == "anthropic" {
		m["reasoning_effort"] = json.RawMessage(`null`)
		m["openai_extra_params_enabled"] = json.RawMessage(`false`)
		m["openai_extra_params"] = json.RawMessage(`{}`)
		if cursorBYOKString(m, "anthropic_thinking_effort") == "" {
			m["anthropic_thinking_effort"] = json.RawMessage(`"xhigh"`)
		}
	} else {
		m["anthropic_extra_params_enabled"] = json.RawMessage(`false`)
		m["anthropic_extra_params"] = json.RawMessage(`{}`)
		m["anthropic_thinking_effort"] = json.RawMessage(`null`)
	}
}

func cursorBYOKCheck(db string) string {
	// Get reads only the journal. Check is asked several times while a
	// page is drawn, so a closed app is probed at most once in five seconds.
	key := db + "\n" + gateway.URL()
	if st, err := os.Stat(cursorBYOKStatePath()); err == nil {
		key += st.ModTime().String()
	}
	cursorBYOKChecked.Lock()
	if !testing.Testing() && key == cursorBYOKChecked.key && time.Since(cursorBYOKChecked.at) < 5*time.Second {
		detail := cursorBYOKChecked.detail
		cursorBYOKChecked.Unlock()
		return detail
	}
	cursorBYOKChecked.Unlock()
	detail := cursorBYOKCheckNow(db)
	cursorBYOKChecked.Lock()
	cursorBYOKChecked.key, cursorBYOKChecked.at, cursorBYOKChecked.detail = key, time.Now(), detail
	cursorBYOKChecked.Unlock()
	return detail
}

var cursorBYOKChecked struct {
	sync.Mutex
	key, detail string
	at          time.Time
}

func cursorBYOKCheckNow(db string) string {
	s, err := cursorBYOKLoad()
	if err != nil {
		return err.Error()
	}
	if !s.Active && len(s.Models) == 0 {
		return ""
	}
	addr, err := cursorBYOKAddress(db)
	if err != nil {
		return err.Error()
	}
	rows, err := cursorBYOKList(addr)
	if err != nil {
		return err.Error()
	}
	for _, e := range s.Models {
		if e.Pending != nil {
			return "cursor-byok's model update is unfinished; apply again to complete it"
		}
		if rows[e.Hash] == nil || !cursorBYOKSameConnection(rows[e.Hash], e.Last) {
			return errCursorBYOKChanged.Error()
		}
		endpoint := cursorBYOKString(e.Last, "openai_endpoint")
		if cursorBYOKString(e.Last, "type") == "anthropic" {
			endpoint = "/v1/messages"
		}
		if cursorBYOKString(e.Last, "base_url") != strings.TrimRight(gateway.URL(), "/")+endpoint ||
			cursorBYOKString(e.Last, "api_key") != agentKeyAt(CursorBYOKID, gateway.URL()) {
			return "cursor-byok is configured for magpie's previous gateway address; apply again to update it"
		}
	}
	return ""
}

func cursorBYOKWrite(db string, on, explicit bool) error {
	if disconnectDryRun {
		return errors.New("cursor-byok's live service has no disconnect preview")
	}
	// Sync runs for every known agent, installed or not. It must not
	// create files for one the user has never connected.
	if !explicit {
		s, err := cursorBYOKLoad()
		if err != nil || !s.Active {
			return err
		}
	}
	cursorBYOKMu.Lock()
	defer cursorBYOKMu.Unlock()
	unlock, held := filelock.Lock(cursorBYOKStatePath()+".lock", 2*time.Second)
	if !held {
		return errors.New("another magpie is changing cursor-byok; try again after it finishes")
	}
	defer unlock()
	s, err := cursorBYOKLoad()
	if err != nil {
		return err
	}
	if !explicit && !s.Active {
		return nil
	}
	if !on && !s.Active && len(s.Models) == 0 {
		return nil
	}
	want := map[string]cursorBYOKModel{}
	if on {
		for _, m := range magpieModels(CursorBYOKID) {
			want[m.ID] = cursorBYOKDesired(m, gateway.URL())
		}
		if explicit && !s.Active && len(want) == 0 {
			return (&Agent{ID: CursorBYOKID, Name: "Cursor (via cursor-byok)"}).noModelLeft()
		}
	}
	addr, err := cursorBYOKAddress(db)
	if err != nil {
		return err
	}
	rows, err := cursorBYOKList(addr)
	if err != nil {
		return err
	}
	if s.Active != on {
		s.Active = on
		if err := cursorBYOKSave(s); err != nil {
			return err
		}
	}
	// Recover acknowledged or lost replies before planning more changes.
	for _, ref := range slices.Sorted(maps.Keys(s.Models)) {
		e := s.Models[ref]
		if e.Pending != nil {
			next := cursorBYOKHash(e.Pending)
			if got := rows[next]; cursorBYOKConfirmed(got, e.Pending) {
				if e.Hash != "" && next != e.Hash && rows[e.Hash] != nil {
					return errCursorBYOKChanged
				}
				e.Hash, e.Last, e.Pending, e.Before, e.After = next, e.After, nil, nil, nil
				if err := cursorBYOKSave(s); err != nil {
					return err
				}
			}
		}
		cur := rows[e.Hash]
		if !on || want[ref] == nil {
			// Missing/changed identity is now the user's. Never search by
			// display name, group or model id to delete a replacement.
			if cur != nil && cursorBYOKSameConnection(cur, e.Last) {
				if err := cursorBYOKAsk(addr, "DELETE", cursorBYOKModelsPath+"/"+e.Hash, nil, nil); err != nil {
					return err
				}
				delete(rows, e.Hash)
			}
			delete(s.Models, ref)
			if err := cursorBYOKSave(s); err != nil {
				return err
			}
			continue
		}
		if e.Pending == nil && (cur == nil || !cursorBYOKSameConnection(cur, e.Last)) {
			return errCursorBYOKChanged
		}
		if e.Pending != nil && e.Hash != "" && !maps.EqualFunc(cur, e.Before, cursorBYOKEqual) {
			return errors.New("cursor-byok's model changed during an unfinished update; magpie left it unchanged. Rename it in cursor-byok before disconnecting if you want to keep this configuration")
		}
		if e.Pending != nil {
			// It was not applied and the old row is unchanged. Replan from
			// today's catalog, which may have changed again after failure.
			// An unacknowledged create can be replaced only while absent.
			if e.Hash == "" {
				if rows[cursorBYOKHash(e.Pending)] != nil {
					return errCursorBYOKChanged
				}
				delete(s.Models, ref)
			} else {
				e.Pending, e.Before, e.After = nil, nil, nil
			}
			if err := cursorBYOKSave(s); err != nil {
				return err
			}
		}
	}
	// Add and update only our rows; user's tuning is copied from the fresh
	// GET, and generated values change only while still as last written.
	for _, ref := range slices.Sorted(maps.Keys(want)) {
		desired := want[ref]
		e := s.Models[ref]
		if e == nil {
			if rows[cursorBYOKHash(desired)] != nil {
				return errors.New("a matching cursor-byok model already exists; magpie left it unchanged")
			}
			e = &cursorBYOKEntry{Last: maps.Clone(desired), Pending: desired, After: maps.Clone(desired)}
			cursorBYOKNormalize(e.Pending)
			s.Models[ref] = e
			if err := cursorBYOKSave(s); err != nil {
				return err
			}
		} else if e.Pending == nil {
			cur := rows[e.Hash]
			next := maps.Clone(cur)
			after := maps.Clone(e.Last)
			changed := false
			for k, v := range desired {
				if cursorBYOKEqual(cur[k], e.Last[k]) && !cursorBYOKEqual(cur[k], v) {
					next[k], changed = v, true
					after[k] = v
				}
			}
			if !changed {
				continue
			}
			delete(next, "model_hash")
			delete(next, "created_at_ms")
			delete(next, "updated_at_ms")
			cursorBYOKNormalize(next)
			if h := cursorBYOKHash(next); h != e.Hash && rows[h] != nil {
				return errors.New("a matching cursor-byok model already exists; magpie left it unchanged")
			}
			e.Pending, e.Before, e.After = next, cur, after
			if err := cursorBYOKSave(s); err != nil {
				return err
			}
		}
		var saved cursorBYOKModel
		if e.Hash == "" {
			var created []cursorBYOKModel
			err = cursorBYOKAsk(addr, "POST", cursorBYOKModelsPath, map[string]any{"models": []cursorBYOKModel{e.Pending}}, &created)
			if err == nil && len(created) == 1 {
				saved = created[0]
			}
		} else {
			err = cursorBYOKAsk(addr, "PUT", cursorBYOKModelsPath+"/"+e.Hash, e.Pending, &saved)
		}
		if err != nil {
			return err
		}
		h := cursorBYOKHash(e.Pending)
		if cursorBYOKString(saved, "model_hash") != h || !cursorBYOKConfirmed(saved, e.Pending) {
			return errors.New("cursor-byok did not confirm the model update; apply again to check it")
		}
		delete(rows, e.Hash)
		rows[h] = saved
		e.Hash, e.Last, e.Pending, e.Before, e.After = h, e.After, nil, nil, nil
		if err := cursorBYOKSave(s); err != nil {
			return err
		}
	}
	return nil
}
