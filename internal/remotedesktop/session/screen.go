package session

import (
	"context"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/platform"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// The physical-screen features of a Control session: anti-peek blackout, lock-screen awareness and the
// opt-in saved-password unlock. A backend that does not implement platform.Screen offers none of them.
// The password is fetched from Config.UnlockSecret only for the instant of typing, handed to the
// backend and dropped; it is never part of any message to the phone, a log line or the session state.

const (
	maxAutoUnlocks  = 2
	unlockCallLimit = 25 * time.Second
)

// Variables so tests can run the watch at a test pace.
var (
	lockPollEvery  = 2 * time.Second
	unlockRetryGap = 6 * time.Second
)

func (s *Session) screen() (platform.Screen, bool) {
	scr, ok := s.cap.(platform.Screen)
	return scr, ok
}

// unlockAvailable reports whether the Mac holds a saved login password this session may use.
func (s *Session) unlockAvailable() bool {
	if s.cfg.UnlockSecret == nil || s.cfg.Mode != protocol.ModeFullControl {
		return false
	}
	if _, ok := s.screen(); !ok {
		return false
	}
	_, ok := s.cfg.UnlockSecret()
	return ok
}

// setBlackout blacks the Mac's own screens out or restores them.
func (s *Session) setBlackout(on bool) {
	if !s.allowInput() {
		return
	}
	scr, ok := s.screen()
	if !ok {
		s.sendError(protocol.CodePrivacyUnavailable, "")
		return
	}
	if err := scr.SetBlackout(on); err != nil {
		s.log.Warn("remote desktop blackout failed", "on", on, "error", protocol.CodeOf(err))
		s.sendError(protocol.CodePrivacyUnavailable, "")
		return
	}
	s.mu.Lock()
	s.state.Blackout = on
	s.mu.Unlock()
	s.sendState()
}

// wakeScreen wakes a sleeping display when a Control session starts.
func (s *Session) wakeScreen() {
	scr, ok := s.screen()
	if !ok || s.cfg.Mode != protocol.ModeFullControl {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
	defer cancel()
	_ = scr.Wake(ctx)
}

// requestUnlock is the phone's "Unlock" button.
func (s *Session) requestUnlock() {
	if !s.allowInput() {
		return
	}
	s.tryUnlock()
}

func (s *Session) tryUnlock() {
	scr, ok := s.screen()
	if !ok || s.cfg.UnlockSecret == nil {
		s.sendError(protocol.CodeUnlockUnavailable, "")
		return
	}
	if !s.unlocking.CompareAndSwap(false, true) {
		return
	}
	defer s.unlocking.Store(false)
	pw, have := s.cfg.UnlockSecret()
	if !have {
		s.sendError(protocol.CodeUnlockUnavailable, "")
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, unlockCallLimit)
	defer cancel()
	still, err := scr.Unlock(ctx, pw)
	if err != nil {
		s.log.Warn("remote desktop unlock failed", "error", protocol.CodeOf(err))
		s.sendError(protocol.CodeUnlockFailed, "")
		return
	}
	if still {
		s.sendError(protocol.CodeUnlockFailed, "")
		return
	}
	s.setLocked(false)
}

func (s *Session) setLocked(locked bool) {
	s.mu.Lock()
	changed := s.state.Locked != locked
	s.state.Locked = locked
	s.mu.Unlock()
	if changed {
		s.sendState()
	}
}

// screenLoop watches the lock screen while the phone is connected: it tells the phone when it appears or
// goes away and, when the Mac's owner opted in, unlocks it by itself (a couple of tries per lock).
func (s *Session) screenLoop() {
	scr, ok := s.screen()
	if !ok {
		return
	}
	tick := time.NewTicker(lockPollEvery)
	defer tick.Stop()
	attempts := 0
	var last time.Time
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
		}
		s.mu.Lock()
		live := s.connected && !s.closed
		s.mu.Unlock()
		if !live || s.unlocking.Load() {
			continue
		}
		ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
		locked, err := scr.LockState(ctx)
		cancel()
		if err != nil {
			continue
		}
		s.setLocked(locked)
		if !locked {
			attempts = 0
			continue
		}
		if s.cfg.AutoUnlock != nil && s.cfg.AutoUnlock() && attempts < maxAutoUnlocks && s.cfg.Now().Sub(last) >= unlockRetryGap && s.unlockAvailable() && s.canActNow() {
			attempts++
			last = s.cfg.Now()
			s.tryUnlock()
		}
	}
}

// canActNow is allowInput without the phone-facing notice.
func (s *Session) canActNow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Mode == protocol.ModeFullControl && s.canInput && s.connected && !s.closed
}
