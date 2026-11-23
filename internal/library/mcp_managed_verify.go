package library

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"
)

// VerifyManagedMCP launches a new local stdio process, not the gateway's cached
// server. Only the explicitly installed local program is eligible. Child stderr
// can contain credentials, so it never becomes part of the returned error.
func VerifyManagedMCP(name string) error {
	_, err := ProbeManagedMCP(context.Background(), name)
	return err
}

// ProbeManagedMCP returns the validated handshake from this exact process so
// the Library row can use its advertised pages without starting a second one.
func ProbeManagedMCP(parent context.Context, name string) (result ServerHealth, failure error) {
	s, _, err := repoTarget(name, nil)
	if err != nil {
		return ServerHealth{}, err
	}
	defer func() {
		if parent.Err() == nil {
			rememberWebEntries(s, result.WebEntries)
		}
	}()
	if s.RepoLocalCommit == "" || s.RepoLocalHash == "" {
		return ServerHealth{}, errors.New("this repository program has not been installed on this computer")
	}
	if s.RepoLocalSource == nil || *s.RepoSource != *s.RepoLocalSource {
		return ServerHealth{}, errors.New("confirm this repository source locally before launching the managed program")
	}
	if err := verifyProgramFiles(s); err != nil {
		return ServerHealth{}, err
	}
	ctx, cancel := context.WithTimeout(parent, 3*stdioStep+5*time.Second)
	defer cancel()
	h := checkStdioIn(ctx, s, "", "")
	if h.State != "ok" {
		return ServerHealth{}, fmt.Errorf("local MCP handshake failed (%s)", h.Why)
	}
	after, _, err := repoTarget(name, nil)
	if err != nil || !reflect.DeepEqual(after, s) {
		return ServerHealth{}, errors.New("the MCP configuration changed during verification; check again")
	}
	if err := verifyProgramFiles(s); err != nil {
		return ServerHealth{}, err
	}
	return h, nil
}

// ManagedGatewayState compares the configuration registered in this process.
// Absence of a local registry (e.g. a separate CLI) says nothing about a gateway.
func ManagedGatewayState(name string) string {
	s, _, err := repoTarget(name, nil)
	if err != nil || s.RepoLocalCommit == "" {
		return "unknown"
	}
	want, ok := specOf(npxDirect(here(s)))
	if !ok {
		return "unknown"
	}
	runs.Lock()
	registered, ok := runs.given[name]
	runs.Unlock()
	if !ok {
		return "unknown"
	}
	if registered.key() != want.key() {
		return "stale"
	}
	return "matching"
}

func verifyProgramFiles(s *Server) error {
	digest, err := repoProgramHash(programDir(s.Name))
	if err != nil || digest != s.RepoLocalHash {
		return errors.New("local program files differ from the confirmed installation")
	}
	return nil
}
