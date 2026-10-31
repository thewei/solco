package nativeui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 history.SubagentsFor/ReadSubagent（各 Provider 自己的子代理文件，只读）、Shell 的 chatLivePath/chatLiveProvider/chatTurns、publishChatBridge
 * [OUTPUT]: ChatBridgeSubagent 与 ChatBridgeStep.SubagentIDs（Chat 卡片/名册的数据）、ChatBridge.OpenSubagent（下钻抽屉的只读对话）、Shell.pokeChatSubagents（合并的后台刷新）
 * [POS]: WebView Chat 的子代理表现层（2026-10-31，design: designs/subagents）：仅对 live 绑定的会话工作；父工具调用 → 子代理的链接来自工具自己的证据（Claude tool_use id == meta.toolUseId，omp `tasks[].name`，Codex `spawn_agent.task_name`），链接不上就当普通工具行，没有空卡片；刷新是 600ms 合并 + 运行期 1.5s 自重排的后台读取，渲染路径零 IO；不持久化、不记录对话内容
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	chatSubagentsSettle = 600 * time.Millisecond
	chatSubagentsTick   = 1500 * time.Millisecond
)

// ChatBridgeSubagent is one sub-agent card/roster row.
type ChatBridgeSubagent struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	// Type is the agent kind (Claude agentType, omp agent, Codex nickname).
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
	// Status is "running", "done" or "failed" (the parent's tool result said
	// so). "Stopped by user" is not derivable from the files and not claimed.
	Status    string `json:"status"`
	StartedMS int64  `json:"startedMs,omitempty"`
	UpdatedMS int64  `json:"updatedMs,omitempty"`
	ToolCalls int    `json:"toolCalls"`
	// Tail is the last tool call (the live line); Result the last assistant
	// text (clipped); Prompt the task the parent gave.
	Tail   string `json:"tail,omitempty"`
	Result string `json:"result,omitempty"`
	Prompt string `json:"prompt,omitempty"`
}

// chatSubagentView is one sub-agent with its summary, as cached on the Shell.
type chatSubagentView struct {
	Sub    history.Subagent
	Detail history.SubagentDetail // Messages always nil here
}

// subagentSummaryCache keys one summary by path+mtime+size so a quiet
// sub-agent costs nothing per tick.
var subagentSummaryCache = struct {
	sync.Mutex
	m map[string]cachedSubagentSummary
}{m: map[string]cachedSubagentSummary{}}

type cachedSubagentSummary struct {
	mtime  time.Time
	size   int64
	detail history.SubagentDetail
}

const subagentSummaryCacheLimit = 256

func subagentSummary(sub history.Subagent) history.SubagentDetail {
	info, err := os.Stat(sub.Path)
	if err != nil {
		return history.SubagentDetail{}
	}
	subagentSummaryCache.Lock()
	cached, ok := subagentSummaryCache.m[sub.Path]
	subagentSummaryCache.Unlock()
	if ok && cached.mtime.Equal(info.ModTime()) && cached.size == info.Size() {
		return cached.detail
	}
	detail, err := history.ReadSubagent(sub.Provider, sub.Path)
	if err != nil {
		return history.SubagentDetail{}
	}
	detail.Messages = nil
	subagentSummaryCache.Lock()
	if len(subagentSummaryCache.m) >= subagentSummaryCacheLimit {
		subagentSummaryCache.m = map[string]cachedSubagentSummary{}
	}
	subagentSummaryCache.m[sub.Path] = cachedSubagentSummary{info.ModTime(), info.Size(), detail}
	subagentSummaryCache.Unlock()
	return detail
}

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
		var views []chatSubagentView
		subs := history.SubagentsFor(provider, path)
		markBusy(subs, time.Now())
		for _, sub := range subs {
			views = append(views, chatSubagentView{Sub: sub, Detail: subagentSummary(sub)})
		}
		s.applyOnWindow(func() {
			s.chatSubagentsBusy = false
			if s.chatLivePath != path || s.chatLiveProvider != provider {
				return // rebound while reading
			}
			// A status is "running" for a window after the last write, so it
			// flips with the clock even when the files stay put.
			now := time.Now()
			active := false
			for _, view := range views {
				if view.Sub.Active(now) {
					active = true
					break
				}
			}
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

// linkedSubagents returns the sub-agents one tool call spawned, from the
// call's own evidence; empty means "render as an ordinary tool row". The
// second result overrides a sub-agent's type with what the call named (omp).
func linkedSubagents(tool *conversation.ConversationToolCall, views []chatSubagentView) (ids []string, types map[string]string) {
	if tool == nil || len(views) == 0 {
		return nil, nil
	}
	name := strings.ToLower(tool.Name)
	input := ""
	if tool.Input != nil {
		input = *tool.Input
	}
	add := func(view chatSubagentView) { ids = append(ids, view.Sub.ID) }
	switch {
	case strings.HasSuffix(name, "spawn_agent"):
		var args struct {
			TaskName string `json:"task_name"`
		}
		if json.Unmarshal([]byte(input), &args) != nil || args.TaskName == "" {
			return nil, nil
		}
		for _, view := range views {
			if view.Sub.Provider == history.AgentCodex && view.Sub.Name == args.TaskName {
				add(view)
			}
		}
	case name == "task" && views[0].Sub.Provider == history.AgentOMP:
		var args struct {
			Tasks []struct {
				Name  string `json:"name"`
				Agent string `json:"agent"`
			} `json:"tasks"`
		}
		if json.Unmarshal([]byte(input), &args) != nil {
			return nil, nil
		}
		for _, task := range args.Tasks {
			for _, view := range views {
				if view.Sub.Provider == history.AgentOMP && task.Name != "" && view.Sub.Name == task.Name {
					add(view)
					if task.Agent != "" {
						if types == nil {
							types = map[string]string{}
						}
						types[view.Sub.ID] = task.Agent
					}
				}
			}
		}
	case tool.ID != "" && (name == "agent" || name == "task"):
		for _, view := range views {
			if view.Sub.Provider == history.AgentClaudeCode && view.Sub.CallID == tool.ID {
				add(view)
			}
		}
	}
	return ids, types
}

// attachSubagents stamps SubagentIDs on the tool steps that spawned one and
// lists exactly those sub-agents (page order = first appearance).
func (state *ChatBridgeState) attachSubagents(views []chatSubagentView, turns []conversation.TimelineTurn, provider history.AgentID, now time.Time) {
	if len(views) == 0 || len(turns) != len(state.Turns) {
		return
	}
	byID := make(map[string]chatSubagentView, len(views))
	for _, view := range views {
		byID[view.Sub.ID] = view
	}
	seen := map[string]bool{}
	for ti, turn := range turns {
		if len(turn.Steps) != len(state.Turns[ti].Steps) {
			continue
		}
		for si, step := range turn.Steps {
			ids, types := linkedSubagents(step.Tool, views)
			if len(ids) == 0 {
				continue
			}
			state.Turns[ti].Steps[si].SubagentIDs = ids
			for _, id := range ids {
				if seen[id] {
					continue
				}
				seen[id] = true
				view := byID[id]
				state.Subagents = append(state.Subagents, bridgeSubagent(view, types[id], step.Tool, now))
			}
		}
	}
}

func bridgeSubagent(view chatSubagentView, typeOverride string, call *conversation.ConversationToolCall, now time.Time) ChatBridgeSubagent {
	sub, detail := view.Sub, view.Detail
	status := "done"
	switch {
	case call != nil && call.IsError:
		status = "failed"
	case sub.Active(now):
		status = "running"
	}
	kind := sub.Type
	if typeOverride != "" {
		kind = typeOverride
	}
	description := sub.Description
	if description == "" {
		description = detail.Description
		if sub.Provider == history.AgentOMP && sub.Name != "" && description != "" {
			description = sub.Name + " · " + description
		}
	}
	out := ChatBridgeSubagent{
		ID:          sub.ID,
		Provider:    string(sub.Provider),
		Type:        kind,
		Description: description,
		Status:      status,
		ToolCalls:   detail.ToolCalls,
		Result:      detail.Result,
		Prompt:      detail.Prompt,
	}
	// The transcript's own timestamps give the true run time; file times are
	// the fallback (a meta file's mtime is not the spawn instant).
	switch {
	case detail.FirstTs > 0:
		out.StartedMS = detail.FirstTs
	case !sub.Started.IsZero():
		out.StartedMS = sub.Started.UnixMilli()
	}
	if !sub.UpdatedAt.IsZero() {
		out.UpdatedMS = sub.UpdatedAt.UnixMilli()
	}
	if detail.TailName != "" {
		out.Tail = strings.TrimSpace(detail.TailName + " " + detail.TailInput)
	}
	return out
}

// ChatBridgeSubagentView is the drawer's read-only conversation.
type ChatBridgeSubagentView struct {
	Subagent ChatBridgeSubagent `json:"subagent"`
	Turns    []ChatBridgeTurn   `json:"turns"`
	// Error is a short reason the transcript cannot be shown ("" = fine).
	Error string `json:"error,omitempty"`
}

// OpenSubagent returns one sub-agent's own transcript for the drawer. The id
// must be one the current chat listed; the page never names a path.
func (b *ChatBridge) OpenSubagent(ctx context.Context, id string) (ChatBridgeSubagentView, error) {
	var (
		view  chatSubagentView
		found bool
		call  *conversation.ConversationToolCall
		kind  string
	)
	b.shell.callOnWindow(func() {
		for _, v := range b.shell.chatSubagents {
			if v.Sub.ID == id {
				view, found = v, true
			}
		}
		for _, turn := range b.shell.chatTurns {
			for _, step := range turn.Steps {
				if ids, types := linkedSubagents(step.Tool, []chatSubagentView{view}); len(ids) > 0 && found {
					call, kind = step.Tool, types[id]
				}
			}
		}
	})
	if !found {
		return ChatBridgeSubagentView{}, errors.New("sub-agent is not part of this conversation")
	}
	out := ChatBridgeSubagentView{Subagent: bridgeSubagent(view, kind, call, time.Now())}
	detail, err := history.ReadSubagent(view.Sub.Provider, view.Sub.Path)
	if err != nil {
		out.Error = "Can't show this sub-agent's transcript."
		if errors.Is(err, history.ErrSubagentTooLarge) {
			out.Error = "This sub-agent's transcript is too large to preview."
		}
		out.Turns = []ChatBridgeTurn{}
		return out, nil
	}
	items := conversation.ItemsFromTranscript(detail.Messages)
	out.Turns = bridgeTurns(conversation.DeriveTimeline(items), "", nil)
	return out, nil
}

// subagentHeadlines remembers the task line of providers whose listing
// carries none (omp, Codex): it never changes once the file has a user
// message, so a path is read for it once.
var subagentHeadlines = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// subagentHeadline is the sub-agent's description for list rows.
func subagentHeadline(sub history.Subagent) string {
	if sub.Description != "" || sub.Path == "" {
		return sub.Description
	}
	subagentHeadlines.Lock()
	cached, ok := subagentHeadlines.m[sub.Path]
	subagentHeadlines.Unlock()
	if ok {
		return cached
	}
	headline := subagentSummary(sub).Description
	if headline != "" && sub.Provider == history.AgentOMP && sub.Name != "" {
		// omp names its tasks (alpha, beta…); the assignment alone is a
		// numbered brief that begins the same way for every sibling.
		headline = sub.Name + " · " + headline
	}
	if headline != "" {
		subagentHeadlines.Lock()
		if len(subagentHeadlines.m) >= subagentSummaryCacheLimit {
			subagentHeadlines.m = map[string]string{}
		}
		subagentHeadlines.m[sub.Path] = headline
		subagentHeadlines.Unlock()
	}
	return headline
}

// markBusy sets Busy on the quiet sub-agents whose transcript ends on a tool
// call that has not returned (a long command writes nothing meanwhile). Only
// sub-agents inside the busy window are read, and a summary is cached per
// file state, so a settled list costs nothing.
func markBusy(subs []history.Subagent, now time.Time) {
	for i := range subs {
		if subs[i].NeedsBusyCheck(now) {
			subs[i].Busy = subagentSummary(subs[i]).PendingTool
		}
	}
}
