package agent

// The cursor-byok protocol and journal live in internal/cursorbyok. This
// adapter supplies Magpie's model selection, gateway key and agent hooks.
import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/cursorbyok"
	"github.com/yetone/magpie/internal/gateway"
)

const CursorBYOKID = "cursor-byok"

var noticeCursorBYOK = newNotice("Install and initialize cursor-byok separately, then keep it running. magpie manages only its model configurations; Cursor setup and the local service stay in cursor-byok. Restart Cursor and start a new conversation to refresh its model list.")

func cursorBYOKStatePath() string { return filepath.Join(appdir.Config(), "cursor-byok.json") }

func cursorBYOKManager(db string) cursorbyok.Manager {
	return cursorbyok.Manager{Database: db, StatePath: cursorBYOKStatePath()}
}

func cursorBYOKGateway() cursorbyok.Gateway {
	url := gateway.URL()
	return cursorbyok.Gateway{URL: url, Key: agentKeyAt(CursorBYOKID, url)}
}

func cursorBYOK(home string) *Agent {
	dir := filepath.Join(home, ".cursor-byok-v3")
	db := filepath.Join(dir, "cursor-byok.db")
	return &Agent{
		ID: CursorBYOKID, Name: "Cursor (via cursor-byok)", Icon: "cursor", Dir: dir,
		// No editable Path: DisconnectPreview must never contact the live
		// service from its copied HOME. The API has no file diff to show.
		detect:      func() bool { return isFile(db) },
		ListsModels: true,
		Notice:      func() string { return noticeCursorBYOK.String() },
		Check:       func() string { return cursorBYOKCheck(db) },
		Sync:        func() error { return cursorBYOKWrite(db, true, false) },
		Fields: []Field{{
			Key: "provider", Label: "provider",
			Get: func() string {
				if cursorBYOKManager(db).Connected() {
					return magpieID
				}
				return ""
			},
			Set: func(v string) error { return cursorBYOKWrite(db, v != "", true) },
			Options: func(map[string]string) []Option {
				return []Option{{Value: magpieID, Label: "magpie", Icon: "magpie", Note: "magpie's models through cursor-byok"}}
			},
		}},
	}
}

func cursorBYOKCheck(db string) string {
	// Get reads only the journal. Check is asked several times while a
	// page is drawn, so a closed app is probed at most once in five seconds.
	key := db + "\n" + gateway.URL()
	if st, err := os.Stat(cursorBYOKStatePath()); err == nil {
		key += st.ModTime().String()
	}
	cursorBYOKChecked.Lock()
	if !testing.Testing() && key == cursorBYOKChecked.key && time.Since(cursorBYOKChecked.at) < 5*time.Second {
		detail := cursorBYOKChecked.detail
		cursorBYOKChecked.Unlock()
		return detail
	}
	cursorBYOKChecked.Unlock()
	detail := "cursor-byok's live service has no disconnect preview"
	if !disconnectDryRun {
		detail = cursorBYOKManager(db).Check(cursorBYOKGateway())
	}
	cursorBYOKChecked.Lock()
	cursorBYOKChecked.key, cursorBYOKChecked.at, cursorBYOKChecked.detail = key, time.Now(), detail
	cursorBYOKChecked.Unlock()
	return detail
}

var cursorBYOKChecked struct {
	sync.Mutex
	key, detail string
	at          time.Time
}

func cursorBYOKWrite(db string, on, explicit bool) error {
	if disconnectDryRun {
		return errors.New("cursor-byok's live service has no disconnect preview")
	}
	manager := cursorBYOKManager(db)
	// Most agents are unconnected. Avoid reading their model catalog on the
	// periodic sync; the manager checks its active state again under lock.
	if !explicit && !manager.Connected() {
		return nil
	}
	var models []cursorbyok.Model
	if on {
		for _, model := range magpieModels(CursorBYOKID) {
			models = append(models, cursorbyok.Model{
				ID: model.ID, Name: model.Name, APIs: model.APIs,
				Context: model.Context, MaxTokens: maxTokens(model),
			})
		}
	}
	err := manager.Write(cursorBYOKGateway(), models, on, explicit)
	if errors.Is(err, cursorbyok.ErrNoModels) {
		return (&Agent{ID: CursorBYOKID, Name: "Cursor (via cursor-byok)"}).noModelLeft()
	}
	return err
}
