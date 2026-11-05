package gui

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/sessions"
)

// sessionHandoffRoutes: handoff writes a session's condensed record and its
// whole conversation for another agent to carry it on
// (sessions.SaveHandoff) and says where they are; the page makes the steps
// and the prompt the user copies. The session is found as listed, never
// named by a path from the page.
func sessionHandoffRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sessions/handoff", func(rw http.ResponseWriter, r *http.Request) {
		var in struct{ Agent, ID string }
		if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 4096)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		s, ok := sessions.Find(in.Agent, in.ID)
		if !ok {
			fail(rw, errors.New("no such session"))
			return
		}
		h, err := sessions.SaveHandoff(s, wslName(clientName(s.Agent), s))
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, h)
	})
}

// clientName is an agent as the user knows it, by its id.
func clientName(id string) string {
	for _, a := range agent.Clients() {
		if a.ID == id {
			return a.SharedName()
		}
	}
	return id
}
