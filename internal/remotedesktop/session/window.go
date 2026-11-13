package session

import (
	"context"
	"strings"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/platform"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// Window mode: the video shows one application window (and its popups) instead of a display. Everything
// here is a view choice inside an already approved session — the session's mode (view / full) and the
// Mac owner's powers are unchanged. View Only can pick and watch a window, but cannot open an app or
// bring a window forward.

// windowMode is the state of a window-mode session (guarded by Session.mu). The zero value = display mode.
type windowMode struct {
	info  protocol.WindowInfo
	scale float64 // pixels per point of the display the window was chosen on
	geom  protocol.WindowGeometry
	on    bool
	// fitted: the window was resized to viewport (and is given back when the session lets go of it).
	fitted   bool
	viewport protocol.Viewport
	// zoom: net steps of the app's own Zoom In the phone pressed (given back with the window).
	zoom int
}

func (w windowMode) active() bool { return w.on }

// dirty: the window is not as the Mac user left it (resized or zoomed), so it must be given back.
func (w windowMode) dirty() bool { return w.fitted || w.zoom > 0 }

// asDisplay is the window's size dressed as a display, so the tier's sizing rule applies unchanged: the
// window's aspect ratio is kept and nothing is upscaled past the window's own pixels.
func (w windowMode) asDisplay() protocol.DisplayInfo {
	scale := w.scale
	if scale <= 0 {
		scale = 1
	}
	return protocol.DisplayInfo{
		Width: w.info.Width, Height: w.info.Height,
		PixelWidth: int(w.info.Width*scale + 0.5), PixelHeight: int(w.info.Height*scale + 0.5), Scale: scale,
	}
}

// windowsAPI is the backend's window capability, when it has one.
func (s *Session) windowsAPI() (platform.Windows, bool) {
	w, ok := s.cap.(platform.Windows)
	if !ok || !w.WindowsSupported() {
		return nil, false
	}
	return w, true
}

func (s *Session) canControl() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Mode == protocol.ModeFullControl && s.canInput
}

// windowAcceptsInput gates input by what the video shows: nothing chosen → nothing to act on; a window
// that is not on a screen → the phone is told to activate it first (rate-limited), never silently clicked.
func (s *Session) windowAcceptsInput() bool {
	s.mu.Lock()
	target, onScreen := s.state.Target, s.win.geom.OnScreen
	window := s.win.active()
	s.mu.Unlock()
	if target == protocol.TargetNone {
		return false
	}
	if window && !onScreen {
		if s.rateLimitedNotice() {
			s.sendError(protocol.CodeWindowOffscreen, "")
		}
		return false
	}
	return true
}

// systemShortcut reports whether pressing code with the keys already held would leave the window the
// phone is looking at (app switcher, Spotlight, Spaces) or end it (hide, minimize, quit, log out: ⌘H ⌘M ⌘Q ⌃⌘Q ⇧⌘Q).
func systemShortcut(code string, held map[string]struct{}) bool {
	_, metaL := held["MetaLeft"]
	_, metaR := held["MetaRight"]
	_, ctrlL := held["ControlLeft"]
	_, ctrlR := held["ControlRight"]
	switch code {
	case "Tab", "Space", "KeyH", "KeyM", "KeyQ":
		return metaL || metaR
	case "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown":
		return ctrlL || ctrlR
	}
	return false
}

// selectTarget is target.select: a display or one window.
func (s *Session) selectTarget(kind, id string) {
	switch kind {
	case protocol.TargetDisplay:
		s.selectDisplay(id)
	case protocol.TargetWindow:
		s.selectWindow(id)
	default:
		s.sendError(protocol.CodeBadRequest, "target")
	}
}

// selectWindow shows window id: its size sets the video size, a full-control session brings it forward
// so clicks and typing reach it, and a new capture generation starts.
func (s *Session) selectWindow(id string) {
	api, ok := s.windowsAPI()
	if !ok {
		s.sendError(protocol.CodeWindowsUnavailable, "")
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	list, err := api.ListWindows(ctx)
	if err != nil {
		s.sendError(protocol.CodeOf(err), "")
		return
	}
	var info protocol.WindowInfo
	found := false
	for _, w := range list {
		if w.ID == id {
			info, found = w, true
			break
		}
	}
	if !found {
		s.sendError(protocol.CodeWindowNotFound, "")
		return
	}
	s.leaveWindow(id)
	s.mu.Lock()
	prev, prevVP := s.win.fitted && s.win.info.ID == id, s.win.viewport
	prevZoom := 0
	if s.win.info.ID == id {
		prevZoom = s.win.zoom
	}
	s.mu.Unlock()
	inherited := s.claimPending(id) // a window left fitted by a link that dropped: the session owns it again
	frame := protocol.Rect{X: info.X, Y: info.Y, Width: info.Width, Height: info.Height}
	s.mu.Lock()
	s.win = windowMode{info: info, scale: s.scaleAtLocked(info), on: true}
	s.win.geom = protocol.WindowGeometry{Source: frame, Windows: []protocol.Rect{frame}, OnScreen: info.OnScreen}
	if prev || inherited {
		s.win.fitted = true
		if prev {
			s.win.viewport = prevVP
		}
	}
	s.win.zoom = prevZoom
	s.state.Fitted, s.state.Clamped, s.state.Zoom = s.win.fitted, nil, prevZoom
	s.state.Target = protocol.TargetWindow
	s.state.Window = &protocol.WindowTarget{ID: info.ID, App: info.App, BundleID: info.BundleID, Title: info.Title, OnScreen: info.OnScreen}
	s.setDisplayLocked(s.display, s.state.Displays)
	s.haveLast = false
	control := s.cfg.Mode == protocol.ModeFullControl && s.canInput
	s.mu.Unlock()

	s.releaseAll("target_switch")
	if control && info.OnScreen {
		_ = api.RaiseWindow(ctx, info.ID) // best effort: a refusal only costs the first click a raise
	}
	s.restartWindow()
}

// scaleAtLocked is the pixel scale of the display the window sits on (its centre decides).
func (s *Session) scaleAtLocked(w protocol.WindowInfo) float64 {
	cx, cy := w.X+w.Width/2, w.Y+w.Height/2
	for _, d := range s.state.Displays {
		if d.Scale > 0 && cx >= d.X && cx < d.X+d.Width && cy >= d.Y && cy < d.Y+d.Height {
			return d.Scale
		}
	}
	if s.display.Scale > 0 {
		return s.display.Scale
	}
	return 1
}

// restartWindow starts the window capture; a window that vanished meanwhile ends only the window, not the session.
func (s *Session) restartWindow() {
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	if err := s.startCapture(ctx); err != nil {
		s.failCapture(err)
		return
	}
	s.sendState()
}

// failCapture ends the session for a capture failure, except that a window that is gone only leaves window mode.
func (s *Session) failCapture(err error) {
	if protocol.CodeOf(err) == protocol.CodeWindowNotFound {
		s.mu.Lock()
		id := s.win.info.ID
		s.mu.Unlock()
		go s.windowGone(id, protocol.GoneClosed)
		return
	}
	s.fail(err)
}

// activateWindow is target.activate: the phone asked to bring a window that is not on a screen to the
// front (which may switch the Mac's Space). Only a full-control session does it.
func (s *Session) activateWindow() {
	api, ok := s.windowsAPI()
	if !ok {
		s.sendError(protocol.CodeWindowsUnavailable, "")
		return
	}
	s.mu.Lock()
	window, id, bundle := s.win.active(), s.win.info.ID, s.win.info.BundleID
	s.mu.Unlock()
	if !window {
		s.sendError(protocol.CodeBadRequest, "target")
		return
	}
	if !s.canControl() {
		s.sendError(protocol.CodeViewOnly, "")
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	if err := api.RaiseWindow(ctx, id); err != nil {
		if bundle != "" && unreachableByRaise(err) {
			s.reopenInstead(bundle)
			return
		}
		s.sendError(protocol.CodeOf(err), "")
		return
	}
	// The Space switch animates; the window counts as on a screen once the window server says so.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if list, err := api.ListWindows(ctx); err == nil {
			for _, w := range list {
				if w.ID == id && w.OnScreen {
					s.mu.Lock()
					s.win.info = w
					s.mu.Unlock()
					s.releaseAll("target_activate")
					s.restartWindow()
					return
				}
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
	s.sendError(protocol.CodeWindowOffscreen, "")
}

// unreachableByRaise: the helper could not find the window among its app's accessible windows (it sits
// on another Space, or the app hid it instead of closing it), which it reports as window_not_found.
func unreachableByRaise(err error) bool {
	return protocol.CodeOf(err) == protocol.CodeWindowNotFound
}

// reopenInstead is what clicking the app's Dock icon does: the app is opened again and shows a window
// (the hidden one comes back, or a new one opens), and that window becomes the target.
func (s *Session) reopenInstead(bundleID string) {
	s.openApp(bundleID)
	s.mu.Lock()
	stillHidden := s.win.active() && !s.win.info.OnScreen
	s.mu.Unlock()
	if stillHidden {
		s.sendError(protocol.CodeWindowOffscreen, "")
	}
}

// onWindowGeometry keeps the pointer mapping on the picture: the window moved, or a popup came or went.
func (s *Session) onWindowGeometry(ev platform.Event) {
	if ev.Geometry == nil || ev.Geometry.CaptureID != s.activeCap.Load() {
		return // a generation we already replaced
	}
	s.mu.Lock()
	if s.win.active() {
		s.win.geom = *ev.Geometry
	}
	s.mu.Unlock()
}

// windowGone leaves window mode when the helper says the target window is no longer there: the capture
// stops, the phone is told why, and the session waits for a new choice.
func (s *Session) windowGone(id string, reason protocol.WindowGoneReason) {
	s.mu.Lock()
	if s.closed || !s.win.active() || s.win.info.ID != id {
		s.mu.Unlock()
		return
	}
	wasFitted := s.win.dirty()
	s.win = windowMode{}
	s.state.Target, s.state.Window = protocol.TargetNone, nil
	s.state.Fitted, s.state.Clamped, s.state.Zoom = false, nil, 0
	s.sharing = false
	s.mu.Unlock()
	if wasFitted {
		go s.restoreFitted(id) // the helper finds it gone and forgets the journal entry
	}
	s.activeCap.Store(0) // frames and stop events of the old generation are dropped from here on
	s.releaseAll("window_gone")
	ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
	_ = s.cap.StopCapture(ctx)
	cancel()
	s.log.Info("remote desktop window gone", "reason", string(reason))
	if b, err := protocol.Marshal(protocol.WindowGone{WindowID: id, Reason: reason}); err == nil {
		s.send(b)
	}
	s.sendState()
}

// sendWindows answers windows.list.
func (s *Session) sendWindows() {
	api, ok := s.windowsAPI()
	if !ok {
		s.sendError(protocol.CodeWindowsUnavailable, "")
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	list, err := api.ListWindows(ctx)
	if err != nil {
		s.sendError(protocol.CodeOf(err), "")
		return
	}
	if len(list) > protocol.MaxWindowsListed {
		list = list[:protocol.MaxWindowsListed]
	}
	if b, err := protocol.Marshal(protocol.WindowsSnapshot{Windows: list}); err == nil {
		s.send(b)
	}
}

// sendApps answers apps.list.
func (s *Session) sendApps() {
	api, ok := s.windowsAPI()
	if !ok {
		s.sendError(protocol.CodeWindowsUnavailable, "")
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	list, err := api.ListApps(ctx)
	if err != nil {
		s.sendError(protocol.CodeOf(err), "")
		return
	}
	if len(list) > protocol.MaxAppsListed {
		list = list[:protocol.MaxAppsListed]
	}
	if b, err := protocol.Marshal(protocol.AppsSnapshot{Apps: list}); err == nil {
		s.send(b)
	}
}

// sendThumb answers window.thumb.get. A picture that would not fit one message is simply not sent
// (the phone keeps its placeholder); nothing about it is logged.
func (s *Session) sendThumb(m *protocol.ThumbGet) {
	api, ok := s.windowsAPI()
	if !ok || m.WindowID == "" {
		return
	}
	width := m.MaxWidth
	if width <= 0 || width > protocol.MaxThumbWidth {
		width = protocol.MaxThumbWidth
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	jpeg, err := api.WindowThumbnail(ctx, m.WindowID, width)
	if err != nil || len(jpeg) == 0 || len(jpeg) > protocol.MaxThumbBytes {
		return
	}
	if b, err := protocol.Marshal(protocol.WindowThumb{WindowID: m.WindowID, JPEG: jpeg}); err == nil {
		s.send(b)
	}
}

// handleBundle is app.open and app.icon.get.
func (s *Session) handleBundle(m *protocol.BundleRef) {
	if m.BundleID == "" || len(m.BundleID) > protocol.MaxBundleIDLength || strings.ContainsAny(m.BundleID, " \t\r\n") {
		s.sendError(protocol.CodeBadRequest, "bundleId")
		return
	}
	switch m.Type {
	case protocol.TypeAppIconGet:
		go s.sendIcon(m.BundleID)
	case protocol.TypeAppOpen:
		if !s.canControl() {
			s.sendError(protocol.CodeViewOnly, "")
			return
		}
		go s.openApp(m.BundleID)
	}
}

func (s *Session) sendIcon(bundleID string) {
	api, ok := s.windowsAPI()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	png, err := api.AppIcon(ctx, bundleID)
	if err != nil || len(png) == 0 || len(png) > protocol.MaxIconBytes {
		return
	}
	if b, err := protocol.Marshal(protocol.AppIcon{BundleID: bundleID, PNG: png}); err == nil {
		s.send(b)
	}
}

// openApp launches an app for the phone and, when it shows a window, switches to that window.
func (s *Session) openApp(bundleID string) {
	api, ok := s.windowsAPI()
	if !ok {
		s.sendError(protocol.CodeWindowsUnavailable, "")
		return
	}
	if b, err := protocol.Marshal(protocol.BundleRef{BundleID: bundleID}); err == nil {
		s.send(b)
	}
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()
	id, err := api.OpenApp(ctx, bundleID)
	if err != nil {
		s.log.Info("remote desktop app open failed", "code", string(protocol.CodeOf(err)))
		if b, err := protocol.Marshal(protocol.AppOpenFailed{BundleID: bundleID, Reason: openFailReason(err)}); err == nil {
			s.send(b)
		}
		return
	}
	s.selectWindow(id)
}

// openFailReason is the stable word the phone shows for a failed launch.
func openFailReason(err error) string {
	e, ok := err.(*protocol.Error)
	if !ok {
		return "failed"
	}
	switch {
	case e.Code == protocol.CodeAppNotFound:
		return "not_found"
	case e.Code == protocol.CodePermissionRequired:
		return "permission"
	case e.Code == protocol.CodeAppOpenFailed && e.Message == "no_window":
		return "no_window"
	case e.Code == protocol.CodeAppOpenFailed && e.Message == "launch":
		return "launch"
	}
	return "failed"
}
