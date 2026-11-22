//go:build !windows

package library

import "os"

// The staging folder is created with mode 0700 on Unix.
func bundlePrivate(root *os.Root) error { return nil }
