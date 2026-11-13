package session

import (
	"context"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/platform"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// The App menu: the phone reads the menu bar of the app that owns the shown window and presses one item.
// Reading is allowed in a view-only session (it only looks); pressing moves the app, so it needs full control,
// like fit. Menu titles are the app's screen content: nothing here logs them, or even how many there are.

func (s *Session) menuAPI() (platform.WindowMenu, bool) {
	m, ok := s.cap.(platform.WindowMenu)
	return m, ok
}

// shownWindow is the id of the window the video shows, when a window (not a display) is the target.
func (s *Session) shownWindow() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.win.info.ID, s.win.active()
}

// sendMenu is window.menu.get.
func (s *Session) sendMenu() {
	api, ok := s.menuAPI()
	if !ok {
		s.sendError(protocol.CodeMenuUnavailable, "")
		return
	}
	id, window := s.shownWindow()
	if !window {
		s.sendError(protocol.CodeBadRequest, "target")
		return
	}
	// one read per window at a time: opening the sheet again while the app is still answering waits for that answer
	s.mu.Lock()
	if s.menuReading == id {
		s.mu.Unlock()
		return
	}
	s.menuReading = id
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.menuReading == id {
			s.menuReading = ""
		}
		s.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	items, err := api.WindowMenu(ctx, id)
	if err != nil {
		s.sendErrorFor(protocol.CodeOf(err), id)
		return
	}
	// the phone may have moved on while the app answered
	if now, window := s.shownWindow(); !window || now != id {
		return
	}
	b, ok := marshalMenu(id, items)
	if !ok {
		s.sendErrorFor(protocol.CodeMenuUnavailable, id)
		return
	}
	s.send(b)
}

// marshalMenu keeps one message under the channel's limit by dropping the tail of the menu bar, never
// the middle: an index the phone holds still means the same item.
func marshalMenu(windowID string, items []protocol.MenuItem) ([]byte, bool) {
	for budget := 300; budget >= 20; budget /= 2 {
		left := budget
		b, err := protocol.Marshal(protocol.WindowMenu{WindowID: windowID, Items: limitMenu(items, &left, 0)})
		if err != nil {
			return nil, false
		}
		if len(b) <= protocol.MaxMenuBytes {
			return b, true
		}
	}
	return nil, false
}

func limitMenu(items []protocol.MenuItem, left *int, depth int) []protocol.MenuItem {
	out := make([]protocol.MenuItem, 0, len(items))
	for _, it := range items {
		if *left <= 0 {
			break
		}
		*left--
		if len(it.Children) > 0 {
			if depth+1 >= protocol.MaxMenuDepth {
				it.Children, it.Enabled = nil, false
			} else if it.Children = limitMenu(it.Children, left, depth+1); len(it.Children) == 0 {
				it.Children, it.Enabled = nil, false // cut to nothing: no longer a submenu the phone could open
			}
		}
		out = append(out, it)
	}
	return out
}

// invokeMenu is window.menu.invoke.
func (s *Session) invokeMenu(path []int) {
	api, ok := s.menuAPI()
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
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	if err := api.InvokeMenu(ctx, id, path); err != nil {
		s.sendErrorFor(protocol.CodeOf(err), id)
		return
	}
	s.log.Info("remote desktop menu item pressed")
}
