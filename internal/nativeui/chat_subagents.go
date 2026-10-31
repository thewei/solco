package nativeui

import (
	"context"
	"reflect"
	"time"

	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 history.SubagentsFor/ReadSubagent（各 Provider 自己的子代理文件，只读）、Shell 的 chatLivePath/chatLiveProvider/chatTurns、publishChatBridge
 * [OUTPUT]: chatsession.SubagentCard 与 ChatStep.SubagentIDs（Chat 卡片/名册的数据）、ChatBridge.OpenSubagent（下钻抽屉的只读对话）、Shell.pokeChatSubagents（合并的后台刷新）
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
