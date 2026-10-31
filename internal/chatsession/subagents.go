package chatsession

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 history.SubagentsFor/ReadSubagent（各 Provider 自己的子代理文件，只读）、conversation.ConversationToolCall（父工具调用的证据）
 * [OUTPUT]: Read（一次读取一个会话的全部子代理 View）、Linked（父工具调用 → 子代理）、Card（View → 页面卡片 SubagentCard）、Headline/MarkBusy/Summary（带缓存的只读摘要）
 * [POS]: 与 Shell/MyGo 无关的 Chat 子代理投影（2026-10-31）：桌面 ChatBridge 与将来的手机 Host 共用同一份读取与投影；链接只来自工具自己的证据（Claude tool_use id == meta.toolUseId，omp `tasks[].name`，Codex `spawn_agent.task_name`），链不上就当普通工具行；缓存按 path+mtime+size，不持久化、不记录对话内容
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// SubagentCard is one sub-agent card/roster row (the page's wire shape).
type SubagentCard struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	// Type is the agent kind (Claude agentType, omp agent, Codex nickname).
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
	// Status is "running", "done", "stopped" or "failed". "failed" is the parent's
	// tool result; "stopped" only the provider's own interruption marker in the
	// child's file (Claude "[Request interrupted by user]", Codex turn_aborted) —
	// omp has none, so it never reads "stopped".
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

// View is one sub-agent with its summary.
type View struct {
	Sub    history.Subagent
	Detail history.SubagentDetail // Messages always nil here
}

// summaryCache keys one summary by path+mtime+size so a quiet
// sub-agent costs nothing per tick.
var summaryCache = struct {
	sync.Mutex
	m map[string]cachedSummary
}{m: map[string]cachedSummary{}}

type cachedSummary struct {
	mtime  time.Time
	size   int64
	detail history.SubagentDetail
}

const summaryCacheLimit = 256

// Summary is one sub-agent's cached transcript summary (no messages).
func Summary(sub history.Subagent) history.SubagentDetail {
	info, err := os.Stat(sub.Path)
	if err != nil {
		return history.SubagentDetail{}
	}
	summaryCache.Lock()
	cached, ok := summaryCache.m[sub.Path]
	summaryCache.Unlock()
	if ok && cached.mtime.Equal(info.ModTime()) && cached.size == info.Size() {
		return cached.detail
	}
	detail, err := history.ReadSubagent(sub.Provider, sub.Path)
	if err != nil {
		return history.SubagentDetail{}
	}
	detail.Messages = nil
	summaryCache.Lock()
	if len(summaryCache.m) >= summaryCacheLimit {
		summaryCache.m = map[string]cachedSummary{}
	}
	summaryCache.m[sub.Path] = cachedSummary{info.ModTime(), info.Size(), detail}
	summaryCache.Unlock()
	return detail
}

// Linked returns the sub-agents one tool call spawned, from the
// call's own evidence; empty means "render as an ordinary tool row". The
// second result overrides a sub-agent's type with what the call named (omp).
func Linked(tool *conversation.ConversationToolCall, views []View) (ids []string, types map[string]string) {
	if tool == nil || len(views) == 0 {
		return nil, nil
	}
	name := strings.ToLower(tool.Name)
	input := ""
	if tool.Input != nil {
		input = *tool.Input
	}
	add := func(view View) { ids = append(ids, view.Sub.ID) }
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

// Card projects one linked sub-agent into the page's card shape.
func Card(view View, typeOverride string, call *conversation.ConversationToolCall, now time.Time) SubagentCard {
	sub, detail := view.Sub, view.Detail
	status := "done"
	switch {
	case call != nil && call.IsError:
		status = "failed"
	case sub.Stopped:
		status = "stopped"
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
	out := SubagentCard{
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

// Read lists the sub-agents of the conversation at path with their
// summaries, Busy-marked and headlined. Blocking file IO: call it off the UI
// lane.
func Read(provider history.AgentID, path string, now time.Time) []View {
	subs := history.SubagentsFor(provider, path)
	MarkBusy(subs, now)
	views := make([]View, 0, len(subs))
	for _, sub := range subs {
		views = append(views, View{Sub: sub, Detail: Summary(sub)})
	}
	return views
}

// AnyActive reports whether any listed sub-agent is running at now.
func AnyActive(views []View, now time.Time) bool {
	for _, view := range views {
		if view.Sub.Active(now) {
			return true
		}
	}
	return false
}

// headlines remembers the task line of providers whose listing
// carries none (omp, Codex): it never changes once the file has a user
// message, so a path is read for it once.
var headlines = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// Headline is the sub-agent's description for list rows.
func Headline(sub history.Subagent) string {
	if sub.Description != "" || sub.Path == "" {
		return sub.Description
	}
	headlines.Lock()
	cached, ok := headlines.m[sub.Path]
	headlines.Unlock()
	if ok {
		return cached
	}
	headline := Summary(sub).Description
	if headline != "" && sub.Provider == history.AgentOMP && sub.Name != "" {
		// omp names its tasks (alpha, beta…); the assignment alone is a
		// numbered brief that begins the same way for every sibling.
		headline = sub.Name + " · " + headline
	}
	if headline != "" {
		headlines.Lock()
		if len(headlines.m) >= summaryCacheLimit {
			headlines.m = map[string]string{}
		}
		headlines.m[sub.Path] = headline
		headlines.Unlock()
	}
	return headline
}

// MarkBusy sets Busy on the quiet sub-agents whose transcript ends on a tool
// call that has not returned (a long command writes nothing meanwhile). Only
// sub-agents inside the busy window are read, and a summary is cached per
// file state, so a settled list costs nothing.
func MarkBusy(subs []history.Subagent, now time.Time) {
	for i := range subs {
		if subs[i].NeedsBusyCheck(now) {
			subs[i].Busy = Summary(subs[i]).PendingTool
		}
	}
}
