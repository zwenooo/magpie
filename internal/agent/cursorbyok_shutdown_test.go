package agent

import (
	"os"
	syncatomic "sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/cursorbyok"
)

type shutdownCursorRuntime struct {
	running syncatomic.Bool
	stops   syncatomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (r *shutdownCursorRuntime) Start() (cursorbyok.Endpoint, error) {
	r.running.Store(true)
	return cursorbyok.Endpoint{}, nil
}
func (r *shutdownCursorRuntime) Current() (cursorbyok.Endpoint, bool) {
	return cursorbyok.Endpoint{}, r.running.Load()
}
func (r *shutdownCursorRuntime) Available() bool { return true }
func (r *shutdownCursorRuntime) Stop() error {
	r.stops.Add(1)
	if r.entered != nil {
		close(r.entered)
		<-r.release
	}
	r.running.Store(false)
	return nil
}

func isolateCursorLifecycle(t *testing.T) {
	t.Helper()
	cursorSetupMu.Lock()
	oldStopping := cursorBackendStopping
	cursorBackendStopping = false
	cursorSetupMu.Unlock()
	cursorBackends.Lock()
	old := cursorBackends.byRoot
	cursorBackends.byRoot = map[string]cursorBackend{}
	cursorBackends.Unlock()
	t.Cleanup(func() {
		cursorSetupMu.Lock()
		cursorBackendStopping = oldStopping
		cursorSetupMu.Unlock()
		cursorBackends.Lock()
		cursorBackends.byRoot = old
		cursorBackends.Unlock()
	})
}

func TestCursorBYOKRunningNeverStartsOrReadsAnotherInstallation(t *testing.T) {
	syncHome(t)
	isolateCursorLifecycle(t)
	if CursorBackendRunning() {
		t.Fatal("an empty process owns no Cursor backend")
	}
	if _, err := os.Stat(cursorBYOKRoot()); !os.IsNotExist(err) {
		t.Fatalf("status created backend state: %v", err)
	}
	r := &shutdownCursorRuntime{}
	cursorBackends.byRoot[cursorBYOKRoot()] = r
	if CursorBackendRunning() {
		t.Fatal("stopped backend reported running")
	}
	r.running.Store(true)
	if !CursorBackendRunning() {
		t.Fatal("live owned backend was not found")
	}
}

func TestCursorBYOKShutdownWaitsAndBlocksBackgroundReconnect(t *testing.T) {
	syncHome(t)
	isolateCursorLifecycle(t)
	r := &shutdownCursorRuntime{entered: make(chan struct{}), release: make(chan struct{})}
	r.running.Store(true)
	cursorBackends.byRoot[cursorBYOKRoot()] = r
	done := make(chan struct{})
	go func() { StopCursorBackend(); close(done) }()
	<-r.entered
	select {
	case <-done:
		t.Fatal("shutdown returned before the backend restored its settings")
	default:
	}
	close(r.release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	if CursorBackendRunning() || r.stops.Load() != 1 {
		t.Fatal("shutdown left its backend running")
	}
	old := cursorBYOKRuntimeFor
	cursorBYOKRuntimeFor = func() cursorBackend { t.Fatal("shutdown restarted the backend"); return nil }
	t.Cleanup(func() { cursorBYOKRuntimeFor = old })
	if err := cursorBYOKWrite(true, false); err == nil {
		t.Fatal("background sync reconnected while the app was exiting")
	}
}

func TestCursorBYOKQuitAfterBackendShutdown(t *testing.T) {
	home := syncHome(t)
	isolateCursorLifecycle(t)
	newCursorBYOKFixture(t, home)
	cursor, err := Find(CursorBYOKID)
	if err != nil {
		t.Fatal(err)
	}
	if err := cursor.Connect(); err != nil {
		t.Fatal(err)
	}
	StopCursorBackend()
	all := []*Agent{cursor}
	restored, err := RestoreOnQuit(all)
	if err != nil || len(restored) != 0 || cursor.Disconnected() || !cursorBYOKConnected() {
		t.Fatalf("quit after shutdown: restored=%v disconnected=%v saved=%v err=%v", restored, cursor.Disconnected(), cursorBYOKConnected(), err)
	}
	if err := cursor.Sync(); err == nil {
		t.Fatal("the exiting process accepted a catalog write")
	}
}

func TestCursorBYOKQuitWithLiveBackend(t *testing.T) {
	home := syncHome(t)
	isolateCursorLifecycle(t)
	newCursorBYOKFixture(t, home)
	cursor, err := Find(CursorBYOKID)
	if err != nil {
		t.Fatal(err)
	}
	if err := cursor.Connect(); err != nil {
		t.Fatal(err)
	}
	all := []*Agent{cursor}
	restored, err := RestoreOnQuit(all)
	if err != nil || len(restored) != 1 || restored[0] != CursorBYOKID || cursor.Disconnected() || cursorBYOKConnected() {
		t.Fatalf("quit restore: restored=%v disconnected=%v saved=%v err=%v", restored, cursor.Disconnected(), cursorBYOKConnected(), err)
	}
	rewired, err := RewireOnStart(all)
	if err != nil || len(rewired) != 1 || !cursorBYOKConnected() {
		t.Fatalf("start: rewired=%v saved=%v err=%v", rewired, cursorBYOKConnected(), err)
	}
	if err := cursor.Disconnect(); err != nil {
		t.Fatal(err)
	}
	restored, err = RestoreOnQuit(all)
	if err != nil || len(restored) != 0 || !cursor.Disconnected() || cursorBYOKConnected() {
		t.Fatalf("manual disconnect: restored=%v disconnected=%v saved=%v err=%v", restored, cursor.Disconnected(), cursorBYOKConnected(), err)
	}
}

func TestCursorBYOKFailedRestartCanRestoreItsSavedConnection(t *testing.T) {
	home := syncHome(t)
	isolateCursorLifecycle(t)
	f := newCursorBYOKFixture(t, home)
	a, err := Find(CursorBYOKID)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	if len(f.snapshot()) == 0 {
		t.Fatal("fixture has no managed models")
	}
	StopCursorBackend()
	oldRuntime := cursorBYOKRuntimeFor
	starts := 0
	cursorBYOKRuntimeFor = func() cursorBackend { starts++; return oldRuntime() }
	t.Cleanup(func() { cursorBYOKRuntimeFor = oldRuntime })
	if err := ResumeCursorBackend(); err != nil {
		t.Fatal(err)
	}
	if starts != 1 || !a.Wired() {
		t.Fatalf("saved connection was not resumed: starts=%d wired=%v", starts, a.Wired())
	}
}
