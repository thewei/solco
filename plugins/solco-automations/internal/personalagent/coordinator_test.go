package personalagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func mkTask(t *testing.T, s *Store, req, asst, title string, deps ...string) Task {
	t.Helper()
	task, _, err := s.CreateTask(req, CreateTaskInput{AssistantID: asst, Title: title, Brief: "do " + title, Kind: KindResearch, DependsOn: deps})
	if err != nil {
		t.Fatalf("create %s: %v", title, err)
	}
	return task
}

// E01: a task with unmet dependencies waits, and nothing claims it.
func TestDependenciesGateTheQueue(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	first := mkTask(t, s, "t1", a.ID, "first")
	second := mkTask(t, s, "t2", a.ID, "second", first.ID)
	if second.State != TaskWaiting || first.State != TaskQueued {
		t.Fatalf("states = %s / %s", first.State, second.State)
	}
	queued, _ := s.QueuedTasks()
	if len(queued) != 1 || queued[0].ID != first.ID {
		t.Fatalf("queued = %+v", queued)
	}
	if _, _, err := s.ClaimTask(second.ID, "x"); !errors.Is(err, ErrNotClaimable) {
		t.Fatalf("claiming a waiting task: %v", err)
	}
	if n, _ := s.Promote(); n != 0 {
		t.Fatalf("promoted %d with the dependency unfinished", n)
	}

	run, _, _ := s.ClaimTask(first.ID, "x")
	_ = s.MarkStarting(run.ID)
	_ = s.RecordLaunch(run.ID, Session{PaneID: "p"})
	_ = s.FinishRun(run.ID, RunDone, "ok")
	if n, err := s.Promote(); err != nil || n != 1 {
		t.Fatalf("promote = %d %v", n, err)
	}
	if got, _ := s.GetTask(second.ID); got.State != TaskQueued {
		t.Fatalf("second = %s", got.State)
	}
	// Promoting again changes nothing.
	if n, _ := s.Promote(); n != 0 {
		t.Fatalf("second promote = %d", n)
	}
}

func TestFailedDependencyFlagsTheWaiterAndRecoversOnRetry(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	first := mkTask(t, s, "t1", a.ID, "first")
	second := mkTask(t, s, "t2", a.ID, "second", first.ID)
	run, _, _ := s.ClaimTask(first.ID, "x")
	_ = s.MarkStarting(run.ID)
	_ = s.FinishRun(run.ID, RunFailed, "boom")
	if _, err := s.Promote(); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetTask(second.ID)
	if got.State != TaskWaiting || !got.NeedsYou {
		t.Fatalf("a task behind a failed dependency must wait and ask for a person: %+v", got)
	}
	// The person retries the dependency and it succeeds: the waiter moves on by itself.
	f, _ := s.GetTask(first.ID)
	if _, _, err := s.RetryTask("retry", first.ID, f.Revision); err != nil {
		t.Fatal(err)
	}
	run2, _, _ := s.ClaimTask(first.ID, "x")
	_ = s.MarkStarting(run2.ID)
	_ = s.RecordLaunch(run2.ID, Session{PaneID: "p"})
	_ = s.FinishRun(run2.ID, RunDone, "ok")
	if n, _ := s.Promote(); n != 1 {
		t.Fatalf("promote = %d", n)
	}
	if got, _ := s.GetTask(second.ID); got.State != TaskQueued || got.NeedsYou {
		t.Fatalf("second = %+v", got)
	}
}

func TestDependencyRulesRefuseCyclesStrangersAndSelf(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	b := mustAssistant(t, s, "a2", "Bob")
	x := mkTask(t, s, "x", a.ID, "x")
	y := mkTask(t, s, "y", a.ID, "y", x.ID)
	other := mkTask(t, s, "o", b.ID, "other")

	// x → y would close a loop (y already waits for x).
	x, _ = s.GetTask(x.ID)
	if _, _, err := s.AddDependencies("c1", x.ID, []string{y.ID}, x.Revision); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cycle: %v", err)
	}
	if _, _, err := s.AddDependencies("c2", x.ID, []string{x.ID}, x.Revision); !errors.Is(err, ErrInvalid) {
		t.Fatalf("self: %v", err)
	}
	if _, _, err := s.AddDependencies("c3", x.ID, []string{other.ID}, x.Revision); !errors.Is(err, ErrInvalid) {
		t.Fatalf("another assistant's task: %v", err)
	}
	if _, _, err := s.AddDependencies("c4", x.ID, []string{"t_nope"}, x.Revision); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown task: %v", err)
	}
	if _, err := s.db.Exec(`SELECT 1`); err != nil {
		t.Fatal(err)
	}
	// A stale revision is refused; a new task cannot depend across assistants either.
	if _, _, err := s.AddDependencies("c5", x.ID, []string{}, x.Revision+5); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale: %v", err)
	}
	if _, _, err := s.CreateTask("bad", CreateTaskInput{AssistantID: a.ID, Title: "z", Brief: "z", Kind: KindResearch, DependsOn: []string{other.ID}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("create across assistants: %v", err)
	}
	// A running task cannot be pushed behind something new.
	run, _, _ := s.ClaimTask(x.ID, "w")
	_ = run
	x2, _ := s.GetTask(x.ID)
	z := mkTask(t, s, "z2", a.ID, "z2")
	if _, _, err := s.AddDependencies("c6", x2.ID, []string{z.ID}, x2.Revision); !errors.Is(err, ErrBadState) {
		t.Fatalf("adding a dependency to a claimed task: %v", err)
	}
}

// A coordinator drafts; a person (or the coordinator's approved rule) publishes.
func TestDraftsArePublishedOnPurpose(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	first := mkTask(t, s, "t1", a.ID, "first")
	d, _, err := s.CreateTask("d", CreateTaskInput{AssistantID: a.ID, Title: "draft", Brief: "b", Kind: KindResearch, Draft: true, DependsOn: []string{first.ID}})
	if err != nil || d.State != TaskDraft {
		t.Fatalf("draft = %+v %v", d, err)
	}
	if n, _ := s.Promote(); n != 0 {
		t.Fatal("a draft must never be promoted by itself")
	}
	if _, _, err := s.PublishTask("p0", d.ID, d.Revision+1); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale publish: %v", err)
	}
	p, _, err := s.PublishTask("p1", d.ID, d.Revision)
	if err != nil || p.State != TaskWaiting {
		t.Fatalf("publish = %+v %v (its dependency is unfinished)", p, err)
	}
	if _, _, err := s.PublishTask("p2", first.ID, 1); !errors.Is(err, ErrBadState) {
		t.Fatalf("publishing a queued task: %v", err)
	}
}

func TestTaskGraphShowsParentsChildrenAndDependencies(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	parent := mkTask(t, s, "p", a.ID, "epic")
	c1, _, _ := s.CreateTask("c1", CreateTaskInput{AssistantID: a.ID, Title: "c1", Brief: "b", Kind: KindResearch, ParentTaskID: parent.ID})
	c2, _, _ := s.CreateTask("c2", CreateTaskInput{AssistantID: a.ID, Title: "c2", Brief: "b", Kind: KindResearch, ParentTaskID: parent.ID, DependsOn: []string{c1.ID}})
	if _, _, err := s.CreateTask("c3", CreateTaskInput{AssistantID: a.ID, Title: "c3", Brief: "b", Kind: KindResearch, ParentTaskID: "t_nope"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown parent: %v", err)
	}
	g, err := s.TaskGraph(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]TaskNode{}
	for _, n := range g {
		by[n.Task.ID] = n
	}
	if len(by[parent.ID].Children) != 2 || by[parent.ID].ChildrenDone != 0 || len(by[c2.ID].DependsOn) != 1 || by[c2.ID].DependsOn[0] != c1.ID {
		t.Fatalf("graph = %+v", by)
	}
}

// E01: each Assistant has its own Coordinator — a normal Herdr Agent with a
// persistent identity — that sees only its own memory and session.
func TestCoordinatorIsPerAssistantAndIsolated(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	b := mustAssistant(t, s, "a2", "Bob")
	approvedMem(t, s, "ma", a.ID, "Ada prefers terse status reports")
	approvedMem(t, s, "mb", b.ID, "Bob owns the billing migration")

	if c, err := s.GetCoordinator(a.ID); err != nil || c.State != CoordinatorNone {
		t.Fatalf("before start = %+v %v", c, err)
	}
	ca, replayed, err := s.StartCoordinator("sa", a.ID)
	if err != nil || replayed || ca.State != CoordinatorStarting || ca.TaskID == "" {
		t.Fatalf("start a = %+v %v", ca, err)
	}
	again, _, err := s.StartCoordinator("sa2", a.ID)
	if err != nil || again.TaskID != ca.TaskID {
		t.Fatalf("a second start must find the same coordinator: %+v %v", again, err)
	}
	cb, _, err := s.StartCoordinator("sb", b.ID)
	if err != nil || cb.TaskID == ca.TaskID {
		t.Fatalf("start b = %+v %v", cb, err)
	}

	fl := &fakeLauncher{}
	d := &Dispatcher{Store: s, Launcher: fl, Claimant: "t"}
	// Each coordinator launches as its own Agent, with only its own memory.
	for i, id := range []string{ca.TaskID, cb.TaskID} {
		fl.provisionedAs = Session{HerdrSession: "default", WorkspaceID: "w" + string(rune('1'+i)), PaneID: "w" + string(rune('1'+i)) + ":p1", AgentSessionID: "sess-" + string(rune('a'+i))}
		fl.workGate = make(chan struct{})
		done := make(chan struct{})
		go func(id string) {
			defer close(done)
			_, _ = d.RunTask(context.Background(), id)
		}(id)
		waitFor(t, "coordinator "+id+" to be ready", func() bool {
			a1, _ := s.GetTask(id)
			c, _ := s.GetCoordinator(a1.AssistantID)
			return c.State == CoordinatorReady
		})
		close(fl.workGate)
		<-done
	}
	ga, _ := s.GetCoordinator(a.ID)
	gb, _ := s.GetCoordinator(b.ID)
	if ga.PaneID == gb.PaneID || ga.AgentSessionID == gb.AgentSessionID || ga.WorkspaceID == gb.WorkspaceID {
		t.Fatalf("coordinators share a session: %+v / %+v", ga, gb)
	}
	if ga.AgentSessionID != "sess-a" || gb.AgentSessionID != "sess-b" {
		t.Fatalf("session refs = %q / %q", ga.AgentSessionID, gb.AgentSessionID)
	}
	if len(fl.specs) != 2 {
		t.Fatalf("specs = %d", len(fl.specs))
	}
	pa, pb := fl.specs[0].Prompt, fl.specs[1].Prompt
	if !strings.Contains(pa, "Ada prefers terse") || strings.Contains(pa, "Bob owns") {
		t.Fatalf("Ada's coordinator prompt leaks or misses memory:\n%s", pa)
	}
	if !strings.Contains(pb, "Bob owns") || strings.Contains(pb, "Ada prefers") {
		t.Fatalf("Bob's coordinator prompt leaks or misses memory:\n%s", pb)
	}
	for _, want := range []string{a.ID, "assistant task create", "draft", "never merge", "dependsOn"} {
		if !strings.Contains(strings.ToLower(pa), strings.ToLower(want)) {
			t.Fatalf("the coordinator brief must mention %q:\n%s", want, pa)
		}
	}
	// The coordinator's own work never lands in Needs You.
	if task, _ := s.GetTask(ca.TaskID); task.NeedsYou {
		t.Fatalf("coordinator task = %+v", task)
	}

	// Disabling one Assistant leaves the other's coordinator alone.
	if _, _, err := s.SetAssistantStatus("dis", a.ID, StatusDisabled, 1); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.GetCoordinator(b.ID); c.State != CoordinatorReady {
		t.Fatalf("b = %+v", c)
	}
	if _, _, err := s.StartCoordinator("sa3", a.ID); !errors.Is(err, ErrAssistantDisabled) {
		t.Fatalf("disabled start: %v", err)
	}
}

func TestCoordinatorStopAndRestart(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	c, _, _ := s.StartCoordinator("s1", a.ID)
	f := &fakeLauncher{}
	d := &Dispatcher{Store: s, Launcher: f, Claimant: "t"}
	if _, err := d.RunTask(context.Background(), c.TaskID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetCoordinator(a.ID); got.State != CoordinatorReady || got.PaneID == "" {
		t.Fatalf("after run = %+v", got)
	}
	stopped, _, err := s.StopCoordinator("st", a.ID)
	if err != nil || stopped.State != CoordinatorStopped {
		t.Fatalf("stop = %+v %v", stopped, err)
	}
	restarted, _, err := s.StartCoordinator("s2", a.ID)
	if err != nil || restarted.State != CoordinatorStarting || restarted.TaskID == c.TaskID {
		t.Fatalf("restart = %+v %v", restarted, err)
	}
	// A coordinator whose launch fails is failed, and starting again is allowed.
	f.provisionErr = errors.New("herdr down")
	if _, err := d.RunTask(context.Background(), restarted.TaskID); err == nil {
		t.Fatal("expected the launch to fail")
	}
	if got, _ := s.GetCoordinator(a.ID); got.State != CoordinatorFailed {
		t.Fatalf("after failed launch = %+v", got)
	}
	// Nobody can create a coordinator task through the ordinary door.
	if _, _, err := s.CreateTask("x", CreateTaskInput{AssistantID: a.ID, Title: "c", Brief: "b", Kind: KindCoordinator}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("creating a coordinator task directly: %v", err)
	}
}

func TestAPICoordinatorAndTaskGraphThroughTheCLI(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	api := &API{Store: s}
	if got, err := call(t, api, ``, "assistant", "coordinator", "show", a.ID); err != nil || got["coordinator"].(map[string]any)["state"] != CoordinatorNone {
		t.Fatalf("show = %v %v", got, err)
	}
	if got, err := call(t, api, `{"requestId":"c1"}`, "assistant", "coordinator", "start", a.ID); err != nil || got["coordinator"].(map[string]any)["state"] != CoordinatorStarting {
		t.Fatalf("start = %v %v", got, err)
	}
	first, err := call(t, api, `{"requestId":"t1","assistantId":"`+a.ID+`","kind":"research","title":"one","brief":"b","draft":true}`, "assistant", "task", "create")
	if err != nil {
		t.Fatal(err)
	}
	t1 := first["task"].(map[string]any)
	second, err := call(t, api, `{"requestId":"t2","assistantId":"`+a.ID+`","kind":"research","title":"two","brief":"b","dependsOn":["`+t1["id"].(string)+`"]}`, "assistant", "task", "create")
	if err != nil || second["task"].(map[string]any)["state"] != TaskWaiting {
		t.Fatalf("second = %v %v", second, err)
	}
	if _, err := call(t, api, `{"requestId":"p1","expectedRevision":1}`, "assistant", "task", "publish", t1["id"].(string)); err != nil {
		t.Fatal(err)
	}
	g, err := call(t, api, ``, "assistant", "task", "graph", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(g["nodes"].([]any)); n != 2 { // the coordinator's own task is plumbing, not a node
		t.Fatalf("nodes = %d: %v", n, g)
	}
}
