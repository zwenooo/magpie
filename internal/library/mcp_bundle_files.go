package library

import (
	"archive/tar"
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"os"
	tarpath "path"
	"path/filepath"
	"strings"

	"golang.org/x/text/cases"
)

// secrets_bundle.py's to_tar emits regular files in an uncompressed tar;
// Python pads its end with zeros. Keep slices of the bounded plaintext, not a
// second copy. Reject concatenated/truncated archives, aliases and collisions.
func bundleTar(raw []byte) (map[string][]byte, error) {
	if len(raw) > bundleLimit || len(raw)%512 != 0 {
		return nil, errBundleInvalid
	}
	r := bytes.NewReader(raw)
	tr := tar.NewReader(r)
	files := map[string][]byte{}
	paths := map[string]string{}
	fold := cases.Fold()
	for {
		before := len(raw) - r.Len()
		h, err := tr.Next()
		if err == io.EOF {
			start := (before + 511) / 512 * 512
			if len(raw)-start < 1024 || len(bytes.Trim(raw[start:], "\x00")) != 0 {
				return nil, errBundleInvalid
			}
			break
		}
		if err != nil {
			return nil, errBundleInvalid
		}
		n := h.Name
		if h.Typeflag != tar.TypeReg || h.Linkname != "" || h.Size < 0 || h.Size > int64(r.Len()) || len(files) >= 512 || !repoPortablePath(n) || n != "config.yaml" && !strings.HasPrefix(n, "secrets/") {
			return nil, errBundleInvalid
		}
		for k := range h.PAXRecords {
			if k != "path" {
				return nil, errBundleInvalid
			}
		}
		// Implicit directories are checked too: secrets/A/x and secrets/a/y
		// cannot silently become the same directory on another filesystem.
		if _, ok := paths[fold.String(n)]; ok {
			return nil, errBundleInvalid
		}
		for p := n; p != "."; p = tarpath.Dir(p) {
			key := fold.String(p)
			if prev, ok := paths[key]; ok && (prev != p || files[p] != nil) {
				return nil, errBundleInvalid
			}
			paths[key] = p
		}
		start := len(raw) - r.Len()
		files[n] = raw[start : start+int(h.Size)]
		if _, err = io.Copy(io.Discard, tr); err != nil {
			return nil, errBundleInvalid
		}
	}
	if _, ok := files["config.yaml"]; !ok {
		return nil, errBundleInvalid
	}
	return files, nil
}

func bundlePlainDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return errBundleTarget
	}
	if parent := filepath.Dir(dir); parent != dir {
		if err := bundlePlainDir(parent); err != nil {
			return err
		}
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errBundleTarget
	}
	return nil
}

// Never open a link, directory or special file as data. Root also confines
// traversal; the opened handle must still identify the checked regular file.
func bundleRead(root *os.Root, rel string, limit int64) ([]byte, error) {
	info, err := root.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errBundleTarget
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(info, after) {
		return nil, errBundleTarget
	}
	return repoRead(f, limit)
}

func bundleCompare(parent *os.Root, files map[string][]byte) (string, error) {
	const dest = "ops-mcp"
	info, err := parent.Lstat(dest)
	if errors.Is(err, fs.ErrNotExist) {
		return "missing", nil
	}
	if err != nil {
		return "", errBundleTarget
	}
	if !info.IsDir() {
		return "conflict", nil
	}
	root, err := parent.OpenRoot(dest)
	if err != nil {
		return "", errBundleTarget
	}
	defer root.Close()
	ok, err := root.Stat(".")
	if err != nil || !os.SameFile(info, ok) {
		return "", errBundleTarget
	}
	for name, want := range files {
		for dir := tarpath.Dir(name); dir != "."; dir = tarpath.Dir(dir) {
			d, e := root.Lstat(filepath.FromSlash(dir))
			if errors.Is(e, fs.ErrNotExist) || e == nil && !d.IsDir() {
				return "conflict", nil
			}
			if e != nil {
				return "", errBundleTarget
			}
		}
		got, e := bundleRead(root, filepath.FromSlash(name), int64(len(want)))
		if e != nil {
			if errors.Is(e, fs.ErrNotExist) || errors.Is(e, errBundleTarget) {
				return "conflict", nil
			}
			return "", errBundleTarget
		}
		same := bytes.Equal(got, want)
		clear(got)
		if !same {
			return "conflict", nil
		}
	}
	return "ready", nil
}

// Stage privately on the destination filesystem, then publish the whole tree
// with an exclusive rename. Even a racing empty directory is never replaced.
func bundlePublish(parent *os.Root, files map[string][]byte) error {
	stage := ".ops-mcp-" + rand.Text()
	if err := parent.Mkdir(stage, 0700); err != nil {
		return errBundleTarget
	}
	defer parent.RemoveAll(stage)
	root, err := parent.OpenRoot(stage)
	if err != nil {
		return errBundleTarget
	}
	defer root.Close()
	if err = bundlePrivate(root); err != nil {
		return errBundleTarget
	}
	for name, b := range files {
		rel := filepath.FromSlash(name)
		if err = root.MkdirAll(filepath.Dir(rel), 0700); err != nil {
			return errBundleTarget
		}
		f, e := root.OpenFile(rel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return errBundleTarget
		}
		_, e = f.Write(b)
		if err = errors.Join(e, f.Close()); err != nil {
			return errBundleTarget
		}
	}
	// Windows cannot move a directory while it is open without delete sharing.
	if err = root.Close(); err != nil {
		return errBundleTarget
	}
	if err = bundleRename(parent, stage, "ops-mcp"); err != nil {
		return errBundleTarget
	}
	return nil
}
