package personalagent

import (
	"testing"
	"time"
)

func TestResultsListsOnlySettledWorkOfTheLastDayNewestFirst(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	clock := now
	s, _ := openTest(t, WithClock(func() time.Time { return clock }))
	a := mustAssistant(t, s, "a", "Ada")
	other := mustAssistant(t, s, "b", "Bo")
	mk := func(req string, owner Assistant, title string) Task {
		t.Helper()
		task, _, err := s.CreateTask(req, CreateTaskInput{AssistantID: owner.ID, Kind: KindResearch, Title: title, Brief: "b"})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	old := mk("t1", a, "Old")
	finishTaskAs(t, s, old.ID, "done")
	clock = now.Add(30 * time.Hour) // the first one is now older than a day
	done := mk("t2", a, "Done one")
	finishTaskAs(t, s, done.ID, "done")
	clock = clock.Add(time.Minute)
	failed := mk("t3", a, "Failed one")
	finishTaskAs(t, s, failed.ID, "failed")
	mk("t4", a, "Still queued")
	foreign := mk("t5", other, "Bo's")
	finishTaskAs(t, s, foreign.ID, "done")

	got, err := s.Results(a.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Title != "Failed one" || got[0].State != TaskFailed || got[1].Title != "Done one" || got[1].State != TaskDone {
		t.Fatalf("results = %+v", got)
	}
	if got[0].PaneID != "w1:p1" || got[0].HerdrSession != "default" {
		t.Fatalf("a card names where its newest run lives: %+v", got[0])
	}
	if one, _ := s.Results(a.ID, 1); len(one) != 1 || one[0].Title != "Failed one" {
		t.Fatalf("limit = %+v", one)
	}
}

func TestAPIResultsAnswersTheWire(t *testing.T) {
	s, _ := openTest(t)
	a := &API{Store: s}
	created, _ := call(t, a, `{"requestId":"r1","name":"Ada"}`, "assistant", "create")
	id := created["assistant"].(map[string]any)["id"].(string)
	out, err := call(t, a, ``, "assistant", "results", id)
	if err != nil {
		t.Fatal(err)
	}
	if rs, ok := out["results"].([]any); !ok || len(rs) != 0 {
		t.Fatalf("an Assistant with nothing settled answers an empty list, not null: %v", out)
	}
	if _, err := call(t, a, ``, "assistant", "results"); codeOf(err) != "invalid" {
		t.Fatalf("code = %q", codeOf(err))
	}
}
