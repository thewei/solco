package session

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// screenCap is a fakeCap that can also act on the physical screen.
type screenCap struct {
	*fakeCap
	sm        sync.Mutex
	dark      bool
	locked    bool
	typed     []string
	unlockErr error
	stays     bool // the lock screen survives the attempt (wrong password)
	wakes     int
}

func (c *screenCap) SetBlackout(on bool) error { c.sm.Lock(); c.dark = on; c.sm.Unlock(); return nil }
func (c *screenCap) LockState(context.Context) (bool, error) {
	c.sm.Lock()
	defer c.sm.Unlock()
	return c.locked, nil
}
func (c *screenCap) Wake(context.Context) error { c.sm.Lock(); c.wakes++; c.sm.Unlock(); return nil }
func (c *screenCap) Unlock(_ context.Context, pw string) (bool, error) {
	c.sm.Lock()
	defer c.sm.Unlock()
	c.typed = append(c.typed, pw)
	if c.unlockErr != nil {
		return true, c.unlockErr
	}
	if !c.stays {
		c.locked = false
	}
	return c.locked, nil
}
func (c *screenCap) isDark() bool     { c.sm.Lock(); defer c.sm.Unlock(); return c.dark }
func (c *screenCap) setLocked(l bool) { c.sm.Lock(); c.locked = l; c.sm.Unlock() }
func (c *screenCap) typedCount() int  { c.sm.Lock(); defer c.sm.Unlock(); return len(c.typed) }

func newScreenRig(t *testing.T, mode protocol.Mode, secret func() (string, bool), auto bool) (*rig, *screenCap) {
	t.Helper()
	oldPoll, oldGap := lockPollEvery, unlockRetryGap
	lockPollEvery, unlockRetryGap = 10*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { lockPollEvery, unlockRetryGap = oldPoll, oldGap })
	sc := &screenCap{fakeCap: newFakeCap()}
	r := &rig{t: t, cap: sc.fakeCap, link: &fakeLink{}, end: make(chan protocol.ErrorCode, 1)}
	r.s = New(Config{
		ID: "s1", Device: protocol.DeviceInfo{ID: "d1", Name: "Phone"}, Mode: mode, Tier: protocol.TierStandard,
		Capturer: sc, OnEnd: func(c protocol.ErrorCode) { r.end <- c },
		UnlockSecret: secret, AutoUnlock: func() bool { return auto },
	})
	if _, err := r.s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.s.Attach(r.link)
	t.Cleanup(func() { r.s.Close(protocol.CodeSessionEnded) })
	return r, sc
}

func (l *fakeLink) anyMessageContains(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, b := range l.ctrl {
		if strings.Contains(string(b), sub) {
			return true
		}
	}
	return false
}

func (l *fakeLink) hasState(match func(raw string) bool) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, b := range l.ctrl {
		if strings.Contains(string(b), `"session.state"`) && match(string(b)) {
			return true
		}
	}
	return false
}

func TestBlackoutIsFullControlOnlyAndRestoredOnClose(t *testing.T) {
	r, sc := newScreenRig(t, protocol.ModeFullControl, nil, false)
	r.connect()
	r.s.OnControl([]byte(`{"type":"privacy.set","on":true}`))
	waitFor(t, "screen dark", sc.isDark)
	waitFor(t, "state says blackout", func() bool {
		return r.link.hasState(func(s string) bool { return strings.Contains(s, `"blackout":true`) })
	})
	r.s.Close(protocol.CodeSessionEnded)
	if sc.isDark() {
		t.Fatal("closing the session must bring the screen back")
	}

	v, vc := newScreenRig(t, protocol.ModeViewOnly, nil, false)
	v.connect()
	v.s.OnControl([]byte(`{"type":"privacy.set","on":true}`))
	time.Sleep(60 * time.Millisecond)
	if vc.isDark() {
		t.Fatal("a view-only phone must not black the Mac out")
	}
}

func TestLockScreenIsAnnouncedAndUnlockTypesTheSavedPasswordOnRequest(t *testing.T) {
	secret := func() (string, bool) { return "hunter2", true }
	r, sc := newScreenRig(t, protocol.ModeFullControl, secret, false)
	r.connect()
	waitFor(t, "wake on connect", func() bool { sc.sm.Lock(); defer sc.sm.Unlock(); return sc.wakes > 0 })
	sc.setLocked(true)
	waitFor(t, "locked announced", func() bool {
		return r.link.hasState(func(s string) bool { return strings.Contains(s, `"locked":true`) })
	})
	if sc.typedCount() != 0 {
		t.Fatal("auto-unlock is off: nothing may be typed until the phone asks")
	}
	r.s.OnControl([]byte(`{"type":"unlock.request"}`))
	waitFor(t, "password typed", func() bool { return sc.typedCount() == 1 })
	waitFor(t, "unlocked announced", func() bool { return !sc.isLocked() })
	if r.link.anyMessageContains("hunter2") {
		t.Fatal("the password must never reach the phone")
	}
}

func (c *screenCap) isLocked() bool { c.sm.Lock(); defer c.sm.Unlock(); return c.locked }

func TestAutoUnlockTriesTwiceThenStops(t *testing.T) {
	secret := func() (string, bool) { return "wrong", true }
	r, sc := newScreenRig(t, protocol.ModeFullControl, secret, true)
	sc.stays = true
	r.connect()
	sc.setLocked(true)
	waitFor(t, "two attempts", func() bool { return sc.typedCount() == 2 })
	time.Sleep(150 * time.Millisecond)
	if n := sc.typedCount(); n != 2 {
		t.Fatalf("a wrong password must not be hammered into the lock screen, got %d tries", n)
	}
	waitFor(t, "failure told", func() bool {
		for _, ty := range r.link.types() {
			if ty == "error:unlock_failed" {
				return true
			}
		}
		return false
	})
}

func TestUnlockWithoutASavedPasswordSaysSo(t *testing.T) {
	r, sc := newScreenRig(t, protocol.ModeFullControl, func() (string, bool) { return "", false }, false)
	r.connect()
	sc.setLocked(true)
	r.s.OnControl([]byte(`{"type":"unlock.request"}`))
	waitFor(t, "unavailable told", func() bool {
		for _, ty := range r.link.types() {
			if ty == "error:unlock_unavailable" {
				return true
			}
		}
		return false
	})
	if sc.typedCount() != 0 {
		t.Fatal("nothing to type")
	}
}
