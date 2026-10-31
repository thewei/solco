package nativeui

import (
	"testing"
	"time"

	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/history"
)

func subView(provider history.AgentID, id, name, callID string, updated time.Time) chatsession.View {
	return chatsession.View{
		Sub: history.Subagent{ID: id, Provider: provider, Name: name, CallID: callID, Started: updated.Add(-time.Minute), UpdatedAt: updated},
	}
}

func TestAttachSubagentsStampsStepsAndListsOnlyLinked(t *testing.T) {
	now := time.Now()
	views := []chatsession.View{
		subView(history.AgentClaudeCode, "a1", "", "call_1", now),                 // running
		subView(history.AgentClaudeCode, "a2", "", "call_2", now.Add(-time.Hour)), // done
		subView(history.AgentClaudeCode, "lost", "", "call_x", now),               // no parent call
	}
	turns := []conversation.TimelineTurn{{Steps: []conversation.TimelineStep{
		{Kind: "tool", Tool: &conversation.ConversationToolCall{ID: "call_1", Name: "Agent"}},
		{Kind: "tool", Tool: &conversation.ConversationToolCall{ID: "call_2", Name: "Agent", IsError: true}},
		{Kind: "tool", Tool: &conversation.ConversationToolCall{ID: "t3", Name: "Bash"}},
	}}}
	state := ChatBridgeState{Turns: chatsession.Turns(turns, "", nil)}
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
	state := ChatBridgeState{Turns: []chatsession.ChatTurn{}}
	state.attachSubagents(nil, nil, history.AgentClaudeCode, time.Now())
	if state.Subagents != nil {
		t.Fatalf("empty input listed %+v", state.Subagents)
	}
}
