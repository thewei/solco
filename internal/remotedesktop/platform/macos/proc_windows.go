//go:build windows

package macos

import "os/exec"

// The helper is macOS-only; Windows compiles this package only because platform/auto names its
// Options type. These are no-ops there.
func ownProcessGroup(*exec.Cmd) {}

func killProcessGroup(int) {}
