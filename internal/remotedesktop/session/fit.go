package session

import (
	"context"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/platform"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// Fit mode: the shown window is resized to the phone's screen and given back when the session lets it go.
// The helper journals the original frame before it changes anything, so a crash anywhere below is repaired
// by the next start (Solco's start-up runs the journal first). Fit is a view choice like window mode itself:
// it needs a full-control session because it moves a window on the Mac.

// fitAPI is the backend's resize capability, when it has one.
func (s *Session) fitAPI() (platform.WindowFitter, bool) {
	f, ok := s.cap.(platform.WindowFitter)
	return f, ok
}

// fitWindow is window.fit: resize the shown window to the viewport, announce it, restart the capture on the
// new size. The same viewport again does nothing (a rotation is a new viewport).
func (s *Session) fitWindow(vp protocol.Viewport) {
	api, ok := s.fitAPI()
	if !ok {
		s.sendError(protocol.CodeFitUnavailable, "")
		return
	}
	if !vp.Valid() {
		s.sendError(protocol.CodeBadRequest, "viewport")
		return
	}
	if !s.canControl() {
		s.sendError(protocol.CodeViewOnly, "")
		return
	}
	s.mu.Lock()
	window, id, same := s.win.active(), s.win.info.ID, s.win.fitted && s.win.viewport == vp
	s.mu.Unlock()
	if !window {
		s.sendError(protocol.CodeBadRequest, "target")
		return
	}
	if same {
		return
	}
	w, h := vp.Size()
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	applied, clamped, err := api.FitWindow(ctx, id, w, h)
	if err != nil {
		s.sendError(protocol.CodeOf(err), "")
		return
	}
	s.mu.Lock()
	if s.closed || !s.win.active() || s.win.info.ID != id { // the phone moved on while the app was resizing
		s.mu.Unlock()
		go s.restoreFitted(id)
		return
	}
	s.win.info.X, s.win.info.Y, s.win.info.Width, s.win.info.Height = applied.X, applied.Y, applied.Width, applied.Height
	s.win.scale = s.scaleAtLocked(s.win.info) // the fit may have moved the window to a taller display
	s.win.fitted, s.win.viewport = true, vp
	s.state.Fitted = true
	s.state.Clamped = nil
	if clamped && applied.Width > w {
		s.state.Clamped = &protocol.Clamped{MinW: applied.Width, Scale: w / applied.Width}
	}
	s.setDisplayLocked(s.display, s.state.Displays)
	s.haveLast = false
	s.mu.Unlock()
	s.releaseAll("window_fit")
	s.log.Info("remote desktop window fitted", "clamped", clamped)
	s.restartWindow()
}

// fitOff is window.fit.off and the Mac's "Restore Window": give the window back now and keep showing it.
func (s *Session) fitOff() {
	s.mu.Lock()
	window, id, fitted := s.win.active(), s.win.info.ID, s.win.fitted
	s.mu.Unlock()
	if !window || !fitted {
		return
	}
	s.restoreFitted(id)
	api, ok := s.windowsAPI()
	if ok {
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		if list, err := api.ListWindows(ctx); err == nil {
			s.mu.Lock()
			for _, w := range list {
				if w.ID == id && s.win.active() && s.win.info.ID == id {
					s.win.info = w
					s.win.scale = s.scaleAtLocked(w) // and back to the display it came from
					break
				}
			}
			s.mu.Unlock()
		}
		cancel()
	}
	s.mu.Lock()
	if !s.win.active() || s.win.info.ID != id {
		s.mu.Unlock()
		return
	}
	s.clearFitLocked()
	s.setDisplayLocked(s.display, s.state.Displays)
	s.haveLast = false
	s.mu.Unlock()
	s.releaseAll("window_fit_off")
	s.restartWindow()
}

// RestoreWindow is the Mac owner's "Restore Window": the window goes back and the session shows it as it is.
func (s *Session) RestoreWindow() { s.fitOff() }

// clearFitLocked forgets the fit in the session's own state (the window itself is restored separately).
func (s *Session) clearFitLocked() {
	s.win.fitted, s.win.viewport = false, protocol.Viewport{}
	s.state.Fitted, s.state.Clamped = false, nil
}

// restoreFitted gives window id back through the helper and tells the phone what became of it. A helper
// that cannot be asked (crashed) hands the window to the journal restore instead, at once.
func (s *Session) restoreFitted(id string) {
	api, ok := s.fitAPI()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	outcome, err := api.RestoreWindow(ctx, id)
	if err != nil {
		s.log.Warn("remote desktop window restore failed", "code", string(protocol.CodeOf(err)))
		if s.cfg.RestoreLater != nil {
			s.cfg.RestoreLater(id, 0)
		}
		return
	}
	s.log.Info("remote desktop window restored", "outcome", string(outcome))
	if outcome == protocol.RestoreNone {
		return
	}
	if b, err := protocol.Marshal(protocol.WindowRestored{WindowID: id, Outcome: outcome}); err == nil {
		s.send(b)
	}
}

// leaveWindow restores the fitted window the session is about to stop showing (a different window, a display,
// the window's own end). It runs before the new target is installed.
func (s *Session) leaveWindow(next string) {
	s.mu.Lock()
	id, fitted := s.win.info.ID, s.win.fitted && s.win.active()
	if fitted && id == next {
		s.mu.Unlock()
		return
	}
	if fitted {
		s.clearFitLocked()
	}
	s.mu.Unlock()
	if fitted {
		s.restoreFitted(id)
	}
}

// claimPending tells the service which window the session now shows (or "" for a display): a deferred
// restore of that very window is cancelled (the session owns the fit again), any other is carried out now.
// It reports whether the window arrives still fitted.
func (s *Session) claimPending(window string) bool {
	if s.cfg.ClaimPending == nil {
		return false
	}
	return s.cfg.ClaimPending(window)
}

// closeFit runs in Close, before the capturer goes: a link that was simply lost leaves the window fitted for
// the service's grace (the phone usually comes back), anything else gives it back now.
func (s *Session) closeFit(lost bool) {
	s.mu.Lock()
	id, fitted := s.win.info.ID, s.win.fitted && s.win.active()
	s.mu.Unlock()
	if !fitted {
		return
	}
	if lost && s.cfg.RestoreLater != nil {
		s.cfg.RestoreLater(id, FitGrace)
		return
	}
	s.restoreFitted(id)
}

// FitGrace is how long a fitted window waits after the link was lost before it is given back.
const FitGrace = 2 * time.Minute
