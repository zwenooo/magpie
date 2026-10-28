package agent

// The agent adapter owns selection and consent; the bundled backend owns
// Cursor's protocol and reversible local setup. No external app is required.
import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	syncatomic "sync/atomic"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/cursorbyok"
	"github.com/yetone/magpie/internal/gateway"
)

const CursorBYOKID = "cursor-byok"

var noticeCursorBYOK = newNotice("Magpie runs the bundled Cursor backend while connected. Cursor must be fully closed when setting up or reapplying the connection. After setup, open Cursor and start a new conversation to use Magpie's models. Disconnect restores the Cursor settings managed by Magpie.")

type CursorSetupRequiredError struct{}

func (*CursorSetupRequiredError) Error() string {
	return "Connect Cursor from Magpie's Agents page and approve its local certificate and proxy setup first"
}

func cursorBYOKRoot() string      { return filepath.Join(appdir.Config(), "cursor-backend") }
func cursorBYOKStatePath() string { return filepath.Join(cursorBYOKRoot(), "models.json") }
func cursorBYOKConnected() bool   { return isFile(filepath.Join(cursorBYOKRoot(), "connected")) }
func cursorBYOKApproved() bool {
	b, err := os.ReadFile(filepath.Join(cursorBYOKRoot(), "approved"))
	return err == nil && string(b) == "cursor-local-setup-v1\n"
}

// ApproveCursorBackend records the explicit user choice made in the setup
// dialog. It does not start a backend or change trust/agent settings itself.
func ApproveCursorBackend() error {
	if disconnectDryRun {
		return errors.New("Cursor setup is unavailable during a preview")
	}
	if err := os.MkdirAll(cursorBYOKRoot(), 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cursorBYOKRoot(), "approved"), []byte("cursor-local-setup-v1\n"), 0600)
}

type cursorBackend interface {
	Start() (cursorbyok.Endpoint, error)
	Current() (cursorbyok.Endpoint, bool)
	Stop() error
	Available() bool
}

var cursorBackends = struct {
	sync.Mutex
	byRoot map[string]cursorBackend
}{byRoot: map[string]cursorBackend{}}
var cursorSetupMu sync.Mutex
var cursorBackendStopping bool // under cursorSetupMu; this process must not reconnect while exiting
var cursorSetupActive syncatomic.Bool
var cursorBYOKRuntimeFor = func() cursorBackend {
	root := cursorBYOKRoot()
	cursorBackends.Lock()
	defer cursorBackends.Unlock()
	if cursorBackends.byRoot[root] == nil {
		cursorBackends.byRoot[root] = cursorbyok.NewRuntime(root)
	}
	return cursorBackends.byRoot[root]
}

// CursorBackendRunning observes only runtimes already owned by this process.
// A setup in progress also prevents starting another app beside this one.
func CursorBackendRunning() bool {
	if cursorSetupActive.Load() {
		return true
	}
	cursorBackends.Lock()
	defer cursorBackends.Unlock()
	for _, backend := range cursorBackends.byRoot {
		if _, running := backend.Current(); running {
			return true
		}
	}
	return false
}

// StopCursorBackend waits for setup and the owned child to finish, then keeps
// background catalog sync from restarting it while this process exits.
func StopCursorBackend() bool {
	cursorSetupMu.Lock()
	defer cursorSetupMu.Unlock()
	cursorBackendStopping = true
	cursorBackends.Lock()
	backends := make([]cursorBackend, 0, len(cursorBackends.byRoot))
	for _, backend := range cursorBackends.byRoot {
		backends = append(backends, backend)
	}
	cursorBackends.Unlock()
	wasRunning := false
	for _, backend := range backends {
		_, running := backend.Current()
		wasRunning = wasRunning || running
		if err := backend.Stop(); err != nil {
			log.Printf("Cursor backend shutdown: %v", err)
		}
	}
	return wasRunning
}

// ResumeCursorBackend restores a saved connection if restarting the app failed.
func ResumeCursorBackend() error {
	cursorSetupMu.Lock()
	cursorBackendStopping = false
	cursorSetupMu.Unlock()
	return cursorBYOKWrite(true, false)
}
func cursorBYOKManager() cursorbyok.Manager {
	return cursorbyok.Manager{StatePath: cursorBYOKStatePath()}
}
func cursorBYOKGateway() cursorbyok.Gateway {
	u := gateway.URL()
	return cursorbyok.Gateway{URL: u, Key: agentKeyAt(CursorBYOKID, u)}
}

func cursorBYOK(home string) *Agent {
	dir := filepath.Join(home, ".cursor")
	return &Agent{
		ID: CursorBYOKID, Name: "Cursor (Magpie)", Icon: "cursor", Dir: dir,
		detect:      func() bool { return isDir(dir) || isDir(cursorEditorDir(home)) },
		ListsModels: true,
		Notice:      func() string { return noticeCursorBYOK.String() },
		Check:       cursorBYOKCheck,
		Sync:        func() error { return cursorBYOKWrite(true, false) },
		Fields: []Field{{Key: "provider", Label: "provider",
			Get: func() string {
				if cursorBYOKConnected() || cursorBYOKManager().Connected() {
					return magpieID
				}
				return ""
			},
			Set: func(v string) error { return cursorBYOKWrite(v != "", true) },
			Options: func(map[string]string) []Option {
				return []Option{{Value: magpieID, Label: "magpie", Icon: "magpie", Note: "magpie's models in Cursor"}}
			},
		}},
	}
}
func cursorEditorDir(home string) string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "Cursor")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Cursor")
	default:
		return filepath.Join(home, ".config", "Cursor")
	}
}
func cursorBYOKCheck() string {
	manager := cursorBYOKManager()
	if !cursorBYOKConnected() {
		if manager.Connected() {
			return "Cursor setup is incomplete; apply again to finish it or disconnect to restore its settings"
		}
		return ""
	}
	if disconnectDryRun {
		return "Cursor's live backend has no disconnect preview"
	}
	endpoint, ok := cursorBYOKRuntimeFor().Current()
	if !ok {
		return "Magpie's Cursor backend is not running; apply again to restart it"
	}
	manager.Control = &endpoint
	if detail := manager.Check(cursorBYOKGateway()); detail != "" {
		return detail
	}
	status, err := manager.Harness()
	if err != nil {
		return err.Error()
	}
	if status.Integration != "enabled" {
		return "Cursor's local connection is not enabled; apply again to complete setup"
	}
	return ""
}
func cursorBYOKWrite(on, explicit bool) error {
	if disconnectDryRun {
		return errors.New("Cursor's live backend has no disconnect preview")
	}
	cursorSetupMu.Lock()
	defer cursorSetupMu.Unlock()
	if cursorBackendStopping {
		return errors.New("Magpie is shutting down its Cursor backend")
	}
	cursorSetupActive.Store(true)
	defer cursorSetupActive.Store(false)
	manager := cursorBYOKManager()
	if err := manager.ValidateState(); err != nil {
		return err
	}
	if !explicit && !cursorBYOKConnected() {
		return nil
	}
	if !on && !manager.Connected() && !cursorBYOKConnected() {
		return nil
	}
	if !on {
		// Save disconnect intent before touching the service. A failed
		// teardown must never be re-enabled by a background catalog sync.
		if err := os.Remove(filepath.Join(cursorBYOKRoot(), "connected")); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if on && !cursorBYOKApproved() {
		return &CursorSetupRequiredError{}
	}
	var models []cursorbyok.Model
	if on {
		for _, model := range magpieModels(CursorBYOKID) {
			models = append(models, cursorbyok.Model{ID: model.ID, Name: model.Name, APIs: model.APIs, Context: model.Context, MaxTokens: maxTokens(model)})
		}
		if len(models) == 0 && explicit {
			return (&Agent{ID: CursorBYOKID, Name: "Cursor"}).noModelLeft()
		}
	}
	backend := cursorBYOKRuntimeFor()
	endpoint, err := backend.Start()
	if err != nil {
		return err
	}
	manager.Control = &endpoint
	if !on {
		if _, err = manager.Enable(false); err != nil {
			return err
		}
		if err = manager.Write(cursorBYOKGateway(), nil, false, true); err != nil {
			return err
		}
		return backend.Stop()
	}
	if err = manager.Write(cursorBYOKGateway(), models, true, explicit); err != nil {
		return err
	}
	status, err := manager.Harness()
	if err != nil {
		return err
	}
	if status.CA != "ready" {
		// Never change system trust from a background catalog refresh.
		if !explicit {
			return errors.New("Cursor setup needs approval on the Agents page")
		}
		status, err = manager.InitializeCA()
		if err != nil {
			return err
		}
		if status.CA != "ready" {
			status, err = manager.TrustCA()
			if err != nil {
				if runtime.GOOS == "linux" {
					ready, _ := manager.Harness()
					if ready.InstallCommand != "" {
						return fmt.Errorf("trust Magpie's local Cursor certificate, then connect again: %s", ready.InstallCommand)
					}
				}
				return err
			}
		}
		if status.CA != "ready" {
			return fmt.Errorf("Cursor's local certificate is not trusted; complete system approval and apply again")
		}
	}
	_, err = manager.Enable(true)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cursorBYOKRoot(), "connected"), []byte("1\n"), 0600)
}
