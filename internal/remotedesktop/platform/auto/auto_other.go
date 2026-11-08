//go:build !darwin

package auto

import (
	"runtime"

	"github.com/thewei/solco/internal/remotedesktop/platform"
	"github.com/thewei/solco/internal/remotedesktop/platform/macos"
	"github.com/thewei/solco/internal/remotedesktop/platform/unsupported"
)

// New returns the "unsupported" backend: only macOS is implemented. Extension point — a Linux
// (PipeWire/XDG portal + uinput) or Windows (Desktop Duplication + SendInput) backend is added here.
func New(_ macos.Options) platform.Backend { return unsupported.Backend{OS: runtime.GOOS} }
