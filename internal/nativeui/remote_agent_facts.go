package nativeui

import (
	"maps"
	"sync"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/remotehost"
)

/**
 * [INPUT]: 依赖 workbench 已调和的 Agent 卡片（标题 = 会话标题 → agent name → provider 显示名；LastActive = 客户端观察到的转换 ∪ 会话文件 mtime）、agent.IsGeneratedAgentName
 * [OUTPUT]: remoteAgentFacts、Shell.publishRemoteAgentFacts（UI 线程，卡片调和后发布快照）、Shell.hostAgentFacts（Host goroutine 读快照）
 * [POS]: 手机端侧栏标题与桌面对齐的唯一来源——Host 只转发桌面已算好的标题/最近活动时间，不重新解析会话文件；只覆盖桌面当前显示的 Herdr session，其它 session 由 Host 退回 provider 显示名
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// remoteAgentFacts is the published snapshot: written on the window lane, read
// on the Host's goroutines, so it carries its own lock and is replaced whole.
type remoteAgentFacts struct {
	mu         sync.RWMutex
	instance   string
	byTerminal map[string]remotehost.AgentFact
}

// publishRemoteAgentFacts snapshots the reconciled cards for the Herdr session
// this window shows. A Solco-minted plumbing name (kind-<6hex>) is no title.
func (s *Shell) publishRemoteAgentFacts() {
	s.publishRemoteLiveSessions()
	cards := s.workbenchCards()
	facts := make(map[string]remotehost.AgentFact, len(cards))
	for _, card := range cards {
		if card.Key.TerminalID == "" {
			continue
		}
		title := card.Title
		if agent.IsGeneratedAgentName(title, string(card.Provider)) {
			title = ""
		}
		facts[card.Key.TerminalID] = remotehost.AgentFact{Title: title, LastActive: card.LastActive}
	}
	s.remoteFacts.mu.Lock()
	changed := s.remoteFacts.instance != s.activeInstance || !maps.Equal(s.remoteFacts.byTerminal, facts)
	s.remoteFacts.instance, s.remoteFacts.byTerminal = s.activeInstance, facts
	s.remoteFacts.mu.Unlock()
	// A title moves without any Herdr event: tell the phones' open sockets.
	if changed && s.remote.srv != nil {
		s.remote.srv.Poke()
	}
}

// hostAgentFacts serves the snapshot to the Host; another Herdr session gets none.
func (s *Shell) hostAgentFacts(instance string) map[string]remotehost.AgentFact {
	s.remoteFacts.mu.RLock()
	defer s.remoteFacts.mu.RUnlock()
	if instance != s.remoteFacts.instance {
		return nil
	}
	return s.remoteFacts.byTerminal
}
