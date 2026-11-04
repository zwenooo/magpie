package gui

import (
	"encoding/json"
	"net/http"

	"github.com/yetone/magpie/internal/sessions"
)

func sessionSourceRoutes(mux *http.ServeMux) {
	list := func(rw http.ResponseWriter) {
		sources, err := sessions.Sources()
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, struct {
			Sources []sessions.SessionSource `json:"sources"`
		}{Sources: sources})
	}
	decode := func(rw http.ResponseWriter, r *http.Request) (sessions.SessionSource, bool) {
		var in sessions.SessionSource
		if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 16384)).Decode(&in); err != nil {
			fail(rw, err)
			return in, false
		}
		return in, true
	}
	mux.HandleFunc("GET /api/sessions/sources", func(rw http.ResponseWriter, r *http.Request) {
		list(rw)
	})
	mux.HandleFunc("POST /api/sessions/scan", func(rw http.ResponseWriter, r *http.Request) {
		in, ok := decode(rw, r)
		if !ok {
			return
		}
		out, err := sessions.ScanSource(in)
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, out)
	})
	mux.HandleFunc("POST /api/sessions/import", func(rw http.ResponseWriter, r *http.Request) {
		in, ok := decode(rw, r)
		if !ok {
			return
		}
		out, err := sessions.ImportSource(in)
		if err != nil {
			fail(rw, err)
			return
		}
		forgetStats()
		writeJSON(rw, out)
	})
	mux.HandleFunc("POST /api/sessions/sources/remove", func(rw http.ResponseWriter, r *http.Request) {
		in, ok := decode(rw, r)
		if !ok {
			return
		}
		if err := sessions.RemoveSource(in); err != nil {
			fail(rw, err)
			return
		}
		forgetStats()
		list(rw)
	})
}
