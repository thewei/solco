package session

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/thewei/solco/internal/remotedesktop/mapper"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// OnControl handles one message from the phone's reliable control channel.
func (s *Session) OnControl(data []byte) { s.handle(data) }

// OnPointer handles one message from the unreliable pointer channel (moves, scroll).
func (s *Session) OnPointer(data []byte) { s.handle(data) }

func (s *Session) handle(data []byte) {
	s.lastRXNano.Store(s.cfg.Now().UnixNano())
	msg, err := protocol.Decode(data)
	if err != nil {
		s.sendError(protocol.CodeOf(err), "")
		return
	}
	switch m := msg.(type) {
	case *protocol.Ping:
		if b, err := protocol.Marshal(protocol.Pong{T: m.T}); err == nil {
			s.send(b)
		}
	case *protocol.Envelope:
		s.handleSimple(m.Type)
	case *protocol.DisplaySelect:
		go s.selectDisplay(m.DisplayID)
	case *protocol.TargetSelect:
		go s.selectTarget(m.Kind, m.ID)
	case *protocol.BundleRef:
		s.handleBundle(m)
	case *protocol.ThumbGet:
		go s.sendThumb(m)
	case *protocol.QualitySet:
		go s.setTier(m.Tier)
	case *protocol.CursorSet:
		go s.setCursor(m.Visible)
	case *protocol.PrivacySet:
		go s.setBlackout(m.On)
	default:
		s.handleInput(msg)
	}
}

func (s *Session) handleSimple(typ string) {
	switch typ {
	case protocol.TypeKeyframe:
		s.OnKeyframeRequest()
	case protocol.TypeSessionEnd:
		go s.Close(protocol.CodeSessionEnded)
	case protocol.TypeStatsRequest:
		s.sendStats()
	case protocol.TypeReleaseAll:
		s.releaseAll("phone_request")
	case protocol.TypeClipboardGet:
		if s.allowInput() {
			go s.readClipboard()
		}
	case protocol.TypeUnlockRequest:
		go s.requestUnlock()
	case protocol.TypeWindowsList:
		go s.sendWindows()
	case protocol.TypeAppsList:
		go s.sendApps()
	case protocol.TypeTargetActive:
		go s.activateWindow()
	}
}

// allowInput reports whether input may act now; otherwise it tells the phone why (rate-limited).
func (s *Session) allowInput() bool {
	s.mu.Lock()
	ok := s.cfg.Mode == protocol.ModeFullControl && s.canInput && s.connected && !s.closed
	mode, can := s.cfg.Mode, s.canInput
	s.mu.Unlock()
	if ok {
		return true
	}
	if s.rateLimitedNotice() {
		switch {
		case mode != protocol.ModeFullControl:
			s.sendError(protocol.CodeViewOnly, "")
		case !can:
			s.sendError(protocol.CodePermissionRequired, "accessibility")
		default:
			s.sendError(protocol.CodeNotConnected, "")
		}
	}
	return false
}

func (s *Session) rateLimitedNotice() bool {
	now := s.cfg.Now().UnixNano()
	last := s.lastNoticeNano.Load()
	if now-last < int64(time.Second) {
		return false
	}
	return s.lastNoticeNano.CompareAndSwap(last, now)
}

func (s *Session) handleInput(msg any) {
	if !s.allowInput() || !s.windowAcceptsInput() {
		return
	}
	in := s.cap.Input()
	switch m := msg.(type) {
	case *protocol.PointerMove:
		x, y, ok := s.pointerTarget(m.Seq, m.X, m.Y, m.DisplayID, false, false)
		if !ok {
			return
		}
		_ = in.PointerMove(x, y)
	case *protocol.PointerButton:
		if m.Button != protocol.ButtonLeft && m.Button != protocol.ButtonRight && m.Button != protocol.ButtonMiddle {
			s.sendError(protocol.CodeBadRequest, "button")
			return
		}
		x, y, ok := s.pointerTarget(m.Seq, m.X, m.Y, m.DisplayID, true, m.Down)
		if !ok {
			return
		}
		count := m.ClickCount
		if count < 1 {
			count = 1
		} else if count > 3 {
			count = 3
		}
		s.mu.Lock()
		if m.Down {
			s.heldButtons[m.Button] = struct{}{}
		} else {
			delete(s.heldButtons, m.Button)
		}
		s.mu.Unlock()
		_ = in.PointerButton(m.Button, m.Down, x, y, count)
	case *protocol.PointerScroll:
		_ = in.Scroll(mapper.ScrollPoints(m.DX), mapper.ScrollPoints(m.DY))
	case *protocol.KeyEvent:
		s.mu.Lock()
		if m.Down && s.win.active() && systemShortcut(m.Code, s.heldKeys) {
			s.mu.Unlock()
			return // ⌘Tab, ⌘Space and the Spaces arrows would leave the window the phone is looking at
		}
		if m.Down {
			if len(s.heldKeys) >= maxHeldKeys {
				s.mu.Unlock()
				return
			}
			s.heldKeys[m.Code] = struct{}{}
		} else {
			delete(s.heldKeys, m.Code)
		}
		s.mu.Unlock()
		if err := in.Key(m.Code, m.Down); err != nil {
			s.mu.Lock()
			delete(s.heldKeys, m.Code)
			s.mu.Unlock()
			s.sendError(protocol.CodeBadRequest, "key")
		}
	case *protocol.TextInsert:
		text := sanitizeText(m.Text)
		if text == "" {
			return
		}
		_ = in.Text(text)
	case *protocol.ClipboardSet:
		if len(m.Text) > protocol.MaxClipboardBytes {
			s.sendError(protocol.CodeBadRequest, "clipboard too large")
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
			defer cancel()
			_ = s.cap.Clipboard().WriteText(ctx, m.Text)
		}()
	}
}

// sanitizeText bounds text.insert and drops control characters other than newline and tab.
func sanitizeText(t string) string {
	if utf8.RuneCountInString(t) > protocol.MaxTextInsertRunes {
		t = string([]rune(t)[:protocol.MaxTextInsertRunes])
	}
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r >= 0x20 && r != 0x7f && r != utf8.RuneError {
			return r
		}
		return -1
	}, t)
}

// pointerTarget applies the ordering rules and the coordinate mapping.
//
// A move whose Seq is not newer than the last applied pointer event is stale (the unreliable
// channel can reorder around a button that travelled the reliable one) and is dropped. A button
// that is itself older than an already-applied move uses that move's position, so a drag never
// presses at a position the pointer already left.
func (s *Session) pointerTarget(seq uint64, nx, ny float64, displayID string, button, down bool) (x, y float64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	window := s.win.active()
	if !window && displayID != "" && displayID != s.display.ID {
		return 0, 0, false // addressed to a display the stream has since left
	}
	stale := seq != 0 && seq <= s.lastSeq
	if stale && !button {
		return 0, 0, false
	}
	if stale && button && s.haveLast {
		return s.lastX, s.lastY, true
	}
	if window {
		x, y, ok = mapper.ToRect(s.win.geom.Source, nx, ny)
		if !ok {
			return 0, 0, false
		}
		// Outside the window and its popups nothing is forwarded — except while a button is held (a drag
		// may leave the window) and the button's release, which must always arrive.
		if !mapper.InAny(s.win.geom.Windows, x, y) && len(s.heldButtons) == 0 && !(button && !down) {
			return 0, 0, false
		}
	} else {
		x, y, ok = mapper.ToGlobal(s.display, nx, ny)
		if !ok {
			return 0, 0, false
		}
	}
	if seq > s.lastSeq {
		s.lastSeq = seq
	}
	s.lastX, s.lastY, s.haveLast = x, y, true
	return x, y, true
}

// releaseAll lifts every key and button the phone held. It is called on link loss, silence,
// permission loss, an explicit request and close.
func (s *Session) releaseAll(reason string) {
	s.mu.Lock()
	held := len(s.heldKeys) + len(s.heldButtons)
	s.heldKeys = map[string]struct{}{}
	s.heldButtons = map[string]struct{}{}
	s.mu.Unlock()
	_ = s.cap.Input().ReleaseAll()
	if held > 0 {
		s.log.Info("remote desktop input released", "reason", reason, "held", held)
	}
}

func (s *Session) readClipboard() {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	text, err := s.cap.Clipboard().ReadText(ctx)
	if err != nil {
		s.sendError(protocol.CodeInternal, "clipboard")
		return
	}
	if len(text) > protocol.MaxClipboardBytes {
		s.sendError(protocol.CodeBadRequest, "clipboard too large")
		return
	}
	if b, err := protocol.Marshal(protocol.ClipboardText{Text: text}); err == nil {
		s.send(b)
	}
}

// selectDisplay switches the streamed display (a new capture generation at the new size).
func (s *Session) selectDisplay(id string) {
	s.mu.Lock()
	var target protocol.DisplayInfo
	found := false
	for _, d := range s.state.Displays {
		if d.ID == id {
			target, found = d, true
		}
	}
	if !found {
		s.mu.Unlock()
		s.sendError(protocol.CodeDisplayNotFound, "")
		return
	}
	all := s.state.Displays
	s.win = windowMode{} // back to showing a whole display
	s.state.Target, s.state.Window = "", nil
	s.setDisplayLocked(target, all)
	s.haveLast = false
	s.mu.Unlock()
	s.releaseAll("display_switch") // a button held on the old display must not follow the pointer
	s.restartAndAnnounce()
}

// setTier changes the quality tier (new resolution = new capture generation).
func (s *Session) setTier(t protocol.Tier) {
	if !t.Valid() {
		s.sendError(protocol.CodeBadRequest, "tier")
		return
	}
	s.mu.Lock()
	if s.cfg.Tier == t {
		s.mu.Unlock()
		return
	}
	s.cfg.Tier = t
	s.setDisplayLocked(s.display, s.state.Displays)
	s.mu.Unlock()
	s.restartAndAnnounce()
}

func (s *Session) setCursor(visible bool) {
	s.mu.Lock()
	if s.hideCursor == !visible {
		s.mu.Unlock()
		return
	}
	s.hideCursor = !visible
	s.state.Cursor = visible
	s.mu.Unlock()
	s.restartAndAnnounce()
}

func (s *Session) restartAndAnnounce() {
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	if err := s.startCapture(ctx); err != nil {
		s.fail(err)
		return
	}
	s.sendState()
}
