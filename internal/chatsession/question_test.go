package chatsession

import (
	"testing"

	"github.com/thewei/solco/internal/conversation"
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
