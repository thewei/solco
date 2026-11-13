package session

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/platform"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// fitCap is a winCap whose windows can be resized: it keeps each window's original frame the way the
// helper's journal does, and answers a restore with a configurable outcome.
type fitCap struct {
	*winCap
	mu3      sync.Mutex
	minW     float64
	fits     []protocol.Rect
	restores []string
	outcome  protocol.RestoreOutcome
	restErr  error
	orig     map[string]protocol.Rect
	moveX    float64 // when set, the fit also moves the window to this x (another display)
}

func (c *fitCap) FitWindow(_ context.Context, id string, w, h float64) (protocol.Rect, bool, error) {
	c.mu3.Lock()
	defer c.mu3.Unlock()
	c.mu2.Lock()
	defer c.mu2.Unlock()
	for i := range c.windows {
		if c.windows[i].ID != id {
			continue
		}
		if c.orig == nil {
			c.orig = map[string]protocol.Rect{}
		}
		if _, ok := c.orig[id]; !ok {
			c.orig[id] = protocol.Rect{X: c.windows[i].X, Y: c.windows[i].Y, Width: c.windows[i].Width, Height: c.windows[i].Height}
		}
		clamped := false
		if c.minW > w {
			w, clamped = c.minW, true
		}
		c.windows[i].Width, c.windows[i].Height = w, h
		if c.moveX != 0 {
			c.windows[i].X = c.moveX
		}
		r := protocol.Rect{X: c.windows[i].X, Y: c.windows[i].Y, Width: w, Height: h}
		c.fits = append(c.fits, r)
		return r, clamped, nil
	}
	return protocol.Rect{}, false, protocol.Errf(protocol.CodeWindowNotFound, "")
}

func (c *fitCap) RestoreWindow(_ context.Context, id string) (protocol.RestoreOutcome, error) {
	c.mu3.Lock()
	defer c.mu3.Unlock()
	c.restores = append(c.restores, id)
	if c.restErr != nil {
		return "", c.restErr
	}
	out := c.outcome
	if out == "" {
		out = protocol.RestoreDone
	}
	if o, ok := c.orig[id]; ok && out == protocol.RestoreDone {
		c.mu2.Lock()
		for i := range c.windows {
			if c.windows[i].ID == id {
				c.windows[i].Width, c.windows[i].Height = o.Width, o.Height
			}
		}
		c.mu2.Unlock()
		delete(c.orig, id)
	}
	return out, nil
}

func (c *fitCap) restored() []string {
	c.mu3.Lock()
	defer c.mu3.Unlock()
	return append([]string(nil), c.restores...)
}

func (c *fitCap) fitted() []protocol.Rect {
	c.mu3.Lock()
	defer c.mu3.Unlock()
	return append([]protocol.Rect(nil), c.fits...)
}

type later struct {
	id    string
	grace time.Duration
}

type fitRig struct {
	*rig
	fc      *fitCap
	mu      sync.Mutex
	laters  []later
	claimed bool
	claims  []string
}

func newFitRig(t *testing.T, mode protocol.Mode) *fitRig {
	t.Helper()
	return newFitRigWith(t, mode, nil)
}

// newFitRigWith is newFitRig with the capturer the session sees wrapped (a test's own capability on top of fitCap).
func newFitRigWith(t *testing.T, mode protocol.Mode, wrap func(*fitCap) platform.Capturer) *fitRig {
	t.Helper()
	wc := &winCap{fakeCap: newFakeCap(), windows: []protocol.WindowInfo{
		{ID: "11", App: "Notes", BundleID: "com.apple.Notes", Title: "Groceries", X: 100, Y: 100, Width: 500, Height: 800, OnScreen: true},
		{ID: "12", App: "Mail", BundleID: "com.apple.mail", Title: "Inbox", X: 40, Y: 60, Width: 900, Height: 600, OnScreen: true},
	}, openID: "11"}
	fc := &fitCap{winCap: wc}
	var seen platform.Capturer = fc
	if wrap != nil {
		seen = wrap(fc)
	}
	fr := &fitRig{fc: fc}
	fr.rig = &rig{t: t, cap: wc.fakeCap, link: &fakeLink{}, end: make(chan protocol.ErrorCode, 1)}
	fr.s = New(Config{
		ID: "s1", Device: protocol.DeviceInfo{ID: "d1", Name: "Phone"}, Mode: mode, Tier: protocol.TierStandard, Idle: true,
		Capturer: seen, OnEnd: func(c protocol.ErrorCode) { fr.end <- c },
		RestoreLater: func(id string, g time.Duration) {
			fr.mu.Lock()
			fr.laters = append(fr.laters, later{id, g})
			fr.mu.Unlock()
		},
		ClaimPending: func(w string) bool {
			fr.mu.Lock()
			defer fr.mu.Unlock()
			fr.claims = append(fr.claims, w)
			return fr.claimed
		},
	})
	if _, err := fr.s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	fr.s.Attach(fr.link)
	t.Cleanup(func() { fr.s.Close(protocol.CodeSessionEnded) })
	return fr
}

func (f *fitRig) deferred() []later {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]later(nil), f.laters...)
}

// phoneViewport is a 390×844 phone with a status bar and a home indicator: the window gets 390×763.
var phoneViewport = map[string]any{"w": 390, "h": 844, "scale": 3, "insets": map[string]any{"top": 47, "bottom": 34, "left": 0, "right": 0}}

// open connects, shows window 11 and fits it.
func (f *fitRig) openFitted(t *testing.T) {
	t.Helper()
	f.s.OnLinkState(LinkConnected)
	f.s.OnControl(ctl("target.select", "kind", "window", "id", "11"))
	waitFor(t, "window capture", func() bool { return f.fc.startCount() == 1 })
	f.s.OnControl(ctl("window.fit", "viewport", phoneViewport))
	waitFor(t, "fitted", func() bool { return f.s.State().Fitted && f.fc.startCount() == 2 })
}

func TestFitResizesTheWindowToThePhoneAndRestartsTheCaptureOnItsShape(t *testing.T) {
	f := newFitRig(t, protocol.ModeFullControl)
	f.openFitted(t)
	if got := f.fc.fitted(); len(got) != 1 || got[0].Width != 390 || got[0].Height != 763 {
		t.Fatalf("fit asked for %+v, want 390×763 (screen minus insets)", got)
	}
	st := f.s.State()
	if st.Clamped != nil || st.Target != protocol.TargetWindow {
		t.Fatalf("state = %+v", st)
	}
	// The capture follows the new shape: 390×763 pt at 2x is 780×1526 px, scaled to the tier's 1080 high.
	cfg := f.fc.lastStart()
	if cfg.WindowID != "11" || cfg.Width*763 < cfg.Height*389 || cfg.Width*763 > cfg.Height*391 {
		t.Fatalf("capture cfg %+v does not follow the fitted shape", cfg)
	}
	// The same viewport again is idempotent; a rotation is a new viewport and fits again.
	f.s.OnControl(ctl("window.fit", "viewport", phoneViewport))
	time.Sleep(40 * time.Millisecond)
	if len(f.fc.fitted()) != 1 {
		t.Fatalf("an unchanged viewport fitted again: %v", f.fc.fitted())
	}
	landscape := map[string]any{"w": 844, "h": 390, "scale": 3, "insets": map[string]any{"top": 0, "bottom": 21, "left": 47, "right": 47}}
	f.s.OnControl(ctl("window.fit", "viewport", landscape))
	waitFor(t, "rotation re-fit", func() bool { return len(f.fc.fitted()) == 2 })
	if got := f.fc.fitted()[1]; got.Width != 750 || got.Height != 369 {
		t.Fatalf("landscape fit = %+v, want 750×369", got)
	}
}

func TestFitReportsAClampedWindowSoThePhoneCanScaleThePicture(t *testing.T) {
	f := newFitRig(t, protocol.ModeFullControl)
	f.fc.minW = 520
	f.s.OnLinkState(LinkConnected)
	f.s.OnControl(ctl("target.select", "kind", "window", "id", "11"))
	waitFor(t, "window capture", func() bool { return f.fc.startCount() == 1 })
	f.s.OnControl(ctl("window.fit", "viewport", phoneViewport))
	waitFor(t, "fitted", func() bool { return f.s.State().Fitted })
	c := f.s.State().Clamped
	if c == nil || c.MinW != 520 || c.Scale < 0.74 || c.Scale > 0.76 {
		t.Fatalf("clamped = %+v, want minW 520 and scale 390/520", c)
	}
}

func TestFitOffGivesTheWindowBackAndTellsThePhone(t *testing.T) {
	f := newFitRig(t, protocol.ModeFullControl)
	f.openFitted(t)
	f.s.OnControl(ctl("window.fit.off"))
	waitFor(t, "restored", func() bool { return !f.s.State().Fitted && f.fc.startCount() == 3 })
	if got := f.fc.restored(); len(got) != 1 || got[0] != "11" {
		t.Fatalf("restores = %v", got)
	}
	var m protocol.WindowRestored
	waitFor(t, "restored report", func() bool { return f.lastOf("window.restored") != nil })
	_ = json.Unmarshal(f.lastOf("window.restored"), &m)
	if m.WindowID != "11" || m.Outcome != protocol.RestoreDone {
		t.Fatalf("report = %+v", m)
	}
	if cfg := f.fc.lastStart(); cfg.Width != 674 { // back to 500×800
		t.Fatalf("capture after restore = %+v", cfg)
	}
}

func TestSwitchingWindowOrDisplayGivesTheFittedWindowBack(t *testing.T) {
	f := newFitRig(t, protocol.ModeFullControl)
	f.openFitted(t)
	f.s.OnControl(ctl("target.select", "kind", "window", "id", "12"))
	waitFor(t, "switched", func() bool { return f.s.State().Window != nil && f.s.State().Window.ID == "12" })
	if got := f.fc.restored(); len(got) != 1 || got[0] != "11" {
		t.Fatalf("restores after a window switch = %v", got)
	}
	if st := f.s.State(); st.Fitted {
		t.Fatalf("the new window arrived fitted: %+v", st)
	}

	f2 := newFitRig(t, protocol.ModeFullControl)
	f2.openFitted(t)
	f2.s.OnControl(ctl("target.select", "kind", "display", "id", f2.s.State().DisplayID))
	waitFor(t, "back to the display", func() bool { return f2.s.State().Target != protocol.TargetWindow })
	if got := f2.fc.restored(); len(got) != 1 || got[0] != "11" {
		t.Fatalf("restores after leaving for a display = %v", got)
	}
}

func TestSessionEndAndMacDisconnectRestoreAtOnce(t *testing.T) {
	f := newFitRig(t, protocol.ModeFullControl)
	f.openFitted(t)
	f.s.OnControl(ctl("session.end"))
	waitFor(t, "ended", func() bool { return len(f.end) == 1 })
	f.s.OnLinkState(LinkClosed) // the transport closing after the phone's own end is not a lost link
	if got := f.fc.restored(); len(got) != 1 || got[0] != "11" {
		t.Fatalf("restores after session.end = %v", got)
	}
	if got := f.deferred(); len(got) != 0 {
		t.Fatalf("an ended session deferred its restore: %v", got)
	}

	g := newFitRig(t, protocol.ModeFullControl)
	g.openFitted(t)
	g.s.Stop()
	if got := g.fc.restored(); len(got) != 1 {
		t.Fatalf("restores after the Mac's Disconnect = %v", got)
	}
}

func TestALostLinkLeavesTheWindowFittedForTheGrace(t *testing.T) {
	f := newFitRig(t, protocol.ModeFullControl)
	f.openFitted(t)
	f.s.OnLinkState(LinkClosed)
	waitFor(t, "ended", func() bool { return len(f.end) == 1 })
	if got := f.fc.restored(); len(got) != 0 {
		t.Fatalf("a lost link restored at once: %v", got)
	}
	if got := f.deferred(); len(got) != 1 || got[0].id != "11" || got[0].grace != FitGrace || FitGrace != 20*time.Second {
		t.Fatalf("deferred = %+v", got)
	}
}

func TestAGoneHelperHandsTheRestoreToTheJournal(t *testing.T) {
	f := newFitRig(t, protocol.ModeFullControl)
	f.openFitted(t)
	f.fc.restErr = protocol.Errf(protocol.CodeHelperFailed, "gone")
	f.s.Stop()
	if got := f.deferred(); len(got) != 1 || got[0].id != "11" || got[0].grace != 0 {
		t.Fatalf("deferred = %+v, want an immediate journal restore", got)
	}
}

func TestARestoreTheUserMovedOnIsReportedAsSkipped(t *testing.T) {
	f := newFitRig(t, protocol.ModeFullControl)
	f.openFitted(t)
	f.fc.outcome = protocol.RestoreSkipped
	f.s.OnControl(ctl("window.fit.off"))
	waitFor(t, "skipped report", func() bool { return f.lastOf("window.restored") != nil })
	var m protocol.WindowRestored
	_ = json.Unmarshal(f.lastOf("window.restored"), &m)
	if m.Outcome != protocol.RestoreSkipped {
		t.Fatalf("report = %+v", m)
	}
}

func TestViewOnlyCannotFitAndABadViewportIsRefused(t *testing.T) {
	v := newFitRig(t, protocol.ModeViewOnly)
	v.s.OnLinkState(LinkConnected)
	v.s.OnControl(ctl("target.select", "kind", "window", "id", "11"))
	waitFor(t, "window capture", func() bool { return v.fc.startCount() == 1 })
	v.s.OnControl(ctl("window.fit", "viewport", phoneViewport))
	waitFor(t, "view_only", func() bool { return v.lastOf("error") != nil })
	if len(v.fc.fitted()) != 0 {
		t.Fatalf("a view-only session moved a window: %v", v.fc.fitted())
	}

	f := newFitRig(t, protocol.ModeFullControl)
	f.s.OnLinkState(LinkConnected)
	f.s.OnControl(ctl("target.select", "kind", "window", "id", "11"))
	waitFor(t, "window capture", func() bool { return f.fc.startCount() == 1 })
	f.s.OnControl(ctl("window.fit", "viewport", map[string]any{"w": 20, "h": 20, "scale": 3, "insets": map[string]any{}}))
	waitFor(t, "bad_request", func() bool { return f.lastOf("error") != nil })
	if len(f.fc.fitted()) != 0 {
		t.Fatalf("an absurd viewport fitted: %v", f.fc.fitted())
	}
}

func TestAWindowLeftFittedByALostLinkIsOwnedAgainWhenChosen(t *testing.T) {
	f := newFitRig(t, protocol.ModeFullControl)
	f.claimed = true
	f.s.OnLinkState(LinkConnected)
	f.s.OnControl(ctl("target.select", "kind", "window", "id", "11"))
	waitFor(t, "arrived fitted", func() bool { return f.s.State().Fitted })
	f.s.Close(protocol.CodeSessionEnded)
	if got := f.fc.restored(); len(got) != 1 || got[0] != "11" {
		t.Fatalf("a claimed window is given back when the session ends: %v", got)
	}
}

func TestAGoneWindowForgetsItsFit(t *testing.T) {
	f := newFitRig(t, protocol.ModeFullControl)
	f.openFitted(t)
	f.s.windowGone("11", protocol.GoneClosed)
	waitFor(t, "journal entry released", func() bool { return len(f.fc.restored()) == 1 })
	if f.s.State().Fitted {
		t.Fatal("a window that is gone is still shown as fitted")
	}
}

func TestAFitThatMovesTheWindowToAnotherDisplayTakesThatDisplaysScale(t *testing.T) {
	f := newFitRig(t, protocol.ModeFullControl)
	f.fc.moveX = 1500 // the second display, 1x (the first is 2x)
	f.s.OnLinkState(LinkConnected)
	f.s.OnControl(ctl("target.select", "kind", "window", "id", "11"))
	waitFor(t, "window capture", func() bool { return f.fc.startCount() == 1 })
	f.s.OnControl(ctl("window.fit", "viewport", phoneViewport))
	waitFor(t, "fitted and recaptured", func() bool { return f.s.State().Fitted && f.fc.startCount() == 2 })
	// At 1x the window's own pixels are 390×763; a stale 2x scale would ask for the tier's 1080 high instead.
	if cfg := f.fc.lastStart(); cfg.Height < 760 || cfg.Height > 764 {
		t.Fatalf("capture %+v: the picture is sized for the old display's scale", cfg)
	}
}
