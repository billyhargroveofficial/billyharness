//go:build darwin || linux

package jobstore

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openRegularRead(path string) (*os.File, error) {
	return openRegularUnix(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW)
}

func openRegularReadWrite(path string) (*os.File, error) {
	return openRegularUnix(path, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW)
}

func openRegularAppend(path string) (*os.File, error) {
	return openRegularUnix(path, unix.O_WRONLY|unix.O_APPEND|unix.O_CLOEXEC|unix.O_NOFOLLOW)
}

func openRegularWrite(path string) (*os.File, error) {
	return openRegularUnix(path, unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW)
}

func openRegularUnix(path string, flags int) (*os.File, error) {
	fd, err := unix.Open(path, flags, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: open regular file %s: %w", ErrCorrupt, path, err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("%w: wrap regular file %s", ErrCorrupt, path)
	}
	if err := validateOpenedRegular(file, path); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func openOwnershipFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, fmt.Errorf("%w: %w: ownership path is a symlink", ErrOwnership, ErrCorrupt)
		}
		return nil, fmt.Errorf("%w: open ownership file %s: %w", ErrOwnership, path, err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("%w: wrap ownership file %s", ErrOwnership, path)
	}
	if err := validateOpenedRegular(file, path); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
