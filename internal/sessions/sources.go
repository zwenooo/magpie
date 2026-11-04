package sessions

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/filelock"
	"github.com/yetone/magpie/internal/filememo"
	"github.com/yetone/magpie/internal/proc"
	"github.com/yetone/magpie/internal/settings"
	"github.com/yetone/magpie/internal/steady"
)

// SessionSource is an additional projects folder, or one project within it.
// Importing registers a read-only source; its transcripts stay where they are.
type SessionSource struct {
	Path  string `json:"path"`
	Agent string `json:"agent"`
}

type SourceSession struct {
	ID    string    `json:"id"`
	Title string    `json:"title"`
	Cwd   string    `json:"cwd"`
	Last  time.Time `json:"last"`
}

// SourceScan counts every recognized session and previews at most 50 of them.
type SourceScan struct {
	Source   SessionSource   `json:"source"`
	Count    int             `json:"count"`
	Sessions []SourceSession `json:"sessions"`
}

func sourcesPath() string { return filepath.Join(settings.Dir(), "session-sources.json") }

var sourceWrites sync.Mutex
var sourceRegistries sync.Map // registry path -> last readable registrations
var sourceRealPath = proc.RealPath

func sourceAgent(agent string) bool { return agent == "claude" || agent == "claude-desktop" }

// Sources reports registry errors rather than replacing unreadable settings.
func Sources() ([]SessionSource, error) {
	rows, err := filememo.Read("session sources", sourcesPath(), func(b []byte) ([]SessionSource, error) {
		var out []SessionSource
		if err := json.Unmarshal(b, &out); err != nil {
			return nil, err
		}
		if out == nil {
			return nil, errors.New("invalid session sources")
		}
		for _, s := range out {
			if !sourceAgent(s.Agent) || !filepath.IsAbs(s.Path) {
				return nil, errors.New("invalid session source")
			}
		}
		return out, nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		rows, err = []SessionSource{}, nil
	}
	if err == nil {
		sourceRegistries.Store(sourcesPath(), slices.Clone(rows))
	}
	return slices.Clone(rows), err
}

// Automatic readers keep the last readable registrations through a partial
// settings write. Explicit management still gets the error from Sources.
func sourcesForRead() []SessionSource {
	rows, err := Sources()
	if err != nil {
		if old, ok := sourceRegistries.Load(sourcesPath()); ok {
			return slices.Clone(old.([]SessionSource))
		}
		return nil
	}
	return rows
}

func normalizeSource(s SessionSource) (SessionSource, error) {
	if !sourceAgent(s.Agent) {
		return s, errors.New("unsupported session source type")
	}
	s.Path = strings.TrimSpace(s.Path)
	if !filepath.IsAbs(s.Path) {
		return s, errors.New("session source must be an absolute folder path")
	}
	s.Path = filepath.Clean(s.Path)
	path, err := sourceRealPath(s.Path)
	if err != nil {
		return s, err
	}
	s.Path = path
	info, err := os.Stat(path)
	if err != nil {
		return s, err
	}
	if !info.IsDir() {
		return s, errors.New("session source must be a folder")
	}
	return s, nil
}

// ScanSource reads only the requested source, without registering or copying it.
func ScanSource(s SessionSource) (SourceScan, error) {
	out := SourceScan{Sessions: []SourceSession{}}
	s, err := normalizeSource(s)
	out.Source = s
	if err != nil {
		return out, err
	}
	if !Reads(s.Agent) {
		return out, ErrNotRead
	}
	files, err := sourceFiles(s)
	if err != nil {
		return out, err
	}
	var mains []file
	seen := map[string]bool{}
	for _, f := range files {
		if f.main && !seen[f.key] {
			mains = append(mains, f)
			seen[f.key] = true
		}
	}
	out.Count = len(mains)
	slices.SortFunc(mains, func(a, b file) int { return b.mod.Compare(a.mod) })
	for _, f := range mains[:min(len(mains), 50)] {
		g := glance(f)
		if g == nil {
			return out, fmt.Errorf("could not read session %s", f.path)
		}
		out.Sessions = append(out.Sessions, SourceSession{
			ID: strings.TrimPrefix(f.key, s.Agent+":"), Title: cmp.Or(g.Custom, g.Named, g.Title, g.First), Cwd: g.Cwd, Last: g.Last,
		})
	}
	return out, nil
}

// ImportSource rechecks the directory before registering it. Repeated imports
// and nested sources of the same type do not create duplicate listings.
func ImportSource(s SessionSource) (SourceScan, error) {
	out, err := ScanSource(s)
	if err != nil {
		return out, err
	}
	if out.Count == 0 {
		return out, errors.New("no sessions found in this folder")
	}
	err = changeSources(func(rows []SessionSource) ([]SessionSource, error) {
		for _, row := range rows {
			if sourceWithin(row.Path, out.Source.Path) || sourceWithin(out.Source.Path, row.Path) {
				if row.Agent != out.Source.Agent {
					return nil, errors.New("an overlapping folder is already imported with another type")
				}
				if sourceWithin(row.Path, out.Source.Path) {
					return rows, nil
				}
			}
		}
		rows = slices.DeleteFunc(rows, func(row SessionSource) bool { return sourceWithin(out.Source.Path, row.Path) })
		return append(rows, out.Source), nil
	})
	if err == nil {
		Reset()
	}
	return out, err
}

// RemoveSource forgets a registration, including one whose disk is offline.
// It never removes the source directory or any transcript in it.
func RemoveSource(s SessionSource) error {
	if !sourceAgent(s.Agent) || !filepath.IsAbs(s.Path) {
		return errors.New("invalid session source")
	}
	err := changeSources(func(rows []SessionSource) ([]SessionSource, error) {
		return slices.DeleteFunc(rows, func(row SessionSource) bool {
			return row.Agent == s.Agent && sourceWithin(row.Path, s.Path) && sourceWithin(s.Path, row.Path)
		}), nil
	})
	if err == nil {
		Reset()
	}
	return err
}

func changeSources(change func([]SessionSource) ([]SessionSource, error)) error {
	sourceWrites.Lock()
	defer sourceWrites.Unlock()
	unlock, held := filelock.Lock(sourcesPath()+".lock", filelock.Wait)
	defer unlock()
	if !held {
		return errors.New("could not lock session sources")
	}
	filememo.Look(sourcesPath())
	rows, err := Sources()
	if err != nil {
		return err
	}
	rows, err = change(rows)
	if err != nil {
		return err
	}
	if rows == nil {
		rows = []SessionSource{}
	}
	b, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	if err := steady.WriteFile(sourcesPath(), append(b, '\n'), 0o600); err != nil {
		return err
	}
	sourceRegistries.Store(sourcesPath(), slices.Clone(rows))
	filememo.Forget()
	return nil
}

func sourceWithin(root, path string) bool {
	root, path = filepath.Clean(root), filepath.Clean(path)
	if runtime.GOOS == "windows" {
		root, path = strings.ToLower(root), strings.ToLower(path)
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

var sourceName = regexp.MustCompile(`^[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}\.jsonl$`)

// sourceFiles follows only Claude's projects/project/session layout. Links
// inside a source and unrelated JSONL journals are not traversal roots.
func sourceFiles(s SessionSource) ([]file, error) {
	entries, err := os.ReadDir(s.Path)
	if err != nil {
		return nil, err
	}
	var out []file
	var scanErr error
	project := func(root string, entries []os.DirEntry) {
		for _, e := range entries {
			if !e.Type().IsRegular() || !sourceName.MatchString(e.Name()) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				scanErr = cmp.Or(scanErr, err)
				continue
			}
			id := strings.TrimSuffix(e.Name(), ".jsonl")
			f := file{agent: s.Agent, key: s.Agent + ":" + id, path: filepath.Join(root, e.Name()), main: true, readOnly: true, size: info.Size(), mod: info.ModTime()}
			valid, err := sourceTranscript(f)
			if err != nil {
				scanErr = cmp.Or(scanErr, fmt.Errorf("%s: %w", f.path, err))
				if old, ok := sourceIdentities.Load(f.path); ok {
					valid = old.(sourceIdentity).valid
				}
			}
			if !valid {
				continue
			}
			out = append(out, f)
			subs := filepath.Join(root, id, "subagents")
			if real, err := sourceRealPath(subs); err == nil && sourceWithin(s.Path, real) {
				err = walkLinked(subs, func(path string, d fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if d.Type().IsRegular() && strings.HasPrefix(d.Name(), "agent-") && strings.HasSuffix(d.Name(), ".jsonl") {
						g := file{agent: s.Agent, key: f.key, path: path, readOnly: true}
						if stat(&g) {
							out = append(out, g)
						}
					}
					return nil
				})
				if err != nil {
					scanErr = cmp.Or(scanErr, err)
				}
			} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
				scanErr = cmp.Or(scanErr, err)
			}
		}
	}
	project(s.Path, entries)
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "subagents" {
			continue
		}
		root := filepath.Join(s.Path, e.Name())
		children, err := os.ReadDir(root)
		if err != nil {
			scanErr = cmp.Or(scanErr, err)
			continue
		}
		project(root, children)
	}
	return uniqueSourceFiles(out), scanErr
}

func uniqueSourceFiles(files []file) []file {
	mains := map[string]file{}
	for _, f := range files {
		if f.main {
			old, ok := mains[f.key]
			if !ok || f.mod.After(old.mod) || f.mod.Equal(old.mod) && f.size > old.size {
				mains[f.key] = f
			}
		}
	}
	out := make([]file, 0, len(files))
	for _, f := range files {
		main, ok := mains[f.key]
		if ok && (f.main && f.path == main.path || !f.main && sourceWithin(strings.TrimSuffix(main.path, ".jsonl"), f.path)) {
			out = append(out, f)
		}
	}
	return out
}

type sourceIdentity struct {
	size  int64
	mod   time.Time
	valid bool
}

var sourceIdentities sync.Map

func sourceTranscript(f file) (bool, error) {
	if old, ok := sourceIdentities.Load(f.path); ok {
		o := old.(sourceIdentity)
		if o.size == f.size && o.mod.Equal(f.mod) {
			return o.valid, nil
		}
	}
	var valid bool
	var decodeErr error
	_, err := scanThin(f.path, 0, nil, thinClaude, func(b []byte, _, end int64) bool {
		var l struct {
			Type    string `json:"type"`
			Message struct {
				Role string `json:"role"`
			} `json:"message"`
		}
		if err := json.Unmarshal(b, &l); err != nil {
			decodeErr = errors.New("invalid session JSONL")
			return false
		}
		valid = (l.Type == "user" || l.Type == "assistant") && l.Message.Role == l.Type
		return !valid && end < glanceBytes
	})
	if err != nil {
		return false, err
	}
	if decodeErr != nil {
		return false, decodeErr
	}
	sourceIdentities.Store(f.path, sourceIdentity{f.size, f.mod, valid})
	return valid, nil
}

func customSourceFiles() []file {
	rows := sourcesForRead()
	var out []file
	for _, s := range rows {
		if Reads(s.Agent) && !sourceCold(s.Path) {
			files := heldFiles("source\x00"+s.Agent+"\x00"+s.Path, func() []file {
				files, _ := sourceFiles(s)
				return files
			})
			out = append(out, files...)
		}
	}
	return uniqueSourceFiles(out)
}

func sourceCold(path string) bool {
	if !strings.HasPrefix(path, `\\`) && !strings.HasPrefix(path, "//") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(strings.ReplaceAll(path, `\`, "/"), "//"), "/")
	if len(parts) < 2 || !strings.EqualFold(parts[0], "wsl.localhost") && !strings.EqualFold(parts[0], "wsl$") {
		return false
	}
	return wslCold(path) || WSLOff != nil && WSLOff() || WSLRunning == nil || !WSLRunning(parts[1])
}

// withCustomSources gives an explicit source's type and read-only policy
// precedence when the same files also appear through a default or linked root.
func withCustomSources(lists [][]file) [][]file {
	rows := sourcesForRead()
	if len(rows) == 0 {
		return lists
	}
	custom := customSourceFiles()
	dirs := map[string]string{}
	key := func(path string) string {
		dir := filepath.Dir(path)
		real, ok := dirs[dir]
		if !ok {
			real, _ = sourceRealPath(dir)
			if real == "" {
				real = dir
			}
			dirs[dir] = real
		}
		out := filepath.Join(real, filepath.Base(path))
		if runtime.GOOS == "windows" {
			out = strings.ToLower(out)
		}
		return out
	}
	seen := map[string]bool{}
	unique := make([]file, 0, len(custom))
	for _, f := range custom {
		k := key(f.path)
		if !seen[k] {
			seen[k] = true
			unique = append(unique, f)
		}
	}
	out := make([][]file, 0, len(lists)+1)
	for _, list := range lists {
		keep := make([]file, 0, len(list))
		for _, f := range list {
			if f.cold || sourceCold(f.path) {
				keep = append(keep, f)
				continue // resolving a stopped WSL distro's UNC path would start it
			}
			path := key(f.path)
			owned := sourceAgent(f.agent) && slices.ContainsFunc(rows, func(s SessionSource) bool { return sourceWithin(s.Path, path) })
			if !seen[path] && !owned {
				keep = append(keep, f)
			}
		}
		out = append(out, keep)
	}
	return append(out, unique)
}
