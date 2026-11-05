package personalagent

import (
	"errors"
	"testing"
	"time"
)

// W09: a task may be given less time than the dispatcher's own limit, never more.
func TestTaskTimeoutOnlyNarrowsTheDispatcherLimit(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	if _, _, err := s.CreateTask("too-long", CreateTaskInput{AssistantID: a.ID, Title: "t", Brief: "b", Kind: KindResearch, TimeoutMin: 90}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a task cannot widen the limit: %v", err)
	}
	if _, _, err := s.CreateTask("negative", CreateTaskInput{AssistantID: a.ID, Title: "t", Brief: "b", Kind: KindResearch, TimeoutMin: -1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative: %v", err)
	}
	short, _, err := s.CreateTask("short", CreateTaskInput{AssistantID: a.ID, Title: "quick look", Brief: "b", Kind: KindResearch, TimeoutMin: 5})
	if err != nil {
		t.Fatal(err)
	}
	plain, _, _ := s.CreateTask("plain", CreateTaskInput{AssistantID: a.ID, Title: "ordinary", Brief: "b", Kind: KindResearch})
	f := &fakeLauncher{}
	d := &Dispatcher{Store: s, Launcher: f, Claimant: "daemon"}
	for _, id := range []string{short.ID, plain.ID} {
		if _, err := d.RunTask(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]time.Duration{}
	for _, sp := range f.specs {
		got[sp.TaskID] = sp.Timeout
	}
	if got[short.ID] != 5*time.Minute || got[plain.ID] != 45*time.Minute {
		t.Fatalf("timeouts = %v", got)
	}
}
