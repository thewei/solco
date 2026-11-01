package remotehost

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
)

/**
 * [INPUT]: 依赖 Options.Continuer（续接一条历史会话的唯一外部缝，nativeui 提供）、Backend.Projection（解析新 pane）、launch.go 的 LaunchError/awaitLaunchedPane、server.go 的 guard/resolveInstance/writeError
 * [OUTPUT]: 对外提供 Continuer/ContinueResult 与 POST /api/v3/sessions/{id}/continue、bootstrap 的 capabilities.sessionContinue
 * [POS]: 手机 Sessions 的「Continue」：与桌面 Sessions 页的 Continue 同一台引擎（同 Agent 的原生续接或上下文交接，已在运行的会话只定位其窗格，不再开第二个）；
 *        规划、身份匹配与 Herdr launch transaction 全在 Continuer 背后（Host 不 import app/nativeui）；会话文件始终只读；
 *        没有 requestId——重复点击由 launch registry 合并，成功后的再次点击会因会话身份已在运行而定位同一窗格；
 *        手机断线不打断事务（WithoutCancel + 150 s）；Mac 焦点不动；日志只记设备、pane、是否复用，会话 id 与标题永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// ContinueResult is the pane the session now runs in. AlreadyLive: an Agent
// already held the session, so nothing was started.
type ContinueResult struct {
	PaneID      string
	AlreadyLive bool
}

// Continuer continues one cataloged session on a local Herdr session. Refusals
// come back as *LaunchError with a stable code (unknown_session,
// needs_project, cannot_continue, path_unavailable, launch_conflict,
// launch_uncertain, launch_failed).
type Continuer interface {
	Continue(ctx context.Context, instance, sessionID string) (ContinueResult, error)
}

type continueResponse struct {
	PaneID      string `json:"paneId"`
	TerminalID  string `json:"terminalId"`
	WorkspaceID string `json:"workspaceId"`
	Agent       bool   `json:"agent"`
	AlreadyLive bool   `json:"alreadyLive"`
}

func (s *Server) handleSessionContinue(w http.ResponseWriter, r *http.Request, dev Device) {
	continuer := s.opts.Continuer
	if continuer == nil {
		writeError(w, http.StatusNotFound, "continue_unavailable", "")
		return
	}
	id := r.PathValue("id")
	if id == "" || len(id) > 256 {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	instance, ok := s.resolveInstance(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), launchTimeout)
	defer cancel()
	result, err := continuer.Continue(ctx, instance, id)
	if err != nil {
		var le *LaunchError
		if errors.As(err, &le) {
			slog.Warn("remote host continue refused", "device", dev.ID, "code", le.Code)
			writeError(w, le.Status, le.Code, truncate(le.Detail, 200))
			return
		}
		slog.Warn("remote host continue failed", "device", dev.ID)
		writeError(w, http.StatusBadGateway, "launch_failed", "")
		return
	}
	launched, found := s.awaitLaunchedPane(ctx, instance, result.PaneID)
	if !found {
		slog.Warn("remote host continue: pane not visible", "device", dev.ID, "pane", result.PaneID)
		writeError(w, http.StatusGatewayTimeout, "launch_uncertain", "")
		return
	}
	slog.Info("remote host continued", "device", dev.ID, "pane", result.PaneID, "alreadyLive", result.AlreadyLive)
	writeJSON(w, http.StatusOK, continueResponse{
		PaneID: launched.PaneID, TerminalID: launched.TerminalID, WorkspaceID: launched.WorkspaceID,
		Agent: launched.Agent, AlreadyLive: result.AlreadyLive,
	})
}
