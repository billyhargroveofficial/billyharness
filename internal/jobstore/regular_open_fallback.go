//go:build !darwin && !linux

package jobstore

import (
	"fmt"
	"os"
)

func openRegularRead(path string) (*os.File, error) {
	return openRegularFallback(path, os.O_RDONLY)
}

func openRegularReadWrite(path string) (*os.File, error) {
	return openRegularFallback(path, os.O_RDWR)
}

func openRegularAppend(path string) (*os.File, error) {
	return openRegularFallback(path, os.O_WRONLY|os.O_APPEND)
}

func openRegularWrite(path string) (*os.File, error) {
	return openRegularFallback(path, os.O_WRONLY)
}

// The fallback performs no I/O before matching the opened handle against a
// fresh Lstat. Darwin and Linux use O_NOFOLLOW above; this path remains safe
// against following a swapped link for reads/writes, though it cannot prevent
// the harmless open itself on platforms without the flag.
func openRegularFallback(path string, flags int) (*os.File, error) {
	file, err := os.OpenFile(path, flags, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: open regular file %s: %w", ErrCorrupt, path, err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("%w: stat opened file %s: %w", ErrCorrupt, path, err)
	}
	current, err := os.Lstat(path)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("%w: restat opened file %s: %w", ErrCorrupt, path, err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		_ = file.Close()
		return nil, fmt.Errorf("%w: file changed while opening %s", ErrCorrupt, path)
	}
	if err := validateOpenedRegular(file, path); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func openOwnershipFile(path string) (*os.File, error) {
	return openRegularFallback(path, os.O_RDWR|os.O_CREATE)
}
