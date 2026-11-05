//go:build windows

package automations

import (
	"os/exec"
	"syscall"
)

// detachProcess starts the daemon without a console window, in its own process
// group (DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP).
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
}
