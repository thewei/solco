package nativeui

import (
	"log/slog"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/thewei/solco/internal/agentstatus"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 agentstatus 唯一状态存储、MyGo Terminal OSC 回调、Herdr Pane/Agent 投影与 Shell.applyOnWindow 调度
 * [OUTPUT]: Shell.applyProgramStatus、applyProgramStatusControl、applyProgramStatusLifetime、retireShellIdleProgramStatuses、pruneProgramStatuses、overlayProgramStatuses、applyPaneStatus、agentOwnsProgramStatus
 * [POS]: OSC 7501 / Solco Hook 的终端与 Agent 状态边界：普通或嵌套非 Agent 程序状态只入终端记录，Herdr 仍掌管进程及 Pane 权威
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// applyProgramStatus is called on the UI lane by a guarded MyGo attachment
// callback. A generic command (brew, cargo, ...) may report OSC 7501
// without being an Agent: its status is recorded per terminal/pane but must
// never synthesize an Agent identity in the Herdr projection.
func (s *Shell) applyProgramStatus(paneID string, body []byte) {
	rep, ok := agentstatus.Parse(body)
	if !ok {
		return
	}
	agent := s.paneAgent(paneID)
	// A nested cargo/brew/deploy OSC report belongs to the terminal,
	// not the coding Agent attached to its Pane. Solco's Hook keeps the
	// Agent status even while the generic program updates its own record.
	if agent == nil || !agentOwnsProgramStatus(agent.Kind, rep.App) {
		s.statusStore.ApplyPassiveProgramReport(paneID, body, time.Now())
		return
	}
	status, ok := s.statusStore.ApplyProgramReport(paneID, body, time.Now())
	if !ok {
		return
	}
	slog.Debug("osc7501 status applied", "pane", paneID, "status", status)
	s.upsertProjectionAgent(paneID, func(agent *herdr.Agent) {
		agent.Status = status
	})
	s.applyPaneStatus(paneID, status)
}

func agentOwnsProgramStatus(agentKind, app string) bool {
	if app == "" { // App is optional in rev 0.4.
		return true
	}
	agentID, agentKnown := history.ParseAgentID(agentKind)
	appID, appKnown := history.ParseAgentID(app)
	return agentKnown && appKnown && agentID == appID
}

// applyProgramStatusControl receives a terminal-native prompt or full reset;
// it never treats a UI detach, resize, DECSTR or reconnect as process exit.
func (s *Shell) applyProgramStatusControl(paneID string, control terminal.ProgramStatusControl) {
	var kind agentstatus.Control
	switch control {
	case terminal.ProgramStatusPrompt:
		kind = agentstatus.ControlPrompt
	case terminal.ProgramStatusReset:
		kind = agentstatus.ControlReset
	default:
		return
	}
	s.applyProgramStatusLifetime(paneID, kind)
}

// applyProgramStatusLifetime is also used by Herdr's measured process-info
// loop when a foreground command has really exited back to its shell.
func (s *Shell) applyProgramStatusLifetime(paneID string, kind agentstatus.Control) {
	status, applied := s.statusStore.ApplyControl(paneID, kind)
	if !applied || s.paneAgent(paneID) == nil {
		return
	}
	s.upsertProjectionAgent(paneID, func(agent *herdr.Agent) {
		agent.Status = status
	})
	s.applyPaneStatus(paneID, status)
}

// retireShellIdleProgramStatuses consumes Herdr's measured shell-idle facts.
// No guessed timeout participates in process-exit cleanup.
func (s *Shell) retireShellIdleProgramStatuses(shellIdle map[string]bool) {
	for paneID := range shellIdle {
		if s.statusStore.HasTransient(paneID) {
			s.applyProgramStatusLifetime(paneID, agentstatus.ControlProcessExit)
		}
	}
}

// pruneProgramStatuses prunes only genuinely removed/replaced Herdr Panes.
// Rendering visibility does not participate in terminal record lifetime.
func (s *Shell) pruneProgramStatuses(next herdr.Projection) {
	byID := make(map[string]string, len(next.Panes))
	for _, pane := range next.Panes {
		byID[pane.ID] = pane.TerminalID
	}
	for _, pane := range s.projection.Panes {
		if nextTerminalID, ok := byID[pane.ID]; !ok || nextTerminalID != pane.TerminalID {
			s.statusStore.PaneClosed(pane.ID)
		}
	}
}

// overlayProgramStatuses prevents the next Herdr snapshot from undoing the
// Solco-local OSC > Solco direct Hook > Herdr spool arbitration. This is a
// disposable UI projection; no Herdr runtime mutation is involved.
func (s *Shell) overlayProgramStatuses(next *herdr.Projection) {
	agents := make(map[string]bool, len(next.Agents))
	for i := range next.Agents {
		a := &next.Agents[i]
		agents[a.PaneID] = true
		s.statusStore.Observe(a.PaneID, a.Status, a.StateChangeSeq)
		if word := s.statusStore.DisplayFor(a.PaneID, a.Status); word != "" {
			a.Status = word
		}
	}
	for i := range next.Panes {
		pane := &next.Panes[i]
		if agents[pane.ID] {
			if word := s.statusStore.DisplayFor(pane.ID, pane.AgentStatus); word != "" {
				pane.AgentStatus = word
			}
		}
	}
}

// applyPaneStatus applies the single effective status to the Agent's Pane
// projection and fans out interaction detection and Desktop/Mobile bridges.
func (s *Shell) applyPaneStatus(paneID, status string) {
	for i := range s.projection.Panes {
		if s.projection.Panes[i].ID == paneID {
			s.projection.Panes[i].AgentStatus = status
			break
		}
	}
	if status == "blocked" {
		s.dispatch(func() { s.detectPaneInteraction(paneID) })
	} else {
		s.interactionBroker.CancelForPane(paneID)
	}
	s.scheduleProjectionRefresh()
	s.publishChatBridge()
}
