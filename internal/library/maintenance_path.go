package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/proc"
)

// Check before Clean/Abs/Rel: folding link/../file before following the
// link would authorize a different file from the one the caller named.
func maintenancePathOK(path string) error {
	if path == "" {
		return errors.New("empty path")
	}
	p := filepath.ToSlash(path)
	if strings.HasPrefix(p, "//?/") || strings.HasPrefix(p, "//./") {
		return errors.New("device paths are not supported")
	}
	for _, part := range strings.Split(filepath.ToSlash(path[len(filepath.VolumeName(path)):]), "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." || strings.EqualFold(part, ".git") || (runtime.GOOS == "windows" && (strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `:<>"|?*`) || !filepath.IsLocal(part))) {
			return fmt.Errorf("unsafe path component %q", part)
		}
	}
	return nil
}

func maintenanceDir(path string) (string, error) {
	if err := maintenancePathOK(path); err != nil {
		return "", err
	}
	real, err := proc.RealPath(path)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(real) {
		real, err = filepath.Abs(real)
		if err != nil {
			return "", err
		}
	}
	if err := maintenancePathOK(real); err != nil {
		return "", err
	}
	st, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("not a directory: %s", path)
	}
	return real, nil
}

// Only the leaf may be absent. Lstat distinguishes an absent leaf from
// a present link whose target is missing. RealPath follows NT junctions.
func maintenanceWritePath(root, request string) (string, error) {
	if err := maintenancePathOK(request); err != nil {
		return "", err
	}
	if base := filepath.ToSlash(request); strings.HasSuffix(base, "/") || strings.HasSuffix(base, "/.") {
		return "", errors.New("write path must name a file, not a directory")
	}
	cur := root
	rel := request
	absolute := filepath.IsAbs(request)
	if absolute {
		cur = filepath.VolumeName(request) + string(filepath.Separator)
		var err error
		rel, err = filepath.Rel(cur, request)
		if err != nil {
			return "", err
		}
	}
	if !filepath.IsLocal(rel) || filepath.Clean(rel) == "." {
		return "", errors.New("write path must be a file under the maintenance root")
	}
	entered := !absolute
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	for i, part := range parts {
		next := filepath.Join(cur, part)
		_, err := os.Lstat(next)
		if i == len(parts)-1 && errors.Is(err, os.ErrNotExist) && within(cur, root) {
			return next, nil
		}
		if err != nil {
			return "", err
		}
		cur, err = proc.RealPath(next)
		if err != nil {
			return "", err
		}
		inside := within(cur, root)
		if !inside && (entered || i == len(parts)-1) {
			return "", fmt.Errorf("path leaves maintenance root: %s", next)
		}
		entered = inside
		if err := maintenancePathOK(cur); err != nil {
			return "", err
		}
		st, err := os.Stat(cur)
		if err != nil {
			return "", err
		}
		if i == len(parts)-1 && !st.Mode().IsRegular() || i < len(parts)-1 && !st.IsDir() {
			return "", fmt.Errorf("not a source file path: %s", next)
		}
	}
	n, err := edit.LinkCount(cur)
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", fmt.Errorf("write target has multiple or unknown hard links: %s", cur)
	}
	return cur, nil
}

// Discover the nearest marker on the physical path, then have Git itself
// validate it, including gitfiles used by linked worktrees. rev-parse does
// not run hooks, filters, package managers, or the program being inspected.
func maintenanceCheckout(root string) error {
	for dir := root; ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, marker)); err == nil {
			return fmt.Errorf("agent installation copy: %s", dir)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			git := "git"
			if runtime.GOOS == "windows" {
				git = "git.exe"
			}
			exe, err := proc.Look(git)
			if err != nil {
				return err
			}
			if !filepath.IsAbs(exe) {
				return errors.New("git executable must be on an absolute PATH")
			}
			exe, err = proc.RealPath(exe)
			if err != nil {
				return err
			}
			if within(exe, dir) {
				return errors.New("refusing to execute git from the checkout")
			}
			cmd := proc.Command(exe, "-C", dir, "rev-parse", "--show-toplevel")
			// Ignore ambient GIT_DIR, GIT_WORK_TREE, configuration injection and
			// tracing. No inherited repository may vouch for the requested path.
			for _, v := range os.Environ() {
				if !strings.HasPrefix(strings.ToUpper(v), "GIT_") {
					cmd.Env = append(cmd.Env, v)
				}
			}
			cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_OPTIONAL_LOCKS=0")
			b, err := cmd.Output()
			if err != nil {
				return fmt.Errorf("not a readable Git worktree at %s: %w", dir, err)
			}
			top, err := maintenanceDir(strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r"))
			if err != nil {
				return err
			}
			if !sameDir(top, dir) {
				return fmt.Errorf("Git worktree root does not match %s", dir)
			}
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if dir == filepath.Dir(dir) {
			return errors.New("no Git worktree contains the maintenance directory")
		}
	}
}
