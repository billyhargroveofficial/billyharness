//go:build windows

package jobstore

import "golang.org/x/sys/windows"

func publishDirectoryNoReplace(from, to string) error {
	fromPointer, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toPointer, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(fromPointer, toPointer, windows.MOVEFILE_WRITE_THROUGH)
}

func syncDirectory(string) error {
	// MoveFileEx with MOVEFILE_WRITE_THROUGH requests durable publication.
	return nil
}
