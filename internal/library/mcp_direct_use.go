package library

import (
	"maps"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/settings"
)

const directUseDB = "MAGPIE_DIRECT_USAGE_DB"
const directUseItem = "MAGPIE_DIRECT_USAGE_ITEM"
const directUseAgent = "MAGPIE_DIRECT_USAGE_AGENT"

// Use only libraryuse.product's fixed buckets. In particular a Claude Code
// config folder's suffix is user data, not a separate product.
func directProduct(id string) string {
	switch {
	case strings.HasPrefix(id, "claude#"):
		return "claude"
	case id == agent.DesktopTwinID:
		return "claude-desktop"
	}
	switch id {
	case "claude", "codex", "cursor", "gemini", "opencode", "claude-desktop", "kiro-cli", "copilot", "droid", "pi", "magpie":
		return id
	}
	return "other"
}

// supportsDirectUse takes the library definition, before here expands it. The
// local receipt binds the install to the source, not merely to the server name
// (putServers can retain a same-named installation across a source change).
func supportsDirectUse(s *Server) bool {
	if !opsProgram(s) || s.RepoLocalSource == nil || *s.RepoLocalSource != *s.RepoSource {
		return false
	}
	if !repoSHA(s.RepoLocalCommit) || s.RepoLocalCommit != s.RepoCommit || s.RepoLocalHash == "" || s.RepoLocalHash != s.RepoHash {
		return false
	}
	if !filepath.IsLocal(s.Name) || checkName("server", s.Name) != nil {
		return false
	}
	info, err := os.Lstat(filepath.Join(programDir(s.Name), "server.py"))
	return err == nil && info.Mode().IsRegular()
}

func directUseKey(k string) bool {
	return strings.EqualFold(k, directUseDB) || strings.EqualFold(k, directUseItem) || strings.EqualFold(k, directUseAgent)
}

// withoutDirectUse leaves ordinary environments alone, including nil. Never
// edit a map owned by the library, a synced start or another agent's entry.
func withoutDirectUse[V any](env map[string]V) map[string]V {
	for k := range env {
		if directUseKey(k) {
			clean := maps.Clone(env)
			maps.DeleteFunc(clean, func(k string, _ V) bool { return directUseKey(k) })
			return clean
		}
	}
	return env
}

func (l *Library) stripDirectUse() {
	for _, servers := range [][]*Server{l.MCP, l.Away} {
		for _, s := range servers {
			s.Env = withoutDirectUse(s.Env)
			if s.Shared != nil {
				s.Shared.Env = withoutDirectUse(s.Shared.Env)
			}
		}
	}
}

func directUseLocalPath(path string) bool {
	return filepath.IsAbs(path) && !strings.HasPrefix(filepath.ToSlash(path), "//") && !strings.HasPrefix(filepath.ToSlash(realBelow(path)), "//")
}

// Only a known local destination may receive the database path. In particular
// a remote gateway with no sharing key is not evidence for a local target.
func directUseLocalBase(base string) bool {
	u, err := url.Parse(base)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && (strings.EqualFold(u.Hostname(), "localhost") || net.ParseIP(u.Hostname()).IsLoopback())
}

// nativeDirectUse is the last step after routing: only a native command can
// receive this overlay, never an HTTP replacement, check or gateway spec.
func nativeDirectUse(raw, given *Server, base string, file *mcpFile, id string) *Server {
	if given.Transport != "stdio" || file.WSL || !supportsDirectUse(raw) || !directUseLocalBase(base) {
		return given
	}
	if !allLocalDirectFiles(file.files()) {
		return given
	}
	c := *given
	c.Env = maps.Clone(given.Env)
	if c.Env == nil {
		c.Env = map[string]string{}
	}
	c.Env[directUseDB] = filepath.Join(settings.Dir(), "library-use", "ops-direct.db")
	c.Env[directUseItem] = raw.Name
	c.Env[directUseAgent] = directProduct(id)
	return &c
}

func allLocalDirectFiles(paths []string) bool {
	for _, p := range paths {
		if !directUseLocalPath(p) {
			return false
		}
	}
	return true
}

// Gateway and probe children must not inherit a direct client's markers,
// whether they came from the parent process or the launch environment.
func mcpChildEnv(env []string) []string {
	return slices.DeleteFunc(env, func(e string) bool { k, _, _ := strings.Cut(e, "="); return directUseKey(k) })
}
