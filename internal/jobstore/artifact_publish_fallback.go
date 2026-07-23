//go:build !darwin && !linux && !windows

package jobstore

import "fmt"

func publishDirectoryNoReplace(string, string) error {
	return fmt.Errorf("atomic no-replace artifact publication is unsupported on this platform")
}

func syncDirectory(string) error {
	return nil
}
