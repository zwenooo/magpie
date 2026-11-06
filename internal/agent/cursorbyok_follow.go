package agent

// Other agents' model lists are files: whichever magpie changes the catalog
// writes them (SyncCatalog), the CLI too. Cursor's is the database of the
// backend, which only the magpie running the backend can write: a found
// group left out from the CLI was gone from Codex's picker at once and
// stayed in Cursor's until the app changed the catalog itself. The magpie
// running the backend follows the catalog (KeepCursorBackendCurrent).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"sync"

	"github.com/yetone/magpie/internal/beat"
	"github.com/yetone/magpie/internal/cursorbyok"
	"github.com/yetone/magpie/internal/filememo"
)

// cursorBYOKModels are the models Cursor is given, as the catalog has them
// now.
func cursorBYOKModels() []cursorbyok.Model {
	var models []cursorbyok.Model
	for _, model := range sharedModels(CursorBYOKID) {
		models = append(models, cursorbyok.Model{ID: model.ID, Name: model.Name, APIs: model.APIs, Context: model.Context, MaxTokens: maxTokens(model)})
	}
	return models
}

// cursorBYOKSum names what the backend is given: the models and the
// gateway they are reached through.
func cursorBYOKSum(models []cursorbyok.Model, gw cursorbyok.Gateway) string {
	b, _ := json.Marshal(struct {
		Models   []cursorbyok.Model
		URL, Key string
	}{models, gw.URL, gw.Key})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// cursorBYOKGiven is the sum of what this magpie last wrote into the
// backend, "" before it wrote any and after a disconnect.
var cursorBYOKGiven struct {
	sync.Mutex
	sum string
}

func cursorBYOKGave(sum string) {
	cursorBYOKGiven.Lock()
	cursorBYOKGiven.sum = sum
	cursorBYOKGiven.Unlock()
}

func cursorBYOKLastGiven() string {
	cursorBYOKGiven.Lock()
	defer cursorBYOKGiven.Unlock()
	return cursorBYOKGiven.sum
}

// KeepCursorBackendCurrent keeps the models of the Cursor backend this
// magpie runs as the catalog has them, until ctx ends. It looks when magpie
// writes a file or sees another process's write (the gateway watches
// providers.json and settings.json), and otherwise now and then
// (filememo.OthersEvery), as KeepCodexDaemonCurrent does.
func KeepCursorBackendCurrent(ctx context.Context) {
	written := filememo.Written()
	said := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-written:
		case <-beat.Next(filememo.OthersEvery()):
		}
		// taken before the look, so a write made during it isn't missed
		written = filememo.Written()
		// a failure said once, not on every look until it clears
		msg := ""
		if err := followCursorCatalog(); err != nil {
			msg = err.Error()
		}
		if msg != said && msg != "" {
			log.Printf("cursor: its models stay as they were: %s", msg)
		}
		said = msg
	}
}

// followCursorCatalog gives the backend this magpie runs the catalog's
// models when they aren't what it was given last. A backend another magpie
// runs is that one's to follow, and a Cursor not connected is left alone.
func followCursorCatalog() error {
	if !cursorBYOKConnected() {
		return nil
	}
	if _, ok := cursorBYOKRuntimeFor().Current(); !ok {
		return nil
	}
	if cursorBYOKSum(cursorBYOKModels(), cursorBYOKGateway()) == cursorBYOKLastGiven() {
		return nil
	}
	return cursorBYOKWrite(true, false)
}
