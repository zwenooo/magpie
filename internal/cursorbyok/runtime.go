package cursorbyok

// The Cursor bridge is built from the pinned native/cursor-backend sources
// and embedded in release binaries. No separately installed application,
// PATH lookup, download, or privileged service is involved.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/filelock"
	"github.com/yetone/magpie/internal/proc"
)

//go:embed payload/*
var runtimePayload embed.FS

// ErrRuntimeUnavailable identifies source-only or unsupported builds. A
// release must build and embed its own bridge before compiling Magpie.
var ErrRuntimeUnavailable = errors.New("this Magpie build does not include the Cursor backend; install a fork release built with the bundled backend")

// Endpoint describes only a child this Runtime owns. Token authorizes its
// local management API; Database belongs to Magpie, not an external app.
type Endpoint struct {
	URL, Token, Database string
}

// Runtime owns one bridge process and its private state. Constructing it,
// Available, Current and Running never write files or start a process.
// Start is reserved for an explicit connection or restoring a saved one.
type Runtime struct {
	root  string
	opMu  sync.Mutex
	mu    sync.Mutex
	child *runtimeChild

	// Tests supply their own test binary, never the user's installed apps.
	executable   string
	command      func(string, ...string) *exec.Cmd
	startTimeout time.Duration
	stopTimeout  time.Duration
}

type runtimeChild struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	done     chan struct{}
	err      error // published by closing done
	endpoint Endpoint
}

// NewRuntime keeps bridge data below root. The caller normally supplies
// filepath.Join(appdir.Config(), "cursor-backend").
func NewRuntime(root string) *Runtime {
	return &Runtime{root: root, startTimeout: 20 * time.Second, stopTimeout: 15 * time.Second}
}

func runtimeName() string {
	name := "magpie-cursor-backend"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func runtimeSupported() bool {
	return (runtime.GOOS == "windows" || runtime.GOOS == "darwin" || runtime.GOOS == "linux") &&
		(runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64")
}

// Available reports whether this binary carries the backend for this OS.
func (r *Runtime) Available() bool {
	if r.executable != "" {
		return true
	}
	if !runtimeSupported() {
		return false
	}
	info, err := runtimePayload.Open("payload/" + runtimeName())
	if err != nil {
		return false
	}
	defer info.Close()
	st, err := info.Stat()
	return err == nil && st.Mode().IsRegular() && st.Size() > 0
}

// Current returns a live, ready child without attempting to restart it.
func (r *Runtime) Current() (Endpoint, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.child != nil {
		select {
		case <-r.child.done:
		default:
			return r.child.endpoint, true
		}
	}
	return Endpoint{}, false
}

func (r *Runtime) Running() bool {
	_, running := r.Current()
	return running
}

// Start reuses a healthy child or starts the embedded bridge. A crash is
// reaped and a subsequent Start creates a fresh process and control token.
// The operation lock is separate from Current's lock so UI status reads
// do not wait for process startup or shutdown.
func (r *Runtime) Start() (Endpoint, error) {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	if endpoint, ok := r.Current(); ok {
		return endpoint, nil
	}
	if !r.Available() {
		return Endpoint{}, ErrRuntimeUnavailable
	}
	if !filepath.IsAbs(r.root) {
		return Endpoint{}, errors.New("Cursor backend data directory must be absolute")
	}
	if err := privateRuntimeDir(r.root); err != nil {
		return Endpoint{}, err
	}
	// A second Magpie must not run another writer against this database or
	// claim the first process's Cursor configuration. Fail closed on lock
	// errors too; filelock.Mutex's best-effort behavior is unsuitable here.
	unlock, held := filelock.Lock(filepath.Join(r.root, "runtime.lock"), 0)
	if !held {
		return Endpoint{}, errors.New("another Magpie is managing the Cursor backend, or its runtime lock cannot be acquired")
	}
	owned := false
	defer func() {
		if !owned {
			unlock()
		}
	}()
	path, err := r.extract()
	if err != nil {
		return Endpoint{}, err
	}
	dataDir := filepath.Join(r.root, "data")
	if err := privateRuntimeDir(dataDir); err != nil {
		return Endpoint{}, err
	}
	runDir, err := os.MkdirTemp(filepath.Join(r.root, "runtime"), "run-")
	if err != nil {
		return Endpoint{}, err
	}
	defer func() {
		if !owned {
			os.RemoveAll(runDir)
		}
	}()
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return Endpoint{}, err
	}
	token := hex.EncodeToString(secret[:])
	tokenFile := filepath.Join(runDir, "control-token")
	readyFile := filepath.Join(runDir, "ready.json")
	if err := os.WriteFile(tokenFile, []byte(token), 0o600); err != nil {
		return Endpoint{}, err
	}
	command := r.command
	if command == nil {
		command = proc.Command
	}
	cmd := command(path, "--data-dir", dataDir, "--control-token-file", tokenFile, "--ready-file", readyFile)
	cmd.Dir = dataDir
	cmd.WaitDelay = 2 * time.Second
	// Never forward backend request logs to a caller or append credentials
	// to the command line. The backend's managed state retains diagnostics.
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Endpoint{}, err
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		return Endpoint{}, fmt.Errorf("start bundled Cursor backend: %w", err)
	}
	child := &runtimeChild{cmd: cmd, stdin: stdin, done: make(chan struct{})}
	owned = true
	go func() {
		child.err = cmd.Wait()
		stdin.Close()
		os.RemoveAll(runDir)
		unlock()
		close(child.done)
	}()
	endpoint, err := r.awaitReady(child, readyFile, token, dataDir)
	if err != nil {
		_ = r.stopChild(child)
		return Endpoint{}, err
	}
	child.endpoint = endpoint
	r.mu.Lock()
	r.child = child
	r.mu.Unlock()
	return endpoint, nil
}

func (r *Runtime) awaitReady(child *runtimeChild, readyFile, token, dataDir string) (Endpoint, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.startTimeout)
	defer cancel()
	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	probe := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer probe.CloseIdleConnections()
	for {
		select {
		case <-child.done:
			return Endpoint{}, fmt.Errorf("bundled Cursor backend exited before becoming ready: %v", child.err)
		case <-ctx.Done():
			return Endpoint{}, errors.New("bundled Cursor backend did not become ready within the startup deadline")
		case <-ticker.C:
			b, err := os.ReadFile(readyFile)
			if err != nil {
				continue
			}
			var ready struct{ Port, PID int }
			if json.Unmarshal(b, &ready) != nil || ready.Port < 1 || ready.Port > 65535 || ready.PID != child.cmd.Process.Pid {
				continue
			}
			url := "http://127.0.0.1:" + strconv.Itoa(ready.Port)
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url+"/__byok-api__/healthz", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := probe.Do(req)
			if err != nil {
				continue
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
				return Endpoint{URL: url, Token: token, Database: filepath.Join(dataDir, "cursor-byok.db")}, nil
			}
		}
	}
}

// Stop closes the parent lifetime pipe. The backend restores only Cursor
// settings it still owns, then drains in-flight requests. A wedged child
// is killed after the deadline and reaped before Stop returns.
func (r *Runtime) Stop() error {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	r.mu.Lock()
	child := r.child
	r.child = nil
	r.mu.Unlock()
	if child == nil {
		return nil
	}
	return r.stopChild(child)
}

func (r *Runtime) stopChild(child *runtimeChild) error {
	child.stdin.Close()
	timer := time.NewTimer(r.stopTimeout)
	defer timer.Stop()
	select {
	case <-child.done:
		if child.err != nil {
			return fmt.Errorf("Cursor backend stopped with an error: %w", child.err)
		}
		return nil
	case <-timer.C:
		_ = child.cmd.Process.Kill()
		<-child.done
		return errors.New("Cursor backend did not stop gracefully; it was terminated, and its saved Cursor settings will be recovered on the next connection")
	}
}

func privateRuntimeDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Cursor backend directory is not a private directory: %s", path)
	}
	return privateRuntimePermissions(path)
}

func (r *Runtime) extract() (string, error) {
	dir := filepath.Join(r.root, "runtime")
	if err := privateRuntimeDir(dir); err != nil {
		return "", err
	}
	if r.executable != "" {
		return r.executable, nil
	}
	b, err := runtimePayload.ReadFile("payload/" + runtimeName())
	if err != nil || len(b) == 0 {
		return "", ErrRuntimeUnavailable
	}
	return extractRuntimePayload(dir, runtimeName(), b)
}

// Each version has its own digest path: an update never overwrites a
// running executable (which is also forbidden on Windows).
func extractRuntimePayload(dir, name string, b []byte) (string, error) {
	digest := sha256.Sum256(b)
	dir = filepath.Join(dir, hex.EncodeToString(digest[:]))
	if err := privateRuntimeDir(dir); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if st, err := os.Lstat(path); err == nil {
		if !st.Mode().IsRegular() {
			return "", errors.New("bundled Cursor backend executable is not a regular file")
		}
		old, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if sha256.Sum256(old) == digest {
			return path, os.Chmod(path, 0o700)
		}
		if err := os.Remove(path); err != nil {
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".extract-")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o700); err != nil {
		f.Close()
		return "", err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
