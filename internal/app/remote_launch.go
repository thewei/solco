package app

import (
	"context"
	"errors"
	"net/http"

	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/remotehost"
)

/**
 * [INPUT]: 依赖 SessionStarter（共享的启动新会话服务层）、remotehost.Launcher 契约
 * [OUTPUT]: 对外提供 RemoteLauncher（实现 remotehost.Launcher：手机「启动新会话」）
 * [POS]: 手机 Host 与共享层之间的薄适配——只翻译类型与错误码（SessionError → LaunchError 的 HTTP 状态），不含任何启动逻辑
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

type RemoteLauncher struct {
	sessions *SessionStarter
}

func NewRemoteLauncher(service *LaunchService) *RemoteLauncher {
	return &RemoteLauncher{sessions: service.Sessions()}
}

func (l *RemoteLauncher) Kinds() []remotehost.LaunchKind {
	kinds := l.sessions.Kinds()
	out := make([]remotehost.LaunchKind, len(kinds))
	for i, k := range kinds {
		out[i] = remotehost.LaunchKind{ID: k.ID, Label: k.Label}
	}
	return out
}

func (l *RemoteLauncher) Launch(ctx context.Context, instance string, spec remotehost.LaunchSpec) (remotehost.LaunchResult, error) {
	session := SessionSpec{RequestID: spec.RequestID, WorkspaceID: spec.WorkspaceID, CWD: spec.CWD, Prompt: spec.Prompt}
	if spec.Kind != remotehost.LaunchTerminal {
		provider, ok := history.ParseAgentID(spec.Kind)
		if !ok {
			return remotehost.LaunchResult{}, &remotehost.LaunchError{Code: "launch_failed", Status: http.StatusBadGateway, Detail: "unknown kind"}
		}
		session.Agent = provider
	}
	result, err := l.sessions.StartSession(ctx, instance, session)
	var failure *SessionError
	if errors.As(err, &failure) {
		return remotehost.LaunchResult{}, &remotehost.LaunchError{Code: string(failure.Code), Status: launchStatus(failure.Code), Detail: failure.Detail}
	}
	return remotehost.LaunchResult{PaneID: result.PaneID, PromptPending: result.PromptPending}, err
}

func launchStatus(code SessionErrorCode) int {
	switch code {
	case SessionPathUnavailable:
		return http.StatusBadRequest
	case SessionConflict:
		return http.StatusConflict
	case SessionUncertain:
		return http.StatusGatewayTimeout
	}
	return http.StatusBadGateway
}
