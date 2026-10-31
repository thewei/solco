package workboard

import "testing"

func stripStates(t Task) [6]StepState {
	var out [6]StepState
	for i, s := range RunStrip(t) {
		out[i] = s.State
	}
	return out
}

func TestRunStrip(t *testing.T) {
	const (
		P, D, C, W = StepPending, StepDone, StepCurrent, StepWaiting
	)
	cases := []struct {
		name string
		task Task
		want [6]StepState
	}{
		{"todo lights nothing", Task{Stage: "todo"}, [6]StepState{P, P, P, P, P, P}},
		{"queued after another task", Task{Stage: "after", State: "idle"}, [6]StepState{P, P, P, P, P, P}},
		{"working", Task{Stage: "working", State: "working"}, [6]StepState{C, P, P, P, P, P}},
		{"agent asks a question", Task{Stage: "asks", State: "blocked", Kind: "question"}, [6]StepState{W, P, P, P, P, P}},
		{"plan waits for a yes", Task{Stage: "plan", State: "blocked"}, [6]StepState{W, P, P, P, P, P}},
		{"checking", Task{Stage: "checking", State: "working"}, [6]StepState{D, C, P, P, P, P}},
		{"pushing", Task{Stage: "pushing", State: "working"}, [6]StepState{D, D, C, P, P, P}},
		{"review round", Task{Stage: "reviewing", State: "working", ReviewRound: 2}, [6]StepState{D, D, D, C, P, P}},
		{"review needs you", Task{Stage: "reviewneeds", State: "blocked"}, [6]StepState{D, D, D, W, P, P}},
		{"approval waits", Task{Stage: "approval", State: "blocked", Kind: "permission"}, [6]StepState{D, D, D, D, W, P}},
		{"merging", Task{Stage: "merging", State: "working"}, [6]StepState{D, D, D, D, D, C}},
		{"done", Task{Stage: "done", State: "done"}, [6]StepState{D, D, D, D, D, D}},
		{"state done wins over a stale stage", Task{Stage: "working", State: "done"}, [6]StepState{D, D, D, D, D, D}},
		{"failed does not guess where", Task{Stage: "failed", State: "blocked"}, [6]StepState{P, P, P, P, P, P}},
		{"unknown stage lights nothing", Task{Stage: "future-stage", State: "working"}, [6]StepState{P, P, P, P, P, P}},
	}
	for _, tc := range cases {
		if got := stripStates(tc.task); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRunStripLabels(t *testing.T) {
	steps := RunStrip(Task{Stage: "working"})
	want := []string{"Work", "Check", "PR", "Review", "Approve", "Merge"}
	if len(steps) != len(want) {
		t.Fatalf("len = %d, want %d", len(steps), len(want))
	}
	for i, s := range steps {
		if s.Label != want[i] {
			t.Errorf("step %d = %q, want %q", i, s.Label, want[i])
		}
	}
}
