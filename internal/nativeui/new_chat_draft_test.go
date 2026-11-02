package nativeui

import (
	"context"
	"errors"
	"testing"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/herdr"
)

/**
 * [INPUT]: Shell、newTabStarter 假实现、agent.AgentDirectory 卡片
 * [OUTPUT]: 空白会话草稿的回归：待写态、发送即渲染、成功后同 pane 交接回显、失败保留草稿并带原因、选择切换丢弃草稿
 * [POS]: 2026-11-02 新建 Agent Tab 的空白 Chat（design: designs/mobile-projection 议题 7）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

type paneStarter struct {
	specs  []app.SessionSpec
	result app.SessionResult
	err    error
}

func (p *paneStarter) Kinds() []app.SessionKind { return testKinds }
func (p *paneStarter) StartSession(_ context.Context, _ string, spec app.SessionSpec) (app.SessionResult, error) {
	p.specs = append(p.specs, spec)
	return p.result, p.err
}

func draftShell(starter *paneStarter) *Shell {
	s := NewShell()
	s.loading = false
	s.activeInstance = "main"
	s.newTabStarter = starter
	s.runtime = nil // keep the projection the test built
	s.projection = herdr.Projection{
		Agents: []herdr.Agent{{TerminalID: "term-new", PaneID: "p-new", Status: "idle"}},
	}
	s.workbench.directory = agent.NewAgentDirectory()
	s.workbench.directory.Replace([]agent.AgentCardModel{{
		Key:    agent.AgentKey{InstanceID: "main", TerminalID: "term-new"},
		PaneID: "p-new",
	}})
	return s
}

var draftKind = app.SessionKind{ID: "claude-code", Label: "Claude Code"}

func TestDraftStartsInTheInvitationState(t *testing.T) {
	s := draftShell(&paneStarter{})
	s.beginNewChatDraft(herdr.Project{ID: "w1", Label: "solco", CWD: "/tmp/solco"}, draftKind)
	state := s.chatBridgeState()
	if !state.Waiting || state.EchoPending || state.Provider != "Claude Code" || state.Disposition != "ready" || len(state.Turns) != 0 {
		t.Fatalf("composing state = %+v", state)
	}
}

func TestDraftSendRendersAtOnceThenHandsOverToTheRealPane(t *testing.T) {
	starter := &paneStarter{result: app.SessionResult{PaneID: "p-new"}}
	s := draftShell(starter)
	s.beginNewChatDraft(herdr.Project{ID: "w1", Label: "solco", CWD: "/tmp/solco"}, draftKind)
	state := s.sendChatText("  fix the tests ")
	if len(starter.specs) != 1 || starter.specs[0].Prompt != "fix the tests" || starter.specs[0].WorkspaceID != "w1" || string(starter.specs[0].Agent) != "claude-code" {
		t.Fatalf("spec = %+v", starter.specs)
	}
	if state.Waiting || !state.EchoPending {
		t.Fatalf("sent state = %+v", state)
	}
	if s.newChatDraft != nil || s.selectedPaneID != "p-new" {
		t.Fatalf("hand-over: draft=%v selected=%q", s.newChatDraft, s.selectedPaneID)
	}
	if s.chatPendingEcho == nil || s.chatPendingEcho.PaneID != "p-new" || s.chatPendingEcho.Text != "fix the tests" {
		t.Fatalf("the real pane lost the echo: %#v", s.chatPendingEcho)
	}
	if !s.chatBridgeState().EchoPending {
		t.Fatal("the page must keep the sent message until the provider row arrives")
	}
}

func TestDraftLaunchFailureKeepsTheDraftWithTheReason(t *testing.T) {
	starter := &paneStarter{err: errors.New("launch_failed: boom")}
	s := draftShell(starter)
	s.beginNewChatDraft(herdr.Project{ID: "w1", Label: "solco"}, draftKind)
	s.sendChatText("hello")
	state := s.chatBridgeState()
	if s.newChatDraft == nil || !state.Waiting || state.EchoPending {
		t.Fatalf("a failed launch must return to composing: %+v", state)
	}
	if state.Outcome == "" {
		t.Fatal("the failure reason must reach the page so it keeps the text")
	}
}

func TestDraftIsDroppedWhenTheSelectionMoves(t *testing.T) {
	s := draftShell(&paneStarter{})
	s.beginNewChatDraft(herdr.Project{ID: "w1", Label: "solco"}, draftKind)
	s.selectPane("p-new")
	if s.newChatDraft != nil {
		t.Fatal("selecting a pane must discard an unsent draft")
	}
}

func TestDraftWaitsForTheAgentCardThenHandsOver(t *testing.T) {
	starter := &paneStarter{result: app.SessionResult{PaneID: "p-late"}}
	s := draftShell(starter)
	s.beginNewChatDraft(herdr.Project{ID: "w1", Label: "solco"}, draftKind)
	s.sendChatText("go")
	waiting := s.chatBridgeState()
	if s.newChatDraft == nil || !waiting.EchoPending || len(waiting.Turns) != 1 || waiting.Turns[0].Text != "go" {
		t.Fatalf("without a card the page must keep showing the sent message: %+v", waiting)
	}
	s.workbench.directory.Replace([]agent.AgentCardModel{{
		Key:    agent.AgentKey{InstanceID: "main", TerminalID: "term-late"},
		PaneID: "p-late",
	}})
	s.completeDraftHandoff()
	if s.newChatDraft != nil || s.selectedPaneID != "p-late" || s.chatPendingEcho == nil || s.chatPendingEcho.Text != "go" {
		t.Fatalf("hand-over: draft=%v selected=%q echo=%#v", s.newChatDraft, s.selectedPaneID, s.chatPendingEcho)
	}
}
