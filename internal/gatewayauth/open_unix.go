//go:build darwin || linux

package gatewayauth

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func openRegularNoFollow(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open secure file %s: %w", path, err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("wrap secure file %s", path)
	}
	return file, nil
}

func lockTokenStore(dir string) (*os.File, error) {
	path := dir + string(os.PathSeparator) + ".gateway.token.lock"
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open gateway token store lock %s: %w", path, err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("wrap gateway token store lock %s", path)
	}
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock gateway token store %s: %w", path, err)
	}
	if err := validateOpenedRegular(file, path, false); err != nil {
		_ = unix.Flock(fd, unix.LOCK_UN)
		_ = file.Close()
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = unix.Flock(fd, unix.LOCK_UN)
		_ = file.Close()
		return nil, fmt.Errorf("secure gateway token store lock %s: %w", path, err)
	}
	if err := validateOpenedRegular(file, path, true); err != nil {
		_ = unix.Flock(fd, unix.LOCK_UN)
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func unlockTokenStore(file *os.File) error {
	if file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN)
	closeErr := file.Close()
	if unlockErr != nil {
		return fmt.Errorf("unlock gateway token store: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close gateway token store lock: %w", closeErr)
	}
	return nil
}

func validateRegularPlatform(info fs.FileInfo, path string, requirePrivate bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot inspect secure file link count at %s", path)
	}
	if stat.Nlink != 1 {
		return fmt.Errorf("refusing multiply-linked secure file at %s", path)
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("secure file at %s is not owned by the current user", path)
	}
	if requirePrivate && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("dedicated gateway token permissions at %s are not owner-only", path)
	}
	return nil
}

func validateDirectoryPlatform(info fs.FileInfo, path string, requirePrivate bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot inspect secure directory ownership at %s", path)
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("secure directory at %s is not owned by the current user", path)
	}
	if requirePrivate && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("gateway auth directory permissions at %s are not owner-only", path)
	}
	return nil
}

func syncDirectory(path string) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("wrap directory %s for sync", path)
	}
	defer file.Close()
	return file.Sync()
}
