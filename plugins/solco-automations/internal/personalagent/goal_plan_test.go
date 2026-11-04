package personalagent

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func finishTaskAs(t *testing.T, s *Store, taskID, status string) {
	t.Helper()
	r, _, err := s.ClaimTask(taskID, "d")
	if err != nil {
		t.Fatalf("claim %s: %v", taskID, err)
	}
	_ = s.MarkStarting(r.ID)
	_ = s.RecordLaunch(r.ID, Session{HerdrSession: "default", WorkspaceID: "w1", PaneID: "w1:p1"})
	if err := s.FinishRun(r.ID, status, "note"); err != nil {
		t.Fatalf("finish %s: %v", taskID, err)
	}
}

func activeGoalWithPlan(t *testing.T, s *Store, a Assistant, in GoalPlanInput) Goal {
	t.Helper()
	g, _, err := s.CreateGoal("g-"+a.ID, CreateGoalInput{AssistantID: a.ID, Title: "Ship the report"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetGoalPlan("p-"+g.ID, g.ID, in, "first plan", 0); err != nil {
		t.Fatalf("plan: %v", err)
	}
	g, _, err = s.ApproveGoal("ap-"+g.ID, g.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func twoStepPlan() GoalPlanInput {
	return GoalPlanInput{
		SuccessCriteria: "The report exists and was reviewed.",
		NextAction:      "Research first.",
		Milestones: []Milestone{
			{ID: "m1", Title: "Collect the numbers", Task: &MilestoneTask{Kind: KindResearch, Title: "Collect numbers", Brief: "Find Q3 numbers."}},
			{ID: "m2", Title: "Write the report", DependsOn: []string{"m1"}, Task: &MilestoneTask{Kind: KindManual, Title: "Write report", Brief: "Write it."}},
		},
	}
}

func factKinds(t *testing.T, s *Store, goalID string) []string {
	t.Helper()
	fs, err := s.Facts("", goalID, 100)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(fs))
	for i := len(fs) - 1; i >= 0; i-- { // oldest first
		out = append(out, fs[i].Kind)
	}
	return out
}

func TestSetGoalPlanValidatesVersionsAndBoundsReplans(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	g, _, _ := s.CreateGoal("g1", CreateGoalInput{AssistantID: a.ID, Title: "Goal"})

	p, replayed, err := s.SetGoalPlan("p1", g.ID, twoStepPlan(), "first", 0)
	if err != nil || replayed || p.PlanRevision != 1 || p.Total != 2 || p.Verified != 0 {
		t.Fatalf("plan = %+v replayed=%v err=%v", p, replayed, err)
	}
	if again, rep, err := s.SetGoalPlan("p1", g.ID, twoStepPlan(), "first", 0); err != nil || !rep || again.PlanRevision != 1 {
		t.Fatalf("replay = %+v %v %v", again, rep, err)
	}
	// A stale editor is refused; a first plan cannot overwrite an existing one.
	if _, _, err := s.SetGoalPlan("p2", g.ID, twoStepPlan(), "x", 0); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale err = %v", err)
	}

	bad := map[string]GoalPlanInput{
		"no milestones":  {SuccessCriteria: "x"},
		"duplicate ids":  {Milestones: []Milestone{{ID: "m1", Title: "a"}, {ID: "m1", Title: "b"}}},
		"unknown dep":    {Milestones: []Milestone{{ID: "m1", Title: "a", DependsOn: []string{"zz"}}}},
		"forward dep":    {Milestones: []Milestone{{ID: "m1", Title: "a", DependsOn: []string{"m2"}}, {ID: "m2", Title: "b"}}},
		"empty title":    {Milestones: []Milestone{{ID: "m1", Title: "  "}}},
		"coding no repo": {Milestones: []Milestone{{ID: "m1", Title: "a", Task: &MilestoneTask{Kind: KindCoding, Title: "c", Brief: "b"}}}},
		"bad kind":       {Milestones: []Milestone{{ID: "m1", Title: "a", Task: &MilestoneTask{Kind: "magic", Title: "c", Brief: "b"}}}},
		"task no brief":  {Milestones: []Milestone{{ID: "m1", Title: "a", Task: &MilestoneTask{Kind: KindResearch, Title: "c"}}}},
		"too many":       {Milestones: make([]Milestone, MaxMilestones+1)},
	}
	for i := range bad["too many"].Milestones {
		bad["too many"].Milestones[i] = Milestone{ID: fmt.Sprintf("m%d", i+1), Title: "x"}
	}
	for name, in := range bad {
		if _, _, err := s.SetGoalPlan("", g.ID, in, "r", 1); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}

	// Replans are bounded: the sixth revision after the first is refused.
	rev := 1
	for i := 0; i < MaxReplans; i++ {
		in := twoStepPlan()
		in.NextAction = fmt.Sprintf("round %d", i)
		got, _, err := s.SetGoalPlan("", g.ID, in, fmt.Sprintf("replan %d", i), rev)
		if err != nil {
			t.Fatalf("replan %d: %v", i, err)
		}
		rev = got.PlanRevision
	}
	if _, _, err := s.SetGoalPlan("", g.ID, twoStepPlan(), "one too many", rev); !errors.Is(err, ErrReplanLimit) {
		t.Fatalf("limit err = %v", err)
	}
	cur, _ := s.GetGoalPlan(g.ID)
	if cur.PlanRevision != MaxReplans+1 || cur.Replans != MaxReplans || len(cur.Revisions) != MaxReplans+1 {
		t.Fatalf("plan after limit = rev %d replans %d revisions %d", cur.PlanRevision, cur.Replans, len(cur.Revisions))
	}
	if kinds := factKinds(t, s, g.ID); kinds[0] != "planned" || kinds[1] != "replanned" {
		t.Fatalf("facts = %v", kinds)
	}
}

func TestSupervisorDelegatesVerifiesAndOnlyThenCompletes(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	g, _, _ := s.CreateGoal("g1", CreateGoalInput{AssistantID: a.ID, Title: "Ship the report"})
	_, _, _ = s.SetGoalPlan("p1", g.ID, twoStepPlan(), "first", 0)
	sv := &Supervisor{Store: s}

	// A draft Goal is a proposal: the supervisor delegates nothing for it.
	if r, err := sv.Tick(); err != nil || r.Dispatched != 0 {
		t.Fatalf("draft tick = %+v %v", r, err)
	}
	g, _, _ = s.ApproveGoal("ap", g.ID, 1)

	r, err := sv.Tick()
	if err != nil || r.Dispatched != 1 {
		t.Fatalf("tick 1 = %+v %v", r, err)
	}
	tasks, _ := s.ListTasks(a.ID)
	if len(tasks) != 1 || tasks[0].GoalID != g.ID || tasks[0].State != TaskQueued {
		t.Fatalf("tasks = %+v", tasks)
	}
	// m2 waits for m1; ticking again neither duplicates nor jumps ahead.
	if r, _ := sv.Tick(); r.Dispatched != 0 {
		t.Fatalf("tick 2 dispatched %d", r.Dispatched)
	}
	plan, _ := s.GetGoalPlan(g.ID)
	if plan.Milestones[0].State != MilestoneWorking || plan.Milestones[1].State != MilestonePending || plan.State != "working" {
		t.Fatalf("plan states = %s/%s %s", plan.Milestones[0].State, plan.Milestones[1].State, plan.State)
	}

	finishTaskAs(t, s, tasks[0].ID, RunDone)
	r, _ = sv.Tick()
	if r.Dispatched != 1 || r.Completed != 0 {
		t.Fatalf("tick 3 = %+v", r)
	}
	if gg, _ := s.GetGoal(g.ID); gg.State != GoalActive {
		t.Fatalf("goal completed with a milestone open: %s", gg.State)
	}

	tasks, _ = s.ListTasks(a.ID)
	var second Task
	for _, tk := range tasks {
		if tk.Title == "Write report" {
			second = tk
		}
	}
	finishTaskAs(t, s, second.ID, RunDone)
	r, _ = sv.Tick()
	if r.Completed != 1 {
		t.Fatalf("tick 4 = %+v", r)
	}
	done, _ := s.GetGoal(g.ID)
	if done.State != GoalCompleted || !strings.Contains(done.Evidence, "Collect numbers") || !strings.Contains(done.Evidence, "Write report") {
		t.Fatalf("goal = %s evidence=%q", done.State, done.Evidence)
	}
	want := []string{"planned", "accepted", "delegated", "verified", "delegated", "verified", "done"}
	if got := factKinds(t, s, g.ID); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("facts = %v want %v", got, want)
	}
	// Idempotent: more ticks add nothing.
	before, _ := s.Facts("", g.ID, 100)
	for i := 0; i < 3; i++ {
		_, _ = sv.Tick()
	}
	if after, _ := s.Facts("", g.ID, 100); len(after) != len(before) {
		t.Fatalf("facts grew %d → %d", len(before), len(after))
	}
}

func TestSupervisorReportsFailureAndNeverInventsProgress(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	g := activeGoalWithPlan(t, s, a, twoStepPlan())
	sv := &Supervisor{Store: s}
	_, _ = sv.Tick()
	tasks, _ := s.ListTasks(a.ID)
	finishTaskAs(t, s, tasks[0].ID, RunFailed)
	_, _ = sv.Tick()
	plan, _ := s.GetGoalPlan(g.ID)
	if plan.Milestones[0].State != MilestoneBlocked || plan.State != "blocked" || plan.Verified != 0 {
		t.Fatalf("plan = %s %s verified=%d", plan.Milestones[0].State, plan.State, plan.Verified)
	}
	kinds := factKinds(t, s, g.ID)
	if kinds[len(kinds)-1] != "blocked" {
		t.Fatalf("facts = %v", kinds)
	}
	// The dependent milestone is never delegated behind a failed one.
	if all, _ := s.ListTasks(a.ID); len(all) != 1 {
		t.Fatalf("tasks = %d", len(all))
	}
	// Facts carry ids and titles only — no error text from the run.
	fs, _ := s.Facts("", g.ID, 50)
	for _, f := range fs {
		if strings.Contains(f.Text, "note") || strings.Contains(f.Evidence, "note") {
			t.Fatalf("fact leaks run text: %+v", f)
		}
	}
	// A replan after the failure sends the milestone out again as a new task.
	in := twoStepPlan()
	in.Milestones[0].Task = &MilestoneTask{Kind: KindResearch, Title: "Collect numbers (retry)", Brief: "Try another source."}
	if _, _, err := s.SetGoalPlan("rp", g.ID, in, "source failed", plan.PlanRevision); err != nil {
		t.Fatal(err)
	}
	if r, _ := sv.Tick(); r.Dispatched != 1 {
		t.Fatalf("replan did not delegate: %+v", r)
	}
}

func TestSupervisorTaskBudgetHoldsOnce(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	in := twoStepPlan()
	in.Milestones[1].DependsOn = nil // both ready at once
	g := activeGoalWithPlan(t, s, a, in)
	if err := s.setPlanLimits(g.ID, 1, 0); err != nil {
		t.Fatal(err)
	}
	sv := &Supervisor{Store: s}
	for i := 0; i < 3; i++ {
		_, _ = sv.Tick()
	}
	if tasks, _ := s.ListTasks(a.ID); len(tasks) != 1 {
		t.Fatalf("tasks = %d", len(tasks))
	}
	held := 0
	for _, k := range factKinds(t, s, g.ID) {
		if k == "held" {
			held++
		}
	}
	if held != 1 {
		t.Fatalf("held facts = %d", held)
	}
}

func TestSupervisorFanOutIsBounded(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	var ms []Milestone
	for i := 1; i <= 5; i++ {
		ms = append(ms, Milestone{ID: fmt.Sprintf("m%d", i), Title: "x", Task: &MilestoneTask{Kind: KindResearch, Title: fmt.Sprintf("T%d", i), Brief: "b"}})
	}
	activeGoalWithPlan(t, s, a, GoalPlanInput{Milestones: ms})
	sv := &Supervisor{Store: s, MaxFanOut: 2}
	_, _ = sv.Tick()
	if tasks, _ := s.ListTasks(a.ID); len(tasks) != 2 {
		t.Fatalf("fan-out tasks = %d", len(tasks))
	}
}

func TestManualMilestoneNeedsAPersonAndRestartDoesNotDuplicate(t *testing.T) {
	s, path := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	g := activeGoalWithPlan(t, s, a, GoalPlanInput{Milestones: []Milestone{
		{ID: "m1", Title: "Gather", Task: &MilestoneTask{Kind: KindResearch, Title: "Gather", Brief: "b"}},
		{ID: "m2", Title: "You read it and decide"},
	}})
	sv := &Supervisor{Store: s}
	_, _ = sv.Tick()
	tasks, _ := s.ListTasks(a.ID)
	finishTaskAs(t, s, tasks[0].ID, RunDone)
	r, _ := sv.Tick()
	if r.Completed != 0 {
		t.Fatalf("completed with a manual milestone: %+v", r)
	}
	plan, _ := s.GetGoalPlan(g.ID)
	if plan.State != "waiting_user" || plan.Milestones[1].State != MilestoneNeedsYou {
		t.Fatalf("plan = %s / %s", plan.State, plan.Milestones[1].State)
	}
	_ = s.Close()

	// A fresh process over the same file derives the same world: no second
	// task, no repeated fact.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	before, _ := s2.Facts("", g.ID, 100)
	sv2 := &Supervisor{Store: s2}
	for i := 0; i < 2; i++ {
		if r, err := sv2.Tick(); err != nil || r.Dispatched != 0 || r.Facts != 0 {
			t.Fatalf("restart tick = %+v %v", r, err)
		}
	}
	after, _ := s2.Facts("", g.ID, 100)
	if len(after) != len(before) {
		t.Fatalf("facts %d → %d after restart", len(before), len(after))
	}
	if all, _ := s2.ListTasks(a.ID); len(all) != 1 {
		t.Fatalf("tasks = %d", len(all))
	}
	ready := 0
	for _, k := range factKinds(t, s2, g.ID) {
		if k == "ready" {
			ready++
		}
	}
	if ready != 1 {
		t.Fatalf("ready facts = %d", ready)
	}
}

func TestStaleGoalSpeaksUpWithBackoff(t *testing.T) {
	clock := &tickClock{t: time.Unix(1_800_000_000, 0)}
	s, _ := openTest(t, WithClock(clock.now))
	a := mustAssistant(t, s, "a1", "Ada")
	g := activeGoalWithPlan(t, s, a, twoStepPlan())
	if err := s.setPlanLimits(g.ID, 0, 10*60*1000); err != nil { // stale after 10 min
		t.Fatal(err)
	}
	sv := &Supervisor{Store: s}
	_, _ = sv.Tick() // delegates m1
	count := func() int {
		n := 0
		for _, k := range factKinds(t, s, g.ID) {
			if k == "stale" {
				n++
			}
		}
		return n
	}
	clock.t = clock.t.Add(5 * time.Minute)
	_, _ = sv.Tick()
	if count() != 0 {
		t.Fatal("stale too early")
	}
	// 11 min, 22 min (2×), 44 min (4×): three reminders at most, then silence.
	for _, m := range []int{6, 11, 22, 60, 200} {
		clock.t = clock.t.Add(time.Duration(m) * time.Minute)
		_, _ = sv.Tick()
		_, _ = sv.Tick()
	}
	if n := count(); n != MaxStaleReminders {
		t.Fatalf("stale reminders = %d, want %d", n, MaxStaleReminders)
	}
}

func TestSupervisorLeavesPausedAndCancelledGoalsAlone(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	g := activeGoalWithPlan(t, s, a, twoStepPlan())
	cur, _ := s.GetGoal(g.ID)
	_, _, _ = s.SetGoalState("pause", g.ID, GoalPaused, cur.Revision)
	sv := &Supervisor{Store: s}
	if r, _ := sv.Tick(); r.Dispatched != 0 {
		t.Fatalf("paused goal dispatched %d", r.Dispatched)
	}
	if tasks, _ := s.ListTasks(a.ID); len(tasks) != 0 {
		t.Fatalf("tasks = %d", len(tasks))
	}
}

// setPlanLimits is a test hook: tighten a plan's task budget / stale window.
func (s *Store) setPlanLimits(goalID string, maxTasks int, staleAfterMS int64) error {
	if maxTasks > 0 {
		if _, err := s.db.Exec(`UPDATE goal_plans SET max_tasks=? WHERE goal_id=?`, maxTasks, goalID); err != nil {
			return err
		}
	}
	if staleAfterMS > 0 {
		if _, err := s.db.Exec(`UPDATE goal_plans SET stale_after_ms=? WHERE goal_id=?`, staleAfterMS, goalID); err != nil {
			return err
		}
	}
	return nil
}

func TestCodingMilestoneWaitsForItsPublisher(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	g := activeGoalWithPlan(t, s, a, GoalPlanInput{Milestones: []Milestone{
		{ID: "m1", Title: "Fix it", Task: &MilestoneTask{Kind: KindCoding, Title: "Fix bug", Brief: "b", RepoPath: "/tmp/repo"}},
	}})
	sv := &Supervisor{Store: s}
	_, _ = sv.Tick()
	tasks, _ := s.ListTasks(a.ID)
	if len(tasks) != 1 || tasks[0].State != TaskDraft {
		t.Fatalf("tasks = %+v", tasks)
	}
	plan, _ := s.GetGoalPlan(g.ID)
	if plan.Milestones[0].State != MilestoneNeedsYou || plan.State != "waiting_user" {
		t.Fatalf("plan = %s %s", plan.Milestones[0].State, plan.State)
	}
	if !strings.Contains(strings.Join(factTexts(t, s, g.ID), "|"), "waits for your approval") {
		t.Fatal("no approval fact")
	}
}

func factTexts(t *testing.T, s *Store, goalID string) []string {
	t.Helper()
	fs, _ := s.Facts("", goalID, 100)
	out := []string{}
	for _, f := range fs {
		out = append(out, f.Text)
	}
	return out
}

func TestCoordinatorBriefExplainsPlans(t *testing.T) {
	p := CoordinatorPrompt(Assistant{ID: "a_1", Name: "Ada"})
	for _, want := range []string{"goal set-plan", "goal plan", "verified", "routine draft", "Never enable"} {
		if !strings.Contains(p, want) {
			t.Errorf("brief lacks %q", want)
		}
	}
}
