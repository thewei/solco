package nativeui

import (
	"reflect"
	"time"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 history.ClaudeSubagents（只读读会话文件）、Herdr 投影里的 agent_session 身份、workbench 的 reconcile
 * [OUTPUT]: 对外提供 Shell.refreshSubagents（节流的后台读取）、subagentsChanged（缓存是否变化的纯函数）
 * [POS]: 侧栏 Agents 子代理树的数据调度（2026-10-30）：Herdr 没有父子信息，只对 path 身份的 Claude Code 会话读 `<会话>/subagents/`；读取在后台、渲染只读 map，没有渲染期 IO，也没有常驻定时器——由投影更新与侧栏重建触发，4 秒节流
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// subagentsThrottle spaces the background reads; a sub-agent's liveness
// window (history.SubagentActiveWindow) is longer than it.
const subagentsThrottle = 4 * time.Second

// refreshSubagents re-reads the sub-agent listing of every live Claude Code
// agent. It only spawns the read — the render path never touches the disk.
func (s *Shell) refreshSubagents() {
	if s.win == nil || s.subagentsBusy || time.Since(s.subagentsAt) < subagentsThrottle {
		return
	}
	type target struct {
		key  agent.AgentKey
		path string
	}
	var targets []target
	for _, card := range s.workbenchCards() {
		runtimeAgent, ok := s.paneRuntimeAgent(card.PaneID)
		if !ok || runtimeAgent.AgentSession == nil || runtimeAgent.AgentSession.Kind != "path" {
			continue
		}
		if provider, ok := history.ParseAgentID(runtimeAgent.AgentSession.Agent); !ok || provider != history.AgentClaudeCode {
			continue
		}
		targets = append(targets, target{card.Key, runtimeAgent.AgentSession.Value})
	}
	s.subagentsAt = time.Now()
	if len(targets) == 0 {
		if len(s.agentSubagents) > 0 {
			s.agentSubagents = nil
			s.reconcileWorkbench()
		}
		return
	}
	s.subagentsBusy = true
	go func() {
		found := make(map[agent.AgentKey][]history.Subagent, len(targets))
		for _, t := range targets {
			if subs := history.ClaudeSubagents(t.path); len(subs) > 0 {
				found[t.key] = subs
			}
		}
		s.applyOnWindow(func() {
			s.subagentsBusy = false
			if subagentsChanged(s.agentSubagents, found) {
				s.agentSubagents = found
				s.reconcileWorkbench()
			}
		})
	}()
}

// subagentsChanged compares two cached listings.
func subagentsChanged(old, next map[agent.AgentKey][]history.Subagent) bool {
	if len(old) == 0 && len(next) == 0 {
		return false
	}
	return !reflect.DeepEqual(old, next)
}
