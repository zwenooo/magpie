package cursorbyok

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/proc"
	"github.com/yetone/magpie/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func TestCursorRuntimeStatusDoesNotCreateFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "untouched")
	r := NewRuntime(root)
	_ = r.Available()
	if r.Running() {
		t.Fatal("new runtime is running")
	}
	if _, ok := r.Current(); ok {
		t.Fatal("new runtime has an endpoint")
	}
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only runtime inspection created its folder: %v", err)
	}
	if !r.Available() {
		if _, err := r.Start(); !errors.Is(err, ErrRuntimeUnavailable) {
			t.Fatalf("source build error = %v", err)
		}
		if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("missing backend created runtime state")
		}
	}
}

func fakeRuntime(t *testing.T, root, mode string) *Runtime {
	t.Helper()
	r := NewRuntime(root)
	r.executable = os.Args[0]
	r.command = func(path string, args ...string) *exec.Cmd {
		return proc.Command(path, append([]string{"-test.run=^TestCursorRuntimeProcess$", "--", mode}, args...)...)
	}
	t.Cleanup(func() {
		if err := r.Stop(); err != nil {
			t.Errorf("stop runtime fixture: %v", err)
		}
	})
	return r
}

func TestCursorRuntimeConcurrentStartAndGracefulStop(t *testing.T) {
	root := t.TempDir()
	r := fakeRuntime(t, root, "serve")
	const n = 8
	endpoints := make([]Endpoint, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			endpoints[i], errs[i] = r.Start()
		}()
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("start %d: %v", i, errs[i])
		}
		if endpoints[i] != endpoints[0] {
			t.Fatal("concurrent connections started different backends")
		}
	}
	endpoint := endpoints[0]
	if endpoint.Database != filepath.Join(root, "data", "cursor-byok.db") || len(endpoint.Token) != 64 {
		t.Fatalf("unexpected endpoint paths or control token: %+v", endpoint)
	}
	if current, ok := r.Current(); !ok || current != endpoint {
		t.Fatal("ready backend missing from Current")
	}
	args, err := os.ReadFile(filepath.Join(root, "data", "arguments"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), endpoint.Token) {
		t.Fatal("control token leaked into child command line")
	}
	other := fakeRuntime(t, root, "serve")
	if _, err := other.Start(); err == nil || !strings.Contains(err.Error(), "runtime lock") {
		t.Fatalf("second owner should fail closed: %v", err)
	}
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	if r.Running() {
		t.Fatal("stopped backend remains running")
	}
	if _, err := os.Stat(filepath.Join(root, "data", "stopped")); err != nil {
		t.Fatalf("backend did not receive EOF and restore before Stop returned: %v", err)
	}
	runs, err := filepath.Glob(filepath.Join(root, "runtime", "run-*"))
	if err != nil || len(runs) != 0 {
		t.Fatalf("temporary control credentials remain: %v, %v", runs, err)
	}
	// Stopping releases ownership even if the Runtime object stays alive.
	if _, err := other.Start(); err != nil {
		t.Fatalf("new owner could not start after shutdown: %v", err)
	}
}

func TestCursorRuntimeRestartsAfterCrash(t *testing.T) {
	r := fakeRuntime(t, t.TempDir(), "serve")
	first, err := r.Start()
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	child := r.child
	r.mu.Unlock()
	if err := child.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-child.done
	if _, ok := r.Current(); ok {
		t.Fatal("crashed backend reported live")
	}
	second, err := r.Start()
	if err != nil {
		t.Fatal(err)
	}
	if second.Token == first.Token {
		t.Fatal("restart reused the exited process's control credential")
	}
	if second.Database != first.Database {
		t.Fatal("restart lost its persistent model database")
	}
}

func TestCursorRuntimeRejectsFalseReadinessAndReapsChild(t *testing.T) {
	for _, mode := range []string{"wrong-pid", "wrong-token", "exit"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			r := fakeRuntime(t, root, mode)
			// The timeout is the behavior under test; no elapsed-time bound is
			// asserted and a loaded runner can start the child at any time.
			r.startTimeout = 300 * time.Millisecond
			if _, err := r.Start(); err == nil {
				t.Fatal("untrusted or dead readiness was accepted")
			}
			if r.Running() {
				t.Fatal("failed startup left a live runtime")
			}
			runs, err := filepath.Glob(filepath.Join(root, "runtime", "run-*"))
			if err != nil || len(runs) != 0 {
				t.Fatalf("failed startup was not reaped: %v %v", runs, err)
			}
			// A retry can acquire the same lifetime lock after cleanup.
			retry := fakeRuntime(t, root, "serve")
			if _, err := retry.Start(); err != nil {
				t.Fatalf("retry after failed readiness: %v", err)
			}
		})
	}
}

func TestCursorRuntimeDigestExtraction(t *testing.T) {
	dir := t.TempDir()
	first, err := extractRuntimePayload(dir, "backend", []byte("version one"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := extractRuntimePayload(dir, "backend", []byte("version two"))
	if err != nil || second == first {
		t.Fatalf("update did not get a distinct executable path: %v", err)
	}
	if b, err := os.ReadFile(first); err != nil || string(b) != "version one" {
		t.Fatal("extracting an update overwrote the earlier executable")
	}
	if err := os.WriteFile(first, []byte("corrupt"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, err := extractRuntimePayload(dir, "backend", []byte("version one")); err != nil || got != first {
		t.Fatalf("damaged cache was not restored: %v", err)
	}
	if b, _ := os.ReadFile(first); string(b) != "version one" {
		t.Fatal("damaged executable cache survived the digest check")
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(first)
		if st.Mode().Perm() != 0o700 {
			t.Fatalf("executable is not private: %v", st.Mode())
		}
		if err := os.Remove(first); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(second, first); err != nil {
			t.Fatal(err)
		}
		if _, err := extractRuntimePayload(dir, "backend", []byte("version one")); err == nil {
			t.Fatal("extraction accepted a substituted symlink")
		}
	}
}

// Release CI enables this after compiling the actual native payload. It
// checks the binary embedded in Go, not a separately installed desktop
// app. TestMain points every home/config variable into the test sandbox.
func TestCursorBYOKBundledRuntime(t *testing.T) {
	if os.Getenv("MAGPIE_TEST_BUNDLED") != "1" {
		t.Skip("set MAGPIE_TEST_BUNDLED=1 after building the embedded native backend")
	}
	r := NewRuntime(filepath.Join(t.TempDir(), "backend"))
	if !r.Available() {
		t.Fatal("release validation requires the bundled Cursor backend")
	}
	t.Cleanup(func() {
		if err := r.Stop(); err != nil {
			t.Error(err)
		}
	})
	endpoint, err := r.Start()
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer httpClient.CloseIdleConnections()
	for _, route := range []string{"/__byok-api__/healthz", modelsPath} {
		for _, authorized := range []bool{false, true} {
			req, _ := http.NewRequest(http.MethodGet, endpoint.URL+route, nil)
			if authorized {
				req.Header.Set("Authorization", "Bearer "+endpoint.Token)
			}
			resp, err := httpClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			want := http.StatusUnauthorized
			if authorized {
				want = http.StatusOK
				if route == "/__byok-api__/healthz" {
					want = http.StatusNoContent
				}
			}
			if resp.StatusCode != want {
				t.Fatalf("%s (authorized=%v): HTTP %d, want %d", route, authorized, resp.StatusCode, want)
			}
		}
	}
	for _, origin := range []string{"null", "http://127.0.0.1:3000", "vscode-file://vscode-app"} {
		req, _ := http.NewRequest(http.MethodGet, endpoint.URL+modelsPath, nil)
		req.Header.Set("Authorization", "Bearer "+endpoint.Token)
		req.Header.Set("Origin", origin)
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("browser origin %q reached the control API: HTTP %d", origin, resp.StatusCode)
		}
	}
	if _, err := os.Stat(endpoint.Database); err != nil {
		t.Fatalf("native backend did not create its private database: %v", err)
	}
	manager := Manager{StatePath: filepath.Join(r.root, "models.json"), Control: &endpoint}
	status, err := manager.Harness()
	if err != nil {
		t.Fatal(err)
	}
	if status.CA == "ready" || status.Integration == "enabled" || status.SettingsApplied {
		t.Fatalf("fresh backend performed local setup without approval: %+v", status)
	}
	gw := Gateway{URL: "http://127.0.0.1:19342", Key: "sandbox-gateway-key"}
	models := []Model{
		{ID: "test/chat", Name: "Chat Fixture", APIs: []string{"chat"}, Context: 32000, MaxTokens: 4096},
		{ID: "test/responses", Name: "Responses Fixture", APIs: []string{"responses"}, Context: 64000, MaxTokens: 8192},
		{ID: "test/anthropic", Name: "Anthropic Fixture", APIs: []string{"anthropic"}, Context: 100000, MaxTokens: 4096},
	}
	if err := manager.Write(gw, models, true, true); err != nil {
		t.Fatalf("Go model journal failed against native backend: %v", err)
	}
	if detail := manager.Check(gw); detail != "" {
		t.Fatalf("native backend did not retain Go model configuration: %s", detail)
	}
	rows, err := manager.list(endpoint.URL)
	if err != nil || len(rows) != len(models) {
		t.Fatalf("native model count = %d, want %d: %v", len(rows), len(models), err)
	}
	// Moving the gateway changes model identity in Rust. Verify update and
	// recovery use the same hash and normalized schema in both languages.
	gw.URL = "http://127.0.0.1:19343"
	models[0].Context = 48000
	if err := manager.Write(gw, models, true, false); err != nil {
		t.Fatalf("native model update: %v", err)
	}
	if detail := manager.Check(gw); detail != "" {
		t.Fatalf("native updated model drift: %s", detail)
	}
	if _, err := manager.Enable(false); err != nil {
		t.Fatalf("native no-op disable: %v", err)
	}
	if err := manager.Write(gw, nil, false, true); err != nil {
		t.Fatalf("native model removal: %v", err)
	}
	if manager.Connected() {
		t.Fatal("model disconnect left an active journal")
	}
	if rows, err := manager.list(endpoint.URL); err != nil || len(rows) != 0 {
		t.Fatalf("native model disconnect retained %d rows: %v", len(rows), err)
	}
	if after, err := manager.Harness(); err != nil || after.CA != status.CA || after.Integration == "enabled" || after.SettingsApplied {
		t.Fatalf("model management changed local setup: before=%+v after=%+v error=%v", status, after, err)
	}
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	if r.Running() {
		t.Fatal("native backend still running after shutdown")
	}
	if resp, err := httpClient.Get(endpoint.URL + "/__byok-api__/healthz"); err == nil {
		resp.Body.Close()
		t.Fatal("native backend listener remains open after shutdown")
	}
}

// This process fixture implements only lifetime/readiness. It never opens
// Cursor's files, imports the native backend, or reaches an external host.
func TestCursorRuntimeProcess(t *testing.T) {
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		return
	}
	args := os.Args[separator+1:]
	mode := args[0]
	if mode == "exit" {
		os.Exit(2)
	}
	flags := map[string]string{}
	for i := 1; i+1 < len(args); i += 2 {
		flags[args[i]] = args[i+1]
	}
	data := flags["--data-dir"]
	token, err := os.ReadFile(flags["--control-token-file"])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "arguments"), []byte(strings.Join(os.Args, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__byok-api__/healthz" || r.Header.Get("Authorization") != "Bearer "+string(token) || mode == "wrong-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})}
	done := make(chan struct{})
	go func() {
		_ = server.Serve(l)
		close(done)
	}()
	defer func() { server.Close(); <-done }()
	pid := os.Getpid()
	if mode == "wrong-pid" {
		pid++
	}
	_, portText, _ := net.SplitHostPort(l.Addr().String())
	port, _ := strconv.Atoi(portText)
	ready, _ := json.Marshal(map[string]int{"port": port, "pid": pid})
	if err := os.WriteFile(flags["--ready-file"], ready, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	if err := os.WriteFile(filepath.Join(data, "stopped"), []byte("restored"), 0o600); err != nil {
		t.Fatal(err)
	}
}
