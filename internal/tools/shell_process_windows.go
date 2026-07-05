//go:build windows

package tools

import (
	"os/exec"
	"strconv"
	"time"
)

func configureManagedShellCommand(cmd *exec.Cmd) {}

func terminateManagedShellProcess(proc *managedShellProcess, grace time.Duration) error {
	pid := proc.cmd.Process.Pid
	if pid <= 0 {
		return nil
	}
	killErr := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
	if killErr != nil {
		killErr = proc.cmd.Process.Kill()
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if proc.isExited() {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	if proc.isExited() {
		return nil
	}
	if killErr != nil {
		return killErr
	}
	return proc.cmd.Process.Kill()
}
