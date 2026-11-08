package remotehost

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

/**
 * [INPUT]: 依赖 Options.RemoteDesktop（App 注入的 Remote Desktop 服务，*remotedesktop.Service 直接满足）、remotedesktop/protocol 的线格式、server.go 的 guard/writeError/closeDevice
 * [OUTPUT]: 对外提供 RemoteDesktop 接口、ScopeRemoteDesktop、GET /api/v3/remote-desktop（就绪状态与权限）、POST /api/v3/remote-desktop/session（SDP offer → answer，最长阻塞到 Mac 用户批准）、bootstrap 的 capabilities.remoteDesktop
 * [POS]: 手机看 Mac 整个桌面并操控的“信令门”：Host 只做已配对设备的认证、错误码到 HTTP 状态的映射与撤销联动，媒体与输入走 WebRTC 点对点（LAN / Tailscale 直连，中继只转发这一次 HTTP 信令），
 *        视频、键鼠、剪贴板、屏幕内容永不经过也永不进入 Host 的日志；设备被 Revoke 时 closeDevice 同步结束它的桌面会话；
 *        功能在桌面 Settings 关闭时 bootstrap 不声明 remoteDesktop、路由回 404 remote_disabled，使手机界面里根本没有入口
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// RemoteDesktop is the seam to the desktop's Remote Desktop service (*remotedesktop.Service).
type RemoteDesktop interface {
	// Enabled reports the user's switch (Settings → Remote Desktop). It must be cheap: bootstrap calls it.
	Enabled() bool
	Info(ctx context.Context) protocol.Info
	// Open admits one phone. It blocks while the Mac user decides, honours ctx, and leaves nothing running on error.
	Open(ctx context.Context, device protocol.DeviceInfo, offer protocol.Offer) (protocol.Answer, error)
	// DisconnectDevice ends that phone's session (it was revoked).
	DisconnectDevice(deviceID string)
}

// offerBodyLimit bounds one SDP offer body (a real one is a few KiB).
const offerBodyLimit = 96 << 10

func (s *Server) remoteDesktopOn() bool {
	return s.opts.RemoteDesktop != nil && s.opts.RemoteDesktop.Enabled()
}

func (s *Server) handleRemoteDesktopInfo(w http.ResponseWriter, r *http.Request, _ Device) {
	if !s.remoteDesktopOn() {
		writeError(w, http.StatusNotFound, string(protocol.CodeDisabled), "")
		return
	}
	writeJSON(w, http.StatusOK, s.opts.RemoteDesktop.Info(r.Context()))
}

func (s *Server) handleRemoteDesktopSession(w http.ResponseWriter, r *http.Request, dev Device) {
	if !s.remoteDesktopOn() {
		writeError(w, http.StatusNotFound, string(protocol.CodeDisabled), "")
		return
	}
	var offer protocol.Offer
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, offerBodyLimit)).Decode(&offer); err != nil {
		writeError(w, http.StatusBadRequest, string(protocol.CodeBadRequest), "")
		return
	}
	answer, err := s.opts.RemoteDesktop.Open(r.Context(), protocol.DeviceInfo{ID: dev.ID, Name: dev.Name}, offer)
	if err != nil {
		code := protocol.CodeOf(err)
		slog.Info("remote desktop session refused", "device", dev.ID, "code", string(code))
		writeError(w, remoteDesktopStatus(code), string(code), "")
		return
	}
	writeJSON(w, http.StatusOK, answer)
}

// remoteDesktopStatus maps a stable refusal code to its HTTP status. Bodies carry the code only —
// never helper, ssh or SDP text.
func remoteDesktopStatus(code protocol.ErrorCode) int {
	switch code {
	case protocol.CodeDisabled:
		return http.StatusNotFound
	case protocol.CodeBadRequest, protocol.CodeBadOffer, protocol.CodeProtocolUnsupported:
		return http.StatusBadRequest
	case protocol.CodeDenied:
		return http.StatusForbidden
	case protocol.CodeApprovalTimeout:
		return http.StatusRequestTimeout
	case protocol.CodeBusy, protocol.CodePermissionRequired, protocol.CodeDisplayNotFound:
		return http.StatusConflict
	case protocol.CodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}
