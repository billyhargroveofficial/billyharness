//go:build !darwin && !linux

package gatewayauth

import (
	"fmt"
	"io/fs"
	"os"
)

func openRegularNoFollow(path string) (*os.File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open secure file %s: %w", path, err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect opened secure file %s: %w", path, err)
	}
	current, err := os.Lstat(path)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("reinspect secure file %s: %w", path, err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		_ = file.Close()
		return nil, fmt.Errorf("secure file changed while opening %s", path)
	}
	return file, nil
}

func lockTokenStore(dir string) (*os.File, error) {
	return nil, fmt.Errorf("secure managed gateway token persistence in %s is supported only on Darwin and Linux; use %s as an explicit process credential", dir, PrimaryEnv)
}

func unlockTokenStore(file *os.File) error {
	if file == nil {
		return nil
	}
	return file.Close()
}

func validateRegularPlatform(_ fs.FileInfo, path string, requirePrivate bool) error {
	if requirePrivate {
		return fmt.Errorf("cannot verify owner-only gateway token permissions at %s on this platform; use %s as an explicit process credential", path, PrimaryEnv)
	}
	return nil
}

func validateDirectoryPlatform(_ fs.FileInfo, path string, requirePrivate bool) error {
	if requirePrivate {
		return fmt.Errorf("cannot verify owner-only gateway auth directory permissions at %s on this platform; use %s as an explicit process credential", path, PrimaryEnv)
	}
	return nil
}

func syncDirectory(string) error { return nil }
