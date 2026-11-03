package remotedesktop

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/thewei/solco/internal/remotedesktop/platform"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
	"github.com/thewei/solco/internal/remotedesktop/rtc"
)

type nopInput struct {
	mu    sync.Mutex
	calls int
}

func (n *nopInput) hit() error                                              { n.mu.Lock(); n.calls++; n.mu.Unlock(); return nil }
func (n *nopInput) PointerMove(float64, float64) error                      { return n.hit() }
func (n *nopInput) PointerButton(string, bool, float64, float64, int) error { return n.hit() }
func (n *nopInput) Scroll(float64, float64) error                           { return n.hit() }
func (n *nopInput) Key(string, bool) error                                  { return n.hit() }
func (n *nopInput) Text(string) error                                       { return n.hit() }
func (n *nopInput) ReleaseAll() error                                       { return nil }
func (n *nopInput) inputs() int                                             { n.mu.Lock(); defer n.mu.Unlock(); return n.calls }

type nopClip struct{}

func (nopClip) ReadText(context.Context) (string, error) { return "", nil }
func (nopClip) WriteText(context.Context, string) error  { return nil }

type fakeCapturer struct {
	frames chan platform.Frame
	events chan platform.Event
	done   chan struct{}
	once   sync.Once
	in     *nopInput
	cfgs   chan protocol.CaptureConfig
}

func (c *fakeCapturer) Displays(context.Context) ([]protocol.DisplayInfo, error) {
	return []protocol.DisplayInfo{{ID: "1", Width: 1440, Height: 900, PixelWidth: 2880, PixelHeight: 1800, Scale: 2, IsMain: true}}, nil
}
func (c *fakeCapturer) Permissions(context.Context) (protocol.PermissionStatus, error) {
	return protocol.PermissionStatus{ScreenRecording: true, Accessibility: true}, nil
}
func (c *fakeCapturer) StartCapture(_ context.Context, cfg protocol.CaptureConfig) (protocol.CaptureStarted, error) {
	c.cfgs <- cfg
	return protocol.CaptureStarted{CaptureID: cfg.CaptureID, Width: cfg.Width, Height: cfg.Height, HardwareEncoder: true}, nil
}
func (c *fakeCapturer) StopCapture(context.Context) error { return nil }
func (c *fakeCapturer) RequestKeyframe()                  {}
func (c *fakeCapturer) TuneBitrate(int)                   {}
func (c *fakeCapturer) Frames() <-chan platform.Frame     { return c.frames }
func (c *fakeCapturer) Events() <-chan platform.Event     { return c.events }
func (c *fakeCapturer) Input() platform.Input             { return c.in }
func (c *fakeCapturer) Clipboard() platform.Clipboard     { return nopClip{} }
func (c *fakeCapturer) Done() <-chan struct{}             { return c.done }
func (c *fakeCapturer) Err() error                        { return nil }
func (c *fakeCapturer) Close() error                      { c.once.Do(func() { close(c.done) }); return nil }

type fakeBackend struct {
	avail   bool
	perms   protocol.PermissionStatus
	opened  atomic.Int32
	current *fakeCapturer
	mu      sync.Mutex
}

func (b *fakeBackend) ID() string { return "fake" }
func (b *fakeBackend) Available() (bool, string) {
	if b.avail {
		return true, ""
	}
	return false, "no helper"
}
func (b *fakeBackend) Permissions(context.Context) (protocol.PermissionStatus, error) {
	return b.perms, nil
}
func (b *fakeBackend) RequestPermission(context.Context, protocol.PermissionKind) (protocol.PermissionStatus, error) {
	return b.perms, nil
}
func (b *fakeBackend) Open(context.Context) (platform.Capturer, error) {
	b.opened.Add(1)
	c := &fakeCapturer{frames: make(chan platform.Frame, 64), events: make(chan platform.Event, 4), done: make(chan struct{}), in: &nopInput{}, cfgs: make(chan protocol.CaptureConfig, 4)}
	b.mu.Lock()
	b.current = c
	b.mu.Unlock()
	return c, nil
}
func (b *fakeBackend) cap() *fakeCapturer { b.mu.Lock(); defer b.mu.Unlock(); return b.current }

type authFn func(ctx context.Context, r AuthRequest) (Decision, error)

func (f authFn) Authorize(ctx context.Context, r AuthRequest) (Decision, error) { return f(ctx, r) }

func codeOf(err error) protocol.ErrorCode { return protocol.CodeOf(err) }

func newOffer(t *testing.T) (*webrtc.PeerConnection, *webrtc.DataChannel, chan string, *atomic.Int64, string) {
	t.Helper()
	m := &webrtc.MediaEngine{}
	_ = m.RegisterDefaultCodecs()
	se := webrtc.SettingEngine{}
	se.SetIncludeLoopbackCandidate(true)
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	pc, err := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithSettingEngine(se)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	_, _ = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly})
	control, _ := pc.CreateDataChannel(rtc.ChannelControl, nil)
	ordered, retx := false, uint16(0)
	_, _ = pc.CreateDataChannel(rtc.ChannelPointer, &webrtc.DataChannelInit{Ordered: &ordered, MaxRetransmits: &retx})
	msgs := make(chan string, 32)
	control.OnMessage(func(m webrtc.DataChannelMessage) { msgs <- string(m.Data) })
	rtp := &atomic.Int64{}
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			if _, _, err := tr.ReadRTP(); err != nil {
				return
			}
			rtp.Add(1)
		}
	})
	offer, _ := pc.CreateOffer(nil)
	done := webrtc.GatheringCompletePromise(pc)
	_ = pc.SetLocalDescription(offer)
	<-done
	return pc, control, msgs, rtp, pc.LocalDescription().SDP
}

func readyService(b *fakeBackend, auth Authorizer) *Service {
	s := NewService(Config{Backend: b, Authorizer: auth, InterfaceFilter: func(string) bool { return true }})
	s.SetSettings(Settings{Enabled: true, RequireApproval: true})
	return s
}

func TestOpenGatesInOrder(t *testing.T) {
	b := &fakeBackend{avail: true, perms: protocol.PermissionStatus{ScreenRecording: true, Accessibility: true}}
	dev := protocol.DeviceInfo{ID: "d", Name: "Phone"}
	ctx := context.Background()
	s := NewService(Config{Backend: b})

	if _, err := s.Open(ctx, dev, protocol.Offer{Protocol: protocol.Version, SDP: "x"}); codeOf(err) != protocol.CodeDisabled {
		t.Fatalf("disabled: %v", err)
	}
	s.SetSettings(Settings{Enabled: true, RequireApproval: true})
	if _, err := s.Open(ctx, dev, protocol.Offer{Protocol: 99, SDP: "x"}); codeOf(err) != protocol.CodeProtocolUnsupported {
		t.Fatalf("protocol: %v", err)
	}
	if _, err := s.Open(ctx, dev, protocol.Offer{Protocol: protocol.Version}); codeOf(err) != protocol.CodeBadOffer {
		t.Fatalf("empty sdp: %v", err)
	}
	if _, err := s.Open(ctx, dev, protocol.Offer{Protocol: protocol.Version, SDP: "x", Mode: "root"}); codeOf(err) != protocol.CodeBadRequest {
		t.Fatalf("mode: %v", err)
	}
	b.perms.ScreenRecording = false
	if _, err := s.Open(ctx, dev, protocol.Offer{Protocol: protocol.Version, SDP: "x"}); codeOf(err) != protocol.CodePermissionRequired {
		t.Fatalf("permission: %v", err)
	}
	b.perms.ScreenRecording = true
	// Approval needed and no authorizer: refuse, and never spawn the helper.
	if _, err := s.Open(ctx, dev, protocol.Offer{Protocol: protocol.Version, SDP: "x"}); codeOf(err) != protocol.CodeDenied {
		t.Fatalf("no authorizer: %v", err)
	}
	if b.opened.Load() != 0 {
		t.Fatal("helper started before approval")
	}
	b.avail = false
	if _, err := s.Open(ctx, dev, protocol.Offer{Protocol: protocol.Version, SDP: "x"}); codeOf(err) != protocol.CodeUnavailable {
		t.Fatalf("unavailable: %v", err)
	}
}

func TestApprovalDeclineAndTimeout(t *testing.T) {
	b := &fakeBackend{avail: true, perms: protocol.PermissionStatus{ScreenRecording: true, Accessibility: true}}
	dev := protocol.DeviceInfo{ID: "d"}
	offer := protocol.Offer{Protocol: protocol.Version, SDP: "x"}

	s := readyService(b, authFn(func(context.Context, AuthRequest) (Decision, error) { return Decision{Allow: false}, nil }))
	if _, err := s.Open(context.Background(), dev, offer); codeOf(err) != protocol.CodeDenied {
		t.Fatalf("decline: %v", err)
	}

	s = readyService(b, authFn(func(ctx context.Context, _ AuthRequest) (Decision, error) {
		<-ctx.Done()
		return Decision{}, ctx.Err()
	}))
	s.approvalTimeout = 50 * time.Millisecond
	if _, err := s.Open(context.Background(), dev, offer); codeOf(err) != protocol.CodeApprovalTimeout {
		t.Fatalf("timeout: %v", err)
	}
	if s.Info(context.Background()).State != protocol.StateReady {
		t.Fatal("service stuck busy after a refused request")
	}

	s = readyService(b, authFn(func(context.Context, AuthRequest) (Decision, error) { return Decision{}, errors.New("x") }))
	if _, err := s.Open(context.Background(), dev, offer); codeOf(err) != protocol.CodeDenied {
		t.Fatalf("auth error: %v", err)
	}
}

func TestEndToEndSessionViewOnlyDowngradeBusyAndDisconnect(t *testing.T) {
	b := &fakeBackend{avail: true, perms: protocol.PermissionStatus{ScreenRecording: true, Accessibility: true}}
	var asked atomic.Int32
	s := readyService(b, authFn(func(_ context.Context, r AuthRequest) (Decision, error) {
		asked.Add(1)
		if r.RequestedMode != protocol.ModeFullControl {
			t.Errorf("requested mode = %s", r.RequestedMode)
		}
		return Decision{Allow: true, Mode: protocol.ModeViewOnly}, nil // the Mac user downgrades
	}))
	changes := atomic.Int32{}
	s.cfg.OnChange = func() { changes.Add(1) }

	ph, control, msgs, rtp, sdp := newOffer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	ans, err := s.Open(ctx, protocol.DeviceInfo{ID: "d1", Name: "Phone"}, protocol.Offer{Protocol: protocol.Version, SDP: sdp, Mode: protocol.ModeFullControl})
	if err != nil {
		t.Fatal(err)
	}
	if ans.State.Mode != protocol.ModeViewOnly || ans.SessionID == "" || ans.State.Width == 0 {
		t.Fatalf("answer %+v", ans)
	}
	if err := ph.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: ans.SDP}); err != nil {
		t.Fatal(err)
	}
	if got := s.Info(ctx).State; got != protocol.StateBusy {
		t.Fatalf("info state = %s", got)
	}
	// A second phone is refused while this one is live.
	if _, err := s.Open(ctx, protocol.DeviceInfo{ID: "d2"}, protocol.Offer{Protocol: protocol.Version, SDP: sdp}); codeOf(err) != protocol.CodeBusy {
		t.Fatalf("second session: %v", err)
	}

	// The capture starts once the link is up; feed frames until the phone sees RTP.
	capr := b.cap()
	var cfg protocol.CaptureConfig
	select {
	case cfg = <-capr.cfgs:
	case <-ctx.Done():
		t.Fatal("capture never started")
	}
	idr := append([]byte{0, 0, 0, 1, 0x67, 0x42, 0x00, 0x1f, 0x8d, 0, 0, 0, 1, 0x68, 0xce, 0x3c, 0x80, 0, 0, 0, 1, 0x65, 0x88}, make([]byte, 2000)...)
	for i := 0; rtp.Load() == 0 && i < 300; i++ {
		capr.frames <- platform.Frame{CaptureID: cfg.CaptureID, Keyframe: i == 0, PTS: time.Duration(i) * 33 * time.Millisecond, Data: idr}
		time.Sleep(33 * time.Millisecond)
	}
	if rtp.Load() == 0 {
		t.Fatal("no video reached the phone")
	}

	// View-only: input never reaches the Mac, the phone is told.
	if err := control.SendText(`{"type":"key","code":"KeyA","down":true}`); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	sawViewOnly := false
	for !sawViewOnly {
		select {
		case m := <-msgs:
			if contains(m, `"view_only"`) {
				sawViewOnly = true
			}
		case <-deadline:
			t.Fatal("phone was not told view_only")
		}
	}
	if capr.in.inputs() != 0 {
		t.Fatal("input injected in a view-only session")
	}

	// Mac-side disconnect ends it and frees the slot.
	s.Disconnect(ans.SessionID)
	waitUntil(t, func() bool { return len(s.Sessions()) == 0 })
	select {
	case <-capr.done:
	case <-time.After(3 * time.Second):
		t.Fatal("helper not released")
	}
	if s.Info(ctx).State != protocol.StateReady {
		t.Fatalf("state after end = %s", s.Info(ctx).State)
	}
	if asked.Load() != 1 || changes.Load() == 0 {
		t.Fatalf("asked=%d changes=%d", asked.Load(), changes.Load())
	}
}

// A phone whose link died on a network change asks again before the Mac noticed: it replaces its own session
// instead of being told "busy"; another phone is still refused.
func TestSameDeviceReplacesItsStaleSession(t *testing.T) {
	b := &fakeBackend{avail: true, perms: protocol.PermissionStatus{ScreenRecording: true, Accessibility: true}}
	s := readyService(b, authFn(func(context.Context, AuthRequest) (Decision, error) {
		return Decision{Allow: true, Mode: protocol.ModeFullControl}, nil
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	_, _, _, _, sdp1 := newOffer(t)
	if _, err := s.Open(ctx, protocol.DeviceInfo{ID: "d1"}, protocol.Offer{Protocol: protocol.Version, SDP: sdp1}); err != nil {
		t.Fatal(err)
	}
	first := b.cap()
	_, _, _, _, sdp2 := newOffer(t)
	if _, err := s.Open(ctx, protocol.DeviceInfo{ID: "d2"}, protocol.Offer{Protocol: protocol.Version, SDP: sdp2}); codeOf(err) != protocol.CodeBusy {
		t.Fatalf("other phone: %v", err)
	}
	if _, err := s.Open(ctx, protocol.DeviceInfo{ID: "d1"}, protocol.Offer{Protocol: protocol.Version, SDP: sdp2}); err != nil {
		t.Fatalf("same phone: %v", err)
	}
	select {
	case <-first.done:
	case <-time.After(3 * time.Second):
		t.Fatal("stale session not released")
	}
	if got := len(s.Sessions()); got != 1 {
		t.Fatalf("sessions = %d", got)
	}
	s.DisconnectDevice("d1")
}

func TestDisableAndRevokeEndTheSession(t *testing.T) {
	b := &fakeBackend{avail: true, perms: protocol.PermissionStatus{ScreenRecording: true, Accessibility: true}}
	for name, end := range map[string]func(*Service){
		"disable": func(s *Service) { s.SetSettings(Settings{Enabled: false}) },
		"revoke":  func(s *Service) { s.DisconnectDevice("d1") },
		"close":   func(s *Service) { s.Close() },
	} {
		t.Run(name, func(t *testing.T) {
			s := readyService(b, authFn(func(context.Context, AuthRequest) (Decision, error) {
				return Decision{Allow: true, Mode: protocol.ModeFullControl}, nil
			}))
			_, _, _, _, sdp := newOffer(t)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err := s.Open(ctx, protocol.DeviceInfo{ID: "d1"}, protocol.Offer{Protocol: protocol.Version, SDP: sdp}); err != nil {
				t.Fatal(err)
			}
			if len(s.Sessions()) != 1 {
				t.Fatal("no session")
			}
			end(s)
			waitUntil(t, func() bool { return len(s.Sessions()) == 0 })
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

type fakeTrust struct {
	mu     sync.Mutex
	grants map[string]protocol.Mode
}

func (f *fakeTrust) Granted(id string) (protocol.Mode, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.grants[id]
	return m, ok
}
func (f *fakeTrust) Remember(d protocol.DeviceInfo, m protocol.Mode) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grants[d.ID] = m
}
func (f *fakeTrust) Forget(id string) { f.mu.Lock(); defer f.mu.Unlock(); delete(f.grants, id) }

func TestRememberedApprovalSkipsThePromptWithinItsMode(t *testing.T) {
	b := &fakeBackend{avail: true, perms: protocol.PermissionStatus{ScreenRecording: true, Accessibility: true}}
	trust := &fakeTrust{grants: map[string]protocol.Mode{"d": protocol.ModeFullControl, "v": protocol.ModeViewOnly}}
	s := NewService(Config{Backend: b, Trust: trust}) // no Authorizer: any prompt would be refused
	s.SetSettings(Settings{Enabled: true, RequireApproval: true})
	ctx := context.Background()
	offer := protocol.Offer{Protocol: protocol.Version, SDP: "x"}

	// A remembered phone is not asked: the request gets past approval and dies on the bogus SDP instead.
	if _, err := s.Open(ctx, protocol.DeviceInfo{ID: "d"}, offer); codeOf(err) == protocol.CodeDenied {
		t.Fatalf("remembered phone was asked: %v", err)
	}
	// A view-only grant does not cover a Control request: it asks again (and is refused here).
	offer.Mode = protocol.ModeFullControl
	if _, err := s.Open(ctx, protocol.DeviceInfo{ID: "v"}, offer); codeOf(err) != protocol.CodeDenied {
		t.Fatalf("view grant covered control: %v", err)
	}
	// A phone nobody approved is asked.
	if _, err := s.Open(ctx, protocol.DeviceInfo{ID: "new"}, offer); codeOf(err) != protocol.CodeDenied {
		t.Fatalf("stranger was not asked: %v", err)
	}
}

func TestApprovalIsRememberedAndForgottenOnRevoke(t *testing.T) {
	b := &fakeBackend{avail: true, perms: protocol.PermissionStatus{ScreenRecording: true, Accessibility: true}}
	trust := &fakeTrust{grants: map[string]protocol.Mode{}}
	allow := authFn(func(context.Context, AuthRequest) (Decision, error) {
		return Decision{Allow: true, Mode: protocol.ModeViewOnly}, nil
	})
	s := NewService(Config{Backend: b, Trust: trust, Authorizer: allow})
	s.SetSettings(Settings{Enabled: true, RequireApproval: true})
	dev := protocol.DeviceInfo{ID: "d", Name: "Phone"}

	_, _ = s.Open(context.Background(), dev, protocol.Offer{Protocol: protocol.Version, SDP: "x"})
	if m, ok := trust.Granted("d"); !ok || m != protocol.ModeViewOnly {
		t.Fatalf("approval not remembered at the granted mode: %v %v", m, ok)
	}
	s.DisconnectDevice("d") // the revoke path
	if _, ok := trust.Granted("d"); ok {
		t.Fatal("revoked phone still remembered")
	}

	// A refusal is never remembered.
	deny := authFn(func(context.Context, AuthRequest) (Decision, error) { return Decision{}, nil })
	s2 := NewService(Config{Backend: b, Trust: trust, Authorizer: deny})
	s2.SetSettings(Settings{Enabled: true, RequireApproval: true})
	_, _ = s2.Open(context.Background(), dev, protocol.Offer{Protocol: protocol.Version, SDP: "x"})
	if _, ok := trust.Granted("d"); ok {
		t.Fatal("a refusal was remembered")
	}
}

func TestICEServersOnlyForAnOffLANOffer(t *testing.T) {
	var asked atomic.Int32
	s := NewService(Config{Backend: &fakeBackend{}, ICEServers: func(context.Context) []rtc.ICEServer {
		asked.Add(1)
		return []rtc.ICEServer{{URLs: []string{"stun:example.invalid:3478"}}}
	}})
	ctx := context.Background()
	lan := "a=candidate:1 1 udp 2130706431 192.168.1.5 5000 typ host\r\n"
	if got := s.iceServersFor(ctx, lan); got != nil || asked.Load() != 0 {
		t.Fatalf("a LAN offer was given ICE servers: %v", got)
	}
	for _, typ := range []string{"srflx", "relay"} {
		if got := s.iceServersFor(ctx, lan+"a=candidate:2 1 udp 1694498815 203.0.113.9 5000 typ "+typ+" raddr 0.0.0.0 rport 0\r\n"); len(got) != 1 {
			t.Fatalf("a %s offer was not given ICE servers: %v", typ, got)
		}
	}
}
