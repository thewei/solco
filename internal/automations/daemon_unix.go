//go:build !windows

package automations

import (
	"os/exec"
	"syscall"
)

// detachProcess starts the daemon in its own session so it outlives the window.
func detachProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
