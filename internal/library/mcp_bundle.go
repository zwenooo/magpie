package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

// MCPBundleStatus describes the default local credential directory, not whether
// the server can sign in. Only a missing directory is eligible for restore.
type MCPBundleStatus struct {
	State string `json:"state"`
	Token string `json:"token,omitempty"`
}

var errBundleChanged = errors.New("the confirmed local installation or bundle changed; preview again")
var errBundleInvalid = errors.New("invalid or oversized ops-mcp bundle")
var errBundleTarget = errors.New("cannot safely access the local ops-mcp directory")

const opsBundleFile = "secrets.tar.age"
const bundleLimit = 8 << 20

// opsProgram identifies the standalone repository and its native entry point,
// independently of the credential bundle's stricter environment requirements.
func opsProgram(s *Server) bool {
	if s == nil || s.RepoSource == nil || s.Transport != "stdio" || s.Bundle != "" || s.Plugin != "" || s.Bridge != "" {
		return false
	}
	repo, _, err := s.RepoSource.coordinates()
	if err != nil || repo != "zwenooo/ops-mcp" || s.RepoSource.Path != "" {
		return false
	}
	command := strings.ToLower(filepath.Base(s.Command))
	switch command {
	case "python", "python3", "python.exe", "python3.exe":
	default:
		return false
	}
	if len(s.Args) != 1 || filepath.ToSlash(s.Args[0]) != programRef+"/server.py" {
		return false
	}
	return true
}

func bundleStart(s *Server, home string) bool {
	if !opsProgram(s) {
		return false
	}
	// Don't expand a synced variable or follow a custom config, home or identity.
	defaults := map[string]string{"OPS_MCP_CONFIG": ".config/ops-mcp/config.yaml", "OPS_MCP_BUNDLE_KEY": ".config/ops-mcp-secrets-age.key"}
	matches := func(value, rel string) bool {
		return value == "~/"+rel || filepath.FromSlash(value) == filepath.Join(home, filepath.FromSlash(rel))
	}
	for key, rel := range defaults {
		if value, ok := os.LookupEnv(key); ok && !matches(value, rel) {
			return false
		}
	}
	for key, value := range s.Env {
		key = strings.ToUpper(key)
		if key == "OPS_MCP_STATE_DIR" && value == stateRef || key == "PYTHONDONTWRITEBYTECODE" && value == "1" {
			continue
		}
		rel, ok := defaults[key]
		if !ok || !matches(value, rel) {
			return false
		}
	}
	return true
}

// bundleInstall is called under the library lock. The hash helper captures
// exactly the ciphertext whose bytes it includes in the installation receipt.
func bundleInstall(name, home string) (data []byte, token string, err error) {
	if checkName("server", name) != nil || !filepath.IsLocal(name) {
		return nil, "", nil
	}
	lib, err := load()
	if err != nil {
		return nil, "", errors.New("cannot read the local library")
	}
	s := lib.server(name)
	if !bundleStart(s, home) {
		return nil, "", nil
	}
	if s.RepoLocalSource == nil || *s.RepoLocalSource != *s.RepoSource || !repoSHA(s.RepoLocalCommit) || s.RepoLocalCommit != s.RepoCommit || s.RepoLocalHash == "" || s.RepoLocalHash != s.RepoHash {
		return nil, "", errors.New("confirm the current repository commit with a local repo-update before previewing its bundle")
	}
	dir := programDir(name)
	if err := bundlePlainDir(dir); err != nil {
		return nil, "", errBundleChanged
	}
	var total int64
	count := 0
	hash, err := repoProgramHashWith(dir, func(rel string, size int64, r io.Reader) error {
		count++
		if count > 8192 || size < 0 || size > maxCarried-total {
			return errBundleInvalid
		}
		total += size
		if rel == opsBundleFile {
			var e error
			data, e = repoRead(r, bundleLimit)
			return e
		}
		return nil
	})
	if err != nil || hash != s.RepoLocalHash {
		return nil, "", errBundleChanged
	}
	if !bytes.HasPrefix(data, []byte("age-encryption.org/v1\n")) {
		return nil, "", errBundleInvalid
	}
	token = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("ops-mcp/1\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%x", name, home, s.RepoSource.Repo, s.RepoSource.Path, s.RepoSource.Ref, s.RepoLocalCommit, hash, sha256.Sum256(data)))))
	return data, token, nil
}

// PreviewMCPBundle is an explicit local operation. Never call it while polling.
func PreviewMCPBundle(name string) (*MCPBundleStatus, error) { return useMCPBundle(name, "", false) }

// RestoreMCPBundle requires the token the user approved in a previous preview.
func RestoreMCPBundle(name, expected string) (*MCPBundleStatus, error) {
	if expected == "" {
		return nil, errBundleChanged
	}
	return useMCPBundle(name, expected, true)
}

func useMCPBundle(name, expected string, restore bool) (*MCPBundleStatus, error) {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return nil, errBundleTarget
	}
	mu.Lock()
	cipher, token, err := bundleInstall(name, home)
	mu.Unlock()
	if err != nil {
		return nil, err
	}
	if token == "" {
		return &MCPBundleStatus{State: "unsupported"}, nil
	}
	if restore && expected != token {
		return nil, errBundleChanged
	}
	parent := filepath.Join(home, ".config")
	if err = bundlePlainDir(parent); errors.Is(err, fs.ErrNotExist) {
		return &MCPBundleStatus{State: "locked"}, nil
	} else if err != nil {
		return nil, errBundleTarget
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return nil, errBundleTarget
	}
	defer root.Close()
	raw, locked, err := bundleDecrypt(root, cipher)
	if err != nil {
		return nil, err
	}
	if locked {
		return &MCPBundleStatus{State: "locked"}, nil
	}
	defer clear(raw)
	files, err := bundleTar(raw)
	if err != nil {
		return nil, err
	}
	// A decrypt may have taken a while. Recheck the receipt before reporting or
	// writing; installation changes in this process use the same lock.
	mu.Lock()
	defer mu.Unlock()
	_, now, err := bundleInstall(name, home)
	if err != nil || now != token {
		return nil, errBundleChanged
	}
	state, err := bundleCompare(root, files)
	if err != nil {
		return nil, err
	}
	if restore && state == "missing" {
		if err = bundlePublish(root, files); err != nil {
			return nil, err
		}
		state = "ready"
	}
	result := &MCPBundleStatus{State: state}
	if state == "missing" || state == "ready" {
		result.Token = token
	}
	return result, nil
}

// A repository and its env/command never select the executable, identity or
// decryptor environment. Only the ordinary native X25519 identity is supported.
func bundleDecrypt(root *os.Root, cipher []byte) ([]byte, bool, error) {
	const identity = "ops-mcp-secrets-age.key"
	key, err := bundleRead(root, identity, 64<<10)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, errors.New("cannot safely read the default local age identity")
	}
	defer clear(key)
	native := false
	for line := range bytes.Lines(key) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		if !bytes.HasPrefix(line, []byte("AGE-SECRET-KEY-1")) {
			return nil, true, nil
		}
		native = true
	}
	if !native {
		return nil, true, nil
	}
	exe := "age"
	if runtime.GOOS == "windows" {
		exe = "age.exe"
	}
	tool, err := proc.Look(exe)
	if err != nil {
		tool = ""
		for _, gp := range filepath.SplitList(os.Getenv("GOPATH")) {
			candidate := filepath.Join(gp, "bin", exe)
			if filepath.IsAbs(gp) {
				if info, e := os.Stat(candidate); e == nil && info.Mode().IsRegular() {
					tool = candidate
					break
				}
			}
		}
	}
	if tool == "" || !filepath.IsAbs(tool) {
		return nil, true, nil
	}
	tool, err = filepath.EvalSymlinks(tool)
	if err != nil {
		return nil, true, nil
	}
	// Even if a PATH entry points into the managed library, do not execute it.
	if rel, e := filepath.Rel(Dir(), tool); e == nil && (rel == "." || filepath.IsLocal(rel)) {
		return nil, false, errors.New("age must be installed outside the managed library")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := proc.CommandContext(ctx, tool, "-d", "-i", filepath.Join(root.Name(), identity), "-")
	cmd.Dir = root.Name()
	cmd.Env = []string{"PATH="}
	if runtime.GOOS == "windows" {
		cmd.Env = append(cmd.Env, "SystemRoot="+os.Getenv("SystemRoot"))
	}
	cmd.Stdin = bytes.NewReader(cipher)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, errors.New("cannot prepare age")
	}
	if err = cmd.Start(); err != nil {
		return nil, false, errors.New("cannot start age")
	}
	raw, readErr := repoRead(out, bundleLimit)
	if readErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, false, errBundleInvalid
	}
	if waitErr != nil {
		clear(raw)
		return nil, true, nil
	}
	return raw, false, nil
}
