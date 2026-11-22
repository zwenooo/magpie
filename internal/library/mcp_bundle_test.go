package library

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/proc"
)

func bundleTestArchive(t *testing.T, names ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, n := range names {
		h := &tar.Header{Name: n, Mode: 0600, Size: 4}
		switch n {
		case "secrets/link":
			h.Typeflag = tar.TypeSymlink
			h.Linkname = "../outside"
			h.Size = 0
		case "secrets/hardlink":
			h.Typeflag = tar.TypeLink
			h.Linkname = "config.yaml"
			h.Size = 0
		case "secrets/directory":
			h.Typeflag = tar.TypeDir
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size != 0 {
			if _, err := tw.Write([]byte("demo")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestMCPBundleTar(t *testing.T) {
	raw := bundleTestArchive(t, "config.yaml", "secrets/nested/test")
	raw = append(raw, make([]byte, 10240-len(raw))...)
	files, err := bundleTar(raw)
	if err != nil || string(files["config.yaml"]) != "demo" || string(files["secrets/nested/test"]) != "demo" {
		t.Fatal("cannot read Python-style padded tar", err)
	}
	for _, names := range [][]string{{"secrets/no-config"}, {"config.yaml", "config.yaml"}, {"config.yaml", "secrets/../../outside"}, {"config.yaml", "/config.yaml"}, {"config.yaml", "other"}, {"config.yaml", "secrets/a:b"}, {"config.yaml", `secrets/a\b`}, {"config.yaml", "secrets/CON"}, {"config.yaml", "secrets/x", "secrets/X"}, {"config.yaml", "secrets/x/a", "secrets/X/b"}, {"config.yaml", "secrets/x", "secrets/x/a"}, {"config.yaml", "secrets/x/a", "secrets/x"}, {"config.yaml", "secrets/link"}, {"config.yaml", "secrets/hardlink"}, {"config.yaml", "secrets/directory"}} {
		if _, err := bundleTar(bundleTestArchive(t, names...)); !errors.Is(err, errBundleInvalid) {
			t.Errorf("accepted %q", names)
		}
	}
	many := []string{"config.yaml"}
	for i := 0; i < 512; i++ {
		many = append(many, fmt.Sprintf("secrets/%d", i))
	}
	for _, bad := range [][]byte{raw[:512], append(append([]byte{}, raw...), raw...), make([]byte, bundleLimit+512), bundleTestArchive(t, many...)} {
		if _, err := bundleTar(bad); err == nil {
			t.Error("accepted oversized, truncated or concatenated tar")
		}
	}
}

func bundleServer() Server {
	src := &MCPRepoSource{Repo: "https://github.com/zwenooo/magpie-agent-toolkit", Ref: "main", Path: "mcp/ops-mcp"}
	return Server{Name: "ops", RepoSource: src, RepoLocalSource: src, RepoCommit: repoTestSHA, RepoLocalCommit: repoTestSHA, Transport: "stdio", Command: "python3", Args: []string{programRef + "/server.py"}, Env: map[string]string{"OPS_MCP_STATE_DIR": stateRef, "PYTHONDONTWRITEBYTECODE": "1"}}
}
func TestMCPBundleScope(t *testing.T) {
	home := sandbox(t)
	s := bundleServer()
	if !bundleStart(&s, home) {
		t.Fatal("default managed launch not supported")
	}
	for _, mutate := range []func(*Server){func(s *Server) { s.RepoSource.Repo = "https://github.com/other/magpie-agent-toolkit" }, func(s *Server) { s.RepoSource.Path = "mcp/other" }, func(s *Server) { s.Command = "sh" }, func(s *Server) { s.Args = append(s.Args, "--config=x") }, func(s *Server) { s.Env["ops_mcp_bundle_key"] = "other" }, func(s *Server) { s.Env["OPS_MCP_CONFIG"] = "../other" }, func(s *Server) { s.Env["HOME"] = "other" }} {
		s = bundleServer()
		mutate(&s)
		if bundleStart(&s, home) {
			t.Error("accepted an unsupported source or start")
		}
	}
	t.Setenv("OPS_MCP_CONFIG", "other")
	s = bundleServer()
	if bundleStart(&s, home) {
		t.Fatal("followed custom process config")
	}
}

func TestMCPBundleLocalAge(t *testing.T) {
	// Optional interoperability check with a locally installed age: generate a
	// disposable identity here, never read or copy the user's real identity.
	exe := "age"
	gen := "age-keygen"
	if runtime.GOOS == "windows" {
		exe += ".exe"
		gen += ".exe"
	}
	tool, err := proc.Look(exe)
	if err != nil {
		for _, gp := range filepath.SplitList(os.Getenv("GOPATH")) {
			p := filepath.Join(gp, "bin", exe)
			if filepath.IsAbs(gp) {
				if _, e := os.Stat(p); e == nil {
					tool = p
					break
				}
			}
		}
	}
	if tool == "" {
		t.Skip("local age not installed")
	}
	keygen := filepath.Join(filepath.Dir(tool), gen)
	if _, err := os.Stat(keygen); err != nil {
		t.Skip("local age-keygen not installed")
	}
	h := sandbox(t)
	t.Setenv("PATH", filepath.Dir(tool))
	config := filepath.Join(h, ".config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(config, "ops-mcp-secrets-age.key")
	if err := proc.Command(keygen, "-o", key).Run(); err != nil {
		t.Fatal("sandbox key generation:", err)
	}
	recipient, err := proc.Command(keygen, "-y", key).Output()
	if err != nil {
		t.Fatal("sandbox public recipient:", err)
	}
	// Literal output of Python tarfile.open(mode="w"), TarInfo(mode=0600),
	// addfile(..., BytesIO(b"demo")) for config.yaml and secrets/nested/example.
	// gzip is only for storing the fixture; age encrypts the original tar.
	packed, err := base64.StdEncoding.DecodeString("H4sIAAAAAAACCu3TQQqDQAyF4RylJ9BUS3ueQdMiOFqcKejtHV0JXXRVXfh/mxdCIKtX9d2zeWWT8638iyZ31TX1O5PbZk7763IvF5UdfEJ0Q3ov51Sb7wWnFawaLIa8sxCtzm10/t3a0f0vivJB/+k/AAAAAAAAAAAAAAAAfpkBhMODYAAoAAA=")
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	encrypt := func() []byte {
		cmd := proc.Command(tool, "-r", strings.TrimSpace(string(recipient)))
		cmd.Stdin = bytes.NewReader(raw)
		cipher, e := cmd.Output()
		if e != nil {
			t.Fatal(e)
		}
		return cipher
	}
	s := bundleServer()
	cipher := encrypt()
	install := func(commit string) {
		_, err := applyMCPRepo(s.Name, s.RepoSource, commit, func(MCPRepoSource, string) (map[string][]byte, []string, error) {
			return map[string][]byte{"server.py": []byte("not a runnable server"), opsBundleFile: cipher}, nil, nil
		}, (*Library).save)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := (&Library{MCP: []*Server{{Name: s.Name, Command: s.Command, Transport: s.Transport, Args: s.Args, Env: s.Env}}}).save(); err != nil {
		t.Fatal(err)
	}
	install(repoTestSHA)
	preview := func(want string) *MCPBundleStatus {
		t.Helper()
		st, err := PreviewMCPBundle(s.Name)
		if err != nil || st.State != want {
			t.Fatalf("preview %s: %+v, %v", want, st, err)
		}
		b, _ := json.Marshal(st)
		if bytes.Contains(b, []byte("demo")) || bytes.Contains(b, []byte("example")) {
			t.Fatal("preview disclosed bundle contents")
		}
		return st
	}
	dir := filepath.Join(config, "ops-mcp")
	before, err := os.ReadDir(config)
	if err != nil {
		t.Fatal(err)
	}
	status := preview("missing")
	after, err := os.ReadDir(config)
	if err != nil || len(before) != len(after) {
		t.Fatal("preview created output")
	}
	if len(status.Token) != 64 {
		t.Fatal("missing confirmation token")
	}
	if _, err := RestoreMCPBundle(s.Name, ""); err == nil {
		t.Fatal("restored without consent")
	}
	cipher = encrypt()
	install(strings.Repeat("d", 40))
	if _, err := RestoreMCPBundle(s.Name, status.Token); err == nil {
		t.Fatal("stale confirmation accepted")
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("wrote without fresh approval")
	}
	status = preview("missing")
	st, err := RestoreMCPBundle(s.Name, status.Token)
	if err != nil || st.State != "ready" {
		t.Fatalf("restore: %+v %v", st, err)
	}
	preview("ready")
	if got := read(t, filepath.Join(dir, "secrets/nested/example")); got != "demo" {
		t.Fatal("wrong restored file")
	}
	cfg := filepath.Join(dir, "config.yaml")
	info, err := os.Stat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = RestoreMCPBundle(s.Name, status.Token)
	next, e := os.Stat(cfg)
	if err != nil || e != nil || !os.SameFile(info, next) || info.ModTime() != next.ModTime() {
		t.Fatal("rewrote matching config")
	}
	write(t, cfg, "local")
	preview("conflict")
	st, err = RestoreMCPBundle(s.Name, status.Token)
	if err != nil || st.State != "conflict" || read(t, cfg) != "local" {
		t.Fatal("overwrote conflict")
	}
	if err = os.Remove(key); err != nil {
		t.Fatal(err)
	}
	preview("locked")
	write(t, filepath.Join(programDir(s.Name), "server.py"), "edited")
	if _, err = PreviewMCPBundle(s.Name); !errors.Is(err, errBundleChanged) {
		t.Fatal("trusted edited installation")
	}
}

func TestMCPBundleNoReceipt(t *testing.T) {
	sandbox(t)
	s := bundleServer()
	s.RepoLocalSource = nil
	if err := (&Library{MCP: []*Server{&s}}).save(); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewMCPBundle(s.Name); err == nil || !strings.Contains(err.Error(), "repo-update") {
		t.Fatal("legacy metadata was accepted")
	}
	status, err := PreviewMCPBundle("different")
	if err != nil || status.State != "unsupported" || status.Token != "" {
		t.Fatal("unknown server eligible")
	}
}

func TestMCPBundleFilesystemLinks(t *testing.T) {
	parent := t.TempDir()
	outside := t.TempDir()
	write(t, filepath.Join(outside, "config.yaml"), "same")
	root, err := os.OpenRoot(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	files := map[string][]byte{"config.yaml": []byte("same"), "secrets/key": []byte("same")}
	if err := dirLink(outside, filepath.Join(parent, "ops-mcp")); err != nil {
		t.Fatal(err)
	}
	if st, err := bundleCompare(root, files); err != nil || st != "conflict" {
		t.Fatal("accepted linked destination", st, err)
	}
	if e := bundlePlainDir(filepath.Join(parent, "ops-mcp")); e == nil {
		t.Fatal("accepted link in receipt or identity path")
	}
	if err := os.Remove(filepath.Join(parent, "ops-mcp")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(parent, "ops-mcp", "config.yaml"), "same")
	write(t, filepath.Join(outside, "key"), "same")
	if err := dirLink(outside, filepath.Join(parent, "ops-mcp", "secrets")); err != nil {
		t.Fatal(err)
	}
	if st, err := bundleCompare(root, files); err != nil || st != "conflict" {
		t.Fatal("accepted linked secrets", st, err)
	}
	if err := dirLink(outside, filepath.Join(parent, "ops-mcp-secrets-age.key")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := bundleDecrypt(root, nil); err == nil {
		t.Fatal("accepted linked identity")
	}
}

func TestMCPBundleExclusivePublish(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err = root.Mkdir("ops-mcp", 0700); err != nil {
		t.Fatal(err)
	}
	info, _ := root.Stat("ops-mcp")
	if err = bundlePublish(root, map[string][]byte{"config.yaml": []byte("demo")}); err == nil {
		t.Fatal("replaced a racing empty directory")
	}
	after, e := root.Stat("ops-mcp")
	entries, e2 := fs.ReadDir(root.FS(), ".")
	if e != nil || e2 != nil || len(entries) != 1 || !os.SameFile(info, after) {
		t.Fatal("publication did not preserve destination and clean its stage")
	}
	if _, err = root.Stat("ops-mcp/config.yaml"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("publication wrote inside existing directory")
	}
}
