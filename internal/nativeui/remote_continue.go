package nativeui

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/remotehost"
)

/**
 * [INPUT]: 只读 HistoryService.Session（目录元数据）、remoteLiveSessions 的身份快照、agent.PlanHistoryContinuation、app.LaunchService.ExecuteHistoryContinuation（既有 Herdr launch transaction）
 * [OUTPUT]: Shell.hostContinuer（remotehost.Continuer）；失败翻译成 remotehost.LaunchError 的稳定错误码
 * [POS]: 手机 Sessions 的 Continue 与桌面 Continue 同一台引擎（history_continue.go）：同一份规划器、同一条精确身份匹配、同一个 launch transaction——只是没有确认框（手机的按钮就是确认）也不选目标 Agent（v1 只续接同一 Agent）；
 *        Host goroutine 只碰构造时捕获的服务与线程安全快照，不碰 Shell 状态、不移动 Mac 焦点；会话文件只读；晚失败但 pane 已存在时返回该 pane（与桌面一样让用户看见，而不是重试出第二个）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// sessionLookup is the catalog read Continue needs; the production
// HistoryService has it, test fakes opt in.
type sessionLookup interface {
	Session(key string) (history.SessionMeta, bool)
}

type remoteContinuer struct {
	launch  *app.LaunchService
	lookup  sessionLookup
	service HistoryListView
	live    *remoteLiveSessions
}

// hostContinuer builds the Host's Continue seam; nil when the pieces it needs
// are missing, so the phone is told there is no Continue.
func (s *Shell) hostContinuer(service HistoryListView) remotehost.Continuer {
	lookup, ok := service.(sessionLookup)
	if !ok || s.launch == nil {
		return nil
	}
	return &remoteContinuer{launch: s.launch, lookup: lookup, service: service, live: &s.remoteLive}
}

func (c *remoteContinuer) Continue(ctx context.Context, instance, sessionID string) (remotehost.ContinueResult, error) {
	meta, ok := c.lookup.Session(sessionID)
	if !ok {
		return remotehost.ContinueResult{}, &remotehost.LaunchError{Code: "unknown_session", Status: http.StatusNotFound}
	}
	source := agent.ContinuationSource{
		Agent:           meta.Agent,
		ID:              meta.ID,
		FilePath:        meta.FilePath,
		ProjectPath:     meta.ProjectPath,
		ResumeSupported: agent.ResumeSupported(meta.Agent),
	}
	plan := agent.PlanHistoryContinuation(source, nil, c.liveIdentities(instance), "")
	switch plan.Strategy {
	case agent.StrategyAlreadyLive:
		return remotehost.ContinueResult{PaneID: plan.PaneID, AlreadyLive: true}, nil
	case agent.StrategyNativeResume, agent.StrategyContextTransfer:
	case agent.StrategyNeedsProjectSelect:
		return remotehost.ContinueResult{}, &remotehost.LaunchError{Code: "needs_project", Status: http.StatusConflict, Detail: "This session has no project folder, so it can't be continued here."}
	default:
		slog.Info("remote continue unavailable", "agent", meta.Agent, "reason", plan.Reason)
		return remotehost.ContinueResult{}, &remotehost.LaunchError{Code: "cannot_continue", Status: http.StatusConflict, Detail: "This session can't be continued from here."}
	}
	deps := app.HistoryContinuationDeps{
		OpenWindow: func(ctx context.Context, _ history.SessionFileRef) (history.TranscriptWindow, error) {
			return c.service.Open(ctx, history.ConversationWindowRequest{ConversationID: sessionID, Tail: true})
		},
	}
	outcome, err := c.launch.ExecuteHistoryContinuation(ctx, instance, plan, "", deps)
	pane := continuedPane(outcome, err)
	if pane != "" {
		if err != nil {
			slog.Warn("remote continue finished late", "instance", instance, "agent", meta.Agent, "pane_id", pane, "error", err)
		}
		return remotehost.ContinueResult{PaneID: pane}, nil
	}
	return remotehost.ContinueResult{}, continueLaunchError(err)
}

// liveIdentities are the running Agents' typed identities, only for the Herdr
// session the window shows (the only one the snapshot describes).
func (c *remoteContinuer) liveIdentities(instance string) []agent.LiveAgentIdentity {
	c.live.mu.RLock()
	defer c.live.mu.RUnlock()
	if c.live.instance != instance {
		return nil
	}
	return append([]agent.LiveAgentIdentity(nil), c.live.ids...)
}

// continueLaunchError translates an engine failure into the phone's stable codes.
func continueLaunchError(err error) *remotehost.LaunchError {
	var failure *agent.LaunchFailure
	if !errors.As(err, &failure) {
		return &remotehost.LaunchError{Code: "launch_failed", Status: http.StatusBadGateway, Detail: "Couldn't continue this session."}
	}
	switch failure.Kind {
	case "project-unavailable", "branch-preparation":
		return &remotehost.LaunchError{Code: "path_unavailable", Status: http.StatusBadRequest, Detail: "The project folder isn't available."}
	case "operation-conflict":
		return &remotehost.LaunchError{Code: "launch_conflict", Status: http.StatusConflict, Detail: "This session is already being continued."}
	case "agent-start-uncertain":
		return &remotehost.LaunchError{Code: "launch_uncertain", Status: http.StatusGatewayTimeout, Detail: "The Agent may have started — check the Mac."}
	}
	return &remotehost.LaunchError{Code: "launch_failed", Status: http.StatusBadGateway, Detail: "Couldn't continue this session."}
}
