package session

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/platform"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// winCap is a fake backend that also offers window mode.
type winCap struct {
	*fakeCap
	mu2       sync.Mutex
	windows   []protocol.WindowInfo
	raised    []string
	opened    []string
	openID    string
	openErr   error
	unsupport bool
}

func (c *winCap) WindowsSupported() bool { return !c.unsupport }
func (c *winCap) ListWindows(context.Context) ([]protocol.WindowInfo, error) {
	c.mu2.Lock()
	defer c.mu2.Unlock()
	return append([]protocol.WindowInfo(nil), c.windows...), nil
}
func (c *winCap) ListApps(context.Context) ([]protocol.AppInfo, error) {
	return []protocol.AppInfo{{BundleID: "com.apple.Notes", Name: "Notes", Running: true}}, nil
}
func (c *winCap) AppIcon(context.Context, string) (string, error) { return "UE5H", nil }
func (c *winCap) OpenApp(_ context.Context, id string) (string, error) {
	c.mu2.Lock()
	defer c.mu2.Unlock()
	c.opened = append(c.opened, id)
	return c.openID, c.openErr
}
func (c *winCap) RaiseWindow(_ context.Context, id string) error {
	c.mu2.Lock()
	defer c.mu2.Unlock()
	c.raised = append(c.raised, id)
	for i := range c.windows {
		if c.windows[i].ID == id {
			c.windows[i].OnScreen = true
		}
	}
	return nil
}
func (c *winCap) WindowThumbnail(context.Context, string, int) (string, error) { return "/9j/", nil }
func (c *winCap) raisedIDs() []string {
	c.mu2.Lock()
	defer c.mu2.Unlock()
	return append([]string(nil), c.raised...)
}

// StartCapture answers a window capture with the geometry a helper would report.
func (c *winCap) StartCapture(ctx context.Context, cfg protocol.CaptureConfig) (protocol.CaptureStarted, error) {
	started, err := c.fakeCap.StartCapture(ctx, cfg)
	if err != nil || cfg.WindowID == "" {
		return started, err
	}
	c.mu2.Lock()
	defer c.mu2.Unlock()
	for _, w := range c.windows {
		if w.ID == cfg.WindowID {
			r := protocol.Rect{X: w.X, Y: w.Y, Width: w.Width, Height: w.Height}
			started.Window = &protocol.WindowGeometry{CaptureID: cfg.CaptureID, Source: r, Windows: []protocol.Rect{r}, OnScreen: w.OnScreen}
			return started, nil
		}
	}
	return protocol.CaptureStarted{}, protocol.Errf(protocol.CodeWindowNotFound, "")
}

func newWinRig(t *testing.T, mode protocol.Mode, idle bool) (*rig, *winCap) {
	t.Helper()
	wc := &winCap{fakeCap: newFakeCap(), windows: []protocol.WindowInfo{
		{ID: "11", App: "Notes", BundleID: "com.apple.Notes", Title: "Groceries", X: 100, Y: 100, Width: 500, Height: 800, OnScreen: true},
		{ID: "12", App: "Mail", BundleID: "com.apple.mail", Title: "Inbox", X: 40, Y: 60, Width: 900, Height: 600, OnScreen: false},
	}, openID: "11"}
	r := &rig{t: t, cap: wc.fakeCap, link: &fakeLink{}, end: make(chan protocol.ErrorCode, 1)}
	r.s = New(Config{
		ID: "s1", Device: protocol.DeviceInfo{ID: "d1", Name: "Phone"}, Mode: mode, Tier: protocol.TierStandard, Idle: idle,
		Capturer: wc, OnEnd: func(c protocol.ErrorCode) { r.end <- c },
	})
	if _, err := r.s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.s.Attach(r.link)
	t.Cleanup(func() { r.s.Close(protocol.CodeSessionEnded) })
	return r, wc
}

func (r *rig) has(typ string) bool {
	for _, ty := range r.link.types() {
		if ty == typ {
			return true
		}
	}
	return false
}

func (r *rig) lastOf(typ string) []byte {
	r.link.mu.Lock()
	defer r.link.mu.Unlock()
	for i := len(r.link.ctrl) - 1; i >= 0; i-- {
		var e struct{ Type string }
		_ = json.Unmarshal(r.link.ctrl[i], &e)
		if e.Type == typ {
			return r.link.ctrl[i]
		}
	}
	return nil
}

func ctl(typ string, kv ...any) []byte {
	m := map[string]any{"type": typ}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return mustJSON(m)
}

func TestIdleSessionCapturesNothingUntilTheTargetIsChosen(t *testing.T) {
	r, wc := newWinRig(t, protocol.ModeFullControl, true)
	if st := r.s.State(); st.Target != protocol.TargetNone {
		t.Fatalf("idle state = %+v", st)
	}
	r.s.OnLinkState(LinkConnected)
	waitFor(t, "state sent", func() bool { return r.has("session.state") })
	if wc.startCount() != 0 {
		t.Fatalf("an idle session captured before the phone chose: %v", wc.starts)
	}
	// Nothing chosen: input is not forwarded anywhere.
	r.s.OnPointer(move(1, 0.5, 0.5))
	time.Sleep(30 * time.Millisecond)
	if n := len(wc.in.snapshot()) - wc.in.count("release"); n != 0 {
		t.Fatalf("input with no target: %v", wc.in.snapshot())
	}
	r.s.OnControl(ctl("target.select", "kind", "window", "id", "11"))
	waitFor(t, "window capture", func() bool { return wc.startCount() == 1 })
	cfg := wc.lastStart()
	if cfg.WindowID != "11" {
		t.Fatalf("cfg = %+v", cfg)
	}
	// 500x800 pt on a 2x display = 1000x1600 px; standard tier is 1080 high, so the width follows the window.
	if cfg.Width != 674 || cfg.Height != 1080 {
		t.Fatalf("window size %dx%d", cfg.Width, cfg.Height)
	}
	st := r.s.State()
	if st.Target != protocol.TargetWindow || st.Window == nil || st.Window.App != "Notes" || st.Width != 674 {
		t.Fatalf("state = %+v", st)
	}
	if got := wc.raisedIDs(); len(got) != 1 || got[0] != "11" {
		t.Fatalf("a full-control selection brings the window forward once: %v", got)
	}
}

func TestWindowPointerMapsOntoTheWindowAndStaysInsideIt(t *testing.T) {
	r, wc := newWinRig(t, protocol.ModeFullControl, true)
	r.s.OnLinkState(LinkConnected)
	r.s.OnControl(ctl("target.select", "kind", "window", "id", "11"))
	waitFor(t, "window capture", func() bool { return wc.startCount() == 1 })

	r.s.OnPointer(move(1, 0.5, 0.5))
	waitFor(t, "move", func() bool { return wc.in.count("move 350.0,500.0") == 1 })

	// A popup grows out of the window: the helper widens the picture and reports where it now sits.
	id := wc.lastStart().CaptureID
	src := protocol.Rect{X: 100, Y: 100, Width: 1000, Height: 800}
	r.cap.events <- platform.Event{Kind: platform.EventWindowGeometry, CaptureID: id, Geometry: &protocol.WindowGeometry{
		CaptureID: id, Source: src,
		Windows:  []protocol.Rect{{X: 100, Y: 100, Width: 500, Height: 800}, {X: 550, Y: 300, Width: 300, Height: 200}},
		OnScreen: true,
	}}
	waitFor(t, "geometry applied", func() bool {
		r.s.OnPointer(move(uint64(100+len(wc.in.snapshot())), 0.5, 0.35))
		return wc.in.count("move 600.0,380.0") > 0 // inside the popup, outside the window
	})

	// Outside the window and every popup: dropped.
	before := len(wc.in.snapshot())
	r.s.OnPointer(move(900, 0.95, 0.9))
	time.Sleep(30 * time.Millisecond)
	if len(wc.in.snapshot()) != before {
		t.Fatalf("a move outside the window was forwarded: %v", wc.in.snapshot()[before:])
	}
	// A press outside is dropped too; a drag that started inside may leave, and the release always arrives.
	r.s.OnControl(mustJSON(map[string]any{"type": "pointer.button", "seq": 901, "x": 0.95, "y": 0.9, "button": "left", "down": true}))
	if wc.in.count("button left true 1050.0,820.0 n1") != 0 {
		t.Fatalf("a press outside was forwarded: %v", wc.in.snapshot())
	}
	r.s.OnControl(mustJSON(map[string]any{"type": "pointer.button", "seq": 902, "x": 0.1, "y": 0.1, "button": "left", "down": true}))
	r.s.OnPointer(move(903, 0.95, 0.9))
	waitFor(t, "drag leaves the window", func() bool { return wc.in.count("move 1050.0,820.0") == 1 })
	r.s.OnControl(mustJSON(map[string]any{"type": "pointer.button", "seq": 904, "x": 0.95, "y": 0.9, "button": "left", "down": false}))
	waitFor(t, "release outside", func() bool { return wc.in.count("button left false 1050.0,820.0 n1") == 1 })
}

func TestSystemShortcutsDoNotLeaveTheWindow(t *testing.T) {
	r, wc := newWinRig(t, protocol.ModeFullControl, true)
	r.s.OnLinkState(LinkConnected)
	r.s.OnControl(ctl("target.select", "kind", "window", "id", "11"))
	waitFor(t, "window capture", func() bool { return wc.startCount() == 1 })

	r.s.OnControl(mustJSON(map[string]any{"type": "key", "code": "MetaLeft", "down": true}))
	r.s.OnControl(mustJSON(map[string]any{"type": "key", "code": "Tab", "down": true}))
	r.s.OnControl(mustJSON(map[string]any{"type": "key", "code": "KeyC", "down": true}))
	r.s.OnControl(mustJSON(map[string]any{"type": "key", "code": "ControlLeft", "down": true}))
	r.s.OnControl(mustJSON(map[string]any{"type": "key", "code": "ArrowRight", "down": true}))
	got := wc.in.snapshot()
	want := []string{"release", "key MetaLeft true", "key KeyC true", "key ControlLeft true"}
	for _, w := range want {
		if wc.in.count(w) == 0 {
			t.Fatalf("missing %q in %v", w, got)
		}
	}
	if wc.in.count("key Tab true") != 0 || wc.in.count("key ArrowRight true") != 0 {
		t.Fatalf("a system shortcut reached the Mac: %v", got)
	}
}

func TestShortcutsThatHideMinimizeOrQuitDoNotReachTheMac(t *testing.T) {
	r, wc := newWinRig(t, protocol.ModeFullControl, true)
	r.s.OnLinkState(LinkConnected)
	r.s.OnControl(ctl("target.select", "kind", "window", "id", "11"))
	waitFor(t, "window capture", func() bool { return wc.startCount() == 1 })

	r.s.OnControl(mustJSON(map[string]any{"type": "key", "code": "MetaLeft", "down": true}))
	for _, code := range []string{"KeyH", "KeyM", "KeyQ", "KeyC"} {
		r.s.OnControl(mustJSON(map[string]any{"type": "key", "code": code, "down": true}))
	}
	r.s.OnControl(mustJSON(map[string]any{"type": "key", "code": "ControlLeft", "down": true}))
	r.s.OnControl(mustJSON(map[string]any{"type": "key", "code": "KeyQ", "down": true}))
	for _, code := range []string{"KeyH", "KeyM", "KeyQ"} {
		if wc.in.count("key "+code+" true") != 0 {
			t.Fatalf("⌘%s reached the Mac: %v", code, wc.in.snapshot())
		}
	}
	if wc.in.count("key KeyC true") != 1 {
		t.Fatalf("⌘C must still pass: %v", wc.in.snapshot())
	}
	// Without ⌘ the same letters are ordinary typing.
	r.s.OnControl(mustJSON(map[string]any{"type": "key", "code": "MetaLeft", "down": false}))
	r.s.OnControl(mustJSON(map[string]any{"type": "key", "code": "KeyQ", "down": true}))
	if wc.in.count("key KeyQ true") != 1 {
		t.Fatalf("a plain Q must pass: %v", wc.in.snapshot())
	}
}

func TestOffscreenWindowIsWatchedFirstAndActivatedOnRequest(t *testing.T) {
	r, wc := newWinRig(t, protocol.ModeFullControl, true)
	r.s.OnLinkState(LinkConnected)
	r.s.OnControl(ctl("target.select", "kind", "window", "id", "12"))
	waitFor(t, "window capture", func() bool { return wc.startCount() == 1 })
	if len(wc.raisedIDs()) != 0 {
		t.Fatalf("selecting an off-screen window must not switch the Mac's Space: %v", wc.raisedIDs())
	}
	r.s.OnPointer(move(1, 0.5, 0.5))
	waitFor(t, "offscreen notice", func() bool { return r.has("error:window_offscreen") })
	if n := len(wc.in.snapshot()) - wc.in.count("release"); n != 0 {
		t.Fatalf("input reached an off-screen window: %v", wc.in.snapshot())
	}

	r.s.OnControl(ctl("target.activate"))
	waitFor(t, "recaptured on screen", func() bool { return wc.startCount() == 2 })
	if got := wc.raisedIDs(); len(got) != 1 || got[0] != "12" {
		t.Fatalf("activate raised %v", got)
	}
	r.s.OnPointer(move(2, 0.5, 0.5))
	waitFor(t, "move after activation", func() bool { return wc.in.count("move 490.0,360.0") == 1 })
}

func TestViewOnlyWatchesAWindowButNeverOpensOrRaises(t *testing.T) {
	r, wc := newWinRig(t, protocol.ModeViewOnly, true)
	r.s.OnLinkState(LinkConnected)
	r.s.OnControl(ctl("target.select", "kind", "window", "id", "11"))
	waitFor(t, "window capture", func() bool { return wc.startCount() == 1 })
	if len(wc.raisedIDs()) != 0 {
		t.Fatalf("view-only raised a window: %v", wc.raisedIDs())
	}
	r.s.OnControl(ctl("app.open", "bundleId", "com.apple.Notes"))
	waitFor(t, "view_only", func() bool { return r.has("error:view_only") })
	if len(wc.opened) != 0 {
		t.Fatalf("view-only opened an app: %v", wc.opened)
	}
	r.s.OnControl(ctl("windows.list"))
	waitFor(t, "snapshot", func() bool { return r.has("windows.snapshot") })
}

func TestWindowGoneStopsTheCaptureAndKeepsTheSession(t *testing.T) {
	r, wc := newWinRig(t, protocol.ModeFullControl, true)
	r.s.OnLinkState(LinkConnected)
	r.s.OnControl(ctl("target.select", "kind", "window", "id", "11"))
	waitFor(t, "window capture", func() bool { return wc.startCount() == 1 })
	id := wc.lastStart().CaptureID

	r.cap.events <- platform.Event{Kind: platform.EventWindowGone, WindowID: "11", GoneReason: protocol.GoneClosed}
	waitFor(t, "window.gone", func() bool { return r.has("window.gone") })
	var gone protocol.WindowGone
	_ = json.Unmarshal(r.lastOf("window.gone"), &gone)
	if gone.WindowID != "11" || gone.Reason != protocol.GoneClosed {
		t.Fatalf("gone = %+v", gone)
	}
	waitFor(t, "capture stopped", func() bool { wc.mu.Lock(); defer wc.mu.Unlock(); return wc.stops > 0 })
	if st := r.s.State(); st.Target != protocol.TargetNone || st.Window != nil {
		t.Fatalf("state after gone = %+v", st)
	}
	// The helper's own capture.stopped for that generation must not restart anything or end the session.
	r.cap.events <- platform.Event{Kind: platform.EventCaptureStopped, CaptureID: id}
	time.Sleep(50 * time.Millisecond)
	if wc.startCount() != 1 {
		t.Fatalf("capture restarted after the window left: %d starts", wc.startCount())
	}
	select {
	case code := <-r.end:
		t.Fatalf("session ended: %s", code)
	default:
	}
	// A different window can be chosen straight away.
	r.s.OnControl(ctl("target.select", "kind", "window", "id", "12"))
	waitFor(t, "second window", func() bool { return wc.startCount() == 2 })
}

func TestOpeningAnAppSelectsItsWindowAndFailuresAreStableWords(t *testing.T) {
	r, wc := newWinRig(t, protocol.ModeFullControl, true)
	r.s.OnLinkState(LinkConnected)
	r.s.OnControl(ctl("app.open", "bundleId", "com.apple.Notes"))
	waitFor(t, "app.opening", func() bool { return r.has("app.opening") })
	waitFor(t, "window capture", func() bool { return wc.startCount() == 1 })
	if cfg := wc.lastStart(); cfg.WindowID != "11" {
		t.Fatalf("cfg = %+v", cfg)
	}

	wc.mu2.Lock()
	wc.openErr = protocol.Errf(protocol.CodeAppOpenFailed, "no_window")
	wc.mu2.Unlock()
	r.s.OnControl(ctl("app.open", "bundleId", "com.example.Slow"))
	waitFor(t, "app.open_failed", func() bool { return r.has("app.open_failed") })
	var failed protocol.AppOpenFailed
	_ = json.Unmarshal(r.lastOf("app.open_failed"), &failed)
	if failed.BundleID != "com.example.Slow" || failed.Reason != "no_window" {
		t.Fatalf("failed = %+v", failed)
	}

	r.s.OnControl(ctl("app.open", "bundleId", "bad id"))
	waitFor(t, "bad bundle", func() bool { return r.has("error:bad_request") })
}

func TestCatalogsAnswerAndLargeAnswersAreBounded(t *testing.T) {
	r, wc := newWinRig(t, protocol.ModeViewOnly, true)
	r.s.OnLinkState(LinkConnected)
	for i := 0; i < 100; i++ {
		wc.mu2.Lock()
		wc.windows = append(wc.windows, protocol.WindowInfo{ID: "w" + string(rune('A'+i%26)) + string(rune('a'+i/26)), App: "X", Width: 200, Height: 200, OnScreen: true})
		wc.mu2.Unlock()
	}
	r.s.OnControl(ctl("windows.list"))
	r.s.OnControl(ctl("apps.list"))
	r.s.OnControl(ctl("app.icon.get", "bundleId", "com.apple.Notes"))
	r.s.OnControl(ctl("window.thumb.get", "windowId", "11", "maxWidth", 9999))
	waitFor(t, "all answers", func() bool {
		return r.has("windows.snapshot") && r.has("apps.snapshot") && r.has("app.icon") && r.has("window.thumb")
	})
	var snap protocol.WindowsSnapshot
	_ = json.Unmarshal(r.lastOf("windows.snapshot"), &snap)
	if len(snap.Windows) != protocol.MaxWindowsListed {
		t.Fatalf("listed %d windows", len(snap.Windows))
	}
}

func TestWithoutWindowSupportTheSessionStaysOnDisplays(t *testing.T) {
	r, wc := newWinRig(t, protocol.ModeFullControl, false)
	wc.unsupport = true
	r.connect()
	r.s.OnControl(ctl("windows.list"))
	r.s.OnControl(ctl("target.select", "kind", "window", "id", "11"))
	waitFor(t, "windows_unavailable", func() bool { return r.has("error:windows_unavailable") })
	if wc.startCount() != 1 || wc.lastStart().WindowID != "" {
		t.Fatalf("starts = %+v", wc.starts)
	}
}

func TestSwitchingBackToADisplayLeavesWindowMode(t *testing.T) {
	r, wc := newWinRig(t, protocol.ModeFullControl, true)
	r.s.OnLinkState(LinkConnected)
	r.s.OnControl(ctl("target.select", "kind", "window", "id", "11"))
	waitFor(t, "window capture", func() bool { return wc.startCount() == 1 })
	r.s.OnControl(ctl("target.select", "kind", "display", "id", "1"))
	waitFor(t, "display capture", func() bool { return wc.startCount() == 2 })
	if cfg := wc.lastStart(); cfg.WindowID != "" || cfg.DisplayID != "1" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if st := r.s.State(); st.Target != "" || st.Window != nil || st.Width != 1728 {
		t.Fatalf("state = %+v", st)
	}
	r.s.OnPointer(move(1, 0.5, 0.5))
	waitFor(t, "display mapping", func() bool { return wc.in.count("move 720.0,450.0") == 1 })
}

func TestScrollInAWindowFirstPutsThePointerOnThatWindow(t *testing.T) {
	r, wc := newWinRig(t, protocol.ModeFullControl, true)
	r.s.OnLinkState(LinkConnected)
	r.s.OnControl(ctl("target.select", "kind", "window", "id", "11"))
	waitFor(t, "window capture", func() bool { return wc.startCount() == 1 })

	r.s.OnControl(ctl("pointer.scroll", "dx", 0, "dy", 40))
	waitFor(t, "scroll", func() bool { return r.cap.in.count("scroll 0,40") == 1 })
	got := r.cap.in.snapshot()
	if len(got) < 2 || got[len(got)-2] != "move 350.0,500.0" {
		t.Fatalf("input = %v, want the pointer on the window's centre (350,500) before the wheel", got)
	}
	// the pointer is on the window now: a second scroll adds no move
	r.s.OnControl(ctl("pointer.scroll", "dx", 0, "dy", 40))
	waitFor(t, "second scroll", func() bool { return r.cap.in.count("scroll 0,40") == 2 })
	moves := 0
	for _, l := range r.cap.in.snapshot() {
		if strings.HasPrefix(l, "move ") {
			moves++
		}
	}
	if moves != 1 {
		t.Fatalf("%d moves for two scrolls, want 1: %v", moves, r.cap.in.snapshot())
	}
}
