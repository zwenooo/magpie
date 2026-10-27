package cursorbyok

// Magpie owns its embedded Cursor service and uses its private model API.
// The schema and hash below are from
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
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/filelock"
)

// Model is the gateway model offered to cursor-byok. Output limits have
// already been clamped by the caller to what its provider can serve.
type Model struct {
	ID, Name           string
	APIs               []string
	Context, MaxTokens int
}

// Gateway is the endpoint and client key cursor-byok sends requests to.
type Gateway struct{ URL, Key string }

// Manager owns the model journal and speaks to a backend owned by Runtime.
type Manager struct {
	StatePath string
	// Control points at a backend owned by Magpie. Missing endpoints fail closed.
	Control *Endpoint
}

// ErrNoModels lets the caller present its own empty-catalog guidance.
var ErrNoModels = errors.New("no models are available for cursor-byok")

const modelsPath = "/__byok-api__/api/models"

var writeMu sync.Mutex
var client = &http.Client{
	Timeout:       2 * time.Second,
	Transport:     &http.Transport{Proxy: nil},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

var errChanged = errors.New("cursor-byok's managed model was changed or removed outside magpie; disconnect and connect again to keep that model as your own")

type modelConfig map[string]json.RawMessage

type entry struct {
	Hash    string      `json:"hash"`
	Last    modelConfig `json:"last"`
	Pending modelConfig `json:"pending,omitempty"`
	Before  modelConfig `json:"before,omitempty"`
	After   modelConfig `json:"after,omitempty"`
}

type state struct {
	Active bool              `json:"active"`
	Models map[string]*entry `json:"models"`
}

func (m Manager) load() (state, error) {
	var s state
	b, err := os.ReadFile(m.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		return state{Models: map[string]*entry{}}, nil
	}
	if err != nil || json.Unmarshal(b, &s) != nil || s.Models == nil {
		return s, errors.New("cursor-byok's magpie connection record cannot be read; it has been left unchanged")
	}
	for _, e := range s.Models {
		if e == nil || e.Last == nil || e.Hash != "" && !hashValid(e.Hash) ||
			e.Pending != nil && e.After == nil {
			return s, errors.New("cursor-byok's magpie connection record is invalid; it has been left unchanged")
		}
	}
	return s, nil
}

func (m Manager) save(s state) error {
	p := m.StatePath
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

// Connected reads only the journal. A damaged or partly disconnected
// journal still counts as connected so the caller offers recovery.
func (m Manager) Connected() bool {
	s, err := m.load()
	return err != nil || s.Active || len(s.Models) > 0
}

// ValidateState lets the host report a corrupt journal even when a saved
// disconnect intent suppresses automatic runtime work.
func (m Manager) ValidateState() error { _, err := m.load(); return err }

func (m Manager) ask(addr, method, path string, body, out any) error {
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
	if m.Control != nil {
		req.Header.Set("Authorization", "Bearer "+m.Control.Token)
	}
	httpClient := client
	if method != http.MethodGet && strings.Contains(path, "/harness/") {
		copy := *client
		copy.Timeout = 90 * time.Second
		httpClient = &copy
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return errors.New("Magpie's bundled Cursor backend is not responding; retry or restart Magpie")
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

func (m Manager) list(addr string) (map[string]modelConfig, error) {
	var list []modelConfig
	if err := m.ask(addr, "GET", modelsPath, nil, &list); err != nil {
		return nil, err
	}
	if list == nil {
		return nil, errors.New("cursor-byok returned no model list")
	}
	out := map[string]modelConfig{}
	for _, m := range list {
		h := stringField(m, "model_hash")
		if !hashValid(h) || out[h] != nil {
			return nil, errors.New("cursor-byok returned an invalid model identity")
		}
		for _, k := range []string{"display_name", "type", "base_url", "api_key", "tooltip_data", "model_id"} {
			if stringField(m, k) == "" {
				return nil, errors.New("cursor-byok returned an incomplete model configuration")
			}
		}
		out[h] = m
	}
	return out, nil
}

func stringField(m modelConfig, k string) string {
	var s string
	json.Unmarshal(m[k], &s)
	return s
}

func hashValid(h string) bool {
	b, err := hex.DecodeString(h)
	return err == nil && len(b) == 8
}

// Every generated model uses a full URL. This is cursor-byok's own hash,
// which includes its display name and changes when the gateway port moves.
func hash(m modelConfig) string {
	parts := []string{stringField(m, "base_url"), stringField(m, "model_id"), stringField(m, "api_key"), stringField(m, "display_name")}
	if stringField(m, "type") == "openai" {
		parts = append(parts, stringField(m, "openai_endpoint"))
	}
	h := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(h[:8])
}

func desired(m Model, gw Gateway) modelConfig {
	typ, endpoint := "openai", "/v1/chat/completions"
	if slices.Contains(m.APIs, "responses") {
		endpoint = "/v1/responses"
	}
	if slices.Contains(m.APIs, "anthropic") {
		typ, endpoint = "anthropic", "/v1/messages"
	}
	v := map[string]any{"display_name": strings.TrimSpace(m.Name), "group_name": "magpie", "type": typ,
		"base_url": strings.TrimRight(gw.URL, "/") + endpoint, "use_full_url": true,
		"api_key": gw.Key, "tooltip_data": "Managed by magpie", "model_id": m.ID,
		"openai_endpoint": endpoint, "context_window_tokens": nil, "max_completion_tokens": nil, "anthropic_max_tokens": nil}
	if v["display_name"] == "" {
		v["display_name"] = m.ID
	}
	if m.Context > 0 {
		v["context_window_tokens"] = m.Context
	}
	if typ == "anthropic" {
		v["openai_endpoint"] = ""
		if n := m.MaxTokens; n > 0 {
			v["anthropic_max_tokens"] = n
		}
	} else if n := m.MaxTokens; n > 0 {
		v["max_completion_tokens"] = n
	}
	b, _ := json.Marshal(v)
	var out modelConfig
	json.Unmarshal(b, &out)
	return out
}

// Connection edits transfer a row out of magpie's control. Tuning, headers,
// labels and token limits may be edited in cursor-byok and are kept.
func sameConnection(a, b modelConfig) bool {
	for _, k := range []string{"base_url", "api_key", "model_id", "type", "use_full_url", "openai_endpoint"} {
		if !equal(a[k], b[k]) {
			return false
		}
	}
	return true
}

func equal(a, b json.RawMessage) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

// A reply (including one found after a lost reply) must confirm every
// submitted field, not just the hash: limits and tuning are not hashed.
func confirmed(got, pending modelConfig) bool {
	if got == nil {
		return false
	}
	for k, v := range pending {
		if !equal(got[k], v) {
			return false
		}
	}
	return true
}

// cursor-byok normalizes fields belonging to the other protocol. Send that
// normalized shape too, so acknowledgement and recovery compare like with
// like even when a catalog model changes its preferred protocol.
func normalize(m modelConfig) {
	if stringField(m, "type") == "anthropic" {
		m["reasoning_effort"] = json.RawMessage(`null`)
		m["openai_extra_params_enabled"] = json.RawMessage(`false`)
		m["openai_extra_params"] = json.RawMessage(`{}`)
		if stringField(m, "anthropic_thinking_effort") == "" {
			m["anthropic_thinking_effort"] = json.RawMessage(`"xhigh"`)
		}
	} else {
		m["anthropic_extra_params_enabled"] = json.RawMessage(`false`)
		m["anthropic_extra_params"] = json.RawMessage(`{}`)
		m["anthropic_thinking_effort"] = json.RawMessage(`null`)
	}
}

// Check reports drift or an unavailable service without changing its models.
func (m Manager) Check(gw Gateway) string {
	s, err := m.load()
	if err != nil {
		return err.Error()
	}
	if !s.Active && len(s.Models) == 0 {
		return ""
	}
	addr, err := m.address()
	if err != nil {
		return err.Error()
	}
	rows, err := m.list(addr)
	if err != nil {
		return err.Error()
	}
	for _, e := range s.Models {
		if e.Pending != nil {
			return "cursor-byok's model update is unfinished; apply again to complete it"
		}
		if rows[e.Hash] == nil || !sameConnection(rows[e.Hash], e.Last) {
			return errChanged.Error()
		}
		endpoint := stringField(e.Last, "openai_endpoint")
		if stringField(e.Last, "type") == "anthropic" {
			endpoint = "/v1/messages"
		}
		if stringField(e.Last, "base_url") != strings.TrimRight(gw.URL, "/")+endpoint ||
			stringField(e.Last, "api_key") != gw.Key {
			return "cursor-byok is configured for magpie's previous gateway address; apply again to update it"
		}
	}
	return ""
}

// Write reconciles the supplied models with only the rows this manager created.
// An automatic sync (explicit=false) never enables a disconnected integration.
func (m Manager) Write(gw Gateway, models []Model, on, explicit bool) error {
	// Sync runs for every known agent, installed or not. It must not
	// create files for one the user has never connected.
	if !explicit {
		s, err := m.load()
		if err != nil || !s.Active {
			return err
		}
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	unlock, held := filelock.Lock(m.StatePath+".lock", 2*time.Second)
	if !held {
		return errors.New("another magpie is changing cursor-byok; try again after it finishes")
	}
	defer unlock()
	s, err := m.load()
	if err != nil {
		return err
	}
	if !explicit && !s.Active {
		return nil
	}
	if !on && !s.Active && len(s.Models) == 0 {
		return nil
	}
	want := map[string]modelConfig{}
	if on {
		for _, model := range models {
			want[model.ID] = desired(model, gw)
		}
		if explicit && !s.Active && len(want) == 0 {
			return ErrNoModels
		}
	}
	addr, err := m.address()
	if err != nil {
		return err
	}
	rows, err := m.list(addr)
	if err != nil {
		return err
	}
	if s.Active != on {
		s.Active = on
		if err := m.save(s); err != nil {
			return err
		}
	}
	// Recover acknowledged or lost replies before planning more changes.
	for _, ref := range slices.Sorted(maps.Keys(s.Models)) {
		e := s.Models[ref]
		if e.Pending != nil {
			next := hash(e.Pending)
			if got := rows[next]; confirmed(got, e.Pending) {
				if e.Hash != "" && next != e.Hash && rows[e.Hash] != nil {
					return errChanged
				}
				e.Hash, e.Last, e.Pending, e.Before, e.After = next, e.After, nil, nil, nil
				if err := m.save(s); err != nil {
					return err
				}
			}
		}
		cur := rows[e.Hash]
		if !on || want[ref] == nil {
			// Missing/changed identity is now the user's. Never search by
			// display name, group or model id to delete a replacement.
			if cur != nil && sameConnection(cur, e.Last) {
				if err := m.ask(addr, "DELETE", modelsPath+"/"+e.Hash, nil, nil); err != nil {
					return err
				}
				delete(rows, e.Hash)
			}
			delete(s.Models, ref)
			if err := m.save(s); err != nil {
				return err
			}
			continue
		}
		if e.Pending == nil && (cur == nil || !sameConnection(cur, e.Last)) {
			return errChanged
		}
		if e.Pending != nil && e.Hash != "" && !maps.EqualFunc(cur, e.Before, equal) {
			return errors.New("cursor-byok's model changed during an unfinished update; magpie left it unchanged. Rename it in cursor-byok before disconnecting if you want to keep this configuration")
		}
		if e.Pending != nil {
			// It was not applied and the old row is unchanged. Replan from
			// today's catalog, which may have changed again after failure.
			// An unacknowledged create can be replaced only while absent.
			if e.Hash == "" {
				if rows[hash(e.Pending)] != nil {
					return errChanged
				}
				delete(s.Models, ref)
			} else {
				e.Pending, e.Before, e.After = nil, nil, nil
			}
			if err := m.save(s); err != nil {
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
			if rows[hash(desired)] != nil {
				return errors.New("a matching cursor-byok model already exists; magpie left it unchanged")
			}
			e = &entry{Last: maps.Clone(desired), Pending: desired, After: maps.Clone(desired)}
			normalize(e.Pending)
			s.Models[ref] = e
			if err := m.save(s); err != nil {
				return err
			}
		} else if e.Pending == nil {
			cur := rows[e.Hash]
			next := maps.Clone(cur)
			after := maps.Clone(e.Last)
			changed := false
			for k, v := range desired {
				if equal(cur[k], e.Last[k]) && !equal(cur[k], v) {
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
			normalize(next)
			if h := hash(next); h != e.Hash && rows[h] != nil {
				return errors.New("a matching cursor-byok model already exists; magpie left it unchanged")
			}
			e.Pending, e.Before, e.After = next, cur, after
			if err := m.save(s); err != nil {
				return err
			}
		}
		var saved modelConfig
		if e.Hash == "" {
			var created []modelConfig
			err = m.ask(addr, "POST", modelsPath, map[string]any{"models": []modelConfig{e.Pending}}, &created)
			if err == nil && len(created) == 1 {
				saved = created[0]
			}
		} else {
			err = m.ask(addr, "PUT", modelsPath+"/"+e.Hash, e.Pending, &saved)
		}
		if err != nil {
			return err
		}
		h := hash(e.Pending)
		if stringField(saved, "model_hash") != h || !confirmed(saved, e.Pending) {
			return errors.New("cursor-byok did not confirm the model update; apply again to check it")
		}
		delete(rows, e.Hash)
		rows[h] = saved
		e.Hash, e.Last, e.Pending, e.Before, e.After = h, e.After, nil, nil, nil
		if err := m.save(s); err != nil {
			return err
		}
	}
	return nil
}
