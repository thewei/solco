package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/platform"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// ---- fakes

type fakeInput struct {
	mu  sync.Mutex
	log []string
}

func (f *fakeInput) add(s string, a ...any) {
	f.mu.Lock()
	f.log = append(f.log, fmt.Sprintf(s, a...))
	f.mu.Unlock()
}
func (f *fakeInput) PointerMove(x, y float64) error { f.add("move %.1f,%.1f", x, y); return nil }
func (f *fakeInput) PointerButton(b string, d bool, x, y float64, n int) error {
	f.add("button %s %v %.1f,%.1f n%d", b, d, x, y, n)
	return nil
}
func (f *fakeInput) Scroll(dx, dy float64) error { f.add("scroll %.0f,%.0f", dx, dy); return nil }
func (f *fakeInput) Key(c string, d bool) error {
	if c == "Bogus" {
		return fmt.Errorf("unknown key")
	}
	f.add("key %s %v", c, d)
	return nil
}
func (f *fakeInput) Text(t string) error { f.add("text %q", t); return nil }
func (f *fakeInput) ReleaseAll() error   { f.add("release"); return nil }
func (f *fakeInput) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}
func (f *fakeInput) count(s string) int {
	n := 0
	for _, l := range f.snapshot() {
		if l == s {
			n++
		}
	}
	return n
}

type fakeClip struct {
	mu   sync.Mutex
	text string
}

func (c *fakeClip) get() string                              { c.mu.Lock(); defer c.mu.Unlock(); return c.text }
func (c *fakeClip) ReadText(context.Context) (string, error) { return c.get(), nil }
func (c *fakeClip) WriteText(_ context.Context, t string) error {
	c.mu.Lock()
	c.text = t
	c.mu.Unlock()
	return nil
}

type fakeCap struct {
	displays []protocol.DisplayInfo
	frames   chan platform.Frame
	events   chan platform.Event
	done     chan struct{}
	in       *fakeInput
	clip     *fakeClip

	mu       sync.Mutex
	starts   []protocol.CaptureConfig
	stops    int
	keyReqs  int
	closed   bool
	startErr error
}

func newFakeCap() *fakeCap {
	return &fakeCap{
		displays: []protocol.DisplayInfo{
			{ID: "1", Name: "Main", X: 0, Y: 0, Width: 1440, Height: 900, PixelWidth: 2880, PixelHeight: 1800, Scale: 2, IsMain: true},
			{ID: "2", Name: "Ext", X: 1440, Y: -200, Width: 1920, Height: 1080, PixelWidth: 1920, PixelHeight: 1080, Scale: 1},
		},
		frames: make(chan platform.Frame, 16), events: make(chan platform.Event, 16), done: make(chan struct{}),
		in: &fakeInput{}, clip: &fakeClip{},
	}
}
func (c *fakeCap) Displays(context.Context) ([]protocol.DisplayInfo, error) { return c.displays, nil }
func (c *fakeCap) Permissions(context.Context) (protocol.PermissionStatus, error) {
	return protocol.PermissionStatus{ScreenRecording: true, Accessibility: true}, nil
}
func (c *fakeCap) StartCapture(_ context.Context, cfg protocol.CaptureConfig) (protocol.CaptureStarted, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.startErr != nil {
		return protocol.CaptureStarted{}, c.startErr
	}
	c.starts = append(c.starts, cfg)
	return protocol.CaptureStarted{CaptureID: cfg.CaptureID, Width: cfg.Width, Height: cfg.Height, HardwareEncoder: true}, nil
}
func (c *fakeCap) StopCapture(context.Context) error {
	c.mu.Lock()
	c.stops++
	c.mu.Unlock()
	return nil
}
func (c *fakeCap) RequestKeyframe()              { c.mu.Lock(); c.keyReqs++; c.mu.Unlock() }
func (c *fakeCap) TuneBitrate(int)               {}
func (c *fakeCap) Frames() <-chan platform.Frame { return c.frames }
func (c *fakeCap) Events() <-chan platform.Event { return c.events }
func (c *fakeCap) Input() platform.Input         { return c.in }
func (c *fakeCap) Clipboard() platform.Clipboard { return c.clip }
func (c *fakeCap) Done() <-chan struct{}         { return c.done }
func (c *fakeCap) Err() error                    { return nil }
func (c *fakeCap) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.done)
	}
	return nil
}
func (c *fakeCap) startCount() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.starts) }
func (c *fakeCap) lastStart() protocol.CaptureConfig {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.starts[len(c.starts)-1]
}

type fakeLink struct {
	mu     sync.Mutex
	video  []time.Duration
	ctrl   [][]byte
	closed bool
}

func (l *fakeLink) WriteVideo(_ []byte, d time.Duration) error {
	l.mu.Lock()
	l.video = append(l.video, d)
	l.mu.Unlock()
	return nil
}
func (l *fakeLink) SendControl(b []byte) error {
	l.mu.Lock()
	l.ctrl = append(l.ctrl, b)
	l.mu.Unlock()
	return nil
}
func (l *fakeLink) Close() { l.mu.Lock(); l.closed = true; l.mu.Unlock() }
func (l *fakeLink) types() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, b := range l.ctrl {
		var e struct{ Type, Code string }
		_ = json.Unmarshal(b, &e)
		if e.Code != "" {
			out = append(out, e.Type+":"+e.Code)
		} else {
			out = append(out, e.Type)
		}
	}
	return out
}
func (l *fakeLink) videoCount() int { l.mu.Lock(); defer l.mu.Unlock(); return len(l.video) }

// ---- harness

type rig struct {
	t    *testing.T
	s    *Session
	cap  *fakeCap
	link *fakeLink
	end  chan protocol.ErrorCode
}

func newRig(t *testing.T, mode protocol.Mode) *rig {
	t.Helper()
	r := &rig{t: t, cap: newFakeCap(), link: &fakeLink{}, end: make(chan protocol.ErrorCode, 1)}
	r.s = New(Config{
		ID: "s1", Device: protocol.DeviceInfo{ID: "d1", Name: "Phone"}, Mode: mode, Tier: protocol.TierStandard,
		Capturer: r.cap, OnEnd: func(c protocol.ErrorCode) { r.end <- c },
	})
	if _, err := r.s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.s.Attach(r.link)
	t.Cleanup(func() { r.s.Close(protocol.CodeSessionEnded) })
	return r
}

func (r *rig) connect() {
	r.s.OnLinkState(LinkConnected)
	waitFor(r.t, "capture started", func() bool { return r.cap.startCount() == 1 })
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func move(seq uint64, x, y float64) []byte {
	return mustJSON(map[string]any{"type": "pointer.move", "seq": seq, "x": x, "y": y})
}

// ---- tests

func TestPrepareAndCaptureStartsOnlyWhenConnected(t *testing.T) {
	r := newRig(t, protocol.ModeFullControl)
	st := r.s.State()
	if st.DisplayID != "1" || st.Width == 0 || st.Tier != protocol.TierStandard {
		t.Fatalf("bad state %+v", st)
	}
	if r.cap.startCount() != 0 {
		t.Fatal("capture must wait for the connected link")
	}
	r.connect()
	cfg := r.cap.lastStart()
	if cfg.CaptureID == 0 || cfg.DisplayID != "1" || cfg.BitrateKbps != protocol.Specs[protocol.TierStandard].BitrateKbps {
		t.Fatalf("bad capture cfg %+v", cfg)
	}
	waitFor(t, "state message", func() bool { return len(r.link.types()) > 0 })
	if got := r.link.types()[0]; got != "session.state" {
		t.Fatalf("first message = %s", got)
	}
}

func TestFramesAreFilteredByGenerationAndTimed(t *testing.T) {
	r := newRig(t, protocol.ModeViewOnly)
	r.connect()
	id := r.cap.lastStart().CaptureID
	r.cap.frames <- platform.Frame{CaptureID: id + 9, Data: []byte{1}} // a replaced generation
	r.cap.frames <- platform.Frame{CaptureID: id, Keyframe: true, PTS: 0, Data: []byte{1}}
	r.cap.frames <- platform.Frame{CaptureID: id, PTS: 40 * time.Millisecond, Data: []byte{1}}
	r.cap.frames <- platform.Frame{CaptureID: id, PTS: 40*time.Millisecond + time.Microsecond, Data: []byte{1}}
	waitFor(t, "3 frames", func() bool { return r.link.videoCount() == 3 })
	r.link.mu.Lock()
	defer r.link.mu.Unlock()
	if r.link.video[0] != firstFrameDur || r.link.video[1] != 40*time.Millisecond || r.link.video[2] != frameDurMin {
		t.Fatalf("durations = %v", r.link.video)
	}
}

func TestPointerMappingOrderingAndButtons(t *testing.T) {
	r := newRig(t, protocol.ModeFullControl)
	r.connect()
	r.s.OnPointer(move(1, 0.5, 0.5))
	r.s.OnPointer(move(3, 1, 1))
	r.s.OnPointer(move(2, 0, 0)) // stale
	waitFor(t, "moves", func() bool { return len(r.cap.in.snapshot()) >= 2 })
	got := r.cap.in.snapshot()
	if got[0] != "move 720.0,450.0" || got[1] != "move 1439.5,899.5" || len(got) != 2 {
		t.Fatalf("moves = %v", got)
	}

	// A button older than an applied move presses where the pointer already is.
	r.s.OnControl(mustJSON(map[string]any{"type": "pointer.button", "seq": 2, "x": 0, "y": 0, "button": "left", "down": true, "clickCount": 9}))
	got = r.cap.in.snapshot()
	if got[len(got)-1] != "button left true 1439.5,899.5 n3" {
		t.Fatalf("button = %v", got)
	}
	r.s.OnControl(mustJSON(map[string]any{"type": "pointer.button", "seq": 4, "x": 0.25, "y": 0.25, "button": "left", "down": false}))
	if r.cap.in.count("button left false 360.0,225.0 n1") != 1 {
		t.Fatalf("up = %v", r.cap.in.snapshot())
	}
	// Dropped: addressed to another display.
	r.s.OnPointer(mustJSON(map[string]any{"type": "pointer.move", "seq": 9, "x": 0.1, "y": 0.1, "displayId": "2"}))
	if n := len(r.cap.in.snapshot()); n != 4 {
		t.Fatalf("foreign display event applied: %v", r.cap.in.snapshot())
	}
}

func TestViewOnlyRefusesInputButAnswersPing(t *testing.T) {
	r := newRig(t, protocol.ModeViewOnly)
	r.connect()
	r.s.OnControl(mustJSON(map[string]any{"type": "key", "code": "KeyA", "down": true}))
	r.s.OnControl(mustJSON(map[string]any{"type": "text.insert", "text": "hi"}))
	r.s.OnControl(mustJSON(map[string]any{"type": "clipboard.get"}))
	r.s.OnControl(mustJSON(map[string]any{"type": "ping", "t": 7}))
	waitFor(t, "pong", func() bool {
		for _, ty := range r.link.types() {
			if ty == "pong" {
				return true
			}
		}
		return false
	})
	if n := len(r.cap.in.snapshot()); n != 0 {
		t.Fatalf("view-only injected input: %v", r.cap.in.snapshot())
	}
	var viewOnly int
	for _, ty := range r.link.types() {
		if ty == "error:"+string(protocol.CodeViewOnly) {
			viewOnly++
		}
	}
	if viewOnly != 1 {
		t.Fatalf("view_only notices = %d (want exactly 1, rate-limited): %v", viewOnly, r.link.types())
	}
}

func TestKeysTextAndReleaseOnLinkLoss(t *testing.T) {
	r := newRig(t, protocol.ModeFullControl)
	r.connect()
	r.s.OnControl(mustJSON(map[string]any{"type": "key", "code": "MetaLeft", "down": true}))
	r.s.OnControl(mustJSON(map[string]any{"type": "key", "code": "KeyC", "down": true}))
	r.s.OnControl(mustJSON(map[string]any{"type": "key", "code": "KeyC", "down": false}))
	r.s.OnControl(mustJSON(map[string]any{"type": "text.insert", "text": "你好\x00\x07 ok\n"}))
	got := r.cap.in.snapshot()
	if got[len(got)-1] != `text "你好 ok\n"` {
		t.Fatalf("text = %v", got)
	}
	r.s.OnLinkState(LinkDisconnected)
	if r.cap.in.count("release") != 1 {
		t.Fatalf("release on disconnect: %v", r.cap.in.snapshot())
	}
	r.s.OnControl(mustJSON(map[string]any{"type": "key", "code": "Bogus", "down": true}))
	waitFor(t, "bad_request", func() bool {
		for _, ty := range r.link.types() {
			if ty == "error:bad_request" {
				return true
			}
		}
		return false
	})
}

func TestWatchdogReleasesAfterSilence(t *testing.T) {
	r := newRig(t, protocol.ModeFullControl)
	r.connect()
	r.s.OnControl(mustJSON(map[string]any{"type": "key", "code": "ShiftLeft", "down": true}))
	r.s.lastRXNano.Store(time.Now().Add(-time.Minute).UnixNano())
	r.s.watchdog()
	if r.cap.in.count("release") != 1 {
		t.Fatalf("watchdog did not release: %v", r.cap.in.snapshot())
	}
	r.s.watchdog() // nothing held now: no second release
	if r.cap.in.count("release") != 1 {
		t.Fatal("released twice")
	}
}

func TestCloseReleasesStopsAndIsIdempotent(t *testing.T) {
	r := newRig(t, protocol.ModeFullControl)
	r.connect()
	r.s.OnControl(mustJSON(map[string]any{"type": "pointer.button", "seq": 1, "x": .5, "y": .5, "button": "left", "down": true}))
	r.s.Close(protocol.CodeSessionEnded)
	r.s.Close(protocol.CodeInternal)
	select {
	case c := <-r.end:
		if c != protocol.CodeSessionEnded {
			t.Fatalf("reason = %s", c)
		}
	case <-time.After(time.Second):
		t.Fatal("OnEnd not called")
	}
	if r.cap.in.count("release") == 0 || r.cap.stops != 1 || !r.cap.closed || !r.link.closed {
		t.Fatalf("teardown incomplete: input=%v stops=%d closed=%v link=%v", r.cap.in.snapshot(), r.cap.stops, r.cap.closed, r.link.closed)
	}
}

func TestQualityAndDisplaySwitchRestartCapture(t *testing.T) {
	r := newRig(t, protocol.ModeFullControl)
	r.connect()
	first := r.cap.lastStart()
	r.s.OnControl(mustJSON(map[string]any{"type": "quality.set", "tier": "high"}))
	waitFor(t, "restart for tier", func() bool { return r.cap.startCount() == 2 })
	hi := r.cap.lastStart()
	if hi.CaptureID == first.CaptureID || hi.BitrateKbps != protocol.Specs[protocol.TierHigh].BitrateKbps {
		t.Fatalf("tier restart %+v", hi)
	}
	r.s.OnControl(mustJSON(map[string]any{"type": "display.select", "displayId": "2"}))
	waitFor(t, "restart for display", func() bool { return r.cap.startCount() == 3 })
	if r.cap.lastStart().DisplayID != "2" {
		t.Fatalf("display restart %+v", r.cap.lastStart())
	}
	// Pointer now maps onto display 2 (origin 1440,-200; 1920x1080).
	r.s.OnPointer(move(1, 0, 0))
	waitFor(t, "move", func() bool { return r.cap.in.count("move 1440.0,-200.0") == 1 })

	r.s.OnControl(mustJSON(map[string]any{"type": "display.select", "displayId": "nope"}))
	waitFor(t, "display_not_found", func() bool {
		for _, ty := range r.link.types() {
			if ty == "error:display_not_found" {
				return true
			}
		}
		return false
	})
}

func TestDisplayVanishFallsBackToMain(t *testing.T) {
	r := newRig(t, protocol.ModeFullControl)
	r.connect()
	r.s.OnControl(mustJSON(map[string]any{"type": "display.select", "displayId": "2"}))
	waitFor(t, "on display 2", func() bool { return r.cap.startCount() == 2 })
	r.cap.events <- platform.Event{Kind: platform.EventDisplaysChanged, Displays: r.cap.displays[:1]}
	waitFor(t, "fallback restart", func() bool { return r.cap.startCount() == 3 })
	if r.cap.lastStart().DisplayID != "1" {
		t.Fatalf("fallback = %+v", r.cap.lastStart())
	}
}

func TestClipboardRoundTripFullControlOnly(t *testing.T) {
	r := newRig(t, protocol.ModeFullControl)
	r.connect()
	r.s.OnControl(mustJSON(map[string]any{"type": "clipboard.set", "text": "from phone"}))
	waitFor(t, "write", func() bool { return r.cap.clip.get() == "from phone" })
	r.s.OnControl(mustJSON(map[string]any{"type": "clipboard.get"}))
	waitFor(t, "clipboard.text", func() bool {
		for _, ty := range r.link.types() {
			if ty == "clipboard.text" {
				return true
			}
		}
		return false
	})
}

func TestHelperDeathAndCaptureStopEndTheSession(t *testing.T) {
	r := newRig(t, protocol.ModeFullControl)
	r.connect()
	id := r.cap.lastStart().CaptureID
	// A stop from a replaced generation is ignored.
	r.cap.events <- platform.Event{Kind: platform.EventCaptureStopped, CaptureID: id + 5}
	r.cap.events <- platform.Event{Kind: platform.EventCaptureStopped, CaptureID: id, Err: protocol.Errf(protocol.CodePermissionRequired, "x")}
	select {
	case c := <-r.end:
		if c != protocol.CodePermissionRequired {
			t.Fatalf("reason = %s", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session did not end")
	}
}

func TestPermissionLossDowngradesToViewOnly(t *testing.T) {
	r := newRig(t, protocol.ModeFullControl)
	r.connect()
	r.cap.events <- platform.Event{Kind: platform.EventError, Err: protocol.Errf(protocol.CodePermissionRequired, "ax")}
	waitFor(t, "downgrade", func() bool { return !r.s.Info().Controlling })
	before := len(r.cap.in.snapshot())
	r.s.OnControl(mustJSON(map[string]any{"type": "key", "code": "KeyA", "down": true}))
	if len(r.cap.in.snapshot()) != before {
		t.Fatal("input injected after permission loss")
	}
}

func TestKeyframeRequestAndStartFailure(t *testing.T) {
	r := newRig(t, protocol.ModeViewOnly)
	r.connect()
	r.s.OnKeyframeRequest()
	r.s.OnControl(mustJSON(map[string]any{"type": "keyframe.request"}))
	r.cap.mu.Lock()
	n := r.cap.keyReqs
	r.cap.mu.Unlock()
	if n != 2 {
		t.Fatalf("keyframe requests = %d", n)
	}

	f := newRig(t, protocol.ModeViewOnly)
	f.cap.startErr = protocol.Errf(protocol.CodePermissionRequired, "screen")
	f.s.OnLinkState(LinkConnected)
	select {
	case c := <-f.end:
		if c != protocol.CodePermissionRequired {
			t.Fatalf("reason = %s", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session did not end on start failure")
	}
}
