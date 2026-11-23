package library

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"slices"

	"github.com/yetone/magpie/internal/filelock"
	"github.com/yetone/magpie/internal/settings"
	"github.com/yetone/magpie/internal/steady"
)

// A sidecar beside (never inside) the carried Library. The last 128 servers
// fit even if every URL byte expands to a six-byte JSON escape.
const (
	maxWebServers = 128
	maxWebURL     = 2048
	maxWebFile    = 5 << 20
)

func webCacheFile() string { return filepath.Join(settings.Dir(), "mcp-web-entries.json") }

// The identity is the Library's original definition, including its local repo
// receipt, NOT here(s), a per-account overlay or a gateway run spec. Never
// put the input bytes (which can contain credentials) into the cache or logs.
func webEntryKey(s *Server) string {
	if s == nil || s.Name == "" {
		return ""
	}
	c := *s
	c.Agents = orNone(c.Agents) // save and the view spell an empty list []
	b, err := json.Marshal(&c)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func safeWebEntries(s *Server, entries []MCPWebEntry) []MCPWebEntry {
	if s == nil || len(entries) == 0 {
		return nil
	}
	var base *url.URL
	local := false
	switch s.Transport {
	case "stdio":
		// checkSides only inspects configuration; it never asks WSL to start.
		local = !remoteLauncher(s.Command) && slices.Contains(checkSides(s), "")
	case "http", "sse":
		base, _ = url.Parse(s.URL)
	default:
		return nil
	}
	// Exactly one of each advertised kind, never arbitrary metadata. A
	// handshake in another namespace already had its narrower policy applied.
	var out []MCPWebEntry
	for _, kind := range []string{"website", "console", "logs"} {
		for _, e := range entries {
			if e.Kind == kind && len(e.URL) <= maxWebURL && allowWebEntry(e.URL, kind, local, base) {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

type webRecord struct {
	Key     string        `json:"key"`
	Entries []MCPWebEntry `json:"entries"`
}
type webRecords []webRecord // oldest first

func readWebRecords() (webRecords, error) {
	f, err := os.Open(webCacheFile())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxWebFile+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxWebFile {
		return nil, errors.New("MCP web entry cache too large")
	}
	var records webRecords
	if err := json.Unmarshal(b, &records); err != nil {
		return nil, err
	}
	if records == nil || len(records) > maxWebServers {
		return nil, errors.New("invalid MCP web entry cache")
	}
	return records, nil
}

func (records webRecords) entries(key string, s *Server) []MCPWebEntry {
	for _, r := range records {
		if r.Key == key && key != "" {
			return safeWebEntries(s, r.Entries)
		}
	}
	return nil
}

func rememberedWebEntries(s *Server) []MCPWebEntry {
	key := webEntryKey(s)
	if key == "" {
		return nil
	}
	records, err := readWebRecords()
	if err != nil || len(records) == 0 {
		return nil
	}
	l, err := load()
	if err != nil || webEntryKey(l.server(s.Name)) != key {
		return nil
	}
	return records.entries(key, s)
}

// A handshake only updates the definition it actually used. A late failure
// (empty entries) is subject to the same check as a new advertisement.
func rememberWebEntries(s *Server, entries []MCPWebEntry) {
	if s != nil {
		updateWebRecords(s, entries)
	}
}

// pruneWebRecords is called after a Library change is saved, so removing and
// re-adding even the very same definition does not revive a deleted address.
func pruneWebRecords() { updateWebRecords(nil, nil) }

func updateWebRecords(s *Server, entries []MCPWebEntry) {
	file := webCacheFile()
	// Before the first handshake there is nothing to prune. Check its stable
	// lock file, not the data file: a writer can already hold the lock and
	// have read the old definition without having published its first entry.
	if s == nil {
		if _, err := os.Stat(file + ".lock"); errors.Is(err, fs.ErrNotExist) {
			return
		}
	}
	unlock, held := filelock.Lock(file+".lock", filelock.Wait)
	if !held {
		log.Print("library: cannot lock MCP web entry cache")
		return
	}
	defer unlock()
	// Both reads must follow the cross-process lock. In particular, pruning
	// from an older sync's snapshot could erase another process's new entry.
	old, err := readWebRecords()
	if err != nil {
		log.Print("library: cannot read MCP web entry cache")
		return
	}
	// An unknown Library is not an empty Library; do not prune on failure.
	l, err := load()
	if err != nil {
		return
	}
	key := webEntryKey(s)
	if s != nil && (key == "" || key != webEntryKey(l.server(s.Name))) {
		return
	}
	current := make(map[string]*Server, len(l.MCP))
	for _, server := range l.MCP {
		if k := webEntryKey(server); k != "" {
			current[k] = server
		}
	}
	next := make(webRecords, 0, len(old)+1)
	for _, r := range old {
		server := current[r.Key]
		if server == nil || r.Key == key {
			continue
		}
		if valid := safeWebEntries(server, r.Entries); len(valid) != 0 {
			next = append(next, webRecord{r.Key, valid})
		}
		delete(current, r.Key)
	}
	if valid := safeWebEntries(s, entries); len(valid) != 0 {
		next = append(next, webRecord{key, valid})
	}
	if len(next) > maxWebServers {
		next = next[len(next)-maxWebServers:]
	}
	if slices.EqualFunc(old, next, func(a, b webRecord) bool { return a.Key == b.Key && slices.Equal(a.Entries, b.Entries) }) {
		return
	}
	data, err := json.Marshal(next)
	if err != nil || len(data) > maxWebFile {
		log.Print("library: cannot encode MCP web entry cache")
		return
	}
	if err := steady.WriteFile(file, data, 0600); err != nil {
		log.Print("library: cannot save MCP web entry cache")
	}
}
