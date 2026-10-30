/**
 * [INPUT]: 依赖 herdr.Projection/PaneProcessInfo、service_index.go 的 computeShellIdlePanes/buildSidebarActivity、agent_visual.go 的 Shell.paneAgent、brands.go 的 agentMarkForPane
 * [OUTPUT]: 对外提供 TestComputeShellIdlePanes、TestSidebarActivitySkipsDeadBindings、TestPaneAgentShellsIdleGate
 * [POS]: Agent 判活的回归测试：pane.process_info 派生的 shell 空闲测量、侧栏 rollup 与 paneAgent/agentMarkForPane 对死绑定的门控（Herdr 绑定不随 Agent 进程退出清除，2026-10-30 侧栏陈旧 omp 图标缺陷）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package nativeui

import (
	"testing"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/apphook"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

// TestComputeShellIdlePanes pins the liveness measurement over
// pane.process_info: a shell at its prompt lists only itself (the verified
// protocol-22 fact), a running agent holds the foreground group under its
// own pid (omp runs as bun.native — the process name never matches the
// agent kind), and unmeasured panes stay absent so callers treat absence
// as "assume alive".
func TestComputeShellIdlePanes(t *testing.T) {
	panes := []herdr.Pane{
		{ID: "prompt"},
		{ID: "agent"},
		{ID: "unmeasured"},
		{ID: "noshell"},
	}
	infos := map[string]*herdr.PaneProcessInfo{
		"prompt": {PaneID: "prompt", ShellPID: 88918, ForegroundProcesses: []herdr.PaneProcessInfoProcess{
			{PID: 88918, Name: "zsh"},
		}},
		"agent": {PaneID: "agent", ShellPID: 90410, ForegroundProcesses: []herdr.PaneProcessInfoProcess{
			{PID: 88599, Name: "bun.native"},
		}},
		"noshell": {PaneID: "noshell", ShellPID: 0, ForegroundProcesses: []herdr.PaneProcessInfoProcess{
			{PID: 5, Name: "zsh"},
		}},
	}

	idle := computeShellIdlePanes(panes, infos)
	if !idle["prompt"] {
		t.Fatalf("shell-prompt pane not measured idle: %v", idle)
	}
	if idle["agent"] {
		t.Fatalf("agent foreground pane measured idle: %v", idle)
	}
	if idle["unmeasured"] || idle["noshell"] {
		t.Fatalf("unmeasured panes must stay absent: %v", idle)
	}
}

// TestSidebarActivitySkipsDeadBindings pins the rollup gate: Herdr still
// carries the binding, but a pane measured at its shell prompt contributes
// no "agent running" activity, while an unmeasured binding keeps today's
// rollup.
func TestSidebarActivitySkipsDeadBindings(t *testing.T) {
	projection := herdr.Projection{
		Projects: []herdr.Project{{ID: "w1"}},
		Tabs:     []herdr.Tab{{ID: "t1", ProjectID: "w1"}},
		Panes:    []herdr.Pane{{ID: "p1", ProjectID: "w1", TabID: "t1"}},
		Agents:   []herdr.Agent{{PaneID: "p1", Kind: "omp", Status: "idle"}},
	}

	if activity := buildSidebarActivity(projection, sidebarServiceIndex{}, map[string]bool{"p1": true}); len(activity) != 0 {
		t.Fatalf("dead binding lit the rollup: %v", activity)
	}
	if activity := buildSidebarActivity(projection, sidebarServiceIndex{}, nil); !activity["w1"].On || activity["w1"].Tone != ToneMuted {
		t.Fatalf("unmeasured binding lost from rollup: %+v", activity["w1"])
	}
}

// TestPaneAgentShellsIdleGate pins the single pane-Agent authority: a
// binding on a pane measured at its bare shell prompt reports no agent and
// the row falls back to the terminal glyph, while an unmeasured binding
// stays visible.
func TestPaneAgentShellsIdleGate(t *testing.T) {
	s := NewShell()
	s.projection = herdr.Projection{
		Panes:  []herdr.Pane{{ID: "p1", TerminalID: "term-1", ProjectID: "w1", TabID: "t1"}},
		Agents: []herdr.Agent{{TerminalID: "term-1", PaneID: "p1", Kind: "omp", Status: "idle"}},
	}

	if s.paneAgent("p1") == nil {
		t.Fatalf("unmeasured binding must stay visible")
	}

	s.paneShellIdle = map[string]bool{"p1": true}
	if s.paneAgent("p1") != nil {
		t.Fatalf("binding on a shell-idle pane must gate away")
	}
	if mark, isAgent := s.agentMarkForPane("p1", true); isAgent || mark.svg != iconTerminal {
		t.Fatalf("shell-idle pane mark = %+v isAgent=%v, want the plain terminal glyph", mark, isAgent)
	}

	// An agent report is proof of life: the stale idle measurement clears.
	s.handleAppHookStatus(apphook.StatusEvent{PaneID: "p1", Status: "working"})
	if _, idle := s.paneShellIdle["p1"]; idle {
		t.Fatalf("agent report must clear the shell-idle measurement")
	}
	if s.paneAgent("p1") == nil {
		t.Fatalf("relaunched agent must be visible again")
	}
}

// TestReconcileWorkbenchSkipsDeadBindings pins the Agents-section gate:
// the reconciled directory drops a binding whose pane is measured at its
// bare shell prompt, keeps an unmeasured live agent, and prunes the dead
// key from the marker store.
func TestReconcileWorkbenchSkipsDeadBindings(t *testing.T) {
	s := NewShell()
	s.activeInstance = "default"
	s.projection = herdr.Projection{
		Agents: []herdr.Agent{
			{TerminalID: "term-dead", PaneID: "p1", Kind: "omp", Status: "idle"},
			{TerminalID: "term-live", PaneID: "p2", Kind: "omp", Status: "working"},
		},
	}
	s.paneShellIdle = map[string]bool{"p1": true}

	s.reconcileWorkbench()
	cards := s.workbenchCards()
	if len(cards) != 1 || cards[0].PaneID != "p2" {
		t.Fatalf("directory after reconcile = %+v, want only the live pane's card", cards)
	}

	// Unmeasured bindings keep today's behavior until a scan measures them.
	s.paneShellIdle = nil
	s.reconcileWorkbench()
	if cards := s.workbenchCards(); len(cards) != 2 {
		t.Fatalf("unmeasured bindings lost: %+v", cards)
	}
}

// TestAgentCardTitleForPane pins the title handoff: a live agent's pane
// speaks the reconciled card title (the Agents section's), while a dead
// binding reports false so the row keeps the pane's own label.
func TestAgentCardTitleForPane(t *testing.T) {
	s := NewShell()
	s.workbench.directory = agent.NewAgentDirectory()
	s.workbench.directory.Replace([]agent.AgentCardModel{{
		Key:    agent.AgentKey{InstanceID: "default", TerminalID: "term-1"},
		PaneID: "p1", Title: "Scout", Provider: history.AgentOMP,
	}})
	s.projection = herdr.Projection{
		Agents: []herdr.Agent{{TerminalID: "term-1", PaneID: "p1", Kind: "omp", Status: "working"}},
	}

	if title, ok := s.agentCardTitleForPane("p1"); !ok || title != "Scout" {
		t.Fatalf("live agent title = %q %v, want Scout", title, ok)
	}
	s.paneShellIdle = map[string]bool{"p1": true}
	if title, ok := s.agentCardTitleForPane("p1"); ok {
		t.Fatalf("dead binding title = %q %v, want false", title, ok)
	}
}
