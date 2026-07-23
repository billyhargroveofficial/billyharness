//go:build !darwin && !linux && !windows

package jobstore

import (
	"fmt"
	"os"
)

func lockOwnershipFile(*os.File) error {
	return fmt.Errorf("advisory job store ownership locks are unsupported on this platform")
}

func unlockOwnershipFile(*os.File) error {
	return nil
}

func ownershipContention(error) bool {
	return false
}
