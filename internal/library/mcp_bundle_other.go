//go:build !darwin && !linux && !windows

package library

import (
	"errors"
	"os"
)

func bundleRename(root *os.Root, from, to string) error {
	return errors.New("exclusive directory rename is not supported on this system")
}
