//go:build darwin || linux

package jobstore

import (
	"fmt"
	"io/fs"
	"syscall"
)

func validateRegularFilePlatform(info fs.FileInfo, path string) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%w: cannot inspect link count for %s", ErrCorrupt, path)
	}
	if stat.Nlink != 1 {
		return fmt.Errorf("%w: refusing multiply-linked store file %s", ErrCorrupt, path)
	}
	return nil
}
