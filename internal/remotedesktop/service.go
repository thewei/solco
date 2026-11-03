// Package remotedesktop is the Mac side of Remote Desktop: one Service that the Host's signalling
// endpoints, the Settings page and the status-bar item all talk to. It owns the rules of *whether* a
// session may exist — enabled, backend available, Screen Recording granted, one at a time, the Mac
// user's approval — and wires an accepted offer into a session (platform backend ⟷ WebRTC peer).
//
// Subpackages hold the parts: protocol (wire vocabulary), platform (+ macos/auto: the OS seam and
// the Swift helper supervisor), helperipc (Go end of the helper socket), rtc (Pion transport),
// session (behaviour of one live session), mapper (coordinates).
//
// No framework code is imported here; the Host and nativeui import this package, never the reverse.
//
// [INPUT]: platform.Backend, rtc, session, protocol
// [OUTPUT]: Service (Info/Open/Sessions/Disconnect/DisconnectDevice/Permissions/RequestPermission/SetSettings/Close), Authorizer, Settings
// [POS]: the facade; remotehost.Options.RemoteDesktop is satisfied by a thin adapter over it
package remotedesktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/platform"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
	"github.com/thewei/solco/internal/remotedesktop/rtc"
	"github.com/thewei/solco/internal/remotedesktop/session"
)

// ApprovalTimeout bounds how long a phone's request waits for the Mac user.
const ApprovalTimeout = 45 * time.Second

// Settings are the user's switches (persisted by the app, handed in whole on every change).
type Settings struct {
	Enabled         bool
	RequireApproval bool
	DefaultTier     protocol.Tier
}

// AuthRequest describes a phone asking to connect.
type AuthRequest struct {
	Device        protocol.DeviceInfo
	RequestedMode protocol.Mode
}

// Decision is the Mac user's answer. Mode may be lower than requested (View Only instead of Full).
type Decision struct {
	Allow bool
	Mode  protocol.Mode
}

// Authorizer asks the person at the Mac. It must honour ctx (the phone gave up, or the timeout hit)
// by dismissing its prompt, and return ctx's error then.
type Authorizer interface {
	Authorize(ctx context.Context, req AuthRequest) (Decision, error)
}

// Config wires a Service.
type Config struct {
	Backend    platform.Backend
	Authorizer Authorizer
	// ICEServers supplies STUN/TURN entries per session (nil = host candidates only: LAN and Tailscale).
	ICEServers      func(ctx context.Context) []rtc.ICEServer
	InterfaceFilter func(name string) bool
	// OnChange fires after the session set, settings or readiness changed (Settings page refresh,
	// status-bar item). It must not block.
	OnChange func()
	Log      *slog.Logger
}

// Service is safe for concurrent use.
type Service struct {
	cfg Config
	log *slog.Logger

	mu       sync.Mutex
	settings Settings
	opening  bool // an offer is in approval / setup: counts as busy
	active   *session.Session
	closed   bool

	approvalTimeout time.Duration
}

// NewService builds a Service. It starts nothing and costs nothing until a session opens.
func NewService(cfg Config) *Service {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Service{cfg: cfg, log: cfg.Log.With("component", "remotedesktop"),
		settings: Settings{RequireApproval: true, DefaultTier: protocol.DefaultTier}, approvalTimeout: ApprovalTimeout}
}

// SetSettings applies the user's switches; turning it off ends the active session at once.
func (s *Service) SetSettings(set Settings) {
	if !set.DefaultTier.Valid() {
		set.DefaultTier = protocol.DefaultTier
	}
	s.mu.Lock()
	prev := s.settings
	s.settings = set
	act := s.active
	s.mu.Unlock()
	if prev.Enabled && !set.Enabled && act != nil {
		go act.Stop()
	}
	s.changed()
}

// Enabled reports the user's switch (cheap; the Host's bootstrap asks).
func (s *Service) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings.Enabled && !s.closed
}

// Settings returns the current switches.
func (s *Service) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

func (s *Service) changed() {
	if s.cfg.OnChange != nil {
		s.cfg.OnChange()
	}
}

// Permissions reads the OS grants without opening a session.
func (s *Service) Permissions(ctx context.Context) (protocol.PermissionStatus, error) {
	return s.cfg.Backend.Permissions(ctx)
}

// RequestPermission triggers the OS prompt for one permission and returns the state afterwards.
func (s *Service) RequestPermission(ctx context.Context, kind protocol.PermissionKind) (protocol.PermissionStatus, error) {
	return s.cfg.Backend.RequestPermission(ctx, kind)
}

// Info is what GET /remote-desktop answers and what the Settings page shows.
func (s *Service) Info(ctx context.Context) protocol.Info {
	s.mu.Lock()
	set, busy := s.settings, s.opening || s.active != nil
	s.mu.Unlock()
	info := protocol.Info{
		Protocol: protocol.Version, Platform: s.cfg.Backend.ID(), RequireApproval: set.RequireApproval, Tiers: protocol.Tiers,
	}
	ok, _ := s.cfg.Backend.Available()
	if ok {
		// Grants are shown even while the feature is off, so Settings can walk the user through them.
		if perms, err := s.cfg.Backend.Permissions(ctx); err == nil {
			info.Permissions = perms
		}
	}
	switch {
	case !set.Enabled:
		info.State = protocol.StateDisabled
	case !ok:
		info.State = protocol.StateUnavailable
	case !info.Permissions.ScreenRecording:
		info.State = protocol.StatePermissionRequired
	case busy:
		info.State = protocol.StateBusy
	default:
		info.State = protocol.StateReady
	}
	return info
}

// Open admits one phone: checks, approval, helper, WebRTC answer. On error nothing is left running.
func (s *Service) Open(ctx context.Context, device protocol.DeviceInfo, offer protocol.Offer) (protocol.Answer, error) {
	if offer.Protocol != protocol.Version {
		return protocol.Answer{}, protocol.Errf(protocol.CodeProtocolUnsupported, "protocol %d", offer.Protocol)
	}
	if offer.SDP == "" || len(offer.SDP) > 64<<10 {
		return protocol.Answer{}, protocol.Errf(protocol.CodeBadOffer, "sdp")
	}
	if offer.Mode != "" && offer.Mode != protocol.ModeViewOnly && offer.Mode != protocol.ModeFullControl {
		return protocol.Answer{}, protocol.Errf(protocol.CodeBadRequest, "mode")
	}
	if offer.Tier != "" && !offer.Tier.Valid() {
		return protocol.Answer{}, protocol.Errf(protocol.CodeBadRequest, "tier")
	}

	s.mu.Lock()
	set := s.settings
	switch {
	case s.closed || !set.Enabled:
		s.mu.Unlock()
		return protocol.Answer{}, protocol.Errf(protocol.CodeDisabled, "off")
	case s.opening || s.active != nil:
		s.mu.Unlock()
		return protocol.Answer{}, protocol.Errf(protocol.CodeBusy, "another session is active")
	}
	s.opening = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.opening = false
		s.mu.Unlock()
		s.changed()
	}()
	s.changed()

	if ok, reason := s.cfg.Backend.Available(); !ok {
		s.log.Warn("remote desktop backend unavailable", "reason", reason)
		return protocol.Answer{}, protocol.Errf(protocol.CodeUnavailable, "backend")
	}
	perms, err := s.cfg.Backend.Permissions(ctx)
	if err != nil {
		return protocol.Answer{}, protocol.Errf(protocol.CodeHelperFailed, "permissions")
	}
	if !perms.ScreenRecording {
		return protocol.Answer{}, protocol.Errf(protocol.CodePermissionRequired, "screen_recording")
	}

	mode := offer.Mode
	if mode == "" {
		mode = protocol.ModeFullControl
	}
	if set.RequireApproval {
		dec, err := s.authorize(ctx, device, mode)
		if err != nil {
			return protocol.Answer{}, err
		}
		if dec.Mode == protocol.ModeViewOnly {
			mode = protocol.ModeViewOnly
		}
	}
	if !perms.Accessibility {
		mode = protocol.ModeViewOnly // no Accessibility: the phone sees, the Mac is not touched
	}

	tier := offer.Tier
	if tier == "" {
		tier = set.DefaultTier
	}
	capr, err := s.cfg.Backend.Open(ctx)
	if err != nil {
		return protocol.Answer{}, protocol.Errf(protocol.CodeHelperFailed, "open")
	}

	id := newID()
	var sess *session.Session
	sess = session.New(session.Config{
		ID: id, Device: device, Mode: mode, Tier: tier, DisplayID: offer.DisplayID, Capturer: capr, Log: s.log,
		OnEnd: func(protocol.ErrorCode) { s.ended(sess) },
	})
	if _, err := sess.Prepare(ctx); err != nil {
		_ = capr.Close()
		return protocol.Answer{}, err
	}
	peer, sdp, err := rtc.Answer(ctx, offer.SDP, rtc.Options{
		ICEServers: s.iceServers(ctx), InterfaceFilter: s.cfg.InterfaceFilter,
		Handlers: rtc.Handlers{
			OnControl:         sess.OnControl,
			OnPointer:         sess.OnPointer,
			OnKeyframeRequest: sess.OnKeyframeRequest,
			OnControlOpen:     sess.OnControlOpen,
			OnState:           func(st rtc.State) { sess.OnLinkState(session.LinkState(st)) },
		},
	})
	if err != nil {
		_ = capr.Close()
		if err == rtc.ErrBadOffer {
			return protocol.Answer{}, protocol.Errf(protocol.CodeBadOffer, "sdp")
		}
		return protocol.Answer{}, protocol.Errf(protocol.CodeInternal, "webrtc")
	}
	sess.SetStats(func() session.LinkStats {
		st := peer.Stats()
		return session.LinkStats{RTT: st.RTT, PathType: st.PathType}
	})

	s.mu.Lock()
	if s.closed || !s.settings.Enabled {
		s.mu.Unlock()
		peer.Close()
		_ = capr.Close()
		return protocol.Answer{}, protocol.Errf(protocol.CodeDisabled, "off")
	}
	s.active = sess
	s.mu.Unlock()
	sess.Attach(peer)
	s.log.Info("remote desktop session admitted", "session", id, "device", device.ID, "mode", string(mode), "tier", string(tier))
	return protocol.Answer{SessionID: id, SDP: sdp, State: sess.State()}, nil
}

func (s *Service) authorize(ctx context.Context, device protocol.DeviceInfo, mode protocol.Mode) (Decision, error) {
	if s.cfg.Authorizer == nil {
		return Decision{}, protocol.Errf(protocol.CodeDenied, "no approval surface")
	}
	actx, cancel := context.WithTimeout(ctx, s.approvalTimeout)
	defer cancel()
	dec, err := s.cfg.Authorizer.Authorize(actx, AuthRequest{Device: device, RequestedMode: mode})
	switch {
	case err != nil && actx.Err() != nil && ctx.Err() == nil:
		return Decision{}, protocol.Errf(protocol.CodeApprovalTimeout, "timeout")
	case err != nil:
		return Decision{}, protocol.Errf(protocol.CodeDenied, "cancelled")
	case !dec.Allow:
		return Decision{}, protocol.Errf(protocol.CodeDenied, "declined")
	}
	return dec, nil
}

func (s *Service) ended(sess *session.Session) {
	s.mu.Lock()
	if s.active == sess {
		s.active = nil
	}
	s.mu.Unlock()
	s.changed()
}

// Sessions lists live sessions (zero or one today).
func (s *Service) Sessions() []session.Info {
	s.mu.Lock()
	act := s.active
	s.mu.Unlock()
	if act == nil {
		return nil
	}
	return []session.Info{act.Info()}
}

// Disconnect ends one session from the Mac side (the one-click "Disconnect").
func (s *Service) Disconnect(id string) {
	s.mu.Lock()
	act := s.active
	s.mu.Unlock()
	if act != nil && act.ID() == id {
		go act.Stop()
	}
}

// DisconnectDevice ends the session of a phone (revoke / forget); the Host calls it with the device
// that lost its token.
func (s *Service) DisconnectDevice(deviceID string) {
	s.mu.Lock()
	act := s.active
	s.mu.Unlock()
	if act != nil && act.DeviceID() == deviceID {
		go act.Stop()
	}
}

// Close ends everything for good (app quit).
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	act := s.active
	s.mu.Unlock()
	if act != nil {
		act.Close(protocol.CodeSessionEnded)
	}
}

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Service) iceServers(ctx context.Context) []rtc.ICEServer {
	if s.cfg.ICEServers == nil {
		return nil
	}
	return s.cfg.ICEServers(ctx)
}
