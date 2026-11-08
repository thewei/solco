package remotehost

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"sync"
	"time"
)

/**
 * [INPUT]: 依赖 Options.Overlay（OverlayFactory，由 hostd 以 internal/tailcatnet 实现；本包不 import tailscale）与 DeviceStore.TailcatKey
 * [OUTPUT]: 对外提供 Overlay/OverlayFactory/OverlayState/OverlayStatus、Server.OverlayStatus、Server.TailcatAddress、handleEnroll（POST /connect/enroll）与隧道 peer 绑定规则
 * [POS]: 内置 Tailcat 隧道接入同一个 Host：隧道只是又一个 net.Listener（同一 http.Server、同一 TLS 证书与指纹、同一 DeviceStore/Bearer/Scope），不是第二套 Host；
 *        准入 = 设备库里已绑定的 peer 节点公钥，或配对窗口开着（配对码有效）时放行未知 peer 以便其到达 /pair，窗口一关就扫掉未绑定 peer；
 *        peer 公钥只取自 WireGuard 握手（Overlay.PeerKey(远端地址)），绝不信客户端自报；经隧道到达的 token 必须与其绑定的 peer 一致；撤销设备 = 先拒后断
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Overlay is a running tunnel whose Listener carries the Host's HTTPS.
type Overlay interface {
	// Listener accepts tunnel connections (TCP streams from authenticated peers).
	Listener() net.Listener
	// Addr is the client-dialable address: a bearer capability, never logged.
	Addr() string
	// PeerKey names the authenticated tunnel peer behind a connection's remote address ("ip:port").
	PeerKey(remote string) (string, bool)
	// Disconnect drops a connected peer.
	Disconnect(peerKey string) bool
	Close() error
}

// OverlayFactory starts a tunnel. allow decides admission of a client key (text form) and must not block long.
// It may take network time, so the Host calls it off its caller's goroutine.
type OverlayFactory func(ctx context.Context, allow func(peerKey string) bool) (Overlay, error)

// OverlayState is where the tunnel stands, for Settings.
type OverlayState string

const (
	OverlayStopped  OverlayState = ""
	OverlayStarting OverlayState = "starting"
	OverlayReady    OverlayState = "ready"
	OverlayError    OverlayState = "error"
)

// OverlayStatus is what Settings shows. Message never carries the address.
type OverlayStatus struct {
	State   OverlayState
	Message string
}

type overlayRuntime struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	ov     Overlay
	status OverlayStatus
	// windowPeers are keys admitted only because the pairing window was open; they are dropped when it closes
	// unless pairing bound them to a device.
	windowPeers map[string]struct{}
	sweepTimer  *time.Timer
}

var nodeKeyPattern = regexp.MustCompile(`^nodekey:[0-9a-f]{64}$`)

// OverlayStatus reports the tunnel's state.
func (s *Server) OverlayStatus() OverlayStatus {
	s.overlay.mu.Lock()
	defer s.overlay.mu.Unlock()
	return s.overlay.status
}

// TailcatAddress is the tunnel address while it is ready ("" otherwise). It is a bearer capability: callers put
// it only in a QR/pairing link or an authenticated answer, never in a log or a UI row.
func (s *Server) TailcatAddress() string {
	s.overlay.mu.Lock()
	defer s.overlay.mu.Unlock()
	if s.overlay.ov == nil || s.overlay.status.State != OverlayReady {
		return ""
	}
	return s.overlay.ov.Addr()
}

func (s *Server) setOverlayStatus(st OverlayStatus) {
	s.overlay.mu.Lock()
	s.overlay.status = st
	s.overlay.mu.Unlock()
}

// startOverlay brings the tunnel up in the background (DERP discovery takes network time and Start is called from
// UI lanes). Failure leaves the Host serving its other routes and puts the state in error.
func (s *Server) startOverlay(srv *http.Server) {
	factory := s.opts.Overlay
	if factory == nil {
		s.setOverlayStatus(OverlayStatus{State: OverlayError, Message: "The built-in tunnel is not available in this build."})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.overlay.mu.Lock()
	s.overlay.cancel, s.overlay.done = cancel, done
	s.overlay.status = OverlayStatus{State: OverlayStarting}
	s.overlay.mu.Unlock()
	go func() {
		defer close(done)
		ov, err := factory(ctx, s.allowPeer)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("remote host tunnel failed to start", "error", err)
				s.setOverlayStatus(OverlayStatus{State: OverlayError, Message: "Could not reach the relay network. Check this Mac's internet connection."})
			}
			return
		}
		s.overlay.mu.Lock()
		if ctx.Err() != nil {
			s.overlay.mu.Unlock()
			_ = ov.Close()
			return
		}
		s.overlay.ov = ov
		s.overlay.status = OverlayStatus{State: OverlayReady}
		s.overlay.mu.Unlock()
		slog.Info("remote host tunnel ready")
		if err := srv.ServeTLS(ov.Listener(), "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) && ctx.Err() == nil {
			slog.Warn("remote host tunnel stopped", "error", err)
			s.setOverlayStatus(OverlayStatus{State: OverlayError, Message: "The tunnel stopped. Turn Remote Access off and on to restart it."})
		}
	}()
}

func (s *Server) stopOverlay() {
	s.overlay.mu.Lock()
	cancel, done, ov := s.overlay.cancel, s.overlay.done, s.overlay.ov
	t := s.overlay.sweepTimer
	s.overlay.cancel, s.overlay.done, s.overlay.ov, s.overlay.sweepTimer = nil, nil, nil, nil
	s.overlay.status = OverlayStatus{}
	s.overlay.windowPeers = nil
	s.overlay.mu.Unlock()
	if t != nil {
		t.Stop()
	}
	if cancel != nil {
		cancel()
	}
	if ov != nil {
		_ = ov.Close()
	}
	if done != nil {
		<-done
	}
}

// allowPeer is the tunnel's admission rule.
func (s *Server) allowPeer(peerKey string) bool {
	if s.devices.HasTailcatKey(peerKey) {
		return true
	}
	if !s.pairing.Active() {
		return false
	}
	s.overlay.mu.Lock()
	if s.overlay.windowPeers == nil {
		s.overlay.windowPeers = map[string]struct{}{}
	}
	if len(s.overlay.windowPeers) < 64 { // a window is minutes long and one phone is expected
		s.overlay.windowPeers[peerKey] = struct{}{}
	}
	s.overlay.mu.Unlock()
	return true
}

// armPairingSweep schedules the post-window sweep for a code that just started.
func (s *Server) armPairingSweep() {
	s.overlay.mu.Lock()
	if s.overlay.sweepTimer != nil {
		s.overlay.sweepTimer.Stop()
	}
	s.overlay.sweepTimer = time.AfterFunc(PairingTTL+2*time.Second, s.sweepWindowPeers)
	s.overlay.mu.Unlock()
}

// sweepWindowPeers drops every peer that was admitted only for the pairing window and never got bound to a device.
func (s *Server) sweepWindowPeers() {
	if s.pairing.Active() {
		return
	}
	s.overlay.mu.Lock()
	ov := s.overlay.ov
	peers := s.overlay.windowPeers
	s.overlay.windowPeers = nil
	s.overlay.mu.Unlock()
	if ov == nil {
		return
	}
	for k := range peers {
		if !s.devices.HasTailcatKey(k) {
			ov.Disconnect(k)
		}
	}
}

// tunnelPeer is the authenticated tunnel peer behind this request, if it arrived through the tunnel.
func (s *Server) tunnelPeer(r *http.Request) (string, bool) {
	s.overlay.mu.Lock()
	ov := s.overlay.ov
	s.overlay.mu.Unlock()
	if ov == nil {
		return "", false
	}
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err != nil || !ap.IsValid() {
		return "", false
	}
	return ov.PeerKey(r.RemoteAddr)
}

// peerMatches enforces the binding: through the tunnel a token works only from the node key it was bound to.
func (s *Server) peerMatches(r *http.Request, dev Device) bool {
	peer, viaTunnel := s.tunnelPeer(r)
	if !viaTunnel {
		return true
	}
	return dev.TailcatKey != "" && dev.TailcatKey == peer
}

// disconnectKey ends a revoked device's tunnel presence. Revoke removed the key from the store first, so
// admission already refuses it; this drops the live peer.
func (s *Server) disconnectKey(key string) {
	s.overlay.mu.Lock()
	ov := s.overlay.ov
	s.overlay.mu.Unlock()
	if ov != nil {
		ov.Disconnect(key)
	}
}

type enrollRequest struct {
	ClientKey string `json:"clientKey"`
}

// handleEnroll lets an already-paired phone (token over a pinned route) bind its tunnel node key and receive the
// tunnel address. The address is a bearer capability, so it travels only here and in the pairing QR; the key
// binding is what makes it usable. Idempotent for the same key; a different key needs a fresh pairing.
func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request, dev Device) {
	if _, viaTunnel := s.tunnelPeer(r); viaTunnel {
		// already admitted by its key: nothing to enroll, just answer the address
		writeJSON(w, http.StatusOK, map[string]string{"tailcat": s.TailcatAddress()})
		return
	}
	var req enrollRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || !nodeKeyPattern.MatchString(req.ClientKey) {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	addr := s.TailcatAddress()
	if addr == "" {
		writeError(w, http.StatusServiceUnavailable, "tunnel_unavailable", "")
		return
	}
	if err := s.devices.BindTailcatKey(dev.ID, req.ClientKey); err != nil {
		if errors.Is(err, ErrKeyBound) {
			writeError(w, http.StatusConflict, "key_bound", "")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tailcat": addr})
}
