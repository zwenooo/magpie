//go:build !windows

package cursorbyok

import "os"

func privateRuntimePermissions(path string) error { return os.Chmod(path, 0o700) }
