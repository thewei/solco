package chatsession

import (
	"testing"
	"time"

	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/history"
)

func subView(provider history.AgentID, id, name, callID string, updated time.Time) View {
	return View{
		Sub: history.Subagent{ID: id, Provider: provider, Name: name, CallID: callID, Started: updated.Add(-time.Minute), UpdatedAt: updated},
	}
}

// TestLinkedSubagentsUseEachProvidersOwnEvidence pins the three link rules
// (real sessions, 2026-10-31): Claude tool_use id == meta.toolUseId, omp
// tasks[].name == the child file's name, Codex spawn_agent.task_name ==
// agent_path's leaf.
func TestLinkedSubagentsUseEachProvidersOwnEvidence(t *testing.T) {
	now := time.Now()
	claude := []View{subView(history.AgentClaudeCode, "a1", "", "call_1", now), subView(history.AgentClaudeCode, "a2", "", "call_2", now)}
	ids, _ := Linked(&conversation.ConversationToolCall{ID: "call_2", Name: "Agent"}, claude)
	if len(ids) != 1 || ids[0] != "a2" {
		t.Fatalf("claude ids = %v", ids)
	}
	if ids, _ := Linked(&conversation.ConversationToolCall{ID: "call_9", Name: "Agent"}, claude); len(ids) != 0 {
		t.Fatalf("unknown call linked: %v", ids)
	}

	omp := []View{subView(history.AgentOMP, "scoutA", "scoutA", "", now), subView(history.AgentOMP, "scoutB", "scoutB", "", now)}
	input := `{"context":"x","tasks":[{"agent":"scout","name":"scoutB","task":"t"},{"agent":"plan","name":"scoutA"}]}`
	ids, types := Linked(&conversation.ConversationToolCall{Name: "task", Input: &input}, omp)
	if len(ids) != 2 || types["scoutB"] != "scout" || types["scoutA"] != "plan" {
		t.Fatalf("omp ids=%v types=%v", ids, types)
	}
	bad := `{"tasks":[`
	if ids, _ := Linked(&conversation.ConversationToolCall{Name: "task", Input: &bad}, omp); len(ids) != 0 {
		t.Fatalf("truncated omp input linked: %v", ids)
	}

	codex := []View{subView(history.AgentCodex, "C1", "ping_agent", "", now)}
	spawn := `{"task_name":"ping_agent","message":"hi"}`
	ids, _ = Linked(&conversation.ConversationToolCall{Name: "spawn_agent", Input: &spawn}, codex)
	if len(ids) != 1 || ids[0] != "C1" {
		t.Fatalf("codex ids = %v", ids)
	}
	if ids, _ := Linked(&conversation.ConversationToolCall{Name: "wait_agent", Input: &spawn}, codex); len(ids) != 0 {
		t.Fatalf("wait_agent must not link: %v", ids)
	}
}

func TestCardReportsStoppedOnlyFromTheProviderMarker(t *testing.T) {
	now := time.Now()
	sub := history.Subagent{ID: "a", Provider: history.AgentClaudeCode, UpdatedAt: now, Ended: true, Stopped: true}
	if got := Card(View{Sub: sub}, "", nil, now).Status; got != "stopped" {
		t.Fatalf("status = %q, want stopped", got)
	}
	sub.Stopped = false
	if got := Card(View{Sub: sub}, "", nil, now).Status; got != "done" {
		t.Fatalf("status = %q, want done", got)
	}
}

func TestLinkedPiSubagentByTaskText(t *testing.T) {
	views := []View{
		{Sub: history.Subagent{ID: "a/run-0", Provider: history.AgentPi}, Detail: history.SubagentDetail{Prompt: "Task: audit the router and report"}},
		{Sub: history.Subagent{ID: "b/run-0", Provider: history.AgentPi}, Detail: history.SubagentDetail{Prompt: "Task: something else entirely"}},
	}
	input := `{"agent":"worker","async":true,"task":"audit the router and report"}`
	ids, _ := Linked(&conversation.ConversationToolCall{Name: "subagent", Input: &input}, views)
	if len(ids) != 1 || ids[0] != "a/run-0" {
		t.Fatalf("ids = %v, want [a/run-0]", ids)
	}
	input = `{"tasks":[{"agent":"x","task":"something else entirely"},{"agent":"y","task":"audit the router and report"}]}`
	ids, _ = Linked(&conversation.ConversationToolCall{Name: "subagent", Input: &input}, views)
	if len(ids) != 2 {
		t.Fatalf("parallel ids = %v, want both", ids)
	}
	input = `{"action":"status","id":"z"}`
	if ids, _ = Linked(&conversation.ConversationToolCall{Name: "subagent", Input: &input}, views); len(ids) != 0 {
		t.Fatalf("management call linked %v", ids)
	}
}

func TestLinkedPiSubagentByRunNamedInTheResult(t *testing.T) {
	views := []View{
		{Sub: history.Subagent{ID: "a/run-0", Provider: history.AgentPi, CallRun: "wf-1"}},
		{Sub: history.Subagent{ID: "b/run-0", Provider: history.AgentPi, CallRun: "wf-2"}},
	}
	input, out := `{"workflowScript":"x","async":true}`, "Async workflow [wf-1]\n\ndetached"
	ids, _ := Linked(&conversation.ConversationToolCall{Name: "subagent", Input: &input, Output: &out}, views)
	if len(ids) != 1 || ids[0] != "a/run-0" {
		t.Fatalf("ids = %v, want [a/run-0]", ids)
	}
	input = `{"action":"status","id":"wf-1"}`
	if ids, _ = Linked(&conversation.ConversationToolCall{Name: "subagent", Input: &input, Output: &out}, views); len(ids) != 0 {
		t.Fatalf("a status call linked %v", ids)
	}
}
