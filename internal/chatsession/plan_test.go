package chatsession

import (
	"testing"

	"github.com/thewei/solco/internal/conversation"
)

func planTurn(name string, input, output string) conversation.TimelineTurn {
	call := conversation.ConversationToolCall{Name: name}
	if input != "" {
		call.Input = &input
	}
	if output != "" {
		call.Output = &output
	}
	return conversation.TimelineTurn{Steps: []conversation.TimelineStep{{Kind: conversation.StepTool, Tool: &call}}}
}

func TestChatPlanReadsEachProvidersTaskList(t *testing.T) {
	claude := planTurn("TodoWrite", `{"todos":[{"content":"Read","status":"completed"},{"content":"Edit","status":"in_progress"},{"content":"Test","status":"pending"}]}`, "")
	plan := PlanOf([]conversation.TimelineTurn{claude})
	if plan == nil || len(plan.Items) != 3 || plan.Items[0].Status != planDone || plan.Items[1].Status != planActive || plan.Items[2].Status != planPending {
		t.Fatalf("claude TodoWrite plan = %+v", plan)
	}

	codex := planTurn("update_plan", `{"plan":[{"step":"Draft","status":"completed"},{"step":"Ship","status":"pending"}]}`, "")
	if plan := PlanOf([]conversation.TimelineTurn{codex}); plan == nil || len(plan.Items) != 2 || plan.Items[0].Status != planDone {
		t.Fatalf("codex update_plan plan = %+v", plan)
	}

	omp := planTurn("todo", `{"op":"done"}`, "Remaining items (1):\n  - Verify [in_progress] (Verify)\nOverall: 2/3 done, 1 open.\nActive phase 2/2 \"Verify\" (0/1).\n  Setup:\n    - [X] Ask which framework\n    - [ ] Dropped one (dropped)\n  Verify:\n    - [ ] Run the tests (in progress)\n    - [ ] Gate (blocked: needs key)")
	plan = PlanOf([]conversation.TimelineTurn{omp})
	if plan == nil || len(plan.Items) != 4 {
		t.Fatalf("omp todo plan = %+v", plan)
	}
	want := []struct{ text, status, phase string }{
		{"Ask which framework", planDone, "Setup"},
		{"Dropped one", planDropped, "Setup"},
		{"Run the tests", planActive, "Verify"},
		{"Gate", planBlocked, "Verify"},
	}
	for index, w := range want {
		got := plan.Items[index]
		if got.Text != w.text || got.Status != w.status || got.Phase != w.phase {
			t.Fatalf("omp item %d = %+v, want %+v", index, got, w)
		}
	}
}

func TestChatPlanSkipsFailedCallsAndKeepsTheNewestList(t *testing.T) {
	good := planTurn("todo", "", "Remaining items (1):\n  - A [in_progress] (P)\nOverall: 0/1 done, 1 open.\n  P:\n    - [ ] A (in progress)")
	refused := planTurn("todo", "", "Errors: Task \"B\" not found")
	plan := PlanOf([]conversation.TimelineTurn{good, refused, {UserRow: true}})
	if plan == nil || plan.Turn != 0 || len(plan.Items) != 1 {
		t.Fatalf("a refused op must fall back to the last parseable list, got %+v", plan)
	}
	if PlanOf([]conversation.TimelineTurn{planTurn("Bash", `{"command":"ls"}`, "")}) != nil {
		t.Fatal("non-plan tools must not produce a plan")
	}
}

// Claude Code's TaskCreate/TaskUpdate build the list call by call. The shapes
// follow the tools' documented input and result text; no real transcript
// could be captured (the lab Claude had no task tools).
func TestChatPlanReplaysClaudeTaskCreateAndUpdate(t *testing.T) {
	turns := []conversation.TimelineTurn{
		planTurn("TaskCreate", `{"subject":"Write plan"}`, "Task #1 created successfully: Write plan"),
		planTurn("TaskCreate", `{"subject":"Implement"}`, "Task #2 created successfully: Implement"),
		planTurn("TaskCreate", `{"subject":"Verify"}`, "Task #3 created successfully: Verify"),
		planTurn("TaskUpdate", `{"taskId":"1","status":"completed"}`, "Updated task #1 status"),
		planTurn("TaskUpdate", `{"taskId":2,"status":"in_progress"}`, "Updated task #2 status"),
		planTurn("TaskUpdate", `{"taskId":"9","status":"completed"}`, "Error: task not found"),
		planTurn("TaskUpdate", `{"taskId":"3","status":"deleted"}`, "Updated task #3 status"),
	}
	plan := PlanOf(turns)
	if plan == nil || len(plan.Items) != 2 || plan.Turn != 6 {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Items[0].Status != planDone || plan.Items[1].Status != planActive || plan.Items[1].Text != "Implement" {
		t.Fatalf("items = %+v", plan.Items)
	}
	// A newer whole-list writer wins over the replayed tasks.
	turns = append(turns, planTurn("TodoWrite", `{"todos":[{"content":"Only","status":"pending"}]}`, ""))
	if plan := PlanOf(turns); plan == nil || len(plan.Items) != 1 || plan.Items[0].Text != "Only" {
		t.Fatalf("newer TodoWrite must win: %+v", plan)
	}
}

// Cursor's TodoWrite may send only what changed (merge:true): the panel is
// the replay of every call, and a plain call still replaces the list.
func TestChatPlanReplaysCursorMergeTodos(t *testing.T) {
	first := planTurn("TodoWrite", `{"merge":false,"todos":[{"id":"a","content":"Read","status":"in_progress"},{"id":"b","content":"Edit","status":"pending"}]}`, "")
	second := planTurn("TodoWrite", `{"merge":true,"todos":[{"id":"a","status":"completed"},{"id":"b","status":"in_progress"},{"id":"c","content":"Test","status":"pending"}]}`, "")
	plan := PlanOf([]conversation.TimelineTurn{first, second})
	if plan == nil || len(plan.Items) != 3 || plan.Turn != 1 {
		t.Fatalf("merged plan = %+v", plan)
	}
	want := []struct{ text, status string }{{"Read", planDone}, {"Edit", planActive}, {"Test", planPending}}
	for i, w := range want {
		if plan.Items[i].Text != w.text || plan.Items[i].Status != w.status {
			t.Fatalf("item %d = %+v, want %+v", i, plan.Items[i], w)
		}
	}
	replaced := planTurn("TodoWrite", `{"merge":false,"todos":[{"id":"z","content":"Fresh","status":"cancelled"}]}`, "")
	plan = PlanOf([]conversation.TimelineTurn{first, second, replaced})
	if plan == nil || len(plan.Items) != 1 || plan.Items[0].Text != "Fresh" || plan.Items[0].Status != planDropped {
		t.Fatalf("a non-merge call replaces the list: %+v", plan)
	}
}

// Pi's todo extension prints only the increment ("Updated #2 …"); the whole
// list rides in the result's details, which the parser keeps as Details. The
// newest call wins; an empty list (clear) retires the tray; deleted tasks
// are not rows.
func TestChatPlanReadsPiTodoSnapshot(t *testing.T) {
	snap := func(tasks string) conversation.TimelineTurn {
		turn := planTurn("todo", `{"action":"update"}`, "Updated #1 (pending → in_progress)")
		details := `{"tasks":` + tasks + `}`
		turn.Steps[0].Tool.Details = &details
		return turn
	}
	first := snap(`[{"id":1,"subject":"Research","status":"completed"},{"id":2,"subject":"Build","status":"in_progress"},{"id":3,"subject":"Drop me","status":"deleted"},{"id":4,"subject":"Ship","status":"pending"}]`)
	plan := PlanOf([]conversation.TimelineTurn{first})
	if plan == nil || len(plan.Items) != 3 || plan.Items[0].Status != planDone || plan.Items[1].Status != planActive || plan.Items[2].Text != "Ship" {
		t.Fatalf("pi todo plan = %+v", plan)
	}
	if got := PlanOf([]conversation.TimelineTurn{first, snap(`[]`)}); got != nil {
		t.Fatalf("an empty snapshot (clear) must retire the tray, got %+v", got)
	}
	refused := planTurn("todo", `{"action":"create"}`, "Error: blockedBy: #1 is another item in this batch")
	if got := PlanOf([]conversation.TimelineTurn{first, refused}); got == nil || len(got.Items) != 3 {
		t.Fatalf("a refused call carries no snapshot and must fall back, got %+v", got)
	}
}
