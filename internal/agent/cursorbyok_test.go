package agent

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	syncatomic "sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/filelock"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

// cursorBYOKFixture follows cursor-byok 1.0.1 (31e9f5c): ModelConfigInput,
// normalize_model_input/model_hash, and control/models.rs. Creating a model
// twice fails; PUT can change its hash; batches are atomic. The service port
// lives in service_settings, not in a fixed port or the proxy port.
type cursorBYOKFixture struct {
	t         *testing.T
	server    *httptest.Server
	mu        sync.Mutex
	models    map[string]map[string]any
	writes    []string
	fail      string
	failNth   int
	seen      int
	badList   bool
	loseReply string
}

func newCursorBYOKFixture(t *testing.T, home string) *cursorBYOKFixture {
	t.Helper()
	f := &cursorBYOKFixture{t: t, models: map[string]map[string]any{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	f.port(home)
	old := cursorBYOKRuntimeFor
	cursorBYOKRuntimeFor = func() cursorBackend { return &fixtureCursorRuntime{f: f} }
	t.Cleanup(func() { cursorBYOKRuntimeFor = old })
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := ApproveCursorBackend(); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *cursorBYOKFixture) port(home string) {
	f.t.Helper()
	u, err := url.Parse(f.server.URL)
	if err != nil {
		f.t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		f.t.Fatal(err)
	}
	path := filepath.Join(cursorBYOKRoot(), "data", "cursor-byok.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		f.t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		f.t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;
		CREATE TABLE IF NOT EXISTS service_settings (setting_key TEXT PRIMARY KEY, value_json TEXT NOT NULL, updated_at_ms INTEGER NOT NULL)`); err != nil {
		f.t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT OR REPLACE INTO service_settings VALUES ('network_ports', ?, 1)`, fmt.Sprintf(`{"proxy_port":1,"service_port":%d}`, port)); err != nil {
		f.t.Fatal(err)
	}
}

func cursorBYOKCopy(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var copy map[string]any
	json.Unmarshal(b, &copy)
	return copy
}

func cursorBYOKInput(m map[string]any) (map[string]any, error) {
	m = cursorBYOKCopy(m)
	for _, k := range []string{"display_name", "type", "base_url", "api_key", "tooltip_data", "model_id"} {
		s, ok := m[k].(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("missing required string %s", k)
		}
		m[k] = strings.TrimSpace(s)
	}
	if m["type"] != "openai" && m["type"] != "anthropic" {
		return nil, fmt.Errorf("invalid type %v", m["type"])
	}
	u, err := url.Parse(m["base_url"].(string))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("invalid base_url")
	}
	endpoint := "/v1/messages"
	if m["type"] == "openai" {
		endpoint, _ = m["openai_endpoint"].(string)
		if endpoint == "" {
			endpoint = "/v1/responses"
		}
		if endpoint != "/v1/responses" && endpoint != "/v1/chat/completions" {
			return nil, fmt.Errorf("invalid OpenAI endpoint %q", endpoint)
		}
		m["openai_endpoint"] = endpoint
	} else {
		m["openai_endpoint"] = ""
		m["reasoning_effort"] = nil
		if m["anthropic_thinking_effort"] == nil {
			m["anthropic_thinking_effort"] = "xhigh"
		}
		m["openai_extra_params_enabled"] = false
		m["openai_extra_params"] = map[string]any{}
	}
	if m["type"] == "openai" {
		m["anthropic_thinking_effort"] = nil
		m["anthropic_extra_params_enabled"] = false
		m["anthropic_extra_params"] = map[string]any{}
	}
	requestURL := m["base_url"].(string)
	if m["use_full_url"] != true {
		p := strings.TrimRight(u.Path, "/")
		if tail := p[strings.LastIndex(p, "/")+1:]; strings.HasPrefix(tail, "v") {
			if _, err := strconv.Atoi(strings.TrimPrefix(tail, "v")); err == nil {
				endpoint = strings.TrimPrefix(endpoint, "/v1")
			}
		}
		u.Path = p + endpoint
		requestURL = u.String()
	}
	parts := []string{requestURL, m["model_id"].(string), m["api_key"].(string), m["display_name"].(string)}
	if m["type"] == "openai" {
		parts = append(parts, m["openai_endpoint"].(string))
	}
	hash := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	m["model_hash"] = hex.EncodeToString(hash[:8])
	for _, k := range []string{"openai_extra_params", "custom_headers", "anthropic_extra_params"} {
		if _, ok := m[k]; !ok {
			m[k] = map[string]any{}
		}
		if _, ok := m[k].(map[string]any); !ok {
			return nil, fmt.Errorf("%s must be an object", k)
		}
	}
	for _, k := range []string{"sort_order", "created_at_ms", "updated_at_ms"} {
		if _, ok := m[k]; !ok {
			m[k] = float64(0)
		}
	}
	for _, k := range []string{"use_full_url", "openai_extra_params_enabled", "custom_headers_enabled", "anthropic_extra_params_enabled"} {
		if _, ok := m[k]; !ok {
			m[k] = false
		}
	}
	for _, k := range []string{"group_name", "reasoning_effort", "context_window_tokens", "max_completion_tokens", "anthropic_max_tokens", "anthropic_thinking_effort", "thinking_budget_tokens"} {
		if _, ok := m[k]; !ok {
			m[k] = nil
		}
	}
	return m, nil
}

func (f *cursorBYOKFixture) add(m map[string]any) string {
	f.t.Helper()
	m, err := cursorBYOKInput(m)
	if err != nil {
		f.t.Fatal(err)
	}
	hash := m["model_hash"].(string)
	f.mu.Lock()
	f.models[hash] = m
	f.mu.Unlock()
	return hash
}

func (f *cursorBYOKFixture) snapshot() map[string]map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]map[string]any{}
	for hash, m := range f.models {
		out[hash] = cursorBYOKCopy(m)
	}
	return out
}

func (f *cursorBYOKFixture) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer test-backend-token" {
		http.Error(w, "unauthorized", 401)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/__byok-api__/api/harness/cursor") {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ca":"ready","integration":"enabled","settings_applied":true}`)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	encode := func(v any) {
		e := json.NewEncoder(w)
		e.SetEscapeHTML(false) // serde_json emits literal &, < and >.
		e.Encode(v)
	}
	if r.Method != http.MethodGet {
		f.writes = append(f.writes, r.Method+" "+r.URL.Path)
		if r.Method == f.fail {
			f.seen++
			if f.seen == f.failNth {
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, `{"error":"temporary failure"}`)
				return
			}
		}
	}
	const path = "/__byok-api__/api/models"
	if r.URL.Path == "/__byok-api__/healthz" && r.Method == http.MethodGet {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path == path && r.Method == http.MethodGet {
		if f.badList {
			fmt.Fprint(w, `{"models":[]}`)
			return
		}
		ms := []map[string]any{}
		for _, hash := range slices.Sorted(maps.Keys(f.models)) {
			ms = append(ms, f.models[hash])
		}
		encode(ms)
		return
	}
	if r.URL.Path == path && r.Method == http.MethodPost {
		var input struct {
			Models []map[string]any `json:"models"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input.Models) == 0 {
			http.Error(w, "invalid model batch", http.StatusBadRequest)
			return
		}
		out := []map[string]any{}
		seen := map[string]bool{}
		for _, m := range input.Models {
			m, err := cursorBYOKInput(m)
			if err != nil {
				f.t.Errorf("invalid model input: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			hash := m["model_hash"].(string)
			if f.models[hash] != nil || seen[hash] {
				http.Error(w, "UNIQUE constraint failed", http.StatusInternalServerError)
				return
			}
			seen[hash] = true
			out = append(out, m)
		}
		for _, m := range out {
			f.models[m["model_hash"].(string)] = m
		}
		w.WriteHeader(http.StatusCreated)
		if f.loseReply == http.MethodPost {
			f.loseReply = ""
			fmt.Fprint(w, `[{"model_hash":`)
			return
		}
		encode(out)
		return
	}
	if hash, ok := strings.CutPrefix(r.URL.Path, path+"/"); ok && (r.Method == http.MethodPut || r.Method == http.MethodDelete) {
		if f.models[hash] == nil {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodDelete {
			delete(f.models, hash)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var m map[string]any
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m, err := cursorBYOKInput(m)
		if err != nil {
			f.t.Errorf("invalid model update: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		next := m["model_hash"].(string)
		if next != hash && f.models[next] != nil {
			http.Error(w, "UNIQUE constraint failed", http.StatusInternalServerError)
			return
		}
		delete(f.models, hash)
		f.models[next] = m
		if f.loseReply == http.MethodPut {
			f.loseReply = ""
			fmt.Fprint(w, `{"model_hash":`)
			return
		}
		encode(m)
		return
	}
	f.t.Errorf("unexpected Cursor BYOK request: %s %s", r.Method, r.URL.Path)
	http.NotFound(w, r)
}

func cursorBYOKOwn() map[string]any {
	return map[string]any{
		"display_name": "My own model", "group_name": "Magpie", "type": "openai",
		"base_url": "https://my-api.example/v1", "api_key": "users-own-key",
		"model_id": "my-model", "tooltip_data": "Keep this model", "openai_endpoint": "/v1/responses",
		"reasoning_effort": "high", "custom_headers_enabled": true,
		"custom_headers": map[string]any{"x-team": "mine"},
	}
}

func TestCursorBYOKFixtureHash(t *testing.T) {
	m, err := cursorBYOKInput(cursorBYOKOwn())
	if err != nil {
		t.Fatal(err)
	}
	// Verified by compiling the upstream configuration.rs unchanged in an
	// isolated Rust harness; also independently calculated using SHA256.
	if m["model_hash"] != "11d183d29256a36f" {
		t.Fatalf("fixture does not match cursor-byok's model identity: %v", m["model_hash"])
	}
}

func TestCursorBYOKConnectSyncDisconnect(t *testing.T) {
	home := syncHome(t)
	f := newCursorBYOKFixture(t, home)
	ownHash := f.add(cursorBYOKOwn())
	own := f.snapshot()[ownHash]
	if err := provider.Save(provider.Provider{ID: "anth", Name: "Anth", Key: "k", Anthropic: "http://127.0.0.1:1", Models: []string{"claude-sonnet-5"}}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Save(provider.Provider{ID: "resp", Name: "Resp", Key: "k", Responses: "http://127.0.0.1:1/v1", Models: []string{"gpt-5.5"}}); err != nil {
		t.Fatal(err)
	}
	a, err := Find("cursor-byok")
	if err != nil {
		t.Fatal(err)
	}
	if !a.Detected() || a.Wired() || !a.ListsModels {
		t.Fatalf("before connect: detected %v, wired %v, lists models %v", a.Detected(), a.Wired(), a.ListsModels)
	}
	if err := a.Connect(); err != nil || !a.Wired() {
		t.Fatalf("connect: %v, wired %v", err, a.Wired())
	}
	models := f.snapshot()
	if len(models) != 4 || !reflect.DeepEqual(models[ownHash], own) {
		t.Fatalf("connect changed another model or missed the catalog: %v", models)
	}
	want := map[string]bool{"relay/glm-4.6": true, "anth/claude-sonnet-5": true, "resp/gpt-5.5": true}
	for hash, m := range models {
		if hash == ownHash {
			continue
		}
		id, _ := m["model_id"].(string)
		if !want[id] || !strings.HasPrefix(m["base_url"].(string), gateway.URL()+"/") || m["api_key"] != gateway.TokenFor("cursor-byok") {
			t.Fatalf("model does not use the agent's gateway and key: %v", m)
		}
		delete(want, id)
		switch id {
		case "relay/glm-4.6":
			if m["type"] != "openai" || m["openai_endpoint"] != "/v1/chat/completions" || m["base_url"] != gateway.URL()+"/v1/chat/completions" || m["use_full_url"] != true {
				t.Fatalf("Chat model is not configured with its native protocol: %v", m)
			}
		case "anth/claude-sonnet-5":
			if m["type"] != "anthropic" || m["base_url"] != gateway.URL()+"/v1/messages" || m["use_full_url"] != true {
				t.Fatalf("Anthropic model is not configured with its native protocol: %v", m)
			}
		case "resp/gpt-5.5":
			if m["type"] != "openai" || m["openai_endpoint"] != "/v1/responses" || m["base_url"] != gateway.URL()+"/v1/responses" || m["use_full_url"] != true {
				t.Fatalf("Responses model is not configured with its native protocol: %v", m)
			}
		}
	}
	if len(want) != 0 {
		t.Fatalf("models missing: %v", want)
	}
	if err := a.Sync(); err != nil || !reflect.DeepEqual(f.snapshot(), models) {
		t.Fatalf("sync isn't idempotent: %v", err)
	}
	// Tuning in Cursor BYOK is the user's; catalog synchronization updates
	// connection fields and leaves these values.
	f.mu.Lock()
	for hash, m := range f.models {
		if hash != ownHash {
			m["context_window_tokens"] = float64(32000)
			if m["type"] == "anthropic" {
				m["anthropic_thinking_effort"] = "high"
			} else {
				m["reasoning_effort"] = "high"
			}
			m["custom_headers_enabled"] = true
			m["custom_headers"] = map[string]any{"x-team": "mine"}
		}
	}
	f.mu.Unlock()
	models = f.snapshot()
	if err := a.Sync(); err != nil || !reflect.DeepEqual(f.snapshot(), models) {
		t.Fatalf("sync changed user tuning: %v", err)
	}
	if err := provider.SetHiddenModels("cursor-byok", []string{"anth/claude-sonnet-5"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if ms := f.snapshot(); len(ms) != 3 || !reflect.DeepEqual(ms[ownHash], own) {
		t.Fatalf("hiding a model changed an unrelated model: %v", ms)
	}
	if err := a.Disconnect(); err != nil || a.Wired() {
		t.Fatalf("disconnect: %v, wired %v", err, a.Wired())
	}
	if ms := f.snapshot(); len(ms) != 1 || !reflect.DeepEqual(ms[ownHash], own) {
		t.Fatalf("disconnect must leave the user's model: %v", ms)
	}
	if err := a.Sync(); err != nil || len(f.snapshot()) != 1 {
		t.Fatalf("sync reconnected after disconnect: %v", err)
	}
}

func TestCursorBYOKPortChanges(t *testing.T) {
	home := syncHome(t)
	f := newCursorBYOKFixture(t, home)
	a, err := Find("cursor-byok")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	before := f.snapshot()
	// Move both ports. The service port is rediscovered from the database;
	// a changed gateway URL changes Cursor BYOK's hash returned by PUT.
	next := newCursorBYOKFixture(t, home)
	next.models = f.snapshot()
	f.server.Close()
	t.Setenv("MAGPIE_ADDR", "127.0.0.1:3637")
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	for hash, m := range next.snapshot() {
		if before[hash] != nil || !strings.HasPrefix(m["base_url"].(string), "http://127.0.0.1:3637/") {
			t.Fatalf("old endpoint or hash after port move: %v", m)
		}
	}
	if err := a.Disconnect(); err != nil || len(next.snapshot()) != 0 {
		t.Fatalf("disconnect didn't follow changed hashes: %v, models %v", err, next.snapshot())
	}
}

func TestCursorBYOKReadFailureKeepsModels(t *testing.T) {
	home := syncHome(t)
	f := newCursorBYOKFixture(t, home)
	a, err := Find("cursor-byok")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	before := f.snapshot()
	f.mu.Lock()
	f.badList = true
	writes := len(f.writes)
	f.mu.Unlock()
	if err := a.Sync(); err == nil {
		t.Fatal("an invalid service response was treated as an empty model list")
	}
	f.mu.Lock()
	gotWrites := len(f.writes)
	f.badList = false
	f.mu.Unlock()
	if writes != gotWrites || !reflect.DeepEqual(before, f.snapshot()) {
		t.Fatal("a failed read modified models")
	}
	if err := a.Disconnect(); err != nil || len(f.snapshot()) != 0 {
		t.Fatalf("failed read lost disconnect ownership: %v", err)
	}
}

func TestCursorBYOKPartialDisconnectCanRetry(t *testing.T) {
	home := syncHome(t)
	f := newCursorBYOKFixture(t, home)
	if err := provider.Save(provider.Provider{ID: "second", Name: "Second", Key: "k", Chat: "http://127.0.0.1:1/v1", Models: []string{"other-model"}}); err != nil {
		t.Fatal(err)
	}
	a, err := Find("cursor-byok")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.fail, f.failNth = http.MethodDelete, 2
	f.mu.Unlock()
	if err := a.Disconnect(); err == nil {
		t.Fatal("partial disconnect reported success")
	}
	if !a.Wired() || len(f.snapshot()) != 1 {
		t.Fatalf("partial disconnect lost the remaining model: wired %v, models %v", a.Wired(), f.snapshot())
	}
	before := f.snapshot()
	if err := a.Sync(); err != nil || !reflect.DeepEqual(before, f.snapshot()) || cursorBYOKConnected() {
		t.Fatalf("failed disconnect was re-enabled by sync: %v", err)
	}
	if err := a.Disconnect(); err != nil || a.Wired() || len(f.snapshot()) != 0 {
		t.Fatalf("retry failed: %v, wired %v, models %v", err, a.Wired(), f.snapshot())
	}
}

func TestCursorBYOKDryRunNeverCallsService(t *testing.T) {
	home := syncHome(t)
	f := newCursorBYOKFixture(t, home)
	a, err := Find("cursor-byok")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	before := f.snapshot()
	f.mu.Lock()
	writes := len(f.writes)
	f.mu.Unlock()
	old := disconnectDryRun
	disconnectDryRun = true
	t.Cleanup(func() { disconnectDryRun = old })
	// This is the same flag DryRun sets in the preview subprocess. A
	// copied database still names the live service, so HOME alone is not
	// enough to isolate a preview.
	_ = a.Disconnect()
	f.mu.Lock()
	gotWrites := len(f.writes)
	f.mu.Unlock()
	if gotWrites != writes || !reflect.DeepEqual(before, f.snapshot()) {
		t.Fatal("disconnect preview reached the live service")
	}
}

func TestCursorBYOKUserChangesConnection(t *testing.T) {
	home := syncHome(t)
	f := newCursorBYOKFixture(t, home)
	a, err := Find("cursor-byok")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	// An edit in Cursor BYOK changes its hash. The old ownership record
	// must not authorize adopting or replacing the user's new endpoint.
	for hash, m := range f.snapshot() {
		m["base_url"] = "https://users-relay.example/v1/chat/completions"
		m["api_key"] = "users-replacement-key"
		f.mu.Lock()
		delete(f.models, hash)
		f.mu.Unlock()
		f.add(m)
	}
	before := f.snapshot()
	if d := a.Drift(); d == nil || d.Kind != "unwired" {
		t.Fatalf("user-replaced connection was not reported: %+v", d)
	}
	if err := a.Sync(); err == nil {
		t.Fatal("sync silently accepted a user-replaced connection")
	}
	if !reflect.DeepEqual(f.snapshot(), before) {
		t.Fatal("sync replaced or duplicated the user's changed model")
	}
	if err := a.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.snapshot(), before) {
		t.Fatal("disconnect deleted the user's changed model")
	}
}

func TestCursorBYOKRecoversLostReplies(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			home := syncHome(t)
			f := newCursorBYOKFixture(t, home)
			a, err := Find("cursor-byok")
			if err != nil {
				t.Fatal(err)
			}
			if method == http.MethodPut {
				if err := a.Connect(); err != nil {
					t.Fatal(err)
				}
				t.Setenv("MAGPIE_ADDR", "127.0.0.1:3638")
			}
			f.mu.Lock()
			f.loseReply = method
			f.mu.Unlock()
			if method == http.MethodPost {
				err = a.Connect()
			} else {
				err = a.Sync()
			}
			if err == nil || len(f.snapshot()) != 1 {
				t.Fatalf("missing reply was not reported after persisting: %v, %v", err, f.snapshot())
			}
			// An interrupted first setup stays disconnected until explicitly
			// retried; background catalog sync must never initialize trust.
			if method == http.MethodPost {
				err = a.Reapply()
			} else {
				err = a.Sync()
			}
			if err != nil || len(f.snapshot()) != 1 {
				t.Fatalf("retry created a duplicate or failed: %v, %v", err, f.snapshot())
			}
			if err := a.Disconnect(); err != nil || len(f.snapshot()) != 0 {
				t.Fatalf("lost reply left an unowned model: %v, %v", err, f.snapshot())
			}
		})
	}
}

func TestCursorBYOKCorruptConnectionRecordKeepsModels(t *testing.T) {
	home := syncHome(t)
	f := newCursorBYOKFixture(t, home)
	a, err := Find("cursor-byok")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	before := f.snapshot()
	path := cursorBYOKStatePath()
	for _, invalid := range []string{`{"active":true,"models":`, `null`, `{}`} {
		if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := a.Sync(); err == nil {
			t.Fatal("a corrupt connection record was treated as unconnected")
		}
		if err := a.Disconnect(); err == nil {
			t.Fatal("a corrupt connection record was treated as a completed disconnect")
		}
		if !reflect.DeepEqual(before, f.snapshot()) || readFile(path) != invalid {
			t.Fatal("corrupt record lost saved state or changed service models")
		}
	}
}

func TestCursorBYOKHeldJournalPreventsWrites(t *testing.T) {
	home := syncHome(t)
	f := newCursorBYOKFixture(t, home)
	a, err := Find("cursor-byok")
	if err != nil {
		t.Fatal(err)
	}
	unlock, held := filelock.Lock(cursorBYOKStatePath()+".lock", time.Second)
	if !held {
		t.Fatal("couldn't hold the fixture journal lock")
	}
	defer unlock()
	if err := a.Field("provider").Set(magpieID); err == nil {
		t.Fatal("changed model configurations without holding the journal lock")
	}
	f.mu.Lock()
	writes := len(f.writes)
	f.mu.Unlock()
	if writes != 0 || len(f.snapshot()) != 0 {
		t.Fatal("a concurrent operation reached the service")
	}
}

func TestCursorBYOKJSONEscapingDoesNotChangeOwnership(t *testing.T) {
	home := syncHome(t)
	f := newCursorBYOKFixture(t, home)
	if err := catalog.SaveLive("relay", "http://127.0.0.1:1/v1", []catalog.Model{{ID: "glm-4.6", Name: "R&D <model>", Context: 204800}}); err != nil {
		t.Fatal(err)
	}
	a, err := Find("cursor-byok")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	if d := a.Drift(); d != nil {
		t.Fatalf("Rust's JSON encoding was mistaken for a changed model: %+v", d)
	}
	if err := catalog.SaveLive("relay", "http://127.0.0.1:1/v1", []catalog.Model{{ID: "glm-4.6", Name: "R&D <renamed>", Context: 204800}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	for _, m := range f.snapshot() {
		if !strings.Contains(m["display_name"].(string), "R&D <renamed>") {
			t.Fatalf("escaped JSON blocked catalog rename: %v", m)
		}
	}
	if err := a.Disconnect(); err != nil || len(f.snapshot()) != 0 {
		t.Fatalf("JSON encoding lost ownership: %v", err)
	}
}

func TestCursorBYOKRetriesFailedTuningUpdate(t *testing.T) {
	home := syncHome(t)
	f := newCursorBYOKFixture(t, home)
	a, err := Find("cursor-byok")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	before := f.snapshot()
	if err := catalog.SaveLive("relay", "http://127.0.0.1:1/v1", []catalog.Model{{ID: "glm-4.6", Context: 100000}}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.fail, f.failNth = http.MethodPut, 1
	f.mu.Unlock()
	if err := a.Sync(); err == nil {
		t.Fatal("rejected tuning update reported success")
	}
	if !reflect.DeepEqual(f.snapshot(), before) {
		t.Fatal("rejected PUT changed the service model")
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	for hash, m := range f.snapshot() {
		if before[hash] == nil || m["context_window_tokens"] != float64(100000) {
			t.Fatalf("same hash was mistaken for confirmation of the failed tuning update: %v", m)
		}
	}
}

func TestCursorBYOKPendingUpdateKeepsLaterUserEdit(t *testing.T) {
	home := syncHome(t)
	f := newCursorBYOKFixture(t, home)
	a, err := Find("cursor-byok")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAGPIE_ADDR", "127.0.0.1:3639")
	f.mu.Lock()
	f.fail, f.failNth = http.MethodPut, 1
	f.mu.Unlock()
	if err := a.Sync(); err == nil {
		t.Fatal("rejected update reported success")
	}
	// A later edit changes no identity fields. Replaying the saved PUT
	// would overwrite it unless the retry verifies its complete snapshot.
	f.mu.Lock()
	for _, m := range f.models {
		m["custom_headers_enabled"] = true
		m["custom_headers"] = map[string]any{"x-user": "keep-after-failure"}
	}
	f.mu.Unlock()
	before := f.snapshot()
	if err := a.Sync(); err == nil {
		t.Fatal("retry overwrote a later user edit")
	}
	if !reflect.DeepEqual(before, f.snapshot()) {
		t.Fatal("retry discarded a user edit made after the failed request")
	}
}

func TestCursorBYOKRetryUsesCurrentGateway(t *testing.T) {
	home := syncHome(t)
	f := newCursorBYOKFixture(t, home)
	a, err := Find("cursor-byok")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAGPIE_ADDR", "127.0.0.1:3639")
	f.mu.Lock()
	f.fail, f.failNth = http.MethodPut, 1
	f.mu.Unlock()
	if err := a.Sync(); err == nil {
		t.Fatal("rejected update reported success")
	}
	t.Setenv("MAGPIE_ADDR", "127.0.0.1:3640")
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	for _, m := range f.snapshot() {
		if m["base_url"] != "http://127.0.0.1:3640/v1/chat/completions" {
			t.Fatalf("retry installed the obsolete pending gateway: %v", m)
		}
	}
}

func TestCursorBYOKDoesNotAdoptExistingModel(t *testing.T) {
	home := syncHome(t)
	f := newCursorBYOKFixture(t, home)
	a, err := Find("cursor-byok")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	saved := f.snapshot()
	if err := a.Disconnect(); err != nil {
		t.Fatal(err)
	}
	// The user recreates an identical entry independently. With no saved
	// ownership, matching gateway credentials and labels cannot claim it.
	for _, m := range saved {
		f.add(m)
	}
	if err := a.Connect(); err == nil {
		t.Fatal("connect adopted an existing matching model")
	}
	if err := a.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved, f.snapshot()) {
		t.Fatal("disconnect deleted the pre-existing matching model")
	}
}

func TestCursorBYOKConfiguredModelReachesThirdParty(t *testing.T) {
	home := syncHome(t)
	f := newCursorBYOKFixture(t, home)
	var calls syncatomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("upstream request: %v", err)
		}
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer third-party-key" || body["model"] != "glm-4.6" {
			t.Errorf("third-party request: path %s, model %v", r.URL.Path, body["model"])
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"byok-test","object":"chat.completion","model":"glm-4.6","choices":[{"index":0,"message":{"role":"assistant","content":"BYOK_GATEWAY_OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
	}))
	t.Cleanup(upstream.Close)
	if err := provider.Save(provider.Provider{ID: "relay", Name: "Relay", Key: "third-party-key", Chat: upstream.URL + "/v1", Models: []string{"glm-4.6"}}); err != nil {
		t.Fatal(err)
	}
	server := gateway.New()
	gw := httptest.NewServer(server.Front())
	t.Cleanup(gw.Close)
	t.Setenv("MAGPIE_ADDR", strings.TrimPrefix(gw.URL, "http://"))
	a, err := Find("cursor-byok")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	for _, model := range f.snapshot() {
		body, err := json.Marshal(map[string]any{"model": model["model_id"], "messages": []any{map[string]any{"role": "user", "content": "hello"}}, "stream": false})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, model["base_url"].(string), bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+model["api_key"].(string))
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		out, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || !bytes.Contains(out, []byte("BYOK_GATEWAY_OK")) {
			t.Fatalf("configured endpoint: status %d, body %s, error %v", resp.StatusCode, out, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("third-party calls: %d", calls.Load())
	}
	recent := server.Recent()
	if len(recent) != 1 || recent[0].Agent != "cursor-byok" || recent[0].Provider != "relay" || recent[0].Status != http.StatusOK {
		t.Fatalf("gateway attribution: %+v", recent)
	}
}

func TestCursorBYOKRedirectNeverReceivesKey(t *testing.T) {
	home := syncHome(t)
	var leaked syncatomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Add(1)
		fmt.Fprint(w, `[]`)
	}))
	t.Cleanup(target.Close)
	f := newCursorBYOKFixture(t, home)
	f.server.Close()
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(f.server.Close)
	f.port(home)
	a, err := Find("cursor-byok")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(); err == nil {
		t.Fatal("redirected service was accepted")
	}
	if leaked.Load() != 0 {
		t.Fatal("a redirect escaped the discovered local service")
	}
}

func TestCursorBYOKUnavailableOrInvalidDatabase(t *testing.T) {
	for _, contents := range []string{"missing", "not a database", "without ports"} {
		t.Run(contents, func(t *testing.T) {
			home := syncHome(t)
			dir := filepath.Join(home, ".cursor-byok-v3")
			path := filepath.Join(dir, "cursor-byok.db")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			switch contents {
			case "not a database":
				if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			case "without ports":
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE unrelated (value TEXT)`); err != nil {
					t.Fatal(err)
				}
				db.Close()
			}
			before, _ := os.ReadFile(path)
			names := dirNames(t, dir)
			a, err := Find("cursor-byok")
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Connect(); err == nil || a.Wired() {
				t.Fatalf("connect accepted an unavailable database: %v, wired %v", err, a.Wired())
			}
			after, _ := os.ReadFile(path)
			if !slices.Equal(before, after) || !reflect.DeepEqual(names, dirNames(t, dir)) {
				t.Fatal("port discovery created or changed Cursor BYOK's database or sidecars")
			}
		})
	}
}
