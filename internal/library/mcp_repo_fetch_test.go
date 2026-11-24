package library

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
)

const repoTestSHA = "1234567890abcdef1234567890abcdef12345678"

type repoTestEntry struct {
	name string
	mode fs.FileMode
	body string
}

func repoTestArchive(test *testing.T, entries ...repoTestEntry) []byte {
	test.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, entry := range entries {
		header := zip.FileHeader{Name: "project-" + repoTestSHA + "/" + entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		file, err := writer.CreateHeader(&header)
		if err != nil {
			test.Fatal(err)
		}
		if _, err := io.WriteString(file, entry.body); err != nil {
			test.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		test.Fatal(err)
	}
	return buf.Bytes()
}

type repoTestTransport func(*http.Request) *http.Response

func (send repoTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return send(req), nil
}

func repoTestHTTP(test *testing.T, send repoTestTransport) {
	test.Helper()
	previous := mcpRepoHTTP
	client := *previous
	client.Transport = send
	mcpRepoHTTP = &client
	test.Cleanup(func() { mcpRepoHTTP = previous })
}

func TestMCPRepoFetchPinnedSubdirectory(test *testing.T) {
	test.Setenv("GITHUB_TOKEN", "not-for-this-request")
	src := MCPRepoSource{"https://github.com/example/project.git", "release/v1", "cmd/mcp"}
	data, err := json.Marshal(src)
	if err != nil || string(data) != `{"repo":"https://github.com/example/project.git","ref":"release/v1","path":"cmd/mcp"}` {
		test.Fatalf("source JSON %s: %v", data, err)
	}
	archive := repoTestArchive(test,
		repoTestEntry{"docs/link", fs.ModeSymlink | 0777, "ignored"},
		repoTestEntry{"cmd/mcp-old/file", 0644, "wrong"},
		repoTestEntry{"cmd/mcp/node_modules/pkg/file", 0644, "installed"},
		repoTestEntry{"cmd/mcp/z", 0755, "#!/bin/sh"},
		repoTestEntry{"cmd/mcp/a", 0755, ""},
		repoTestEntry{"cmd/mcp/lib/data", 0644, "\x00\xff"})
	var calls []string
	repoTestHTTP(test, func(req *http.Request) *http.Response {
		endpoint := req.URL.String()
		calls = append(calls, endpoint)
		if req.Method != "GET" || req.URL.User != nil || req.Header.Get("Authorization") != "" || req.Header.Get("Proxy-Authorization") != "" || req.Header.Get("Cookie") != "" {
			test.Fatal("request was not anonymous GET")
		}
		body := []byte(repoTestSHA + "\n")
		switch endpoint {
		case "https://api.github.com/repos/example/project/commits/release%2Fv1":
			if req.Header.Get("Accept") != "application/vnd.github.sha" {
				test.Fatal(req.Header)
			}
		case "https://codeload.github.com/example/project/zip/" + repoTestSHA:
			body = archive
		default:
			test.Fatalf("unexpected URL %s", endpoint)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body))}
	})
	sha, err := repoHead(src)
	if err != nil || sha != repoTestSHA {
		test.Fatalf("head %q: %v", sha, err)
	}
	files, exec, err := repoFiles(src, sha)
	want := map[string][]byte{"z": []byte("#!/bin/sh"), "a": {}, "lib/data": {0, 255}}
	if err != nil || !maps.EqualFunc(files, want, bytes.Equal) || !slices.Equal(exec, []string{"a", "z"}) || len(calls) != 3 {
		test.Fatalf("files %v exec %v calls %v: %v", files, exec, calls, err)
	}
}

func TestMCPRepoFetchRefusals(test *testing.T) {
	src := MCPRepoSource{Repo: "https://github.com/example/project"}
	for _, input := range []string{"example/project", "https://token@github.com/o/r", "https://github.com/o/r?token=x", "https://github.com/o/r#one", "file:///x", "http://github.com/o/r", "https://github.com.evil/o/r", "https://github.com/o/%2e%2e"} {
		bad := src
		bad.Repo = input
		if _, _, err := bad.coordinates(); err == nil {
			test.Errorf("accepted repository %q", input)
		}
	}
	for _, input := range []string{"-main", "../x", "main~1", "heads/.x", "a//b", "main.lock", "main?x", "a@{1}"} {
		bad := src
		bad.Ref = input
		if _, _, err := bad.coordinates(); err == nil {
			test.Errorf("accepted ref %q", input)
		}
	}
	for _, input := range []string{"../x", `.\x`, "/usr/bin", "lib/.Git", "bad/", "aux.txt", "lib:fork"} {
		bad := src
		bad.Path = input
		if _, _, err := bad.coordinates(); err == nil {
			test.Errorf("accepted subdirectory %q", input)
		}
	}
	response := repoTestSHA
	status := 200
	var calls int
	repoTestHTTP(test, func(req *http.Request) *http.Response {
		calls++
		if req.URL.Host != "api.github.com" {
			test.Fatalf("unexpected host %s", req.URL.Host)
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Location": {"https://other.example/elsewhere"}}, Body: io.NopCloser(strings.NewReader(response))}
	})
	for _, pin := range []string{"", "main", repoTestSHA[:8], strings.Repeat("z", 40)} {
		if _, _, err := repoFiles(src, pin); err == nil || calls != 0 {
			test.Fatalf("invalid pin %q sent %d requests: %v", pin, calls, err)
		}
	}
	if _, _, err := repoFiles(src, strings.Repeat("1", 40)); err == nil || calls != 1 {
		test.Fatalf("changed ref: %v (%d requests)", err, calls)
	}
	response = strings.Repeat("1", 129)
	if _, err := repoHead(src); err == nil {
		test.Fatal("accepted oversized head")
	}
	response = "invalid"
	if _, err := repoHead(src); err == nil {
		test.Fatal("accepted malformed head")
	}
	status = http.StatusFound
	if _, err := repoHead(src); err == nil || calls != 4 {
		test.Fatalf("redirect: %v (%d requests)", err, calls)
	}
}

func TestMCPRepoFetchArchiveBoundaries(test *testing.T) {
	file := repoTestEntry{"safe", 0644, "1234"}
	for _, extra := range []repoTestEntry{
		{"symlink", fs.ModeSymlink | 0777, "safe"}, {"pipe", fs.ModeNamedPipe, ""},
		{"../bad", 0644, ""}, {`bin\bad`, 0644, ""}, {".GiT/config", 0644, ""},
		{"AUX.txt", 0644, ""}, {"safe/file", 0644, ""}, {"safe", 0644, ""},
		{"SAFE", 0644, ""}, {"dir./file", 0644, ""}, {"file:stream", 0644, ""},
		{"one-too-many", 0644, "5"},
	} {
		files, exec, err := repoArchive(repoTestArchive(test, file, extra), repoTestSHA, "", 4)
		if err == nil || files != nil || exec != nil {
			test.Errorf("did not refuse %q atomically: %v", extra.name, err)
		}
	}
	archive := repoTestArchive(test, file)
	if _, _, err := repoArchive(archive, repoTestSHA, "", 4); err != nil {
		test.Fatal(err)
	}
	if _, _, err := repoArchive(archive, repoTestSHA, "missing", 4); err == nil {
		test.Fatal("accepted missing directory")
	}
	if _, _, err := repoArchive(archive, strings.Repeat("a", 40), "", 4); err == nil {
		test.Fatal("accepted wrong archive commit")
	}
	if _, _, err := repoArchive(archive[:len(archive)/2], repoTestSHA, "", 4); err == nil {
		test.Fatal("accepted incomplete archive")
	}
}
