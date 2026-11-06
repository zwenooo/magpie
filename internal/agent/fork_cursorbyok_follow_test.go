package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/cursorbyok"
	"github.com/yetone/magpie/internal/provider"
)

// notHereCursorRuntime is a backend another magpie runs: none here.
type notHereCursorRuntime struct{}

func (notHereCursorRuntime) Start() (cursorbyok.Endpoint, error) {
	return cursorbyok.Endpoint{}, errAnotherMagpie
}
func (notHereCursorRuntime) Current() (cursorbyok.Endpoint, bool) {
	return cursorbyok.Endpoint{}, false
}
func (notHereCursorRuntime) Stop() error     { return nil }
func (notHereCursorRuntime) Available() bool { return true }

var errAnotherMagpie = errors.New("another magpie runs the Cursor backend")

func cursorBYOKHas(f *cursorBYOKFixture, id string) bool {
	for _, m := range f.snapshot() {
		if m["model_id"] == id {
			return true
		}
	}
	return false
}

// A provider saved by another magpie (the CLI) reached every other agent,
// whose lists are files, and never Cursor's, which only the magpie running
// its backend can write: that one follows the catalog.
func TestCursorBYOKFollowsAnotherMagpiesChange(t *testing.T) {
	home := syncHome(t)
	f := newCursorBYOKFixture(t, home)
	if err := provider.Save(provider.Provider{ID: "anth", Name: "Anth", Key: "k", Anthropic: "http://127.0.0.1:1", Models: []string{"claude-sonnet-5"}}); err != nil {
		t.Fatal(err)
	}
	a, err := Find(CursorBYOKID)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}

	// nothing changed: nothing written
	writes := len(f.writes)
	if err := followCursorCatalog(); err != nil || len(f.writes) != writes {
		t.Fatalf("follow with nothing changed: %v, %d writes", err, len(f.writes)-writes)
	}

	// another magpie saves a provider: this one's own sync never ran
	if err := provider.Save(provider.Provider{ID: "resp", Name: "Resp", Key: "k", Responses: "http://127.0.0.1:1/v1", Models: []string{"gpt-5.5"}}); err != nil {
		t.Fatal(err)
	}
	if cursorBYOKHas(f, "resp/gpt-5.5") {
		t.Fatal("the save reached the backend by itself; the test shows nothing")
	}
	if err := followCursorCatalog(); err != nil || !cursorBYOKHas(f, "resp/gpt-5.5") {
		t.Fatalf("the catalog's new model wasn't followed: %v", err)
	}
	writes = len(f.writes)
	if err := followCursorCatalog(); err != nil || len(f.writes) != writes {
		t.Fatalf("followed twice: %v, %d writes", err, len(f.writes)-writes)
	}

	// a backend another magpie runs is that one's to follow
	own := cursorBYOKRuntimeFor
	cursorBYOKRuntimeFor = func() cursorBackend { return notHereCursorRuntime{} }
	if err := provider.Save(provider.Provider{ID: "more", Name: "More", Key: "k", Responses: "http://127.0.0.1:1/v1", Models: []string{"gpt-6-sol"}}); err != nil {
		t.Fatal(err)
	}
	if err := followCursorCatalog(); err != nil || cursorBYOKHas(f, "more/gpt-6-sol") {
		t.Fatalf("followed a backend another magpie runs: %v", err)
	}
	cursorBYOKRuntimeFor = own

	// disconnected: left alone, never connected again
	if err := a.Disconnect(); err != nil {
		t.Fatal(err)
	}
	writes = len(f.writes)
	if err := followCursorCatalog(); err != nil || len(f.writes) != writes || a.Wired() {
		t.Fatalf("follow after disconnect: %v, %d writes, wired %v", err, len(f.writes)-writes, a.Wired())
	}
}

// The loop looks when magpie writes a file, as a save is.
func TestCursorBYOKKeptCurrentOnWrites(t *testing.T) {
	home := syncHome(t)
	f := newCursorBYOKFixture(t, home)
	if err := provider.Save(provider.Provider{ID: "anth", Name: "Anth", Key: "k", Anthropic: "http://127.0.0.1:1", Models: []string{"claude-sonnet-5"}}); err != nil {
		t.Fatal(err)
	}
	a, _ := Find(CursorBYOKID)
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { KeepCursorBackendCurrent(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	if err := provider.Save(provider.Provider{ID: "resp", Name: "Resp", Key: "k", Responses: "http://127.0.0.1:1/v1", Models: []string{"gpt-5.5"}}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); !cursorBYOKHas(f, "resp/gpt-5.5"); {
		if time.Now().After(deadline) {
			t.Fatal("the saved provider's model never reached the backend")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
