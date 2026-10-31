package nativeui

import (
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 history.SubagentsFor（只读读各 Provider 自己的会话文件）、Herdr 投影里的 agent_session 身份、workbench 的 reconcile
 * [OUTPUT]: 对外提供 Shell.refreshSubagents（节流的后台读取）、subagentsChanged（缓存是否变化的纯函数）
 * [POS]: 侧栏 Agents 子代理树的数据调度（2026-10-30）：Herdr 没有父子信息，三家（Claude Code / omp / Codex，pi 无）各按自己的落盘布局读，身份为 id 时在后台解析到文件；读取在后台、渲染只读 map，没有渲染期 IO，也没有常驻定时器——由投影更新与侧栏重建触发，4 秒节流
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
		key      agent.AgentKey
		provider history.AgentID
		id       *herdr.AgentSessionIdentity
	}
	var targets []target
	for _, card := range s.workbenchCards() {
		runtimeAgent, ok := s.paneRuntimeAgent(card.PaneID)
		if !ok || runtimeAgent.AgentSession == nil {
			continue
		}
		provider, ok := history.ParseAgentID(runtimeAgent.AgentSession.Agent)
		if !ok || !history.HasSubagents(provider) {
			continue
		}
		targets = append(targets, target{card.Key, provider, runtimeAgent.AgentSession})
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
		home, _ := os.UserHomeDir()
		found := make(map[agent.AgentKey][]history.Subagent, len(targets))
		for _, t := range targets {
			path, err := resolveSubagentSource(home, t.provider, t.id)
			if err != nil {
				continue
			}
			if subs := history.SubagentsFor(t.provider, path); len(subs) > 0 {
				markBusy(subs, time.Now())
				for i := range subs {
					subs[i].Description = subagentHeadline(subs[i])
				}
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

// subagentSources remembers where an id identity's session file lives: the
// resolution scans the provider's whole session tree, and a session never
// moves, so only the first read pays for it.
var subagentSources = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

func resolveSubagentSource(home string, provider history.AgentID, id *herdr.AgentSessionIdentity) (string, error) {
	if id.Kind == "path" {
		return history.ResolveLiveSource(home, provider, id.Kind, id.Source, id.Value)
	}
	key := string(provider) + "|" + id.Kind + "|" + id.Source + "|" + id.Value
	subagentSources.Lock()
	path, ok := subagentSources.m[key]
	subagentSources.Unlock()
	if ok {
		return path, nil
	}
	path, err := history.ResolveLiveSource(home, provider, id.Kind, id.Source, id.Value)
	if err != nil {
		return "", err
	}
	subagentSources.Lock()
	if len(subagentSources.m) >= 256 {
		subagentSources.m = map[string]string{}
	}
	subagentSources.m[key] = path
	subagentSources.Unlock()
	return path, nil
}
