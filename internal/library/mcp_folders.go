package library

// A command server's program, and the files it reads its keys from, can be
// kept in the library and carried to the other computers with it, as a
// skill's folder is (zwenooo/magpie fork). A server says so by naming the
// folder in its command, arguments or environment:
//
//   - {{program}} is library/mcp/<name>/program: its code. A sync carries
//     it, leaving out what installing or running it writes (installedDirs,
//     compiled Python) and .git.
//   - {{private}} is library/mcp/<name>/private: its configuration and
//     keys. It is carried only by a backup or sync that carries the
//     library's keys, as a server's secret env and headers are; one
//     without them leaves this computer's as it is. Its folder and files
//     are the user's alone (0700, 0600).
//   - {{state}} is mcp-state/<name> in magpie's folder: what the server
//     writes as it runs (logs, caches). It is outside the library, so it
//     is never carried and never counts as a change to it.
//
// The library keeps the names as written; each agent, the check and the
// servers magpie runs itself are given this computer's paths (here). An
// agent in WSL isn't given such a server: the folders are Windows'.

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/settings"
)

const (
	programRef = "{{program}}"
	privateRef = "{{private}}"
	stateRef   = "{{state}}"
)

// CarriedFolders are a server's folders as a backup carries them.
type CarriedFolders struct {
	Name    string            `json:"name"`
	Program map[string][]byte `json:"program,omitempty"` // by path in the folder, with /
	Exec    []string          `json:"exec,omitempty"`    // the program's files that run
	// Private is nil when it isn't carried: a bundle without keys, or a
	// server here that names no {{private}}. Put then keeps this
	// computer's.
	Private map[string][]byte `json:"private"`
}

func serverFolder(name string) string { return filepath.Join(Dir(), "mcp", name) }
func programDir(name string) string   { return filepath.Join(serverFolder(name), "program") }
func privateDir(name string) string   { return filepath.Join(serverFolder(name), "private") }
func stateDir(name string) string     { return filepath.Join(settings.Dir(), "mcp-state", name) }

// names reports whether the server's command, arguments or environment
// name ref.
func (s *Server) names(ref string) bool {
	return strings.Contains(s.Command, ref) ||
		slices.ContainsFunc(s.Args, func(a string) bool { return strings.Contains(a, ref) }) ||
		slices.ContainsFunc(slices.Collect(maps.Values(s.Env)), func(v string) bool { return strings.Contains(v, ref) })
}

// keptHere is whether the server runs from a folder of this computer's.
func (s *Server) keptHere() bool {
	return s.names(programRef) || s.names(privateRef) || s.names(stateRef)
}

// here is s as it runs on this computer: the folders it names written as
// their paths here.
func here(s *Server) *Server {
	if s.Remote() || !s.keptHere() {
		return s
	}
	r := strings.NewReplacer(programRef, programDir(s.Name), privateRef, privateDir(s.Name), stateRef, stateDir(s.Name))
	c := *s
	c.Command = r.Replace(s.Command)
	c.Args = nil
	for _, a := range s.Args {
		c.Args = append(c.Args, r.Replace(a))
	}
	if s.Env != nil {
		c.Env = map[string]string{}
		for k, v := range s.Env {
			c.Env[k] = r.Replace(v)
		}
	}
	return &c
}

var errKeptInWSL = errors.New("its program is kept in the library on Windows, which an agent in WSL can't run")

// readFolder is a server folder's files, without what installing or
// running it wrote, .git and links; an error when it can't be read whole.
func readFolder(dir string) (files map[string][]byte, exec []string, size int64, err error) {
	files = map[string][]byte{}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || fromInstall(d.Name(), true, false) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || fromInstall(d.Name(), false, false) {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		files[rel] = data
		size += int64(len(data))
		if fi.Mode().Perm()&0o111 != 0 {
			exec = append(exec, rel)
		}
		return nil
	})
	slices.Sort(exec)
	return files, exec, size, err
}

// collectFolders are the folders of the servers l carries, and their
// size, which the skills' budget is left to share. A folder that can't be
// read whole, or that is bigger than budget allows, isn't carried: the
// other computers keep theirs.
func collectFolders(l *Library, budget int64) ([]*CarriedFolders, int64) {
	var out []*CarriedFolders
	var used int64
	for _, s := range sorted(l.MCP, func(s *Server) string { return s.Name }) {
		if s.Local || s.Remote() || !s.names(programRef) && !s.names(privateRef) {
			continue
		}
		c := &CarriedFolders{Name: s.Name}
		var size int64
		ok := true
		if s.names(programRef) {
			files, exec, n, err := readFolder(programDir(s.Name))
			ok = err == nil
			c.Program, c.Exec, size = files, exec, n
		}
		if ok && s.names(privateRef) {
			files, _, n, err := readFolder(privateDir(s.Name))
			ok = err == nil
			c.Private, size = files, size+n
		}
		if !ok || size > maxCarried || used+size > budget {
			log.Printf("library: the folders of the server %s aren't carried: unreadable, or over %d bytes", s.Name, min(maxCarried, budget-used))
			continue
		}
		used += size
		out = append(out, c)
	}
	return out, used
}

// putFolders makes the folders here those b carries: a server's program
// or private files that differ are replaced, the program kept with the
// backups, and the folders of a server b no longer has go with it. A
// server kept to this computer keeps its own, and so does one b carries
// no folder of.
func putFolders(l *Library, b *Bundle) error {
	keep := map[string]bool{}
	for _, s := range l.MCP {
		if s.Local {
			keep[s.Name] = true
		}
	}
	carried := map[string]bool{}
	for _, s := range b.MCP {
		if !keep[s.Name] {
			carried[s.Name] = true
		}
		keep[s.Name] = true
	}
	es, err := os.ReadDir(filepath.Join(Dir(), "mcp"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	aside := ""
	for _, e := range es {
		if e.IsDir() && !keep[e.Name()] {
			if aside, err = setAsideFolders(e.Name(), aside); err != nil {
				return err
			}
		}
	}
	for _, f := range b.Folders {
		if f == nil || !carried[f.Name] {
			continue
		}
		if f.Program != nil {
			if aside, err = putFolder(programDir(f.Name), f.Program, f.Exec, false, aside); err != nil {
				return fmt.Errorf("server %s: %w", f.Name, err)
			}
		}
		if f.Private != nil {
			if _, err = putFolder(privateDir(f.Name), f.Private, nil, true, ""); err != nil {
				return fmt.Errorf("server %s: %w", f.Name, err)
			}
		}
	}
	return nil
}

// putFolder writes files as the folder at dir, unless it has them
// already. The folder replaced goes to aside (a fresh backup when it is
// "") when keep is set and is taken away otherwise: a private folder's
// keys are never copied about. private files are the user's alone.
func putFolder(dir string, files map[string][]byte, exec []string, private bool, aside string) (string, error) {
	have, haveExec, _, err := readFolder(dir)
	if err == nil && maps.EqualFunc(have, files, bytes.Equal) && slices.Equal(haveExec, slices.Sorted(slices.Values(exec))) {
		return aside, nil
	}
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return aside, err
	}
	stage, err := os.MkdirTemp(Dir(), ".incoming-")
	if err != nil {
		return aside, err
	}
	defer os.RemoveAll(stage)
	to := filepath.Join(stage, "folder")
	dirMode, fileMode := fs.FileMode(0o755), fs.FileMode(0o644)
	if private {
		dirMode, fileMode = 0o700, 0o600
	}
	for _, rel := range slices.Sorted(maps.Keys(files)) {
		if !localFile(rel) {
			return aside, fmt.Errorf("%q is not a file in its folder", rel)
		}
		p := filepath.Join(to, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), dirMode); err != nil {
			return aside, err
		}
		mode := fileMode
		if slices.Contains(exec, rel) {
			mode = 0o755
		}
		if err := os.WriteFile(p, files[rel], mode); err != nil {
			return aside, err
		}
	}
	if err := os.MkdirAll(to, dirMode); err != nil { // no files at all
		return aside, err
	}
	os.Chmod(to, dirMode)
	if _, err := os.Lstat(dir); err == nil {
		if private {
			if err := os.RemoveAll(dir); err != nil {
				return aside, err
			}
		} else {
			if aside == "" {
				if aside, err = freshAside(); err != nil {
					return aside, err
				}
			}
			kept := filepath.Join(aside, filepath.Base(filepath.Dir(dir)), filepath.Base(dir))
			if err := os.MkdirAll(filepath.Dir(kept), 0o700); err != nil {
				return aside, err
			}
			if err := move(dir, kept); err != nil {
				return aside, err
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return aside, err
	}
	return aside, os.Rename(to, dir)
}

// freshAside is a backup folder for server folders set aside: <when>/mcp,
// which pruneBackups keeps, as it keeps skills set aside.
func freshAside() (string, error) {
	dir := filepath.Join(BackupDir(), time.Now().Format("2006-01-02_15-04-05.000"), "mcp")
	return dir, os.MkdirAll(dir, 0o700)
}

// setAsideFolders takes a server's folders out of the library: its
// program to the backups (aside, or a fresh one), its private files away.
func setAsideFolders(name, aside string) (string, error) {
	if _, err := os.Lstat(programDir(name)); err == nil {
		if aside == "" {
			var err error
			if aside, err = freshAside(); err != nil {
				return aside, err
			}
		}
		if err := os.MkdirAll(filepath.Join(aside, name), 0o700); err != nil {
			return aside, err
		}
		if err := move(programDir(name), filepath.Join(aside, name, "program")); err != nil {
			return aside, err
		}
	}
	return aside, os.RemoveAll(serverFolder(name))
}

// renameFolders moves a renamed server's folders under its new name.
func renameFolders(old, name string) error {
	if _, err := os.Lstat(serverFolder(old)); err != nil {
		return nil
	}
	if _, err := os.Lstat(serverFolder(name)); err == nil {
		return fmt.Errorf("the library's folder already has folders for a server called %s", name)
	}
	return os.Rename(serverFolder(old), serverFolder(name))
}

// withoutPrivate is folders without their private files.
func withoutPrivate(folders []*CarriedFolders) []*CarriedFolders {
	var out []*CarriedFolders
	for _, f := range folders {
		c := *f
		c.Private = nil
		out = append(out, &c)
	}
	return out
}

// withPrivate is folders with the private files have carries for each one
// that carries none.
func withPrivate(folders, have []*CarriedFolders) []*CarriedFolders {
	var out []*CarriedFolders
	for _, f := range folders {
		c := *f
		if i := slices.IndexFunc(have, func(h *CarriedFolders) bool { return h.Name == f.Name }); c.Private == nil && i >= 0 {
			c.Private = have[i].Private
		}
		out = append(out, &c)
	}
	return out
}
