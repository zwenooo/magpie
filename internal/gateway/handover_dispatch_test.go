package gateway

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// StateActive means headers were read, not that net/http passed its
// shuttingDown check. Hold that exact point, as scheduler preemption can,
// and ensure handover still waits for the first request to be dispatched.
func TestHandedOffGatewayWaitsForFirstHandler(t *testing.T) {
	quickChat(t)
	s := New()
	srv := s.httpServer()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	active, release := make(chan struct{}), make(chan struct{})
	letGo := sync.OnceFunc(func() { close(release) })
	note := srv.ConnState
	srv.ConnState = func(conn net.Conn, state http.ConnState) {
		note(conn, state)
		if state == http.StateActive {
			close(active)
			<-release
		}
	}
	served := make(chan struct{})
	go func() { defer close(served); srv.Serve(listener); s.unread.stop() }()
	defer func() { letGo(); srv.Close(); <-served }()
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	defer client.CloseIdleConnections()
	var reused atomic.Bool
	reply := make(chan unroutedReply, 1)
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		req, _ := http.NewRequest(http.MethodPost, "http://"+listener.Addr().String()+"/v1/chat/completions", strings.NewReader(chatReq))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused.Store(info.Reused) }}))
		resp, err := client.Do(req)
		if err != nil {
			reply <- unroutedReply{err: err}
			return
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		reply <- unroutedReply{code: resp.StatusCode, body: string(body), err: err}
	}()
	defer func() { letGo(); srv.Close(); <-clientDone }()
	select {
	case <-active:
	case <-time.After(10 * time.Second):
		t.Fatal("request never reached StateActive")
	}
	listener.Close()
	<-served
	// Both observations are state checks, not an assertion that a timer
	// happened to lose a race. The paused request must still block shutdown.
	s.unread.mu.Lock()
	pending := len(s.unread.conns)
	s.unread.mu.Unlock()
	if reused.Load() || pending != 1 {
		letGo()
		<-reply
		t.Fatalf("fresh request before handler dispatch: reused=%v, pending=%d, want false/1", reused.Load(), pending)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	drained := make(chan struct{})
	var drainErr error
	go func() { defer close(drained); s.unread.wait(ctx); drainErr = srv.Shutdown(ctx) }()
	defer func() { cancel(); <-drained }()
	letGo()
	got := <-reply
	if got.err != nil || got.code != http.StatusOK || !strings.Contains(got.body, "quick") {
		t.Fatalf("fresh request discarded during handover: %+v", got)
	}
	<-drained
	if drainErr != nil {
		t.Fatal(drainErr)
	}
}
