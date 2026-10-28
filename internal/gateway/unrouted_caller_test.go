package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The watch runs before withCaller and body decoding. Its route must keep
// the caller recorded on arrival even when those handlers change headers.
// Advancing the watch explicitly makes this fail without relying on a race.
func TestUnroutedCallerSurvivesHeaderChanges(t *testing.T) {
	unroutedLimits(t, time.Hour, time.Hour)
	unknownLimit(t, time.Hour)
	for _, test := range []struct {
		name, token, userAgent, forwarded, want string
	}{
		{"agent key", TokenFor("codex"), "fixture-client", "", "codex"},
		{"user agent", "", "Cursor-CLI/1.0", "", "cursor-local"},
		{"forwarded agent", "", "magpie/0.1", "claude", "claude"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &Server{}
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(unroutedHi))
			r.Header.Set("Authorization", "Bearer "+test.token)
			r.Header.Set("User-Agent", test.userAgent)
			r.Header.Set(AgentHeader, test.forwarded)
			r.Header.Set("Content-Encoding", "identity")
			r, done := s.active.add(r)
			defer done()
			_, r, unwatch := s.watchUnrouted(httptest.NewRecorder(), r, inflightOf(r.Context()))
			defer unwatch()
			u := inflightOf(r.Context()).unrouted
			// Model the header cleanup by withCaller and decodedBody, then
			// a provider adapter reusing the request's headers.
			r.Header.Del(AgentHeader)
			body, err := decodedBody(r)
			if err != nil {
				t.Fatal(err)
			}
			body.Close()
			r.Header.Set("Authorization", "Bearer "+TokenFor("claude"))
			r.Header.Set("User-Agent", "changed-client")
			if test.forwarded != "" {
				r.Header.Del("Authorization")
			}
			u.show()
			if u.shown == nil || u.shown.Agent != test.want {
				t.Fatalf("held route = %+v, want original caller %q", u.shown, test.want)
			}
		})
	}
}
