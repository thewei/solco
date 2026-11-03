package nativeui

import (
	"strings"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: Herdr 投影里 Agent 的类型化会话身份（liveAgentIdentities，含 hookIdentities 补全）、agent.PlanHistoryContinuation 的 AlreadyLive 精确匹配、agentCardForPane 的运行状态
 * [OUTPUT]: 对外提供 liveSession、Shell.liveSessionFor（一条历史会话是否正被某个 Pane 的 Agent 运行）、Shell.goToPane（落到该 Pane 的记忆表面）、liveSessionLabel/liveSessionTone（行内状态胶囊）
 * [POS]: Sessions 页的“运行中”关联层（2026-11-01 用户拍板）：只读投影，不新增运行时状态；匹配规则与 Continue 同源（精确 id/path，从不看 cwd/mtime），所以列表上的标识和点击 Continue 的结果永远一致
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// liveSession is a history session an Agent in one of the open Panes is
// running right now.
type liveSession struct {
	PaneID string
	State  operationalState
	Where  string // the Tab the Pane sits in; empty when unknown
}

// liveSessionFor reports the Pane running meta, by the same exact identity
// match Continue uses (so the badge and the click cannot disagree).
func (s *Shell) liveSessionFor(meta history.SessionMeta) (liveSession, bool) {
	paneID, ok := matchLiveSession(meta, s.liveAgentIdentities())
	if !ok {
		return liveSession{}, false
	}
	found := liveSession{PaneID: paneID, State: opIdle}
	if card, ok := s.agentCardForPane(paneID); ok {
		found.State = agentIndicatorState(card)
		for _, tab := range s.projection.Tabs {
			if tab.ID == card.TabID {
				found.Where = strings.TrimSpace(tab.Label)
				break
			}
		}
	}
	return found, true
}

// matchLiveSession is the one running-session rule, shared by the Sessions
// page, Continue and the phone's list: the Pane whose Agent holds exactly
// this session (typed id/path identity; never cwd or mtime).
func matchLiveSession(meta history.SessionMeta, live []agent.LiveAgentIdentity) (string, bool) {
	if len(live) == 0 {
		return "", false
	}
	plan := agent.PlanHistoryContinuation(agent.ContinuationSource{
		Agent: meta.Agent, ID: meta.ID, FilePath: meta.FilePath, ProjectPath: meta.ProjectPath,
	}, nil, live, "")
	if plan.Strategy != agent.StrategyAlreadyLive {
		return "", false
	}
	return plan.PaneID, true
}

// liveSessionLabel is the pill's word for a running session.
func liveSessionLabel(state operationalState) string {
	switch state {
	case opWorking:
		return "Running"
	case opNeedsAttention:
		return "Needs you"
	case opReadyForReview:
		return "Ready"
	}
	return "Open · idle"
}

// liveSessionTone is the pill's tone: the state's own, with an idle session
// drawn neutral (it is open, not asking for anything).
func liveSessionTone(state operationalState) StatusTone {
	switch state {
	case opWorking, opNeedsAttention, opReadyForReview:
		return operationalTone(state)
	}
	return ToneNeutral
}

// goToPane brings a running session's Pane into view on the surface it was
// last used on (Chat when remembered and bindable, Terminal otherwise): the
// per-pane memory decides through the shared showPaneSurface owner, so "Go
// to pane" never flips a Chat pane back to its terminal (2026-11-03).
func (s *Shell) goToPane(paneID string) {
	s.selectPane(paneID)
	if s.router.Path() != routeWorkspace {
		s.router.Push(routeWorkspace)
	}
	s.showPaneSurface(paneID)
}
