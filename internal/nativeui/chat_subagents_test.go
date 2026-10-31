package nativeui

import (
	"testing"
	"time"

	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/history"
)

func subView(provider history.AgentID, id, name, callID string, updated time.Time) chatSubagentView {
	return chatSubagentView{
		Sub: history.Subagent{ID: id, Provider: provider, Name: name, CallID: callID, Started: updated.Add(-time.Minute), UpdatedAt: updated},
	}
}

// TestLinkedSubagentsUseEachProvidersOwnEvidence pins the three link rules
// (real sessions, 2026-10-31): Claude tool_use id == meta.toolUseId, omp
// tasks[].name == the child file's name, Codex spawn_agent.task_name ==
// agent_path's leaf.
func TestLinkedSubagentsUseEachProvidersOwnEvidence(t *testing.T) {
	now := time.Now()
	claude := []chatSubagentView{subView(history.AgentClaudeCode, "a1", "", "call_1", now), subView(history.AgentClaudeCode, "a2", "", "call_2", now)}
	ids, _ := linkedSubagents(&conversation.ConversationToolCall{ID: "call_2", Name: "Agent"}, claude)
	if len(ids) != 1 || ids[0] != "a2" {
		t.Fatalf("claude ids = %v", ids)
	}
	if ids, _ := linkedSubagents(&conversation.ConversationToolCall{ID: "call_9", Name: "Agent"}, claude); len(ids) != 0 {
		t.Fatalf("unknown call linked: %v", ids)
	}

	omp := []chatSubagentView{subView(history.AgentOMP, "scoutA", "scoutA", "", now), subView(history.AgentOMP, "scoutB", "scoutB", "", now)}
	input := `{"context":"x","tasks":[{"agent":"scout","name":"scoutB","task":"t"},{"agent":"plan","name":"scoutA"}]}`
	ids, types := linkedSubagents(&conversation.ConversationToolCall{Name: "task", Input: &input}, omp)
	if len(ids) != 2 || types["scoutB"] != "scout" || types["scoutA"] != "plan" {
		t.Fatalf("omp ids=%v types=%v", ids, types)
	}
	bad := `{"tasks":[`
	if ids, _ := linkedSubagents(&conversation.ConversationToolCall{Name: "task", Input: &bad}, omp); len(ids) != 0 {
		t.Fatalf("truncated omp input linked: %v", ids)
	}

	codex := []chatSubagentView{subView(history.AgentCodex, "C1", "ping_agent", "", now)}
	spawn := `{"task_name":"ping_agent","message":"hi"}`
	ids, _ = linkedSubagents(&conversation.ConversationToolCall{Name: "spawn_agent", Input: &spawn}, codex)
	if len(ids) != 1 || ids[0] != "C1" {
		t.Fatalf("codex ids = %v", ids)
	}
	if ids, _ := linkedSubagents(&conversation.ConversationToolCall{Name: "wait_agent", Input: &spawn}, codex); len(ids) != 0 {
		t.Fatalf("wait_agent must not link: %v", ids)
	}
}

func TestAttachSubagentsStampsStepsAndListsOnlyLinked(t *testing.T) {
	now := time.Now()
	views := []chatSubagentView{
		subView(history.AgentClaudeCode, "a1", "", "call_1", now),                 // running
		subView(history.AgentClaudeCode, "a2", "", "call_2", now.Add(-time.Hour)), // done
		subView(history.AgentClaudeCode, "lost", "", "call_x", now),               // no parent call
	}
	turns := []conversation.TimelineTurn{{Steps: []conversation.TimelineStep{
		{Kind: "tool", Tool: &conversation.ConversationToolCall{ID: "call_1", Name: "Agent"}},
		{Kind: "tool", Tool: &conversation.ConversationToolCall{ID: "call_2", Name: "Agent", IsError: true}},
		{Kind: "tool", Tool: &conversation.ConversationToolCall{ID: "t3", Name: "Bash"}},
	}}}
	state := ChatBridgeState{Turns: bridgeTurns(turns, "", nil)}
	state.attachSubagents(views, turns, history.AgentClaudeCode, now)

	if len(state.Subagents) != 2 || state.Subagents[0].ID != "a1" || state.Subagents[1].ID != "a2" {
		t.Fatalf("subagents = %+v, want a1, a2 (linked only, in order)", state.Subagents)
	}
	if state.Subagents[0].Status != "running" || state.Subagents[1].Status != "failed" {
		t.Fatalf("status = %q / %q", state.Subagents[0].Status, state.Subagents[1].Status)
	}
	steps := state.Turns[0].Steps
	if len(steps[0].SubagentIDs) != 1 || len(steps[2].SubagentIDs) != 0 {
		t.Fatalf("step ids = %v / %v", steps[0].SubagentIDs, steps[2].SubagentIDs)
	}
}

func TestAttachSubagentsIsSilentWithoutData(t *testing.T) {
	state := ChatBridgeState{Turns: []ChatBridgeTurn{}}
	state.attachSubagents(nil, nil, history.AgentClaudeCode, time.Now())
	if state.Subagents != nil {
		t.Fatalf("empty input listed %+v", state.Subagents)
	}
}
