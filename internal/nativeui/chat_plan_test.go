package nativeui

import (
	"strings"
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
	plan := chatPlan([]conversation.TimelineTurn{claude})
	if plan == nil || len(plan.Items) != 3 || plan.Items[0].Status != planDone || plan.Items[1].Status != planActive || plan.Items[2].Status != planPending {
		t.Fatalf("claude TodoWrite plan = %+v", plan)
	}

	codex := planTurn("update_plan", `{"plan":[{"step":"Draft","status":"completed"},{"step":"Ship","status":"pending"}]}`, "")
	if plan := chatPlan([]conversation.TimelineTurn{codex}); plan == nil || len(plan.Items) != 2 || plan.Items[0].Status != planDone {
		t.Fatalf("codex update_plan plan = %+v", plan)
	}

	omp := planTurn("todo", `{"op":"done"}`, "Remaining items (1):\n  - Verify [in_progress] (Verify)\nOverall: 2/3 done, 1 open.\nActive phase 2/2 \"Verify\" (0/1).\n  Setup:\n    - [X] Ask which framework\n    - [ ] Dropped one (dropped)\n  Verify:\n    - [ ] Run the tests (in progress)\n    - [ ] Gate (blocked: needs key)")
	plan = chatPlan([]conversation.TimelineTurn{omp})
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
	plan := chatPlan([]conversation.TimelineTurn{good, refused, {UserRow: true}})
	if plan == nil || plan.Turn != 0 || len(plan.Items) != 1 {
		t.Fatalf("a refused op must fall back to the last parseable list, got %+v", plan)
	}
	if chatPlan([]conversation.TimelineTurn{planTurn("Bash", `{"command":"ls"}`, "")}) != nil {
		t.Fatal("non-plan tools must not produce a plan")
	}
}

func TestTranscriptQuestionReadsAPendingSingleAsk(t *testing.T) {
	input := `{"questions":[{"header":"Test framework","id":"fw","question":"Which framework?","options":[{"label":"pytest","description":"asserts"},{"label":"unittest","description":"stdlib"}]}]}`
	turn := planTurn("ask", input, "")
	ask, ok := transcriptQuestion([]conversation.TimelineTurn{turn})
	if !ok || len(ask.Questions) != 1 {
		t.Fatalf("ask = %+v ok=%v", ask, ok)
	}
	question := ask.Questions[0]
	if question.Header != "Test framework" || question.Prompt != "Which framework?" {
		t.Fatalf("question = %+v", question)
	}
	if len(question.Options) != 3 || question.Options[2] != askOtherOption || question.Descriptions[1] != "stdlib" {
		t.Fatalf("options = %v / %v", question.Options, question.Descriptions)
	}

	answered := planTurn("ask", input, "User selected: pytest")
	if _, ok := transcriptQuestion([]conversation.TimelineTurn{answered}); ok {
		t.Fatal("an answered ask is not a pending question")
	}
	multi := planTurn("ask", `{"questions":[{"question":"a","recommended":1,"options":["x","y"]},{"question":"b","options":["x","y","z"]}]}`, "")
	got, ok := transcriptQuestion([]conversation.TimelineTurn{multi})
	if !ok || len(got.Questions) != 2 || got.Questions[0].Start != 1 || got.Questions[1].Start != 0 || len(got.Questions[1].Options) != 4 {
		t.Fatalf("multi-question ask must read in order with its start rows: %+v ok=%v", got, ok)
	}
	multiSelect := planTurn("ask", `{"questions":[{"question":"a","options":["x","y"]},{"question":"b","multi":true,"options":["x","y"]}]}`, "")
	picked, ok := transcriptQuestion([]conversation.TimelineTurn{multiSelect})
	if !ok || !picked.Questions[1].Multi || len(picked.Questions[1].Options) != 2 || picked.Questions[0].Multi {
		t.Fatalf("a multi-select question is ticked, with no Other row: %+v ok=%v", picked, ok)
	}
	withRecommended := planTurn("ask", `{"questions":[{"question":"b","multi":true,"recommended":[0],"options":["x","y"]}]}`, "")
	if _, ok := transcriptQuestion([]conversation.TimelineTurn{withRecommended}); ok {
		t.Fatal("pre-ticked multi-select stays in the terminal until its keys are verified")
	}
	if _, ok := transcriptQuestion([]conversation.TimelineTurn{turn, {UserRow: true}}); ok {
		t.Fatal("a later user turn means the ask is no longer the last thing")
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
	plan := chatPlan(turns)
	if plan == nil || len(plan.Items) != 2 || plan.Turn != 6 {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Items[0].Status != planDone || plan.Items[1].Status != planActive || plan.Items[1].Text != "Implement" {
		t.Fatalf("items = %+v", plan.Items)
	}
	// A newer whole-list writer wins over the replayed tasks.
	turns = append(turns, planTurn("TodoWrite", `{"todos":[{"content":"Only","status":"pending"}]}`, ""))
	if plan := chatPlan(turns); plan == nil || len(plan.Items) != 1 || plan.Items[0].Text != "Only" {
		t.Fatalf("newer TodoWrite must win: %+v", plan)
	}
}

func TestMultiSelectKeysTickRowsThenSubmit(t *testing.T) {
	options := []string{"auth", "logging", "cache", "metrics"}
	got := multiSelectKeys(options, []string{"auth", "cache"})
	want := []string{"space", "down", "down", "space", "enter"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	if got := multiSelectKeys(options, []string{"metrics"}); strings.Join(got, ",") != "down,down,down,space,enter" {
		t.Fatalf("keys = %v", got)
	}
	if got := multiSelectKeys(options, nil); strings.Join(got, ",") != "enter" {
		t.Fatalf("an empty pick only submits: %v", got)
	}
}
