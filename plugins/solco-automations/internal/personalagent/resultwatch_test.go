package personalagent

import "testing"

func TestResultWatcherToastsOnlyFreshPlainCompletions(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a", "Ada")
	mk := func(req, title string) Task {
		t.Helper()
		task, _, err := s.CreateTask(req, CreateTaskInput{AssistantID: a.ID, Kind: KindResearch, Title: title, Brief: "b"})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	before := mk("t0", "Before start")
	finishTaskAs(t, s, before.ID, "done")

	w := &ResultWatcher{Store: s}
	if got, err := w.Tick(); err != nil || len(got) != 0 {
		t.Fatalf("the first look is silent: %+v %v", got, err)
	}

	fresh := mk("t1", "Fresh")
	finishTaskAs(t, s, fresh.ID, "done")
	bad := mk("t2", "Broken")
	finishTaskAs(t, s, bad.ID, "failed")

	got, err := w.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "Fresh" || got[0].Name != "Ada" {
		t.Fatalf("only the plain completion is news (failures reach the inbox toast): %+v", got)
	}
	if again, _ := w.Tick(); len(again) != 0 {
		t.Fatalf("a completion is announced once: %+v", again)
	}
}
