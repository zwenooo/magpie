package gateway

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

// Cmd.Start sets Process before StartTree returns and the run publishes its
// tree. A panel read during that interval must not inspect the command.
func TestClaudePIDWaitsForPublishedLaunch(t *testing.T) {
	r := &subscriptionRun{cmd: &exec.Cmd{Process: &os.Process{Pid: 12345}}}
	if got := r.pid(); got != 0 {
		t.Fatalf("partially launched process exposed pid %d before launch completed", got)
	}
}

func TestClaudePIDLaunchHelper(t *testing.T) {
	if os.Getenv("MAGPIE_CLAUDE_PID_TEST_HELPER") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestClaudePIDConcurrentLaunch(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := proc.Command(executable, "-test.run=^TestClaudePIDLaunchHelper$")
	cmd.Env = append(os.Environ(), "MAGPIE_CLAUDE_PID_TEST_HELPER=1")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	r := &subscriptionRun{cmd: cmd, model: "fixture", started: time.Now()}
	b := newSubscriptionBridge()
	b.runs["fixture"] = r
	stop, ready := make(chan struct{}), make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		close(ready)
		for {
			select {
			case <-stop:
				return
			default:
				b.claudeRunList()
				runtime.Gosched()
			}
		}
	}()
	defer func() { close(stop); readers.Wait() }()
	<-ready
	if err := r.launch(); err != nil {
		input.Close()
		t.Fatal(err)
	}
	defer func() {
		input.Close()
		r.abort()
		r.mu.Lock()
		exited := r.exited
		r.mu.Unlock()
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			t.Error("test process was not reaped")
		}
	}()
	if got := r.pid(); got <= 0 || got != cmd.Process.Pid {
		t.Fatalf("successful launch published pid %d, want %d", got, cmd.Process.Pid)
	}
	failed := &subscriptionRun{cmd: proc.Command(filepath.Join(t.TempDir(), "no-such-program"))}
	if err := failed.launch(); err == nil || failed.pid() != 0 {
		t.Fatal("failed launch published a process id")
	}
}
