//go:build darwin

package jobstore

import (
	"os"

	"golang.org/x/sys/unix"
)

func publishDirectoryNoReplace(from, to string) error {
	return unix.RenamexNp(from, to, unix.RENAME_EXCL)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
