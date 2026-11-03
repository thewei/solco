package nativeui

import (
	"context"
	"time"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/automations"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/remotehost"
)

/**
 * [INPUT]: 依赖 automationsState 的 list 缓存（窗口线程写）、automationRunUnread、internal/remotehost 的 Automations 线格式
 * [OUTPUT]: 对外提供 Shell.syncRemoteAutomations（窗口线程在每次 list 变化后刷新只读快照）、Shell.hostAutomations（Host goroutine 取手机 Automations 列表）、remoteAutomationsOf（纯映射）
 * [POS]: 手机 Automations 只读页与桌面同源：Host 线程只读一个原子快照，从不触 UI 状态；插件门关闭时快照清空，手机端能力位随之消失；
 *        线格式不含提示词、理由、错误文本、仓库路径，时间为 epoch 毫秒；快照的新鲜度就是桌面列表的新鲜度（有 run 在跑 8 s，空闲 60 s）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// syncRemoteAutomations refreshes what the phone can read: the desktop's
// current list when the gate is open, nothing when it is closed.
func (s *Shell) syncRemoteAutomations() {
	if s.automationsAvailable() {
		s.automations.target.Store(&automationsTarget{root: s.automations.avail.Root, session: herdr.ParseRef(s.activeInstance).Session, agents: s.remoteAutomationAgents()})
	} else {
		s.automations.target.Store(nil)
	}
	if !s.automationsAvailable() || s.automations.list.loadedAt.IsZero() || s.automations.list.failed {
		if !s.automationsAvailable() {
			s.automations.remote.Store(nil)
		}
		return
	}
	state := s.automations.list.result.State
	s.automations.remoteState.Store(&state)
	out := remoteAutomationsOf(s.automations.list.result, s.remoteAutomationAgents())
	s.automations.remote.Store(&out)
}

// hostAutomations serves GET /automations from the snapshot; ok is false while
// the plugin gate is closed or the first read has not landed.
func (s *Shell) hostAutomations(context.Context) (remotehost.Automations, bool) {
	snapshot := s.automations.remote.Load()
	if snapshot == nil {
		return remotehost.Automations{}, false
	}
	return *snapshot, true
}

// remoteAutomationsOf maps the plugin's list to the wire.
func remoteAutomationsOf(result automations.ListResult, agents []remotehost.AutomationAgent) remotehost.Automations {
	out := remotehost.Automations{Scheduler: result.Scheduler != 0, DraftPrompt: automationsAskText, Agents: agents, Automations: make([]remotehost.AutomationItem, 0, len(result.Automations))}
	for _, a := range result.Automations {
		item := remotehost.AutomationItem{
			Name: a.Name, State: "active", Schedule: a.ScheduleText, Agent: a.Agent,
			Headless: a.Headless, Unread: automationRunUnread(a),
		}
		switch {
		case a.Draft:
			item.State = "draft"
		case a.Disabled:
			item.State = "paused"
		}
		if item.Schedule == "" {
			item.Schedule = a.Cron
		}
		if t, err := time.Parse(time.RFC3339, a.NextRun); err == nil && item.State == "active" {
			item.NextRunMs = t.UnixMilli()
		}
		if r := a.LastRun; r != nil {
			run := &remotehost.AutomationRun{ID: r.RunID, Status: r.Status, DurationSec: r.DurationSeconds}
			if t, err := time.Parse(time.RFC3339, r.StartedAt); err == nil {
				run.AtMs = t.UnixMilli()
			}
			item.LastRun = run
		}
		out.Automations = append(out.Automations, item)
	}
	return out
}

// remoteAutomationAgents is the form's agent choices: every launchable agent
// of this Mac, under the id the plugin takes (claude, not claude-code). Window
// thread only — it reads the launch kinds.
func (s *Shell) remoteAutomationAgents() []remotehost.AutomationAgent {
	var out []remotehost.AutomationAgent
	for _, kind := range s.automationAgentKinds() {
		out = append(out, remotehost.AutomationAgent{ID: agent.HerdrAgentKind(history.AgentID(kind.ID)), Label: kind.Label})
	}
	return out
}
