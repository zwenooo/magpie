package edit

import (
	"os"

	"golang.org/x/sys/windows"
)

// hardLinked reports whether the file at path has other names too.
func hardLinked(path string) bool {
	n, err := LinkCount(path)
	return err == nil && n > 1
}

// LinkCount returns the number of names for an existing regular file.
func LinkCount(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var fi windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &fi)
	return uint64(fi.NumberOfLinks), err
}

// keepGroup: Windows files have no Unix group to keep.
func keepGroup(*os.File, os.FileInfo) {}
