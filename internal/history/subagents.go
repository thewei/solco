package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

/**
 * [INPUT]: 一个会话 transcript 的路径（Herdr agent_session 身份解析出的 path）与 Provider
 * [OUTPUT]: Subagent、SubagentsFor（该会话派出的子代理清单：类型/说明/子 transcript 路径/父调用链接键/最近写入时间）、ClaudeSubagents、Subagent.Active
 * [POS]: 侧栏 Agents 树、Header 弹层、Chat 子代理卡片与抽屉的只读数据源（2026-10-30 用户拍板，2026-10-31 扩到 omp/Codex/pi/Antigravity，design: designs/agent-surfaces、designs/subagents）：Herdr 没有父子关系，只读各 Provider 自己落盘的文件——Claude `<会话>/subagents/agent-*.jsonl|.meta.json`、omp `<会话目录>/<名>.jsonl`（头部 parentSession）、Codex 独立 rollout（session_meta.source.subagent.thread_spawn）、Antigravity `conversation_summaries.db`（parent_conversation_id）；只读、不落盘、清单本身不解析对话内容
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// SubagentActiveWindow is how recent a sub-agent's last write must be for it
// to count as running: a working sub-agent appends to its transcript every
// few seconds.
const SubagentActiveWindow = 20 * time.Second

// maxSubagents bounds one session's listing (a long session can spawn
// hundreds); the most recent ones are kept.
const maxSubagents = 24

// Subagent is one agent a session spawned.
type Subagent struct {
	ID          string
	Provider    AgentID
	Type        string // Claude "Explore"; omp "task"; Codex the agent's nickname
	Description string // the task the parent gave it ("" until the transcript is read)
	Started     time.Time
	UpdatedAt   time.Time
	// Path is the sub-agent's own transcript (drill-down reads it).
	Path string
	// CallID links it to the parent's tool call: Claude's tool_use id.
	CallID string
	// Name links it where the parent names it in the call's input: omp
	// `tasks[].name`, Codex `spawn_agent.task_name`.
	Name string
	// CallRun links it where the parent's call result names the run: pi's
	// `Async: … [runId]` / `Async workflow [runId]`, recovered from the
	// parent's own subagent-notify messages ("" when none names it yet).
	CallRun string
	Depth   int
	// Ended is the provider's own end marker (omp's session_exit); without
	// one, liveness is the recent-write window.
	Ended bool
	// Stopped is the provider's own interruption marker (Claude's
	// "[Request interrupted by user]" closing the child file, Codex's
	// turn_aborted): the run was cut short, not finished. Implies Ended.
	Stopped bool
	// Busy is set by the caller from SubagentDetail.PendingTool: the last tool
	// call has no result yet, so a quiet file does not mean finished.
	Busy bool
}

// SubagentBusyWindow bounds how long a pending tool call keeps a quiet
// sub-agent "running" (a `sleep 20` or a build writes nothing meanwhile).
const SubagentBusyWindow = 10 * time.Minute

// Active reports a sub-agent still working: it wrote within the active
// window, or it is waiting on a tool call that has not returned (Busy) and
// wrote within the busy window.
func (s Subagent) Active(now time.Time) bool {
	if s.Ended || s.UpdatedAt.IsZero() {
		return false
	}
	age := now.Sub(s.UpdatedAt)
	return age < SubagentActiveWindow || (s.Busy && age < SubagentBusyWindow)
}

// NeedsBusyCheck reports a sub-agent whose quiet time makes the pending-tool
// check worth a read: not ended, past the active window, inside the busy one.
func (s Subagent) NeedsBusyCheck(now time.Time) bool {
	if s.Ended || s.UpdatedAt.IsZero() {
		return false
	}
	age := now.Sub(s.UpdatedAt)
	return age >= SubagentActiveWindow && age < SubagentBusyWindow
}

// SubagentsFor lists the sub-agents of the session whose transcript is at
// sessionPath, oldest first. Providers without a sub-agent layout, a session
// without any, or a layout it does not recognise yield nil.
func SubagentsFor(provider AgentID, sessionPath string) []Subagent {
	switch provider {
	case AgentClaudeCode:
		return ClaudeSubagents(sessionPath)
	case AgentOMP:
		return ompSubagents(sessionPath)
	case AgentCodex:
		return codexSubagents(sessionPath)
	case AgentPi:
		return piSubagents(sessionPath)
	case AgentAntigravity:
		return agySubagents(sessionPath)
	default:
		return nil
	}
}

// finishSubagents orders a listing by spawn time and keeps the newest ones.
func finishSubagents(list []Subagent) []Subagent {
	sort.SliceStable(list, func(i, j int) bool { return list[i].Started.Before(list[j].Started) })
	if len(list) > maxSubagents {
		list = list[len(list)-maxSubagents:]
	}
	return list
}

// claudeInterruptMarker is the user row Claude Code appends to a sub-agent's
// file when it is stopped (TaskStop or Esc); claudeInterruptTail bounds the
// bytes read to find the file's last line.
const (
	claudeInterruptMarker = "[Request interrupted by user"
	claudeInterruptTail   = 8 << 10
)

type subagentMeta struct {
	AgentType   string `json:"agentType"`
	Description string `json:"description"`
	ToolUseID   string `json:"toolUseId"`
	SpawnDepth  int    `json:"spawnDepth"`
}

// ClaudeSubagents lists the sub-agents of the Claude Code session whose
// transcript is at sessionPath, oldest first. A session without any (or a
// layout it does not recognise) yields nil.
func ClaudeSubagents(sessionPath string) []Subagent {
	if !strings.HasSuffix(sessionPath, ".jsonl") {
		return nil
	}
	dir := filepath.Join(strings.TrimSuffix(sessionPath, ".jsonl"), "subagents")
	metas, err := filepath.Glob(filepath.Join(dir, "agent-*.meta.json"))
	if err != nil || len(metas) == 0 {
		return nil
	}
	list := make([]Subagent, 0, len(metas))
	for _, metaPath := range metas {
		info, err := os.Stat(metaPath)
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(metaPath)
		if err != nil || len(raw) > 64<<10 {
			continue
		}
		var meta subagentMeta
		if json.Unmarshal(raw, &meta) != nil {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(metaPath), "agent-"), ".meta.json")
		sub := Subagent{
			ID:          id,
			Provider:    AgentClaudeCode,
			Type:        strings.TrimSpace(meta.AgentType),
			Description: strings.TrimSpace(meta.Description),
			Started:     info.ModTime(),
			UpdatedAt:   info.ModTime(),
			CallID:      strings.TrimSpace(meta.ToolUseID),
			Depth:       meta.SpawnDepth,
		}
		transcript := filepath.Join(dir, "agent-"+id+".jsonl")
		if info, err := os.Stat(transcript); err == nil {
			sub.UpdatedAt = info.ModTime()
			sub.Path = transcript
			if lastLineContains(transcript, info.Size(), claudeInterruptTail, claudeInterruptMarker) {
				sub.Stopped, sub.Ended = true, true
			}
		}
		list = append(list, sub)
	}
	return finishSubagents(list)
}

// HasSubagents reports whether a provider has a sub-agent layout Solco reads
// (every Chat provider except Cursor Agent).
func HasSubagents(provider AgentID) bool {
	switch provider {
	case AgentClaudeCode, AgentOMP, AgentCodex, AgentPi, AgentAntigravity:
		return true
	}
	return false
}
