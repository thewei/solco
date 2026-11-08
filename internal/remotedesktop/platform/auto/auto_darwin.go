// Package auto picks the Remote Desktop backend for the running OS. It is the only place that
// imports concrete backends, so adding Linux or Windows means one new file here and one new package
// beside platform/macos.
package auto

import (
	"github.com/thewei/solco/internal/remotedesktop/platform"
	"github.com/thewei/solco/internal/remotedesktop/platform/macos"
)

// New returns the macOS backend (the Swift helper supervisor).
func New(opts macos.Options) platform.Backend { return macos.New(opts) }
