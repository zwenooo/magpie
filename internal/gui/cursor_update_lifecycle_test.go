package gui

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/update"
)

func TestCursorBackendForcesRestartWithoutHandover(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("this platform never hands over the gateway")
	}
	oldRunning, oldExe, oldHost, oldGateway := cursorBackendRunning, startExe, handoverHost.Load(), served.Load()
	t.Cleanup(func() {
		cursorBackendRunning, startExe = oldRunning, oldExe
		handoverHost.Store(oldHost)
		served.Store(oldGateway)
	})
	t.Setenv("MAGPIE_NO_HANDOVER", "")
	startExe = "/fixture/magpie"
	handoverHost.Store(true)
	served.Store(gateway.New())
	cursorBackendRunning = func() bool { return false }
	if !handsOver() {
		t.Fatal("fixture does not allow normal gateway handover")
	}
	cursorBackendRunning = func() bool { return true }
	if handsOver() {
		t.Fatal("started a second app while the Cursor backend lock is owned")
	}
	oldBusy := gatewayBusy
	gatewayBusy = func() gateway.Busy { return gateway.Busy{Requests: 1} }
	t.Cleanup(func() { gatewayBusy = oldBusy })
	if !restartBusy().Any() {
		t.Fatal("Cursor restart ignored an active gateway request")
	}
}

func TestCursorBackendStopsBeforeReplacementLaunch(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "started", true: "refused"}[fail], func(t *testing.T) {
			oldUpdates, oldLaunch, oldStop, oldResume, oldHands := updates, relaunchBinary, stopCursorBackend, resumeCursorBackend, handsOver
			t.Cleanup(func() {
				updates, relaunchBinary, stopCursorBackend, resumeCursorBackend, handsOver = oldUpdates, oldLaunch, oldStop, oldResume, oldHands
			})
			exe := filepath.Join(t.TempDir(), "magpie")
			if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
				t.Fatal(err)
			}
			self, _ := os.Stat(exe)
			// Already replaced by another instance: exercise restart ordering on
			// Windows too without executing a fixture as a native program.
			if err := os.WriteFile(exe, []byte("already replaced"), 0o755); err != nil {
				t.Fatal(err)
			}
			updates = &updater{exe: exe, self: self, state: "ready", latest: &update.Release{Version: "0.1.1725-vb.0.0.6"}}
			stopped, resumed := false, false
			handsOver = func() bool { return false }
			stopCursorBackend = func() bool { stopped = true; return true }
			resumeCursorBackend = func() error { resumed = true; return nil }
			relaunchBinary = func(string, string, bool, string) error {
				if !stopped {
					t.Fatal("successor started before Cursor cleanup completed")
				}
				if fail {
					return errors.New("fixture launch refused")
				}
				return nil
			}
			if got := restartToUpdate(false, false, ""); got == fail {
				t.Fatalf("restart returned %v on failure=%v", got, fail)
			}
			if resumed != fail {
				t.Fatalf("backend recovery=%v on failure=%v", resumed, fail)
			}
		})
	}
}
