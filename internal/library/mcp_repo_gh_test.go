package library

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestMCPRepoPrivateAccess(t *testing.T) {
	source := MCPRepoSource{Repo: "https://github.com/example/private", Ref: "main", Path: "server"}
	archive := repoTestArchive(t, repoTestEntry{"server/main.py", 0644, "reviewed bytes"})
	repoTestHTTP(t, func(req *http.Request) *http.Response {
		if req.URL.Host != "api.github.com" || req.Header.Get("Authorization") != "" {
			t.Fatalf("unexpected anonymous request %s", req.URL.Host)
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(""))}
	})
	previous := repoAuthenticated
	t.Cleanup(func() { repoAuthenticated = previous })
	var calls []string
	const commit = "repos/example/private/commits/main"
	const pinned = "repos/example/private/zipball/" + repoTestSHA
	repoAuthenticated = func(endpoint, accept string, limit int64) ([]byte, error) {
		calls = append(calls, endpoint)
		switch endpoint {
		case commit:
			return []byte(repoTestSHA), nil
		case pinned:
			return archive, nil
		}
		return nil, errors.New("unexpected authenticated endpoint")
	}
	sha, err := repoHead(source)
	if err != nil || sha != repoTestSHA {
		t.Fatalf("private preview: %q %v", sha, err)
	}
	files, _, err := repoFiles(source, sha)
	if err != nil || !bytes.Equal(files["main.py"], []byte("reviewed bytes")) || !slices.Equal(calls, []string{commit, commit, pinned}) {
		t.Fatalf("private pinned fetch: %v, calls %v", err, calls)
	}
	calls = nil
	if _, _, err := repoFiles(source, strings.Repeat("0", 40)); err == nil || !slices.Equal(calls, []string{commit}) {
		t.Fatalf("changed ref fetched an archive: %v %v", calls, err)
	}
	repoAuthenticated = func(string, string, int64) ([]byte, error) { return nil, errors.New("no repository access") }
	if _, err := repoHead(source); err == nil {
		t.Fatal("missing access reported a successful preview")
	}
}
