package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yetone/magpie/internal/cursorbyok"
	"github.com/yetone/magpie/internal/provider"
)

type fixtureCursorRuntime struct{ f *cursorBYOKFixture }

func (r *fixtureCursorRuntime) Start() (cursorbyok.Endpoint, error) {
	return cursorbyok.Endpoint{URL: r.f.server.URL, Token: "test-backend-token", Database: filepath.Join(cursorBYOKRoot(), "data", "cursor-byok.db")}, nil
}
func (r *fixtureCursorRuntime) Current() (cursorbyok.Endpoint, bool) {
	e, _ := r.Start()
	return e, true
}
func (*fixtureCursorRuntime) Stop() error     { return nil }
func (*fixtureCursorRuntime) Available() bool { return true }

func TestCursorBYOKNeedsConsentBeforeStarting(t *testing.T) {
	home := syncHome(t)
	f := newCursorBYOKFixture(t, home)
	if err := os.Remove(filepath.Join(cursorBYOKRoot(), "approved")); err != nil {
		t.Fatal(err)
	}
	a, _ := Find(CursorBYOKID)
	var required *CursorSetupRequiredError
	if err := a.Connect(); !errors.As(err, &required) {
		t.Fatalf("connect without consent: %v", err)
	}
	if len(f.snapshot()) != 0 || a.Wired() {
		t.Fatal("setup modified models before approval")
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if len(f.snapshot()) != 0 {
		t.Fatal("background sync initialized Cursor")
	}
}

func TestCursorBYOKIsDetectedFromCursor(t *testing.T) {
	home := syncHome(t)
	a := cursorBYOK(home)
	if a.Detected() {
		t.Fatal("Cursor detected in empty home")
	}
	if err := os.MkdirAll(filepath.Join(home, ".cursor-byok-v3"), 0700); err != nil {
		t.Fatal(err)
	}
	if a.Detected() {
		t.Fatal("external backend is not the Cursor editor")
	}
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0700); err != nil {
		t.Fatal(err)
	}
	if !a.Detected() {
		t.Fatal("Cursor editor was not detected")
	}
}

func TestCursorBYOKModelSelectionBeforeConnectStartsNothing(t *testing.T) {
	syncHome(t)
	old := cursorBYOKRuntimeFor
	cursorBYOKRuntimeFor = func() cursorBackend { t.Fatal("model selection started the Cursor backend"); return nil }
	t.Cleanup(func() { cursorBYOKRuntimeFor = old })
	if err := provider.SetHiddenModels(CursorBYOKID, []string{"relay/glm-4.6"}); err != nil {
		t.Fatal(err)
	}
	a, err := Find(CursorBYOKID)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if a.Wired() {
		t.Fatal("selecting models connected Cursor")
	}
	if _, err := os.Stat(cursorBYOKRoot()); !os.IsNotExist(err) {
		t.Fatalf("model selection wrote backend state: %v", err)
	}
}
