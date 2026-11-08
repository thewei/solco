package automations

import (
	"reflect"
	"testing"
)

func auto(name string, draft bool, runID, status string) Automation {
	a := Automation{Name: name, Draft: draft}
	if runID != "" {
		a.LastRun = &LastRun{RunID: runID, Status: status}
	}
	return a
}

func TestWatcherFirstReadOnlyRecords(t *testing.T) {
	var w Watcher
	if got := w.Observe([]Automation{auto("a", false, "r1", "failed"), auto("d", true, "", "")}); got != nil {
		t.Fatalf("first observation notified: %v", got)
	}
	if got := w.Observe([]Automation{auto("a", false, "r1", "failed"), auto("d", true, "", "")}); got != nil {
		t.Fatalf("an echo notified: %v", got)
	}
}

func TestWatcherReportsEachStepOfARun(t *testing.T) {
	var w Watcher
	w.Observe([]Automation{auto("a", false, "r0", "done")})
	steps := []struct {
		list []Automation
		want []Event
	}{
		{[]Automation{auto("a", false, "r1", "running")}, []Event{{"a", "r1", EventStarted}}},
		{[]Automation{auto("a", false, "r1", "running")}, nil},
		{[]Automation{auto("a", false, "r1", "done")}, []Event{{"a", "r1", EventDone}}},
		{[]Automation{auto("a", false, "r2", "failed")}, []Event{{"a", "r2", EventFailed}}}, // began and ended between reads
		{[]Automation{auto("a", false, "r3", "queued")}, nil},                               // not news
	}
	for i, s := range steps {
		if got := w.Observe(s.list); !reflect.DeepEqual(got, s.want) {
			t.Errorf("step %d: got %v, want %v", i, got, s.want)
		}
	}
}

func TestWatcherAnnouncesANewDraftButNotItsApproval(t *testing.T) {
	var w Watcher
	w.Observe([]Automation{auto("a", false, "", "")})
	got := w.Observe([]Automation{auto("a", false, "", ""), auto("d", true, "", "")})
	if want := []Event{{Name: "d", Kind: EventDraft}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := w.Observe([]Automation{auto("a", false, "", ""), auto("d", false, "", "")}); got != nil {
		t.Fatalf("approving a draft notified: %v", got)
	}
}

func TestWatcherResetIsAFirstObservationAgain(t *testing.T) {
	var w Watcher
	w.Observe([]Automation{auto("a", false, "r1", "running")})
	w.Reset()
	if got := w.Observe([]Automation{auto("a", false, "r1", "done")}); got != nil {
		t.Fatalf("read after Reset notified: %v", got)
	}
}
