package nativeui

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/remotehost"
)

/**
 * [INPUT]: 只读 HistoryService（目录元数据与标题/项目搜索）、history_live.go 的 matchLiveSession（与桌面 Continue 同一条精确身份规则）、UI 线程发布的 Agent 身份快照
 * [OUTPUT]: remoteLiveSessions、Shell.publishRemoteLiveSessions（UI 线程）、Shell.hostSessions（Host goroutine 取手机 Sessions 列表）、wireSessionState
 * [POS]: 手机 Sessions 与桌面 Sessions 同源同语义：目录是同一份只读元数据，“运行中”角标沿用桌面的精确匹配（所以手机和桌面永远一致），Host 与手机都不读会话正文、不启动任何东西；
 *        身份快照在 UI 线程算好整体替换，Host goroutine 只读（自带锁），从不触 Shell 状态
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// remoteLiveSessions is the published snapshot of the Agents running in the
// window's Herdr session: written on the window lane, read on the Host's
// goroutines, replaced whole.
type remoteLiveSessions struct {
	mu       sync.RWMutex
	instance string
	ids      []agent.LiveAgentIdentity
	states   map[string]string // pane id → wire state word
}

// wireSessionState is the one status word the phone gets for a running session.
func wireSessionState(state operationalState) string {
	switch state {
	case opWorking:
		return "working"
	case opNeedsAttention:
		return "attention"
	case opReadyForReview:
		return "review"
	}
	return "idle"
}

// publishRemoteLiveSessions snapshots the typed session identities and each
// Agent pane's state for the Herdr session this window shows.
func (s *Shell) publishRemoteLiveSessions() {
	ids := s.liveAgentIdentities()
	states := make(map[string]string, len(ids))
	for _, id := range ids {
		state := opIdle
		if card, ok := s.agentCardForPane(id.PaneID); ok {
			state = agentIndicatorState(card)
		}
		states[id.PaneID] = wireSessionState(state)
	}
	s.remoteLive.mu.Lock()
	s.remoteLive.instance, s.remoteLive.ids, s.remoteLive.states = s.activeInstance, ids, states
	s.remoteLive.mu.Unlock()
}

// hostSessions builds the Host's Sessions seam over the read-only catalog. The
// service is captured by the caller at Host construction, so the Host's
// goroutines never touch the Shell.
func (s *Shell) hostSessions(service HistoryListView) remotehost.SessionsFunc {
	if service == nil {
		return nil
	}
	return func(ctx context.Context, q remotehost.SessionsQuery) (remotehost.Sessions, error) {
		list, err := service.List(ctx, history.HistoryQuery{Search: q.Search, Limit: q.Limit})
		if err != nil {
			return remotehost.Sessions{}, err
		}
		s.remoteLive.mu.RLock()
		instance, ids, states := s.remoteLive.instance, s.remoteLive.ids, s.remoteLive.states
		s.remoteLive.mu.RUnlock()

		rows := make([]remotehost.SessionRow, 0, len(list))
		for _, summary := range list {
			meta := summary.Meta
			if meta.Archived {
				continue
			}
			row := remotehost.SessionRow{
				ID:        meta.Key,
				Agent:     string(meta.Agent),
				Title:     fallbackText(strings.TrimSpace(meta.Title), history.Untitled),
				Project:   fallbackText(meta.ProjectName, shortDir(meta.ProjectPath)),
				UpdatedMs: meta.UpdatedAt,
				Messages:  meta.MessageCount,
			}
			if pane, ok := matchLiveSession(meta, ids); ok {
				row.Running = &remotehost.SessionRunning{Instance: instance, PaneID: pane, State: fallbackText(states[pane], "idle")}
			}
			rows = append(rows, row)
		}
		// Running sessions first; the catalog's newest-first order holds within each group.
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Running != nil && rows[j].Running == nil })
		return remotehost.Sessions{Sessions: rows}, nil
	}
}
