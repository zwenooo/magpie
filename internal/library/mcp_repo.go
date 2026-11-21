package library

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

type MCPRepoCheck struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
}

func (server *Server) keepRepo(previous *Server) {
	server.RepoSource, server.RepoCommit, server.RepoHash = nil, "", ""
	server.RepoLocalCommit, server.RepoLocalHash = "", ""
	if previous != nil {
		server.RepoSource, server.RepoCommit, server.RepoHash = previous.RepoSource, previous.RepoCommit, previous.RepoHash
		server.RepoLocalCommit, server.RepoLocalHash = previous.RepoLocalCommit, previous.RepoLocalHash
	}
}

func repoEntry(server *Server) string {
	if server == nil || server.Transport != "stdio" || server.Bundle != "" || server.Plugin != "" {
		return ""
	}
	for _, arg := range slices.Concat([]string{server.Command}, server.Args) {
		arg = filepath.ToSlash(arg)
		if entry, ok := strings.CutPrefix(arg, programRef+"/"); ok && fs.ValidPath(entry) && entry != "." && localFile(entry) {
			return entry
		}
	}
	return ""
}

func repoTarget(name string, source *MCPRepoSource) (*Server, MCPRepoSource, error) {
	mu.Lock()
	defer mu.Unlock()
	var src MCPRepoSource
	if err := checkName("server", name); err != nil {
		return nil, src, err
	}
	if !filepath.IsLocal(name) {
		return nil, src, fmt.Errorf("invalid server folder %q", name)
	}
	lib, err := load()
	if err != nil {
		return nil, src, err
	}
	server := lib.server(name)
	if server == nil {
		return nil, src, fmt.Errorf("no server called %s", name)
	}
	if repoEntry(server) == "" {
		return nil, src, fmt.Errorf("%s needs a stdio entry file under %s", name, programRef)
	}
	if source == nil {
		source = server.RepoSource
	}
	if source == nil {
		return nil, src, fmt.Errorf("%s has no repository source", name)
	}
	return server, *source, nil
}

func CheckMCPRepo(name string, source *MCPRepoSource) (*MCPRepoCheck, error) {
	server, src, err := repoTarget(name, source)
	if err != nil {
		return nil, err
	}
	latest, err := repoHead(src)
	if err != nil {
		return nil, err
	}
	return &MCPRepoCheck{Current: server.RepoLocalCommit, Latest: latest}, nil
}

func ApplyMCPRepo(name string, source *MCPRepoSource, expected string) (*Result, error) {
	return applyMCPRepo(name, source, expected, repoFiles, (*Library).save)
}

func applyMCPRepo(name string, source *MCPRepoSource, expected string, fetch func(MCPRepoSource, string) (map[string][]byte, []string, error), save func(*Library) error) (*Result, error) {
	server, src, err := repoTarget(name, source)
	if err != nil {
		return nil, err
	}
	files, exec, err := fetch(src, expected)
	if err != nil {
		return nil, err
	}
	if _, ok := files[repoEntry(server)]; !ok {
		return nil, fmt.Errorf("repository snapshot has no entry %s", repoEntry(server))
	}
	mu.Lock()
	defer mu.Unlock()
	lib, err := load()
	if err != nil {
		return nil, err
	}
	current := lib.server(name)
	if !reflect.DeepEqual(server, current) {
		return nil, fmt.Errorf("%s changed while fetching; check again", name)
	}
	dir := programDir(name)
	for _, parent := range []string{Dir(), filepath.Dir(serverFolder(name)), serverFolder(name)} {
		info, err := os.Lstat(parent)
		if errors.Is(err, fs.ErrNotExist) {
			err = os.Mkdir(parent, 0o755)
		} else if err == nil && !info.IsDir() {
			return nil, fmt.Errorf("%s is not a plain folder", parent)
		}
		if err != nil {
			return nil, err
		}
	}
	stage, err := os.MkdirTemp(serverFolder(name), ".repo-")
	if err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(stage)
		}
	}()
	next, old := filepath.Join(stage, "next"), filepath.Join(stage, "old")
	if err := writeSkill(next, &CarriedSkill{Files: files, Exec: exec}); err != nil {
		return nil, err
	}
	digest, err := repoProgramHash(next)
	if err != nil {
		return nil, err
	}
	_, err = os.Lstat(dir)
	had := err == nil
	if !had && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var have string
	if had {
		have, err = repoProgramHash(dir)
		if err != nil {
			return nil, err
		}
	}
	if have != current.RepoLocalHash {
		return nil, fmt.Errorf("%s has local program changes; refusing to replace them", name)
	}
	if had {
		if err := os.Rename(dir, old); err != nil {
			return nil, err
		}
	}
	restore := func(cause error) (*Result, error) {
		if had {
			if err := os.Rename(old, dir); err != nil {
				cleanup = false
				return nil, errors.Join(cause, fmt.Errorf("restore program from %s: %w", old, err))
			}
		}
		return nil, cause
	}
	if had {
		have, err := repoProgramHash(old)
		if err != nil {
			return restore(err)
		}
		if have != current.RepoLocalHash {
			return restore(fmt.Errorf("%s has local program files or edits; refusing to replace them", name))
		}
	}
	if err := os.Rename(next, dir); err != nil {
		return restore(err)
	}
	lib, err = load()
	if err == nil {
		current = lib.server(name)
		if reflect.DeepEqual(server, current) {
			current.RepoSource, current.RepoCommit, current.RepoHash = &src, expected, digest
			current.RepoLocalCommit, current.RepoLocalHash = expected, digest
			err = save(lib)
		} else {
			err = fmt.Errorf("%s changed while preparing the update; check again", name)
		}
	}
	if err != nil {
		if undo := os.Rename(dir, next); undo != nil {
			cleanup = false
			return nil, errors.Join(err, fmt.Errorf("undo program replacement (transaction %s): %w", stage, undo))
		}
		return restore(err)
	}
	return &Result{Updated: []string{name}}, nil
}

func repoProgramHash(dir string) (string, error) {
	digest := sha256.New()
	empty := true
	err := filepath.WalkDir(dir, func(filename string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if filename == dir && entry.IsDir() {
			return nil
		}
		empty = false
		rel, err := filepath.Rel(dir, filename)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			fmt.Fprintf(digest, "dir %q;", rel)
			return nil
		}
		if filename == dir || !entry.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular program file or folder", filename)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		file, err := os.Open(filename)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, err = io.Copy(hash, file)
		err = errors.Join(err, file.Close())
		if err != nil {
			return err
		}
		fmt.Fprintf(digest, "file %q %o %x;", rel, info.Mode().Perm(), hash.Sum(nil))
		return nil
	})
	if err != nil || empty {
		return "", err
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}
