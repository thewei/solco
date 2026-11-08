package nativeui

// DockBadgeText is the P12 desktop attention badge: the same waiting number the
// menu-bar title shows (WaitingText; 2026-11-07 round 3 widened it from the
// status-center snapshot alone to the Header's former sources). Working agents
// do NOT increase the badge count (0.9 P12 invariant). Returns "" when nothing
// waits, which clears the badge.
func DockBadgeText(counts TrayCounts) string { return WaitingText(counts) }

// dockPlatform defines the platform abstraction for macOS Dock manipulations.
type dockPlatform interface {
	SetBadge(text string)
}

// dockController manages updating the Dock badge with deduplication.
type dockController struct {
	platform  dockPlatform
	lastBadge string
	// extraAttention / extraReview: see statusTray — the Assistants inbox and the
	// unopened Automation runs, set by updateDockBadge.
	extraAttention, extraReview int
}

func newDockController(platform dockPlatform) *dockController {
	return &dockController{
		platform:  platform,
		lastBadge: "",
	}
}

// update projects the status center snapshot to the platform Dock badge.
func (d *dockController) update(snapshot StatusCenterSnapshot) {
	if d.platform == nil {
		return
	}
	counts := TrayCountsFromSnapshot(snapshot)
	counts.Attention += d.extraAttention
	counts.Review += d.extraReview
	newBadge := DockBadgeText(counts)
	if newBadge != d.lastBadge {
		d.lastBadge = newBadge
		d.platform.SetBadge(newBadge)
	}
}

// AttachDock binds the macOS Dock platform to the shell.
func (s *Shell) AttachDock(dock dockPlatform) {
	s.dock = newDockController(dock)
	s.updateDockBadge()
}

// updateDockBadge triggers the Dock badge update from the current snapshot.
func (s *Shell) updateDockBadge() {
	if s.dock == nil {
		return
	}
	s.dock.extraAttention, s.dock.extraReview = s.waitingExtras()
	s.dock.update(s.BuildStatusCenterSnapshot())
}
