package nativeui

import (
	"context"
	"reflect"
	"time"

	"github.com/egoist/mygo"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 history.SubagentsFor/ReadSubagent（各 Provider 自己的子代理文件，只读）、Shell 的 chatLivePath/chatLiveProvider/chatTurns、publishChatBridge
 * [OUTPUT]: chatsession.SubagentCard 与 ChatStep.SubagentIDs（Chat 卡片/名册的数据）、ChatBridge.OpenSubagent（下钻抽屉的只读对话）、Shell.pokeChatSubagents（合并的后台刷新）、chatSubagentOpen 事件与 Shell.openSubagentDrawer/consumeSubagentDrawerAsk（原生侧栏/浮层子代理行：点Chat、绑定后让页面开抽屉）
 * [POS]: WebView Chat 的子代理表现层（2026-10-31，design: designs/subagents）：仅对 live 绑定的会话工作；父工具调用 → 子代理的链接来自工具自己的证据（Claude tool_use id == meta.toolUseId，omp `tasks[].name`，Codex `spawn_agent.task_name`），链接不上就当普通工具行，没有空卡片；刷新是 600ms 合并 + 运行期 1.5s 自重排的后台读取，渲染路径零 IO；不持久化、不记录对话内容
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	chatSubagentsSettle = 600 * time.Millisecond
	chatSubagentsTick   = 1500 * time.Millisecond
)

// pokeChatSubagents schedules one coalesced background refresh of the bound
// chat's sub-agents (UI lane). Parent-transcript syncs call it freely.
func (s *Shell) pokeChatSubagents() {
	if s.chatLivePath == "" || s.chatLiveProvider == "" || s.chatSubagentsTimer != nil {
		return
	}
	s.scheduleChatSubagents(chatSubagentsSettle)
}

func (s *Shell) scheduleChatSubagents(delay time.Duration) {
	s.chatSubagentsTimer = time.AfterFunc(delay, func() { s.applyOnWindow(s.refreshChatSubagents) })
}

// refreshChatSubagents re-reads the bound conversation's sub-agents off the
// UI lane and publishes when something changed (UI lane).
func (s *Shell) refreshChatSubagents() {
	s.chatSubagentsTimer = nil
	path, provider := s.chatLivePath, s.chatLiveProvider
	if path == "" || provider == "" || s.chatSubagentsBusy {
		return
	}
	s.chatSubagentsBusy = true
	go func() {
		views := chatsession.Read(provider, path, time.Now())
		s.applyOnWindow(func() {
			s.chatSubagentsBusy = false
			if s.chatLivePath != path || s.chatLiveProvider != provider {
				return // rebound while reading
			}
			// A status is "running" for a window after the last write, so it
			// flips with the clock even when the files stay put.
			active := chatsession.AnyActive(views, time.Now())
			if !reflect.DeepEqual(s.chatSubagents, views) || active != s.chatSubagentsActive {
				s.chatSubagents = views
				s.chatSubagentsActive = active
				s.publishChatBridge()
			}
			if active {
				s.scheduleChatSubagents(chatSubagentsTick)
			}
		})
	}()
}

// clearChatSubagents drops the cache with the binding it belonged to.
func (s *Shell) clearChatSubagents() {
	if s.chatSubagentsTimer != nil {
		s.chatSubagentsTimer.Stop()
		s.chatSubagentsTimer = nil
	}
	s.chatSubagents = nil
	s.chatSubagentsActive = false
}

// attachSubagents stamps SubagentIDs on the tool steps that spawned one and
// lists exactly those sub-agents (chatsession.AttachSubagents).
func (state *ChatBridgeState) attachSubagents(views []chatsession.View, turns []conversation.TimelineTurn, provider history.AgentID, now time.Time) {
	state.Subagents = chatsession.AttachSubagents(state.Turns, turns, views, now)
}

// ChatBridgeSubagentView is the drawer's read-only conversation.
type ChatBridgeSubagentView struct {
	Subagent chatsession.SubagentCard `json:"subagent"`
	Turns    []chatsession.ChatTurn   `json:"turns"`
	// Error is a short reason the transcript cannot be shown ("" = fine).
	Error string `json:"error,omitempty"`
}

// OpenSubagent returns one sub-agent's own transcript for the drawer. The id
// must be one the current chat listed; the page never names a path.
func (b *ChatBridge) OpenSubagent(ctx context.Context, id string) (ChatBridgeSubagentView, error) {
	var (
		views  []chatsession.View
		parent []conversation.TimelineTurn
	)
	b.shell.callOnWindow(func() { views, parent = b.shell.chatSubagents, b.shell.chatTurns })
	view, err := chatsession.OpenSubagent(views, parent, id, time.Now())
	if err != nil {
		return ChatBridgeSubagentView{}, err
	}
	return ChatBridgeSubagentView{Subagent: view.Subagent, Turns: view.Turns, Error: view.Error}, nil
}

// chatSubagentOpen asks the page to open one listed sub-agent in its drawer
// (the native sidebar's and quick panel's nested rows have no page click of
// their own). The Shell emits it only once the sub's conversation is the
// bound one, so the id is always one the current chat lists.
var chatSubagentOpen = mygo.NewEvent[string]("chat:subagent-open")

// subDrawerAskTTL bounds how long a native row's drawer ask waits for its
// conversation to bind; an agent that never binds drops it.
const subDrawerAskTTL = 8 * time.Second

// subDrawerAsk is one nested sub-agent row's click waiting for the Chat
// surface to bind the conversation it belongs to.
type subDrawerAsk struct {
	conversation string
	subID        string
	at           time.Time
}

// openSubagentDrawer is a nested sub-agent row's click (the sidebar and the
// quick panel): land on the parent agent's Chat — the drawer exists only
// there — and open the sub's drawer once that conversation is the bound
// one. UI lane.
func (s *Shell) openSubagentDrawer(card agent.AgentCardModel, sub history.Subagent) {
	s.openChat(card)
	target := liveConversationKey(card.Provider, card.Key.TerminalID)
	if s.chatConversationID == target {
		s.emitSubagentDrawer(sub.ID)
		return
	}
	s.pendingSubagentDrawer = subDrawerAsk{conversation: target, subID: sub.ID, at: time.Now()}
}

// consumeSubagentDrawerAsk fires the pending drawer ask when the
// conversation that just became bound is the one it targeted, and drops a
// stale one (UI lane, at every live bind).
func (s *Shell) consumeSubagentDrawerAsk() {
	ask := s.pendingSubagentDrawer
	if ask.subID == "" {
		return
	}
	if ask.conversation != s.chatConversationID {
		if time.Since(ask.at) > subDrawerAskTTL {
			s.pendingSubagentDrawer = subDrawerAsk{}
		}
		return
	}
	s.pendingSubagentDrawer = subDrawerAsk{}
	s.emitSubagentDrawer(ask.subID)
}

// emitSubagentDrawer asks the page to open one listed sub-agent's drawer.
func (s *Shell) emitSubagentDrawer(id string) {
	if s.win == nil {
		return
	}
	_ = chatSubagentOpen.Emit(s.win, id)
}
