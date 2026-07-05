//go:build !windows

package main

import (
	"errors"
	"syscall"
)

func doctorProcessExistsOS(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
