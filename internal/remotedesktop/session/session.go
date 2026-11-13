// Package session runs one authorised Remote Desktop session: it joins a platform.Capturer
// (screen + input + clipboard) to a Link (the WebRTC peer) and owns everything between them —
// the capture lifecycle (started when the phone is connected, restarted on display or quality
// changes, stopped on any end), the phone's control messages (pointer, keys, text, clipboard,
// display and quality choices), view-only enforcement, pointer ordering, bitrate/stat reporting
// and, above all, the guarantee that a lost phone never leaves a key or button held down on the Mac.
//
// It knows nothing about Pion, Unix sockets or Swift; it talks to interfaces so tests drive it with
// fakes and a Linux/Windows backend slots in unchanged.
//
// [INPUT]: platform (Capturer), protocol, mapper
// [OUTPUT]: Session (New/Attach/Prepare/OnControl/OnPointer/OnLinkState/Close/Info), Link, Config
// [POS]: the behavioural core of Remote Desktop; every rule in the product spec's §6, §7, §8 lands here
package session

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/platform"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// Link is the phone connection as the session sees it (implemented by *rtc.Peer).
type Link interface {
	WriteVideo(annexB []byte, dur time.Duration) error
	SendControl(msg []byte) error
	Close()
}

// LinkState mirrors the transport's state without importing it.
type LinkState string

const (
	LinkConnecting   LinkState = "connecting"
	LinkConnected    LinkState = "connected"
	LinkDisconnected LinkState = "disconnected"
	LinkClosed       LinkState = "closed"
)

const (
	frameDurMin = time.Millisecond
	frameDurMax = time.Second
	// firstFrameDur seeds the first sample (no previous PTS to diff against).
	firstFrameDur = 33 * time.Millisecond
	// staleInputAfter: with keys/buttons held and nothing heard from the phone for this long, they are released
	// (the phone pings every few seconds; a half-open link must not hold a key down).
	staleInputAfter = 10 * time.Second
	statsEvery      = 2 * time.Second
	watchdogEvery   = 2 * time.Second
	// controlOpenWait bounds how long after "connected" the first state message waits for the channel.
	maxHeldKeys = 32
)

// Config builds a Session.
type Config struct {
	ID        string
	Device    protocol.DeviceInfo
	Mode      protocol.Mode
	Tier      protocol.Tier
	DisplayID string // "" = the main display
	// Idle starts the session without capturing: the phone chooses a window or display first.
	Idle     bool
	Capturer platform.Capturer
	// OnEnd is called once, after teardown, with the reason.
	OnEnd func(reason protocol.ErrorCode)
	// UnlockSecret yields the saved Mac login password (ok=false when none is saved); it is called only to type it.
	UnlockSecret func() (password string, ok bool)
	// AutoUnlock reports whether the Mac's owner switched on unlocking by itself when the lock screen is up.
	AutoUnlock func() bool
	// RestoreLater hands a fitted window to the service when the session can no longer give it back itself:
	// after grace (link lost: the phone may return) or at once (grace 0: the helper is gone). Optional.
	RestoreLater func(windowID string, grace time.Duration)
	// ClaimPending is called when a window or display (window "") is chosen: it cancels a deferred restore of
	// that very window and carries out any other. It reports whether the window is still fitted. Optional.
	ClaimPending func(window string) bool
	// Stats supplies transport stats (RTT, path) for the stats message and Info; optional.
	Stats func() LinkStats
	Now   func() time.Time
	Log   *slog.Logger
}

// LinkStats is the transport's contribution to ConnectionStats.
type LinkStats struct {
	RTT      time.Duration
	PathType string
}

// Info is a snapshot for Settings and the status bar.
type Info struct {
	ID          string
	Device      protocol.DeviceInfo
	Mode        protocol.Mode
	StartedAt   time.Time
	Connected   bool
	Sharing     bool // capture running
	Controlling bool // input accepted (full control and Accessibility granted)
	State       protocol.SessionState
	Stats       protocol.ConnectionStats
}

// Session is one live session. Methods are safe for concurrent use.
type Session struct {
	cfg Config
	log *slog.Logger
	cap platform.Capturer

	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	link      Link
	state     protocol.SessionState
	display   protocol.DisplayInfo
	connected bool
	sharing   bool
	startedAt time.Time
	closed    bool
	ended     bool // the phone (or the Mac) asked to end it: a closing link is then not "lost"
	lost      bool // the link closed under a live session
	canInput  bool // Accessibility granted (or presumed until a refusal)

	// pointer ordering (see protocol/control.go)
	lastSeq      uint64
	lastX, lastY float64
	haveLast     bool

	// input bookkeeping, for release-on-loss
	heldKeys       map[string]struct{}
	heldButtons    map[string]struct{}
	lastRXNano     atomic.Int64
	lastNoticeNano atomic.Int64

	captureSeq  atomic.Uint32
	activeCap   atomic.Uint32
	capMu       sync.Mutex  // serialises capture (re)starts
	win         windowMode  // window mode (guarded by mu); zero while a display is shown
	menuReading string      // the window whose menu bar is being read (guarded by mu): one read per window at a time
	restarts    []time.Time // recent unplanned capture restarts (guarded by mu)
	hideCursor  bool        // the phone asked for the Mac's pointer to stay out of the video
	unlocking   atomic.Bool // an unlock attempt is typing
	tunedKbps   int         // the encoder's current target, as last set (guarded by mu)
	lastTune    time.Time   // when the link estimate last retargeted the encoder (guarded by mu)

	framesSent, framesDropped, keyframesSent, keyRequests atomic.Uint64
	lastPTS                                               time.Duration
	havePTS                                               bool

	statSamples struct {
		at     time.Time
		frames uint64
		bytes  uint64
	}
	bytesSent atomic.Uint64

	wg sync.WaitGroup
}

// New builds a session around an open Capturer. Call Prepare, Attach, then feed it transport events.
func New(cfg Config) *Session {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if !cfg.Tier.Valid() {
		cfg.Tier = protocol.DefaultTier
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		cfg: cfg, log: cfg.Log.With("session", cfg.ID, "device", cfg.Device.ID), cap: cfg.Capturer,
		ctx: ctx, cancel: cancel,
		heldKeys: map[string]struct{}{}, heldButtons: map[string]struct{}{},
		startedAt: cfg.Now(), canInput: cfg.Mode == protocol.ModeFullControl,
	}
	s.lastRXNano.Store(cfg.Now().UnixNano())
	return s
}

// SetStats installs the transport's stats source (after the peer exists).
func (s *Session) SetStats(f func() LinkStats) {
	s.mu.Lock()
	s.cfg.Stats = f
	s.mu.Unlock()
}

// ID is the session id.
func (s *Session) ID() string { return s.cfg.ID }

// DeviceID is the paired phone's id.
func (s *Session) DeviceID() string { return s.cfg.Device.ID }

// Prepare lists displays, picks the target and computes the capture size — everything that can fail
// before the answer goes out. It starts no capture.
func (s *Session) Prepare(ctx context.Context) (protocol.SessionState, error) {
	displays, err := s.cap.Displays(ctx)
	if err != nil {
		return protocol.SessionState{}, err
	}
	d, ok := pickDisplay(displays, s.cfg.DisplayID)
	if !ok {
		return protocol.SessionState{}, protocol.Errf(protocol.CodeDisplayNotFound, "no such display")
	}
	can := s.unlockAvailable()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.CanUnlock = can
	s.setDisplayLocked(d, displays)
	if s.cfg.Idle {
		s.state.Target = protocol.TargetNone
	}
	return s.state, nil
}

// Attach binds the phone link (after the answer exists) and starts the session's loops.
func (s *Session) Attach(link Link) {
	s.mu.Lock()
	s.link = link
	s.mu.Unlock()
	s.wg.Add(4)
	go func() { defer s.wg.Done(); s.screenLoop() }()
	go func() { defer s.wg.Done(); s.frameLoop() }()
	go func() { defer s.wg.Done(); s.eventLoop() }()
	go func() { defer s.wg.Done(); s.tickLoop() }()
}

func pickDisplay(displays []protocol.DisplayInfo, want string) (protocol.DisplayInfo, bool) {
	for _, d := range displays {
		if want != "" && d.ID == want {
			return d, true
		}
	}
	if want != "" {
		return protocol.DisplayInfo{}, false
	}
	for _, d := range displays {
		if d.IsMain {
			return d, true
		}
	}
	if len(displays) > 0 {
		return displays[0], true
	}
	return protocol.DisplayInfo{}, false
}

// setDisplayLocked records the display and derives the encoder size and the state message.
func (s *Session) setDisplayLocked(d protocol.DisplayInfo, all []protocol.DisplayInfo) {
	spec := protocol.Specs[s.cfg.Tier]
	w, h := spec.Size(d)
	if s.win.active() {
		w, h = spec.Size(s.win.asDisplay())
	}
	s.display = d
	s.state = protocol.SessionState{
		Mode: s.cfg.Mode, DisplayID: d.ID, Displays: all, Tier: s.cfg.Tier,
		Width: w, Height: h, FPS: spec.FPS, Hardware: s.state.Hardware, Cursor: !s.hideCursor,
		Blackout: s.state.Blackout, Locked: s.state.Locked, CanUnlock: s.state.CanUnlock,
		Target: s.state.Target, Window: s.state.Window, Fitted: s.state.Fitted, Clamped: s.state.Clamped,
	}
}

func (s *Session) captureConfigLocked() protocol.CaptureConfig {
	spec := protocol.Specs[s.cfg.Tier]
	cfg := protocol.CaptureConfig{
		CaptureID: s.captureSeq.Add(1), DisplayID: s.display.ID,
		Width: s.state.Width, Height: s.state.Height, FPS: spec.FPS, BitrateKbps: spec.BitrateKbps,
		KeyframeIntervalSec: protocol.KeyframeIntervalSec, HideCursor: s.hideCursor,
	}
	if s.win.active() {
		cfg.WindowID = s.win.info.ID
	}
	return cfg
}

// startCapture (re)starts the capture with the current display and tier.
func (s *Session) startCapture(ctx context.Context) error {
	s.capMu.Lock()
	defer s.capMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return protocol.Errf(protocol.CodeSessionEnded, "closed")
	}
	cfg := s.captureConfigLocked()
	s.mu.Unlock()

	s.activeCap.Store(0) // frames of the old generation stop flowing now
	started, err := s.cap.StartCapture(ctx, cfg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.state.Width, s.state.Height, s.state.Hardware = started.Width, started.Height, started.HardwareEncoder
	if started.Window != nil && s.win.active() {
		s.win.geom = *started.Window
		if w := s.state.Window; w != nil {
			next := *w // the state message is marshalled outside the lock: replace, never edit in place
			next.OnScreen = started.Window.OnScreen
			s.state.Window = &next
		}
	}
	s.tunedKbps = cfg.BitrateKbps
	s.sharing = true
	s.havePTS = false
	s.mu.Unlock()
	s.activeCap.Store(cfg.CaptureID)
	s.log.Info("remote desktop capture started", "display", cfg.DisplayID, "w", started.Width, "h", started.Height, "fps", cfg.FPS, "kbps", cfg.BitrateKbps, "hw", started.HardwareEncoder)
	return nil
}

const (
	maxCaptureRestarts = 3
	captureRestartSpan = time.Minute
)

// recoverCapture restarts a stream that stopped on its own (display reconfiguration, a screen-saver
// or lock transition, another capture client). A stream that keeps dying ends the session.
func (s *Session) recoverCapture(cause *protocol.Error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	now := s.cfg.Now()
	recent := s.restarts[:0]
	for _, t := range s.restarts {
		if now.Sub(t) < captureRestartSpan {
			recent = append(recent, t)
		}
	}
	s.restarts = recent
	if len(s.restarts) >= maxCaptureRestarts {
		s.mu.Unlock()
		if cause == nil {
			cause = protocol.Errf(protocol.CodeCaptureFailed, "capture stopped")
		}
		s.fail(cause)
		return
	}
	s.restarts = append(s.restarts, now)
	attempt := len(s.restarts)
	s.mu.Unlock()

	s.log.Warn("remote desktop capture stopped; restarting", "attempt", attempt)
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	if err := s.startCapture(ctx); err != nil {
		s.failCapture(err)
		return
	}
	s.sendState()
}

// OnLinkState is fed by the transport.
func (s *Session) OnLinkState(st LinkState) {
	switch st {
	case LinkConnected:
		s.mu.Lock()
		already := s.connected
		s.connected = true
		s.mu.Unlock()
		if already {
			return
		}
		s.mu.Lock()
		nothingYet := s.state.Target == protocol.TargetNone || s.sharing // an idle session waits for the phone's choice
		s.mu.Unlock()
		if !nothingYet {
			ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
			defer cancel()
			if err := s.startCapture(ctx); err != nil {
				s.log.Warn("remote desktop capture failed to start", "error", protocol.CodeOf(err))
				s.fail(err)
				return
			}
		}
		s.sendState()
		go s.wakeScreen()
	case LinkDisconnected:
		// Possibly transient: stop trusting input immediately, keep the session for the transport's grace.
		s.releaseAll("link_disconnected")
	case LinkClosed:
		s.mu.Lock()
		s.lost = !s.ended && !s.closed
		s.mu.Unlock()
		s.Close(protocol.CodeSessionEnded)
	}
}

// OnControlOpen is called when the phone's control channel is writable.
func (s *Session) OnControlOpen() { s.sendState() }

// OnKeyframeRequest is a PLI/FIR from the phone's decoder.
func (s *Session) OnKeyframeRequest() {
	s.keyRequests.Add(1)
	s.cap.RequestKeyframe()
}

// OnBandwidth is the link's congestion estimate (kbps). The encoder follows it down so a weak link keeps
// moving instead of stalling, and back up to the tier's target when the link recovers; moves under 10% and
// retargets closer than a second apart are skipped.
func (s *Session) OnBandwidth(estimateKbps int) {
	s.mu.Lock()
	if s.closed || !s.sharing {
		s.mu.Unlock()
		return
	}
	now := s.cfg.Now()
	target := protocol.AdaptBitrate(s.cfg.Tier, estimateKbps)
	if !protocol.WorthRetuning(s.tunedKbps, target) || now.Sub(s.lastTune) < time.Second {
		s.mu.Unlock()
		return
	}
	s.tunedKbps, s.lastTune = target, now
	s.mu.Unlock()
	s.log.Info("remote desktop bitrate retargeted", "session", s.cfg.ID, "kbps", target, "estimateKbps", estimateKbps)
	s.cap.TuneBitrate(target)
}

// fail tells the phone and ends the session. It never blocks the caller: Close waits for the loops,
// and fail is called from them.
func (s *Session) fail(err error) {
	code := protocol.CodeOf(err)
	s.sendError(code, "")
	go func() {
		time.Sleep(150 * time.Millisecond) // let the message leave before the DTLS teardown
		s.Close(code)
	}()
}

// Close ends the session: release input, stop capture, release the helper, close the link. Idempotent.
func (s *Session) Close(reason protocol.ErrorCode) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	link, lost := s.link, s.lost
	s.mu.Unlock()

	s.releaseAll("close")
	s.closeFit(lost)
	s.mu.Lock()
	dark := s.state.Blackout
	s.mu.Unlock()
	if scr, ok := s.screen(); ok && dark {
		_ = scr.SetBlackout(false) // the helper also restores on exit; this is the polite order
	}
	s.cancel()
	stopCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	_ = s.cap.StopCapture(stopCtx)
	stop()
	_ = s.cap.Close()
	if link != nil {
		link.Close()
	}
	s.wg.Wait()
	s.log.Info("remote desktop session ended", "reason", string(reason), "frames", s.framesSent.Load(), "dropped", s.framesDropped.Load(), "uptime_s", int(s.cfg.Now().Sub(s.startedAt).Seconds()))
	if s.cfg.OnEnd != nil {
		s.cfg.OnEnd(reason)
	}
}

// Stop is the Mac user's "disconnect": the phone is told why before the link drops.
func (s *Session) Stop() {
	s.sendError(protocol.CodeSessionEnded, "stopped on the Mac")
	time.Sleep(150 * time.Millisecond) // let the message leave before the DTLS teardown
	s.Close(protocol.CodeSessionEnded)
}

// Info snapshots the session.
func (s *Session) Info() Info {
	s.mu.Lock()
	info := Info{
		ID: s.cfg.ID, Device: s.cfg.Device, Mode: s.cfg.Mode, StartedAt: s.startedAt, Connected: s.connected,
		Sharing: s.sharing, Controlling: s.cfg.Mode == protocol.ModeFullControl && s.canInput && s.connected, State: s.state,
	}
	s.mu.Unlock()
	info.Stats = s.stats()
	return info
}

func (s *Session) stats() protocol.ConnectionStats {
	st := protocol.ConnectionStats{
		FramesSent: s.framesSent.Load(), FramesDropped: s.framesDropped.Load(), KeyframesSent: s.keyframesSent.Load(),
		KeyframeRequest: s.keyRequests.Load(), UptimeSeconds: int(s.cfg.Now().Sub(s.startedAt).Seconds()),
	}
	s.mu.Lock()
	statsFn := s.cfg.Stats
	s.mu.Unlock()
	if statsFn != nil {
		ls := statsFn()
		st.RTTMillis = float64(ls.RTT) / float64(time.Millisecond)
		st.PathType = ls.PathType
	}
	return st
}

// ---------------------------------------------------------------- video

func (s *Session) frameLoop() {
	frames := s.cap.Frames()
	for {
		select {
		case <-s.ctx.Done():
			return
		case f, ok := <-frames:
			if !ok {
				return
			}
			s.writeFrame(f)
		}
	}
}

func (s *Session) writeFrame(f platform.Frame) {
	if f.CaptureID != s.activeCap.Load() {
		return // a frame of a capture generation that was already replaced
	}
	s.mu.Lock()
	link := s.link
	dur := firstFrameDur
	if s.havePTS {
		dur = f.PTS - s.lastPTS
		if dur < frameDurMin {
			dur = frameDurMin
		}
		if dur > frameDurMax {
			dur = frameDurMax
		}
	}
	s.lastPTS, s.havePTS = f.PTS, true
	s.mu.Unlock()
	if link == nil {
		return
	}
	if err := link.WriteVideo(f.Data, dur); err != nil {
		s.framesDropped.Add(1)
		return
	}
	s.framesSent.Add(1)
	s.bytesSent.Add(uint64(len(f.Data)))
	if f.Keyframe {
		s.keyframesSent.Add(1)
	}
}

// ---------------------------------------------------------------- events

func (s *Session) eventLoop() {
	events := s.cap.Events()
	done := s.cap.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-done:
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if !closed {
				s.log.Warn("remote desktop helper ended")
				s.fail(protocol.Errf(protocol.CodeHelperFailed, "helper ended"))
			}
			return
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			s.handleEvent(ev)
		}
	}
}

func (s *Session) handleEvent(ev platform.Event) {
	switch ev.Kind {
	case platform.EventDisplaysChanged:
		s.onDisplaysChanged(ev.Displays)
	case platform.EventWindowGeometry:
		s.onWindowGeometry(ev)
	case platform.EventWindowGone:
		go s.windowGone(ev.WindowID, ev.GoneReason)
	case platform.EventCaptureStopped:
		if ev.CaptureID != s.activeCap.Load() {
			return // the generation we replaced ourselves
		}
		if ev.Err != nil && ev.Err.Code == protocol.CodePermissionRequired {
			s.fail(ev.Err)
			return
		}
		go s.recoverCapture(ev.Err)
	case platform.EventError:
		if ev.Err != nil && ev.Err.Code == protocol.CodePermissionRequired {
			// Accessibility was taken away mid-session: stop honouring input and say so.
			s.mu.Lock()
			s.canInput = false
			s.mu.Unlock()
			s.releaseAll("permission_revoked")
			s.sendError(protocol.CodePermissionRequired, "accessibility")
			s.sendState()
		}
	}
}

// onDisplaysChanged re-syncs the coordinate geometry immediately and restarts the capture when the
// streamed display moved size, or switches to the main display when it vanished.
func (s *Session) onDisplaysChanged(displays []protocol.DisplayInfo) {
	s.mu.Lock()
	cur := s.display
	s.mu.Unlock()
	next, ok := pickDisplay(displays, cur.ID)
	if !ok {
		next, ok = pickDisplay(displays, "")
	}
	if !ok {
		s.fail(protocol.Errf(protocol.CodeDisplayNotFound, "no displays"))
		return
	}
	s.mu.Lock()
	changed := next.ID != cur.ID || next.PixelWidth != cur.PixelWidth || next.PixelHeight != cur.PixelHeight
	s.setDisplayLocked(next, displays) // geometry (origin, scale) is live even when the stream is unchanged
	sharing := s.sharing
	s.mu.Unlock()
	if changed && sharing {
		ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
		defer cancel()
		if err := s.startCapture(ctx); err != nil {
			s.failCapture(err)
			return
		}
	}
	s.sendState()
}

// ---------------------------------------------------------------- ticks (stats, input watchdog)

func (s *Session) tickLoop() {
	stats := time.NewTicker(statsEvery)
	watch := time.NewTicker(watchdogEvery)
	defer stats.Stop()
	defer watch.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-stats.C:
			s.sendStats()
		case <-watch.C:
			s.watchdog()
		}
	}
}

// watchdog releases held keys/buttons when the phone has gone quiet (half-open link, app suspended).
func (s *Session) watchdog() {
	s.mu.Lock()
	held := len(s.heldKeys) + len(s.heldButtons)
	s.mu.Unlock()
	if held == 0 {
		return
	}
	if time.Since(time.Unix(0, s.lastRXNano.Load())) > staleInputAfter {
		s.releaseAll("phone_silent")
	}
}

func (s *Session) sendStats() {
	now := s.cfg.Now()
	frames, bytes := s.framesSent.Load(), s.bytesSent.Load()
	st := s.stats()
	s.mu.Lock()
	prev := s.statSamples
	s.statSamples.at, s.statSamples.frames, s.statSamples.bytes = now, frames, bytes
	s.mu.Unlock()
	if !prev.at.IsZero() {
		if dt := now.Sub(prev.at).Seconds(); dt > 0 {
			st.FPS = float64(frames-prev.frames) / dt
			st.BitrateKbps = int(float64(bytes-prev.bytes) * 8 / dt / 1000)
		}
	}
	if b, err := protocol.Marshal(protocol.StatsMsg{ConnectionStats: st}); err == nil {
		s.send(b)
	}
}

// ---------------------------------------------------------------- outbound

func (s *Session) send(b []byte) {
	s.mu.Lock()
	link := s.link
	s.mu.Unlock()
	if link != nil {
		_ = link.SendControl(b)
	}
}

func (s *Session) sendState() {
	s.mu.Lock()
	st := s.state
	st.Mode = s.cfg.Mode
	if !s.canInput {
		st.Mode = protocol.ModeViewOnly
	}
	s.mu.Unlock()
	if b, err := protocol.Marshal(protocol.SessionStateMsg{SessionState: st}); err == nil {
		s.send(b)
	}
}

func (s *Session) sendError(code protocol.ErrorCode, msg string) {
	if b, err := protocol.Marshal(protocol.ErrorMsg{Code: code, Message: msg}); err == nil {
		s.send(b)
	}
}

// sendErrorFor is sendError naming the window the error is about.
func (s *Session) sendErrorFor(code protocol.ErrorCode, windowID string) {
	if b, err := protocol.Marshal(protocol.ErrorMsg{Code: code, WindowID: windowID}); err == nil {
		s.send(b)
	}
}

// State is the current session state (for the Host's answer).
func (s *Session) State() protocol.SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state
	return st
}
