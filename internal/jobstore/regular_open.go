package jobstore

import (
	"fmt"
	"os"
)

func validateOpenedRegular(file *os.File, path string) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("%w: stat opened file %s: %w", ErrCorrupt, path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: refusing non-regular file %s", ErrCorrupt, path)
	}
	if err := validateRegularFilePlatform(info, path); err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("secure opened file %s: %w", path, err)
	}
	return nil
}
