package session

import (
	"context"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/platform"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// App zoom: the phone asks the shown app to draw its text larger (its own Zoom In, ⌘+) because a window that cannot be
// narrowed to the phone is otherwise shown scaled down, with small text. The steps are the helper's journal's
// business (it undoes them with the window); here they are only counted, so the phone can show them and the session
// knows the window is not as the Mac user left it. Like fit, it moves an app, so it needs full control.

func (s *Session) zoomAPI() (platform.WindowZoomer, bool) {
	z, ok := s.cap.(platform.WindowZoomer)
	return z, ok
}

// zoomWindow is window.zoom.
func (s *Session) zoomWindow(dir string) {
	api, ok := s.zoomAPI()
	if !ok {
		s.sendError(protocol.CodeMenuUnavailable, "")
		return
	}
	if !s.canControl() {
		s.sendError(protocol.CodeViewOnly, "")
		return
	}
	id, window := s.shownWindow()
	if !window {
		s.sendError(protocol.CodeBadRequest, "target")
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	steps, err := api.ZoomWindow(ctx, id, dir)
	if err != nil {
		s.sendErrorFor(protocol.CodeOf(err), id)
		return
	}
	s.mu.Lock()
	if s.closed || !s.win.active() || s.win.info.ID != id { // the phone moved on while the app answered
		s.mu.Unlock()
		if steps > 0 {
			go s.restoreFitted(id)
		}
		return
	}
	s.win.zoom, s.state.Zoom = steps, steps
	s.mu.Unlock()
	s.log.Info("remote desktop window zoomed", "dir", dir, "steps", steps)
	s.sendState()
}
