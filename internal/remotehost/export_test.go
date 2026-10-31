package remotehost

import "github.com/thewei/solco/internal/herdr"

// ServeLauncher and ServeGit let the external test package (which may import
// internal/app without an import cycle) give the serve-for-client Host the
// real launcher and the real read-only Git view.
var (
	ServeLauncher func(*herdr.Manager) Launcher
	ServeGit      func() GitReader
	// ServeQueue is called after ServeLauncher and shares its service (and its follow-up delivery worker).
	ServeQueue func() FollowUps
)
