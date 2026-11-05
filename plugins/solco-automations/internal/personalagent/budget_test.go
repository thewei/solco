package personalagent

import (
	"fmt"
	"testing"
	"time"
)

// Gap A05: every task the system starts on its own, and every time it holds
// back, is one row of one ledger — so a spent budget is something a person can
// see, not a silent stop.

func setProactivity(t *testing.T, s *Store, id string, mut func(*ProactivityInput)) {
	t.Helper()
	cur, err := s.GetProactivity(id)
	if err != nil {
		t.Fatal(err)
	}
	in := ProactivityInput{AssistantID: id, Level: cur.Level, QuietStart: cur.QuietStart, QuietEnd: cur.QuietEnd, TZ: cur.TZ, ReportEveryMin: cur.ReportEveryMin, DailyBudget: cur.DailyBudget}
	mut(&in)
	if _, _, err := s.SetProactivity(fmt.Sprintf("pol-%s-%d", id, cur.Revision), in, cur.Revision); err != nil {
		t.Fatal(err)
	}
}

func heldCount(sum BudgetSummary, kind, reason string) int {
	for _, h := range sum.Held {
		if h.Kind == kind && h.Reason == reason {
			return h.Count
		}
	}
	return 0
}

func TestBudgetLedgerSupervisorSpendsAndHoldsOnceADay(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	in := twoStepPlan()
	in.Milestones[1].DependsOn = nil // both ready at once
	activeGoalWithPlan(t, s, a, in)
	setProactivity(t, s, a.ID, func(in *ProactivityInput) { in.DailyBudget = 1 })
	sv := &Supervisor{Store: s}
	for i := 0; i < 4; i++ {
		_, _ = sv.Tick()
	}
	sum, err := s.BudgetSummary(a.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if sum.DailyBudget != 1 || sum.SpentToday != 1 || sum.Remaining != 0 || sum.Spent["supervisor"] != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	if got := heldCount(sum, "supervisor", "budget"); got != 1 {
		t.Fatalf("a held budget must be one row a day, not one per tick: %d (%+v)", got, sum.Held)
	}
	if len(sum.Recent) != 2 || sum.Recent[0].Outcome != "held" || sum.Recent[1].Outcome != "spent" {
		t.Fatalf("recent (newest first) = %+v", sum.Recent)
	}
	if len(sum.Unmeasured) != 2 {
		t.Fatalf("tokens and cost are not observable here and must say so: %v", sum.Unmeasured)
	}
}

func TestBudgetLedgerRecordsASuggestLevelHold(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	activeGoalWithPlan(t, s, a, twoStepPlan())
	setProactivity(t, s, a.ID, func(in *ProactivityInput) { in.Level = LevelSuggest })
	sv := &Supervisor{Store: s}
	for i := 0; i < 3; i++ {
		_, _ = sv.Tick()
	}
	sum, _ := s.BudgetSummary(a.ID, 20)
	if heldCount(sum, "supervisor", "level") != 1 || sum.SpentToday != 0 {
		t.Fatalf("summary = %+v", sum)
	}
}

func TestBudgetLedgerTriggerSpendAndHold(t *testing.T) {
	e := newTrigEnv(t)
	_, ap := e.draft(t, "d1")
	e.approve(t, ap)
	setProactivity(t, e.s, e.a.ID, func(in *ProactivityInput) { in.DailyBudget = 1 })
	e.fail(t, e.mon)
	e.sch.Tick()
	e.fail(t, e.mon)
	e.sch.Tick()
	e.sch.Tick()
	sum, _ := e.s.BudgetSummary(e.a.ID, 20)
	if sum.Spent["trigger"] != 1 || sum.SpentToday != 1 || heldCount(sum, "trigger", "budget") != 1 {
		t.Fatalf("summary = %+v", sum)
	}
}

func TestBudgetLedgerIsBoundedAndKeepsNoText(t *testing.T) {
	clock := &tickClock{t: time.Unix(1_800_000_000, 0)}
	s, _ := openTest(t, WithClock(clock.now))
	a := mustAssistant(t, s, "a1", "Ada")
	for i := 0; i < 5; i++ {
		if err := s.recordBudget(s.db, a.ID, "2026-01-01", BudgetSupervisor, fmt.Sprintf("t_%d", i), BudgetSpent, ""); err != nil {
			t.Fatal(err)
		}
	}
	// A row past the retention window goes when the next one is written.
	clock.t = clock.t.AddDate(0, 0, BudgetRetentionDays+1)
	if err := s.recordBudget(s.db, a.ID, "2026-02-01", BudgetSupervisor, "t_new", BudgetSpent, ""); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM budget_events`).Scan(&n)
	if n != 1 {
		t.Fatalf("rows after retention = %d", n)
	}
}

func TestBudgetLedgerWakeSpendAndWakeBudgetHold(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s, _ := openTest(t, WithClock(func() time.Time { return now }))
	a := mustAssistant(t, s, "a1", "Ada")
	h := setBeat(t, s, a, HeartbeatInput{Cron: "*/10 * * * *", Checklist: "- look", WakeBudget: 1})
	h = approveWake(t, s, h)
	b := beater(s, &now)
	_, _ = b.Tick() // baseline
	for i := 0; i < 3; i++ {
		now = now.Add(11 * time.Minute)
		_, _ = b.Tick()
		for _, tk := range taskTitles(t, s, a, "Heartbeat") {
			if tk.State == TaskQueued {
				_, run := claimAndRun(t, s, tk)
				_ = s.FinishRun(run.ID, RunDone, "nothing to do")
			}
		}
	}
	sum, err := s.BudgetSummary(a.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Spent["wake"] != 1 || heldCount(sum, "wake", "wake_budget") != 1 || !sum.Wake.Approved || sum.Wake.Budget != 1 || sum.Wake.UsedToday != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	// Quiet hours hold a wake with their own reason.
	setProactivity(t, s, a.ID, func(in *ProactivityInput) { in.Level = LevelSuggest })
	now = now.Add(24 * time.Hour)
	_, _ = b.Tick()
	now = now.Add(11 * time.Minute)
	_, _ = b.Tick()
	sum, _ = s.BudgetSummary(a.ID, 20)
	if heldCount(sum, "wake", "level") != 1 {
		t.Fatalf("a Suggest-level hold is recorded: %+v", sum.Held)
	}
}
