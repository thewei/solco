package nativeui

/**
 * [INPUT]: 依赖测试 Shell、Herdr Projection、OSC 7501/Hook 仲裁与前台进程退出语义
 * [OUTPUT]: 证明通用程序不伪造 Agent、Solco 优先、快照不覆盖、隐藏 Pane 保留记录及退出仅清瞬态
 * [POS]: nativeui program_status.go 与 Herdr/Hook 事件接线的确定性回归测试
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/thewei/solco/internal/agentstatus"
	"github.com/thewei/solco/internal/apphook"
	"github.com/thewei/solco/internal/herdr"
)

// Protocol states are not agent identities: cargo, brew, deploy etc. may
// legitimately report 7501 without running any coding agent.
func TestOSC7501GenericProgramDoesNotSynthesizeAgent(t *testing.T) {
	s := NewShell()
	s.projection = herdr.Projection{Panes: []herdr.Pane{{ID: "p", TerminalID: "term"}}}
	s.applyProgramStatus("p", []byte("state=blocked:app=cargo"))
	if len(s.projection.Agents) != 0 {
		t.Fatalf("generic command created %d synthetic agents", len(s.projection.Agents))
	}
	if report, ok := s.statusStore.DisplayTerminal("p"); !ok || report.State != agentstatus.StateBlocked {
		t.Fatalf("generic terminal record lost: %+v ok=%v", report, ok)
	}
	if got := s.projection.Panes[0].AgentStatus; got != "" {
		t.Fatalf("generic command leaked into AgentStatus: %q", got)
	}
}

func TestOSC7501NestedGenericCommandDoesNotOverrideAgentHook(t *testing.T) {
	s := NewShell()
	s.projection = herdr.Projection{
		Panes:  []herdr.Pane{{ID: "p", TerminalID: "term"}},
		Agents: []herdr.Agent{{PaneID: "p", TerminalID: "term", Kind: "claude", Status: "idle"}},
	}
	s.handleAppHookStatus(apphook.StatusEvent{PaneID: "p", Agent: "claude", Status: "working"})
	s.applyProgramStatus("p", []byte("state=blocked:app=cargo"))
	if got := s.projection.Agents[0].Status; got != "working" {
		t.Fatalf("nested cargo stole Agent status: %q", got)
	}
	if report, ok := s.statusStore.DisplayTerminal("p"); !ok || report.App != "cargo" || report.State != agentstatus.StateBlocked {
		t.Fatalf("terminal did not preserve generic report: %+v ok=%v", report, ok)
	}
	s.applyProgramStatus("p", []byte("state=blocked:app=claude-code"))
	if got := s.projection.Agents[0].Status; got != "blocked" {
		t.Fatalf("Agent native status did not lead: %q", got)
	}
}

func TestOSC7501SolcoHookOutranksHerdrSpoolAndSnapshot(t *testing.T) {
	s := NewShell()
	s.projection = herdr.Projection{
		Panes:  []herdr.Pane{{ID: "p", TerminalID: "term"}},
		Agents: []herdr.Agent{{PaneID: "p", TerminalID: "term", Kind: "claude", Status: "idle"}},
	}
	s.handleHerdrHookStatus("p", "blocked")
	if got := s.projection.Agents[0].Status; got != "blocked" {
		t.Fatalf("Herdr fallback did not apply: %q", got)
	}
	s.handleAppHookStatus(apphook.StatusEvent{PaneID: "p", Agent: "claude", Status: "working", SessionID: "session-1"})
	s.handleHerdrHookStatus("p", "idle")
	if got := s.projection.Agents[0].Status; got != "working" {
		t.Fatalf("Herdr overrode direct Solco Hook: %q", got)
	}
	s.applyProgramStatus("p", []byte("state=blocked:id=task"))
	s.handleHerdrHookStatus("p", "done")
	s.handleAppHookStatus(apphook.StatusEvent{PaneID: "p", Agent: "claude", Status: "idle", SessionID: "session-1"})
	if got := s.projection.Agents[0].Status; got != "blocked" {
		t.Fatalf("Hook overrode in-flight protocol status: %q", got)
	}
	// A true shell prompt is a native lifecycle boundary. The Solco Hook
	// that previously reported idle is now the leading fallback source.
	s.applyProgramStatusControl("p", terminal.ProgramStatusPrompt)
	if got := s.projection.Agents[0].Status; got != "idle" {
		t.Fatalf("prompt failed to restore Solco Hook: %q", got)
	}

	next := herdr.Projection{
		Panes:  []herdr.Pane{{ID: "p", TerminalID: "term", AgentStatus: "blocked"}},
		Agents: []herdr.Agent{{PaneID: "p", TerminalID: "term", Kind: "claude", Status: "blocked"}},
	}
	s.overlayProgramStatuses(&next)
	if next.Agents[0].Status != "idle" || next.Panes[0].AgentStatus != "idle" {
		t.Fatalf("Herdr snapshot erased Solco direct Hook truth: agent=%q pane=%q", next.Agents[0].Status, next.Panes[0].AgentStatus)
	}
}

func TestOSC7501VisibilityAndTerminalIdentity(t *testing.T) {
	s := NewShell()
	s.projection = herdr.Projection{
		Panes: []herdr.Pane{
			{ID: "one", TerminalID: "term-1"},
			{ID: "two", TerminalID: "term-2"},
		},
	}
	s.statusStore.ApplyProgramReport("one", []byte("state=done:app=brew"), time.Now())
	// Hiding this pane is a presentation change; it is still a live Herdr
	// pane, so keep its record even if no Terminal widget is attached.
	s.pruneProgramStatuses(s.projection)
	if got := s.statusStore.Display("one"); got != "done" {
		t.Fatalf("visibility dropped completed record: %q", got)
	}
	s.pruneProgramStatuses(herdr.Projection{Panes: []herdr.Pane{{ID: "one", TerminalID: "new-pty"}}})
	if got := s.statusStore.Display("one"); got != "" {
		t.Fatalf("replaced terminal retained old record: %q", got)
	}
	s.statusStore.ApplyProgramReport("two", []byte("state=working"), time.Now())
	s.statusStore.ApplyControl("two", agentstatus.ControlReset)
	if got := s.statusStore.Display("two"); got != "" {
		t.Fatalf("full reset retained record: %q", got)
	}
}

func TestOSC7501HerdrForegroundExitDropsOnlyTransient(t *testing.T) {
	s := NewShell()
	s.projection = herdr.Projection{
		Panes:  []herdr.Pane{{ID: "p", TerminalID: "t"}},
		Agents: []herdr.Agent{{PaneID: "p", TerminalID: "t", Kind: "claude", Status: "working"}},
	}
	s.applyProgramStatus("p", []byte("state=done:id=completed"))
	s.applyProgramStatus("p", []byte("state=blocked:id=waiting"))
	s.retireShellIdleProgramStatuses(map[string]bool{"p": true})
	if got := s.statusStore.Display("p"); got != "done" {
		t.Fatalf("process exit must retain completed task, got %q", got)
	}
	if got := s.projection.Agents[0].Status; got != "done" {
		t.Fatalf("Agent projection not updated after authoritative exit: %q", got)
	}
}
