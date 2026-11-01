package chatsession

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/history"
)

func TestTranscriptQuestionReadsAPendingSingleAsk(t *testing.T) {
	input := `{"questions":[{"header":"Test framework","id":"fw","question":"Which framework?","options":[{"label":"pytest","description":"asserts"},{"label":"unittest","description":"stdlib"}]}]}`
	turn := planTurn("ask", input, "")
	ask, ok := TranscriptQuestion([]conversation.TimelineTurn{turn})
	if !ok || len(ask.Questions) != 1 {
		t.Fatalf("ask = %+v ok=%v", ask, ok)
	}
	question := ask.Questions[0]
	if question.Header != "Test framework" || question.Prompt != "Which framework?" {
		t.Fatalf("question = %+v", question)
	}
	if len(question.Options) != 3 || question.Options[2] != AskOtherOption || question.Descriptions[1] != "stdlib" {
		t.Fatalf("options = %v / %v", question.Options, question.Descriptions)
	}

	answered := planTurn("ask", input, "User selected: pytest")
	if _, ok := TranscriptQuestion([]conversation.TimelineTurn{answered}); ok {
		t.Fatal("an answered ask is not a pending question")
	}
	multi := planTurn("ask", `{"questions":[{"question":"a","recommended":1,"options":["x","y"]},{"question":"b","options":["x","y","z"]}]}`, "")
	got, ok := TranscriptQuestion([]conversation.TimelineTurn{multi})
	if !ok || len(got.Questions) != 2 || got.Questions[0].Start != 1 || got.Questions[1].Start != 0 || len(got.Questions[1].Options) != 4 {
		t.Fatalf("multi-question ask must read in order with its start rows: %+v ok=%v", got, ok)
	}
	multiSelect := planTurn("ask", `{"questions":[{"question":"a","options":["x","y"]},{"question":"b","multi":true,"options":["x","y"]}]}`, "")
	picked, ok := TranscriptQuestion([]conversation.TimelineTurn{multiSelect})
	if !ok || !picked.Questions[1].Multi || len(picked.Questions[1].Options) != 2 || picked.Questions[0].Multi {
		t.Fatalf("a multi-select question is ticked, with no Other row: %+v ok=%v", picked, ok)
	}
	withRecommended := planTurn("ask", `{"questions":[{"question":"b","multi":true,"recommended":[0],"options":["x","y"]}]}`, "")
	if _, ok := TranscriptQuestion([]conversation.TimelineTurn{withRecommended}); ok {
		t.Fatal("pre-ticked multi-select stays in the terminal until its keys are verified")
	}
	if _, ok := TranscriptQuestion([]conversation.TimelineTurn{turn, {UserRow: true}}); ok {
		t.Fatal("a later user turn means the ask is no longer the last thing")
	}
}

func TestTranscriptQuestionReadsAgyAskQuestion(t *testing.T) {
	// Captured from a real Antigravity CLI 1.4.12 transcript (2026-11-01):
	// one call, two questions, plain-string options, a "(Recommended)" label on
	// the second row (the cursor still opens on the first), a multi-select.
	input := `{"questions":[{"is_multi_select":false,"options":["Small","(Recommended) Medium","Large"],"question":"Pick a size"},{"is_multi_select":true,"options":["Cheese","Olives","Onions"],"question":"Pick toppings"}]}`
	ask, ok := TranscriptQuestion([]conversation.TimelineTurn{planTurn("ask_question", input, "")})
	if !ok || len(ask.Questions) != 2 {
		t.Fatalf("ask = %+v ok=%v", ask, ok)
	}
	size, toppings := ask.Questions[0], ask.Questions[1]
	if size.Multi || size.Start != 0 || len(size.Options) != 4 || size.Options[1] != "(Recommended) Medium" || size.Options[3] != AgyWriteInOption {
		t.Fatalf("single question keeps its labels, opens on row 0 and ends on the Write-in row: %+v", size)
	}
	if !toppings.Multi || len(toppings.Options) != 3 {
		t.Fatalf("a multi-select question is ticked and offers no Write-in row: %+v", toppings)
	}
	if _, ok := TranscriptQuestion([]conversation.TimelineTurn{planTurn("ask_question", input, "A1: Medium")}); ok {
		t.Fatal("an answered ask_question is not pending")
	}
}

func TestAgyAskQuestionIsPendingThroughTheRealReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript_full.jsonl")
	rows := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-01T04:42:20Z","content":"ask me"}` + "\n" +
		`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-01T04:42:31Z","tool_calls":[{"name":"ask_question","args":{"questions":[{"is_multi_select":false,"options":["Tea","Coffee"],"question":"Drink?"}]}}]}` + "\n"
	if err := os.WriteFile(path, []byte(rows), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Open(history.AgentAntigravity, path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = r.Sync()
	ask, ok := r.Ask()
	if !ok || len(ask.Questions) != 1 || ask.Questions[0].Options[2] != AgyWriteInOption {
		t.Fatalf("ask = %+v ok=%v", ask, ok)
	}
}
