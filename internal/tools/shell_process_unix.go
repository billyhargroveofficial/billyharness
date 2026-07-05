//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
	"time"
)

func configureManagedShellCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateManagedShellProcess(proc *managedShellProcess, grace time.Duration) error {
	pid := proc.cmd.Process.Pid
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		_ = proc.cmd.Process.Kill()
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
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		return proc.cmd.Process.Kill()
	}
	return nil
}
