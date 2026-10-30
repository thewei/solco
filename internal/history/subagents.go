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
 * [INPUT]: 一个 Claude Code 会话 transcript 的路径（Herdr agent_session.value 的 path 身份）
 * [OUTPUT]: Subagent、ClaudeSubagents（该会话派出的子代理清单：类型/说明/最近写入时间）、Subagent.Active（最近写入够新）
 * [POS]: 侧栏 Agents 树的只读数据源（2026-10-30 用户拍板，design: designs/agent-surfaces）：Claude Code 把子代理落在 `<会话>/subagents/agent-<id>.jsonl` + `.meta.json`，Herdr 没有父子关系，所以只读这两个文件；布局是 Claude Code 的未文档化实现细节——变了就读不到、静默退化为平铺，不影响其他功能。只读、不落盘、不解析对话内容（只读 meta 的三个字段与文件 mtime）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// SubagentActiveWindow is how recent a sub-agent's last write must be for it
// to count as running: a working sub-agent appends to its transcript every
// few seconds.
const SubagentActiveWindow = 20 * time.Second

// maxSubagents bounds one session's listing (a long session can spawn
// hundreds); the most recent ones are kept.
const maxSubagents = 24

// Subagent is one agent a Claude Code session spawned.
type Subagent struct {
	ID          string
	Type        string // "Explore", "general-purpose"
	Description string // the task the parent gave it
	Started     time.Time
	UpdatedAt   time.Time
}

// Active reports a sub-agent still writing.
func (s Subagent) Active(now time.Time) bool {
	return !s.UpdatedAt.IsZero() && now.Sub(s.UpdatedAt) < SubagentActiveWindow
}

type subagentMeta struct {
	AgentType   string `json:"agentType"`
	Description string `json:"description"`
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
			Type:        strings.TrimSpace(meta.AgentType),
			Description: strings.TrimSpace(meta.Description),
			Started:     info.ModTime(),
			UpdatedAt:   info.ModTime(),
		}
		if transcript, err := os.Stat(filepath.Join(dir, "agent-"+id+".jsonl")); err == nil {
			sub.UpdatedAt = transcript.ModTime()
		}
		list = append(list, sub)
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Started.Before(list[j].Started) })
	if len(list) > maxSubagents {
		list = list[len(list)-maxSubagents:]
	}
	return list
}
