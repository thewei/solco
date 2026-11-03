// Package unsupported is the Backend of operating systems without a Remote Desktop implementation.
// Linux and Windows backends will replace it in package auto; until then Settings and the Host
// report "unavailable" instead of failing in surprising places.
package unsupported

import (
	"context"

	"github.com/thewei/solco/internal/remotedesktop/platform"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// Backend implements platform.Backend by refusing everything.
type Backend struct{ OS string }

func (b Backend) ID() string { return "unsupported" }

func (b Backend) Available() (bool, string) {
	return false, "Remote Desktop has no backend for " + b.OS + " yet"
}

func (Backend) Permissions(context.Context) (protocol.PermissionStatus, error) {
	return protocol.PermissionStatus{}, platform.ErrUnsupported
}

func (Backend) RequestPermission(context.Context, protocol.PermissionKind) (protocol.PermissionStatus, error) {
	return protocol.PermissionStatus{}, platform.ErrUnsupported
}

func (Backend) Open(context.Context) (platform.Capturer, error) { return nil, platform.ErrUnsupported }
