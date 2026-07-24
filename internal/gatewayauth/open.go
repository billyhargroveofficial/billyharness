package gatewayauth

import (
	"fmt"
	"os"
)

func validateOpenedRegular(file *os.File, path string, requirePrivate bool) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened secure file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing non-regular file at %s", path)
	}
	if err := validateRegularPlatform(info, path, requirePrivate); err != nil {
		return err
	}
	return nil
}
