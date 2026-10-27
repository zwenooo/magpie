package proc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The Codex app's app-server is restarted only when it runs on magpie's
// Codex home: its HOME and CODEX_HOME are read from what ps -E prints
// (macOS) or /proc/<pid>/environ (Linux).
func TestParsePSEnv(t *testing.T) {
	line := " /Applications/ChatGPT.app/Contents/Resources/codex app-server --analytics-default-enabled TMPDIR=/var/folders/x/T/ HOME=/Users/me/sand box CODEX_HOME=/Users/me/sand box/.codex LANG=en_US.UTF-8"
	got := parsePSEnv(line, []string{"HOME", "CODEX_HOME", "PATH"})
	want := map[string]string{"HOME": "/Users/me/sand box", "CODEX_HOME": "/Users/me/sand box/.codex"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsePSEnv = %v, want %v", got, want)
	}
	// a name in the command line is not the environment's: the last is
	got = parsePSEnv(" codex app-server -c HOME=/nope HOME=/Users/me", []string{"HOME"})
	if got["HOME"] != "/Users/me" {
		t.Fatalf("HOME = %q", got["HOME"])
	}
	// the last variable runs to the end of the line
	got = parsePSEnv(" codex app-server HOME=/Users/me\n", []string{"HOME"})
	if got["HOME"] != "/Users/me" {
		t.Fatalf("last HOME = %q", got["HOME"])
	}
}

func TestParseEnviron(t *testing.T) {
	b := []byte("PATH=/usr/bin\x00HOME=/home/me\x00CODEX_HOME=/home/me/x=y\x00junk\x00")
	got := parseEnviron(b, []string{"HOME", "CODEX_HOME", "LANG"})
	want := map[string]string{"HOME": "/home/me", "CODEX_HOME": "/home/me/x=y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseEnviron = %v, want %v", got, want)
	}
}

// TestEnvOfHelper is TestEnvOfChild's child: it waits to be ended. macOS
// keeps a system binary's environment (sleep's) from ps, so the child is
// this test binary, as the Codex app's codex is its own.
func TestEnvOfHelper(t *testing.T) {
	if os.Getenv("MAGPIE_ENV_HELPER") != "1" {
		return
	}
	fmt.Fprintln(os.Stdout, "ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

// Starting the test binary is not a readiness signal: its runtime and
// TestMain have not necessarily finished. Keep it alive until the test
// ends, and reap it even when an assertion or the readiness wait fails.
func startEnvHelper(t *testing.T, env []string) (*exec.Cmd, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cmd := CommandContext(ctx, os.Args[0], "-test.run=^TestEnvOfHelper$")
	cmd.Env = append(env, "MAGPIE_ENV_HELPER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	reaped := make(chan struct{})
	go func() {
		done <- cmd.Wait()
		close(reaped)
	}()
	t.Cleanup(func() { cancel(); <-reaped })
	ready := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err == nil && strings.TrimSpace(line) != "ready" {
			err = fmt.Errorf("helper said %q, want ready", line)
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("helper readiness: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("helper did not become ready within 20s")
	}
	return cmd, done
}

// EnvOf reads a running child's own environment, not magpie's.
func TestEnvOfChild(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("EnvOf answers ErrNoEnv here")
	}
	home := filepath.Join(t.TempDir(), "sand box", "h")
	var env []string
	// Preserve the sandbox marker and platform temp folders so TestMain
	// keeps the child's fixture. LANG is deliberately absent.
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "LANG=") {
			env = append(env, kv)
		}
	}
	env = append(env, "HOME="+home, "CODEX_HOME="+filepath.Join(home, ".codex"))
	cmd, _ := startEnvHelper(t, env)
	got, err := EnvOf(context.Background(), cmd.Process.Pid, "HOME", "CODEX_HOME", "LANG")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"HOME": home, "CODEX_HOME": filepath.Join(home, ".codex")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EnvOf = %v, want %v", got, want)
	}
}

// End ends a process as a clean exit, which the Codex app takes for an
// app-server that quit, not one that crashed: code 0 on Windows (any other
// is shown as a fatal error), SIGTERM elsewhere.
func TestEndExitsClean(t *testing.T) {
	cmd, done := startEnvHelper(t, os.Environ())
	if err := End(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	var err error
	select {
	case err = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("still running 20s after End")
	}
	if runtime.GOOS == "windows" {
		if err != nil {
			t.Fatalf("exit after End: %v, want code 0", err)
		}
		return
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) || !strings.Contains(ee.String(), "terminated") {
		t.Fatalf("exit after End: %v, want terminated by SIGTERM", err)
	}
}
