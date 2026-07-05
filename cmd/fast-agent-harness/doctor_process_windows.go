//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func doctorProcessExistsOS(pid int) bool {
	if pid <= 0 {
		return false
	}
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return false
	}
	text := strings.TrimSpace(string(out))
	if text == "" || strings.Contains(text, "No tasks") || strings.Contains(text, "INFO:") {
		return false
	}
	needle := `","` + strconv.Itoa(pid) + `","`
	return strings.Contains(text, needle)
}
