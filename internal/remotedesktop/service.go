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
// [OUTPUT]: Service (Info/Open/Sessions/Disconnect/DisconnectDevice/ForgetDevice/Permissions/RequestPermission/SetSettings/Close), Authorizer, TrustStore, Settings
// [POS]: the facade; remotehost.Options.RemoteDesktop is satisfied by a thin adapter over it
package remotedesktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"strings"
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

// TrustStore remembers which phones the Mac user already said yes to, and at what level, so a paired
// phone is asked about once. The Service only reads and writes through it; where the grants live
// (settings) and which thread touches them is the app's business. Both methods must be safe for
// concurrent use.
type TrustStore interface {
	// Granted returns the highest mode the user allowed this phone to use without asking again.
	Granted(deviceID string) (protocol.Mode, bool)
	// Remember records the user's answer for the phone (never called for a refusal).
	Remember(device protocol.DeviceInfo, mode protocol.Mode)
	// Forget drops the phone's grant: its next request asks again.
	Forget(deviceID string)
}

// Config wires a Service.
type Config struct {
	Backend    platform.Backend
	Authorizer Authorizer
	// Trust, when set, lets an approved phone connect without a prompt next time (nil = ask every time).
	Trust TrustStore
	// ICEServers supplies STUN/TURN entries per session, used only when the phone's offer shows it is
	// off the LAN (nil = host candidates only: LAN and Tailscale).
	// UnlockSecret yields the saved Mac login password for a Control session's lock-screen unlock (nil = none
	// saved); AutoUnlock says whether a locked screen is unlocked without the phone asking. Both are called off
	// the UI lane and must be safe for concurrent use.
	UnlockSecret    func() (string, bool)
	AutoUnlock      func() bool
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
		if granted, ok := s.trusted(device.ID); ok && modeRank(granted) >= modeRank(mode) {
			s.log.Info("remote desktop approval remembered", "device", device.ID, "mode", string(mode))
		} else {
			dec, err := s.authorize(ctx, device, mode)
			if err != nil {
				return protocol.Answer{}, err
			}
			if dec.Mode == protocol.ModeViewOnly {
				mode = protocol.ModeViewOnly
			}
			if s.cfg.Trust != nil {
				s.cfg.Trust.Remember(device, mode)
			}
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
		UnlockSecret: s.cfg.UnlockSecret, AutoUnlock: s.cfg.AutoUnlock,
		OnEnd: func(protocol.ErrorCode) { s.ended(sess) },
	})
	if _, err := sess.Prepare(ctx); err != nil {
		_ = capr.Close()
		return protocol.Answer{}, err
	}
	peer, sdp, err := rtc.Answer(ctx, offer.SDP, rtc.Options{
		ICEServers: s.iceServersFor(ctx, offer.SDP), InterfaceFilter: s.cfg.InterfaceFilter,
		StartKbps: protocol.Specs[tier].BitrateKbps,
		Handlers: rtc.Handlers{
			OnBandwidth:       sess.OnBandwidth,
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

// modeRank orders the grants: watching is below controlling.
func modeRank(m protocol.Mode) int {
	if m == protocol.ModeViewOnly {
		return 1
	}
	return 2
}

func (s *Service) trusted(deviceID string) (protocol.Mode, bool) {
	if s.cfg.Trust == nil || deviceID == "" {
		return "", false
	}
	return s.cfg.Trust.Granted(deviceID)
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

// DisconnectDevice ends the session of a phone (revoke / forget) and drops its remembered approval; the
// Host calls it with the device that lost its token.
func (s *Service) DisconnectDevice(deviceID string) {
	s.ForgetDevice(deviceID)
}

// ForgetDevice drops a phone's remembered approval and ends its session: it is asked again next time.
func (s *Service) ForgetDevice(deviceID string) {
	if s.cfg.Trust != nil {
		s.cfg.Trust.Forget(deviceID)
	}
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

// iceServersFor gives STUN/TURN entries only to a phone that arrives from off the LAN. Such a phone
// gathered reflexive or relayed candidates itself (it asks for ICE servers only off-LAN), so its offer
// says so; a phone on the LAN or Tailscale offers host candidates alone and a dead STUN server must never
// delay it.
func (s *Service) iceServersFor(ctx context.Context, sdp string) []rtc.ICEServer {
	if s.cfg.ICEServers == nil || !offersOffLAN(sdp) {
		return nil
	}
	return s.cfg.ICEServers(ctx)
}

// offersOffLAN reports whether an SDP offer carries a server-reflexive or relayed candidate.
func offersOffLAN(sdp string) bool {
	return strings.Contains(sdp, " typ srflx") || strings.Contains(sdp, " typ relay")
}
