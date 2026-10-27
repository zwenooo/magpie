package gui

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/yetone/magpie/internal/agent"
)

func TestCursorSetupErrorRequestsExplicitApproval(t *testing.T) {
	w := httptest.NewRecorder()
	fail(w, &agent.CursorSetupRequiredError{})
	var reply map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if w.Code != 400 || reply["code"] != "cursor_setup_required" {
		t.Fatalf("setup error has no actionable confirmation code: status %d, code %q", w.Code, reply["code"])
	}
}
