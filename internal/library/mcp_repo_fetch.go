package library

import (
	"archive/zip"
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type MCPRepoSource struct {
	Repo string `json:"repo"`
	Ref  string `json:"ref"`
	Path string `json:"path"`
}

var mcpRepoHTTP = &http.Client{
	Timeout:       45 * time.Second,
	Transport:     &http.Transport{DisableCompression: true, IdleConnTimeout: 30 * time.Second},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func repoASCIIWord(char rune) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._-", char)
}

func repoSHA(value string) bool {
	if len(value) != 40 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func repoPortablePath(value string) bool {
	if len(value) > 1024 || strings.Count(value, "/") >= 32 || !fs.ValidPath(value) || value == "." || !norm.NFC.IsNormalString(value) {
		return false
	}
	for part := range strings.SplitSeq(value, "/") {
		if len(part) > 255 || strings.EqualFold(part, ".git") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `\:<>"|?*~`) || strings.ContainsFunc(part, func(char rune) bool { return unicode.IsControl(char) || unicode.Is(unicode.Cf, char) }) {
			return false
		}
		base, _, _ := strings.Cut(strings.ToUpper(part), ".")
		base = strings.TrimRight(base, " ")
		switch base {
		case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
			return false
		}
		if (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && slices.Contains([]string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³"}, base[3:]) {
			return false
		}
	}
	return true
}

func (src MCPRepoSource) coordinates() (repo, ref string, err error) {
	repo, ok := strings.CutPrefix(src.Repo, "https://github.com/")
	repo = strings.TrimSuffix(strings.TrimSuffix(repo, "/"), ".git")
	owner, name, split := strings.Cut(repo, "/")
	if !ok || !split || owner == "" || name == "" || len(repo) > 256 || strings.Contains(name, "/") || !repoPortablePath(repo) || strings.ContainsFunc(owner+name, func(char rune) bool { return !repoASCIIWord(char) }) {
		return "", "", errors.New("expected an HTTPS GitHub repository URL")
	}
	if src.Path != "" && !repoPortablePath(src.Path) {
		return "", "", errors.New("invalid repository subdirectory")
	}
	ref = src.Ref
	if ref == "" {
		ref = "HEAD"
	}
	if len(ref) > 255 || strings.HasPrefix(ref, "-") || strings.Contains(ref, "..") || strings.HasSuffix(ref, ".") || strings.ContainsFunc(ref, func(char rune) bool { return char != '/' && !repoASCIIWord(char) }) {
		return "", "", errors.New("invalid repository ref")
	}
	for part := range strings.SplitSeq(ref, "/") {
		if part == "" || part[0] == '.' || strings.HasSuffix(part, ".lock") {
			return "", "", errors.New("invalid repository ref")
		}
	}
	return repo, ref, nil
}

func repoRead(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if int64(len(data)) > limit {
		return nil, errors.New("repository exceeds size limit")
	}
	return data, err
}

func repoGet(endpoint, accept string, limit int64) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	resp, err := mcpRepoHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, errRepoNeedsAuth
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}
	return repoRead(resp.Body, limit)
}

func repoHead(src MCPRepoSource) (string, error) {
	repo, ref, err := src.coordinates()
	if err != nil {
		return "", err
	}
	sha, _, err := repoResolve(repo, ref)
	return sha, err
}

func repoResolve(repo, ref string) (string, bool, error) {
	endpoint := "repos/" + repo + "/commits/" + url.PathEscape(ref)
	const accept = "application/vnd.github.sha"
	data, err := repoGet("https://api.github.com/"+endpoint, accept, 128)
	auth := errors.Is(err, errRepoNeedsAuth)
	if auth {
		data, err = repoAuthenticated(endpoint, accept, 128)
	}
	if err != nil {
		return "", auth, err
	}
	sha := strings.TrimSpace(string(data))
	if !repoSHA(sha) || repoSHA(ref) && sha != ref {
		return "", auth, errors.New("GitHub did not return the requested commit")
	}
	return sha, auth, nil
}

func repoFiles(src MCPRepoSource, expected string) (map[string][]byte, []string, error) {
	if !repoSHA(expected) {
		return nil, nil, errors.New("expected a full lowercase commit SHA")
	}
	repo, ref, err := src.coordinates()
	if err != nil {
		return nil, nil, err
	}
	head, auth, err := repoResolve(repo, ref)
	if err != nil {
		return nil, nil, err
	}
	if head != expected {
		return nil, nil, errors.New("repository ref no longer matches the expected commit")
	}
	var data []byte
	if auth {
		data, err = repoAuthenticated("repos/"+repo+"/zipball/"+expected, "application/vnd.github+json", maxCarried+1<<20)
	} else {
		data, err = repoGet("https://codeload.github.com/"+repo+"/zip/"+expected, "application/zip", maxCarried+1<<20)
	}
	if err != nil {
		return nil, nil, err
	}
	return repoArchive(data, expected, src.Path, maxCarried)
}

func repoArchive(data []byte, expected, subdir string, budget int64) (map[string][]byte, []string, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, err
	}
	if len(archive.File) == 0 || len(archive.File) > 8192 {
		return nil, nil, errors.New("invalid repository entry count")
	}
	root, _, _ := strings.Cut(archive.File[0].Name, "/")
	full := strings.HasSuffix(root, "-"+expected)
	short := repoSHA(expected) && strings.HasSuffix(root, "-"+expected[:7]) && archive.Comment == expected
	if !repoPortablePath(root) || (!full && !short) {
		return nil, nil, errors.New("repository archive is not for the expected commit")
	}
	files := make(map[string][]byte)
	var exec []string
	type node struct {
		name string
		dir  bool
	}
	seen := make(map[string]node)
	fold := cases.Fold()
	for _, entry := range archive.File {
		rel, ok := strings.CutPrefix(entry.Name, root+"/")
		if !ok {
			return nil, nil, errors.New("multiple repository archive roots")
		}
		if rel == "" {
			continue
		}
		rel = strings.TrimSuffix(rel, "/")
		mode := entry.Mode()
		if subdir != "" {
			if (subdir == rel || strings.HasPrefix(subdir, rel+"/")) && !mode.IsDir() {
				return nil, nil, errors.New("repository source path is not a directory")
			}
			rel, ok = strings.CutPrefix(rel, subdir+"/")
			if !ok {
				continue
			}
		}
		if !repoPortablePath(rel) || mode.Type()&^fs.ModeDir != 0 {
			return nil, nil, fmt.Errorf("unsafe repository entry %q", rel)
		}
		prefix := ""
		ignore := false
		for part := range strings.SplitSeq(rel, "/") {
			prefix += part
			dir := prefix != rel || mode.IsDir()
			key := fold.String(prefix)
			value := node{prefix, dir}
			if prev, exists := seen[key]; exists && (prev != value || !dir) {
				return nil, nil, fmt.Errorf("conflicting repository path %q", prefix)
			}
			seen[key] = value
			if len(seen) > 8192 {
				return nil, nil, errors.New("repository exceeds path count limit")
			}
			if fromInstall(part, dir, false) {
				ignore = true
				break
			}
			prefix += "/"
		}
		if ignore || mode.IsDir() {
			continue
		}
		if entry.UncompressedSize64 > uint64(budget) {
			return nil, nil, errors.New("repository program exceeds size limit")
		}
		reader, err := entry.Open()
		if err != nil {
			return nil, nil, err
		}
		contents, err := repoRead(reader, budget)
		reader.Close()
		if err != nil {
			return nil, nil, err
		}
		budget -= int64(len(contents))
		files[rel] = contents
		if mode.Perm()&0111 != 0 {
			exec = append(exec, rel)
		}
	}
	if len(files) == 0 {
		return nil, nil, errors.New("repository directory contains no program files")
	}
	slices.Sort(exec)
	return files, exec, nil
}
