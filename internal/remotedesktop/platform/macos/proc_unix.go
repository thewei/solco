//go:build !windows

package macos

import (
	"os/exec"
	"syscall"
)

// ownProcessGroup puts the helper in its own process group so kill reaches its children too.
func ownProcessGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func killProcessGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) }
