//go:build !darwin && !linux

package jobstore

import "fmt"

func validateStorePlatform() error {
	return fmt.Errorf(
		"%w: durable job storage is disabled on this platform until owner-only filesystem permissions are enforced",
		ErrOwnership,
	)
}
