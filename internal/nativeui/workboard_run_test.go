package nativeui

import (
	"testing"

	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/workboard"
)

func TestTaskViewCarriesTheRunStrip(t *testing.T) {
	var ran []string
	s := pageShell(t, "approval_real_claude", &ran)
	strip := s.projectsView().Task.Strip
	var labels []string
	for _, step := range strip {
		labels = append(labels, step.Label)
	}
	want := []string{"Work", "Check", "PR", "Review", "Approve", "Merge"}
	if len(labels) != len(want) {
		t.Fatalf("strip = %v", labels)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("strip = %v, want %v", labels, want)
		}
	}
}

func TestChatAndResearchTasksHaveNoRunStrip(t *testing.T) {
	var ran []string
	s := pageShell(t, "approval_real_claude", &ran)
	for _, kind := range []string{"chat", "research"} {
		s.workboard.board.tasks[0].Chat = kind == "chat"
		s.workboard.board.tasks[0].Research = kind == "research"
		if got := s.projectsView().Task.Strip; len(got) != 0 {
			t.Fatalf("%s changes nothing in the repo and has no pipeline: %+v", kind, got)
		}
	}
}

// The Chat column follows the task's agent: a pane in the worktree with an
// agent card, nothing for a plain shell.
func TestTaskChatTargetNeedsAnAgentInTheTaskWorktree(t *testing.T) {
	var ran []string
	s := pageShell(t, "approval_real_claude", &ran)
	const wt = "/scratch/worktrees/repo/work-2-add-hello-file"
	// The sidebar's task→pane join reads the board's details cache.
	s.workboard.board.details = map[string]workboardDetail{"repo/2": {detail: *s.workboard.page.detail}}

	if got := s.projectsView().Chat; got.Target != "" || got.Why == "" {
		t.Fatalf("no pane in the worktree: no Chat target, a reason instead: %+v", got)
	}

	// A plain shell in the worktree has no agent card, so there is no Chat.
	s.projection = herdr.Projection{Panes: []herdr.Pane{{ID: "sh", TerminalID: "t0", CWD: wt}}}
	if got := s.projectsView().Chat; got.Target != "" {
		t.Fatalf("a shell is not an agent: %+v", got)
	}

	s.projection = herdr.Projection{
		Panes:  []herdr.Pane{{ID: "p1", TerminalID: "t1", CWD: wt, Agent: "claude"}},
		Agents: []herdr.Agent{{PaneID: "p1", TerminalID: "t1", Name: "claude"}},
	}
	if got := s.projectsView().Chat; got.Target != "p1" {
		t.Fatalf("an agent pane in the worktree is the Chat target: %+v", got)
	}
}

// An agent stopped on a first-start prompt has no conversation to read; the
// page must say so instead of spinning on "Loading conversation".
func TestChatTargetWaitingOnAPromptBeforeAnyConversation(t *testing.T) {
	var ran []string
	s := pageShell(t, "approval_real_claude", &ran)
	const wt = "/scratch/worktrees/repo/work-2-add-hello-file"
	s.workboard.board.details = map[string]workboardDetail{"repo/2": {detail: *s.workboard.page.detail}}
	s.projection = herdr.Projection{
		Panes:  []herdr.Pane{{ID: "p1", TerminalID: "t1", CWD: wt, Agent: "claude"}},
		Agents: []herdr.Agent{{PaneID: "p1", TerminalID: "t1", Name: "claude", Status: "blocked"}},
	}
	got := s.projectsView().Chat
	if got.Target != "p1" || !got.Waiting {
		t.Fatalf("a blocked agent with no conversation is waiting: %+v", got)
	}
	s.projection.Agents[0].Status = "idle"
	if got := s.projectsView().Chat; got.Waiting {
		t.Fatalf("an idle agent is not waiting on a prompt: %+v", got)
	}
}

var _ = workboard.Task{}
