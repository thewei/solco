package personalagent

import (
	"testing"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
)

// R10: any Routine occurrence can be followed back — owner, who asked and how,
// the approval that enabled it, the occurrence that fired, the worker run — from
// the task id alone. A link in a digest is then not "the Agent said so".
func TestGapR10TraceOfARoutineOccurrenceNamesItsWholeChain(t *testing.T) {
	e := newRoutineEnv(t)
	got, err := e.draft(t, "r10-draft", nil)
	if err != nil {
		t.Fatal(err)
	}
	ap := got["approval"].(map[string]any)
	e.approve(t, ap["id"].(string), ap["actionHash"].(string))
	next := at(9, 0).Add(24 * time.Hour)
	rs := &RuleScheduler{Store: e.s, Load: config.Load, Now: func() time.Time { return next.Add(5 * time.Second) }}
	if rep, err := rs.Tick(); err != nil || rep.Fired != 1 {
		t.Fatalf("tick = %+v %v", rep, err)
	}
	tasks, _ := e.s.ListTasks(e.a.ID)
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v", tasks)
	}
	// A run claimed for the occurrence carries the occurrence's event id.
	if _, _, err := e.s.ClaimTask(tasks[0].ID, "r10"); err != nil {
		t.Fatal(err)
	}
	tr, err := e.s.Trace(tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	r := tr.Routine
	if r == nil {
		t.Fatalf("no routine in the trace: %+v", tr)
	}
	if r.Name != "morning-check" || r.AssistantID != e.a.ID || r.RequestedBy != "assistant" || r.Source != "chat" || !r.Approved {
		t.Fatalf("routine link = %+v", r)
	}
	if r.ApprovalID != ap["id"].(string) {
		t.Fatalf("the approval that enabled it is missing: %+v", r)
	}
	if r.Occurrence == "" {
		t.Fatalf("the occurrence that fired the task is missing: %+v", r)
	}
	if len(tr.Runs) != 1 {
		t.Fatalf("runs = %+v", tr.Runs)
	}
}
