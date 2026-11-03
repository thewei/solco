package personalagent

import (
	"errors"
	"testing"
)

func mustGoal(t *testing.T, s *Store, req, assistant, title string) Goal {
	t.Helper()
	g, _, err := s.CreateGoal(req, CreateGoalInput{AssistantID: assistant, Title: title, Objective: "Keep " + title + " moving"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	return g
}

// SOLCO-A02: a personal Goal needs no repository and no Workspace.
func TestGoalNeedsNoRepoOrWorkspace(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	g := mustGoal(t, s, "g1", a.ID, "Plan the trip")
	if g.State != GoalDraft || g.AssistantID != a.ID || g.Revision != 1 {
		t.Fatalf("goal = %+v", g)
	}
}

// Draft → approve → active; an Agent's turn ending never completes a Goal
// (SOLCO-W05): completing is an explicit command that names its evidence.
func TestGoalLifecycleAndExplicitCompletion(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	g := mustGoal(t, s, "g1", a.ID, "Watch PR 12")

	if _, _, err := s.SetGoalState("p0", g.ID, GoalPaused, g.Revision); !errors.Is(err, ErrBadState) {
		t.Fatalf("pausing a draft must be refused, got %v", err)
	}
	g, _, err := s.ApproveGoal("ap1", g.ID, g.Revision)
	if err != nil || g.State != GoalActive || g.Revision != 2 {
		t.Fatalf("approve: %+v %v", g, err)
	}
	g, _, err = s.SetGoalState("p1", g.ID, GoalPaused, g.Revision)
	if err != nil || g.State != GoalPaused {
		t.Fatalf("pause: %+v %v", g, err)
	}
	g, _, err = s.SetGoalState("p2", g.ID, GoalActive, g.Revision)
	if err != nil || g.State != GoalActive {
		t.Fatalf("resume: %+v %v", g, err)
	}

	// A task finishing does not touch the Goal.
	task, _, err := s.CreateTask("t1", CreateTaskInput{AssistantID: a.ID, GoalID: g.ID, Kind: KindResearch, Title: "Check", Brief: "check"})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.ClaimTask(task.ID, "d")
	if err != nil {
		t.Fatal(err)
	}
	_ = s.MarkStarting(run.ID)
	_ = s.RecordLaunch(run.ID, Session{WorkspaceID: "w1", PaneID: "w1:p1"})
	_ = s.FinishRun(run.ID, RunDone, "")
	if g2, _ := s.GetGoal(g.ID); g2.State != GoalActive {
		t.Fatalf("an Agent finishing a turn completed the goal: %s", g2.State)
	}

	if _, _, err := s.CompleteGoal("c0", g.ID, g.Revision, "  "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("completion without evidence must be refused, got %v", err)
	}
	g, _, err = s.CompleteGoal("c1", g.ID, g.Revision, "PR 12 merged at abc123")
	if err != nil || g.State != GoalCompleted || g.Evidence == "" {
		t.Fatalf("complete: %+v %v", g, err)
	}
	if _, _, err := s.SetGoalState("p3", g.ID, GoalActive, g.Revision); !errors.Is(err, ErrBadState) {
		t.Fatalf("a completed goal must stay completed, got %v", err)
	}
}

// SOLCO-A06 for goals: two phones editing with the same expectedRevision.
func TestGoalStaleRevisionIs409(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	g := mustGoal(t, s, "g1", a.ID, "G")
	if _, _, err := s.ApproveGoal("phone-a", g.ID, g.Revision); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetGoalState("phone-b", g.ID, GoalCancelled, g.Revision); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("second writer must see stale_revision, got %v", err)
	}
}

// Commitments live in the Goal and follow its state.
func TestCommitmentsFollowTheGoal(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	g, _, err := s.CreateGoal("g1", CreateGoalInput{AssistantID: a.ID, Title: "PR", Objective: "watch", Commitments: []CommitmentInput{
		{Kind: "pr_watch", SuccessCriteria: "PR merged", ExitCondition: "merged or closed", Spec: `{"repo":"o/r","number":12}`},
	}})
	if err != nil || len(g.Commitments) != 1 || g.Commitments[0].State != GoalDraft {
		t.Fatalf("goal = %+v %v", g, err)
	}
	g, _, _ = s.ApproveGoal("ap", g.ID, g.Revision)
	if g.Commitments[0].State != GoalActive {
		t.Fatalf("commitment = %s", g.Commitments[0].State)
	}
	g, _, _ = s.SetGoalState("pa", g.ID, GoalPaused, g.Revision)
	if g.Commitments[0].State != GoalPaused {
		t.Fatalf("commitment = %s", g.Commitments[0].State)
	}
}

// SOLCO-A03: a task cannot hang off another Assistant's Goal.
func TestTaskCannotBorrowAnotherAssistantsGoal(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	b := mustAssistant(t, s, "a2", "Bo")
	g := mustGoal(t, s, "g1", a.ID, "Ada's")
	_, _, err := s.CreateTask("t1", CreateTaskInput{AssistantID: b.ID, GoalID: g.ID, Kind: KindResearch, Title: "x", Brief: "x"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want invalid, got %v", err)
	}
	list, _ := s.ListGoals(b.ID)
	if len(list) != 0 {
		t.Fatalf("Bo sees Ada's goals: %+v", list)
	}
}

// A coding task needs a repository to branch from; research and manual do not.
func TestCodingTaskRequiresARepo(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	if _, _, err := s.CreateTask("t1", CreateTaskInput{AssistantID: a.ID, Kind: KindCoding, Title: "Fix", Brief: "fix"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want invalid, got %v", err)
	}
	if _, _, err := s.CreateTask("t2", CreateTaskInput{AssistantID: a.ID, Kind: KindCoding, Title: "Fix", Brief: "fix", RepoPath: "/tmp/x"}); err != nil {
		t.Fatal(err)
	}
}

// W04: a paused Goal's queued tasks are not started.
func TestPausedGoalHoldsItsTasks(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	g := mustGoal(t, s, "g1", a.ID, "G")
	g, _, _ = s.ApproveGoal("ap", g.ID, g.Revision)
	task, _, _ := s.CreateTask("t1", CreateTaskInput{AssistantID: a.ID, GoalID: g.ID, Kind: KindResearch, Title: "x", Brief: "x"})
	g, _, _ = s.SetGoalState("pa", g.ID, GoalPaused, g.Revision)
	q, _ := s.QueuedTasks()
	if len(q) != 0 {
		t.Fatalf("queued while paused: %+v", q)
	}
	if _, _, err := s.ClaimTask(task.ID, "d"); !errors.Is(err, ErrNotClaimable) {
		t.Fatalf("claim under a paused goal: %v", err)
	}
	_, _, _ = s.SetGoalState("re", g.ID, GoalActive, g.Revision)
	if q, _ := s.QueuedTasks(); len(q) != 1 {
		t.Fatalf("not queued after resume: %+v", q)
	}
}
