package personalagent

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type trigEnv struct {
	s   *Store
	a   Assistant
	clk *tickClock
	sch *TriggerScheduler
	mon Task
}

func newTrigEnv(t *testing.T) *trigEnv {
	t.Helper()
	clk := &tickClock{t: time.Unix(1_800_000_000, 0)}
	s, _ := openTest(t, WithClock(clk.now))
	a := mustAssistant(t, s, "a1", "Ada")
	mon := mkTask(t, s, "mon-1", a.ID, "Monitor prod site")
	return &trigEnv{s: s, a: a, clk: clk, sch: &TriggerScheduler{Store: s, Now: clk.now}, mon: mon}
}

func (e *trigEnv) draft(t *testing.T, req string, mut ...func(*TriggerDraftInput)) (Trigger, Approval) {
	t.Helper()
	in := TriggerDraftInput{AssistantID: e.a.ID, Name: "diagnose", On: "task.failed", TaskID: e.mon.ID, Title: "Diagnose the failure", Brief: "Find out why the monitor failed"}
	for _, m := range mut {
		m(&in)
	}
	tr, ap, _, err := e.s.DraftTrigger(req, in)
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	return tr, ap
}

func (e *trigEnv) approve(t *testing.T, ap Approval) {
	t.Helper()
	if _, _, err := e.s.DecideApproval("yes-"+ap.ID, ap.ID, ap.ActionHash, true); err != nil {
		t.Fatalf("approve: %v", err)
	}
}

func (e *trigEnv) fail(t *testing.T, task Task) {
	t.Helper()
	e.clk.t = e.clk.t.Add(time.Second)
	if _, err := e.s.db.Exec(`UPDATE tasks SET state='failed', attempt=attempt+1 WHERE id=?`, task.ID); err != nil {
		t.Fatal(err)
	}
	tx, _ := e.s.db.Begin()
	if err := emitTask(tx, task.ID, "task.failed", e.clk.now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func (e *trigEnv) children(t *testing.T) []Task {
	t.Helper()
	all, _ := e.s.ListTasks(e.a.ID)
	var out []Task
	for _, x := range all {
		if strings.HasPrefix(x.RuleID, TriggerRulePrefix) {
			out = append(out, x)
		}
	}
	return out
}

// R03/AC-11: a failure of the monitored task creates exactly one diagnostic
// task, linked to its cause, and that task's own failure never recurses.
func TestTriggerStartsOneLinkedTaskPerEventAndNeverRecurses(t *testing.T) {
	e := newTrigEnv(t)
	tr, ap := e.draft(t, "d1")
	if tr.State != TriggerDraft || !strings.Contains(ap.Action, "at most 5 a day") {
		t.Fatalf("draft = %+v / %q", tr, ap.Action)
	}
	e.fail(t, e.mon)
	if r, _ := e.sch.Tick(); r.Fired != 0 {
		t.Fatalf("a draft fired: %+v", r)
	}
	e.approve(t, ap)
	if r, _ := e.sch.Tick(); r.Fired != 0 {
		t.Fatalf("approval replayed the past: %+v", r)
	}
	// A retry fails again: a new event (attempt differs) → one task.
	e.fail(t, e.mon)
	for i := 0; i < 3; i++ {
		if _, err := e.sch.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	kids := e.children(t)
	if len(kids) != 1 || kids[0].ParentTaskID != e.mon.ID || kids[0].Kind != KindResearch || kids[0].State != TaskQueued {
		t.Fatalf("children = %+v", kids)
	}
	if !strings.Contains(kids[0].Brief, "Find out why") || !strings.Contains(kids[0].Brief, e.mon.ID) {
		t.Fatalf("brief = %q", kids[0].Brief)
	}
	// The diagnosis itself fails: no second diagnosis.
	e.fail(t, kids[0])
	e.sch.Tick()
	if got := e.children(t); len(got) != 1 {
		t.Fatalf("recursed: %d children", len(got))
	}
}

func TestTriggerFilterOwnerAndRefusals(t *testing.T) {
	e := newTrigEnv(t)
	other := mkTask(t, e.s, "o1", e.a.ID, "Unrelated chore")
	b := mustAssistant(t, e.s, "a2", "Bob")
	bobTask := mkTask(t, e.s, "b1", b.ID, "Monitor prod site")
	_, ap := e.draft(t, "d1")
	e.approve(t, ap)
	e.fail(t, other)
	e.fail(t, bobTask)
	e.sch.Tick()
	if len(e.children(t)) != 0 {
		t.Fatal("fired for a task the filter does not name / another Assistant's task")
	}
	bad := []TriggerDraftInput{
		{AssistantID: e.a.ID, Name: "nofilter", On: "task.failed", Title: "t", Brief: "b"},
		{AssistantID: e.a.ID, Name: "badkind", On: "review.merged", TitleContains: "x", Title: "t", Brief: "b"},
		{AssistantID: e.a.ID, Name: "coding", On: "task.failed", TitleContains: "x", Title: "t", Brief: "b", Kind: KindCoding},
		{AssistantID: e.a.ID, Name: "foreign", On: "task.failed", TaskID: bobTask.ID, Title: "t", Brief: "b"},
	}
	for _, in := range bad {
		if _, _, _, err := e.s.DraftTrigger("bad-"+in.Name, in); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", in.Name, err)
		}
	}
}

func TestTriggerPauseWithdrawAndDailyCap(t *testing.T) {
	e := newTrigEnv(t)
	tr, ap := e.draft(t, "d1", func(in *TriggerDraftInput) { in.TaskID = ""; in.TitleContains = "monitor" })
	e.approve(t, ap)
	paused, _, err := e.s.SetTriggerState("p1", tr.ID, TriggerPaused, 0)
	if err != nil || paused.State != TriggerPaused {
		t.Fatalf("pause: %+v %v", paused, err)
	}
	e.fail(t, e.mon)
	e.sch.Tick()
	if len(e.children(t)) != 0 {
		t.Fatal("a paused trigger fired")
	}
	if _, _, err := e.s.SetTriggerState("p2", tr.ID, TriggerActive, 0); err != nil {
		t.Fatal(err)
	}
	e.sch.Tick()
	if len(e.children(t)) != 0 {
		t.Fatal("resuming replayed what happened while paused")
	}
	for i := 0; i < TriggerDailyCap+2; i++ {
		e.fail(t, e.mon)
		e.sch.Tick()
	}
	if got := len(e.children(t)); got != TriggerDailyCap {
		t.Fatalf("children = %d, want the cap %d", got, TriggerDailyCap)
	}
	e.clk.t = e.clk.t.Add(25 * time.Hour)
	e.sch.Tick()
	if got := len(e.children(t)); got != TriggerDailyCap+2 {
		t.Fatalf("the held events must run the next day: %d", got)
	}
	w, _, err := e.s.SetTriggerState("w1", tr.ID, TriggerWithdrawn, 0)
	if err != nil || w.State != TriggerWithdrawn {
		t.Fatalf("withdraw: %+v %v", w, err)
	}
	if _, _, err := e.s.SetTriggerState("w2", tr.ID, TriggerActive, 0); !errors.Is(err, ErrBadState) {
		t.Fatalf("a withdrawn trigger came back: %v", err)
	}
}

func TestTriggerDeniedOrWithdrawnDraftNeverFires(t *testing.T) {
	e := newTrigEnv(t)
	_, ap := e.draft(t, "d1")
	if _, _, err := e.s.DecideApproval("no", ap.ID, ap.ActionHash, false); err != nil {
		t.Fatal(err)
	}
	e.fail(t, e.mon)
	e.sch.Tick()
	if len(e.children(t)) != 0 {
		t.Fatal("a denied trigger fired")
	}
}

// A05: a trigger starts work the system chose, so the Assistant's own rules
// govern it — Respond/Suggest start nothing, quiet hours and the daily budget hold.
func TestTriggerObeysProactivityLevelQuietHoursAndBudget(t *testing.T) {
	e := newTrigEnv(t)
	_, ap := e.draft(t, "d1")
	e.approve(t, ap)
	set := func(mut func(*ProactivityInput)) {
		t.Helper()
		cur, _ := e.s.GetProactivity(e.a.ID)
		in := ProactivityInput{AssistantID: e.a.ID, Level: cur.Level, QuietStart: cur.QuietStart, QuietEnd: cur.QuietEnd, TZ: cur.TZ, ReportEveryMin: cur.ReportEveryMin, DailyBudget: cur.DailyBudget}
		mut(&in)
		if _, _, err := e.s.SetProactivity(fmt.Sprintf("pol-%d", time.Now().UnixNano()), in, cur.Revision); err != nil {
			t.Fatal(err)
		}
	}
	// Respond: the event is let go, and stays gone when the level is raised.
	set(func(in *ProactivityInput) { in.Level = LevelRespond })
	e.fail(t, e.mon)
	e.sch.Tick()
	set(func(in *ProactivityInput) { in.Level = LevelFollowUp })
	e.sch.Tick()
	if len(e.children(t)) != 0 {
		t.Fatalf("Respond started a task (or it was replayed later)")
	}
	// Budget of 1: the first starts, the second is held, not dropped.
	set(func(in *ProactivityInput) { in.DailyBudget = 1 })
	e.fail(t, e.mon)
	e.sch.Tick()
	e.fail(t, e.mon)
	e.sch.Tick()
	if got := len(e.children(t)); got != 1 {
		t.Fatalf("children with a budget of 1 = %d", got)
	}
	if p, _ := e.s.GetProactivity(e.a.ID); p.DispatchToday != 1 {
		t.Fatalf("the trigger did not spend the daily budget: %d", p.DispatchToday)
	}
	set(func(in *ProactivityInput) { in.DailyBudget = 5 })
	e.sch.Tick()
	if got := len(e.children(t)); got != 2 {
		t.Fatalf("the held event should run once the budget allows: %d", got)
	}
}
