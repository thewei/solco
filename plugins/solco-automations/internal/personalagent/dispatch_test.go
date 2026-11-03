package personalagent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeLauncher scripts the Herdr seam and counts every side effect.
type fakeLauncher struct {
	mu            sync.Mutex
	provisions    int
	works         int
	provisionErr  error
	workErr       error
	provisionedAs Session
	found         map[string]Session // marker -> session Herdr already has
	findErr       error
	specs         []LaunchSpec
	workGate      chan struct{}
}

func (f *fakeLauncher) Provision(spec LaunchSpec) (Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.provisions++
	f.specs = append(f.specs, spec)
	if f.provisionErr != nil {
		return Session{}, f.provisionErr
	}
	s := f.provisionedAs
	if s.PaneID == "" {
		s = Session{HerdrSession: "default", WorkspaceID: "w1", PaneID: "w1:p1"}
	}
	return s, nil
}

func (f *fakeLauncher) Work(ctx context.Context, sess Session, spec LaunchSpec) error {
	f.mu.Lock()
	f.works++
	gate := f.workGate
	err := f.workErr
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (f *fakeLauncher) Await(ctx context.Context, sess Session, spec LaunchSpec) error {
	return f.Work(ctx, sess, spec)
}

func (f *fakeLauncher) Find(marker string) (Session, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.findErr != nil {
		return Session{}, false, f.findErr
	}
	s, ok := f.found[marker]
	return s, ok, nil
}

func newQueuedTask(t *testing.T, s *Store, kind string) (Assistant, Task) {
	t.Helper()
	a := mustAssistant(t, s, "a-"+kind, "Ada "+kind)
	task, _, err := s.CreateTask("t-"+kind, CreateTaskInput{AssistantID: a.ID, Title: "Check PR", Brief: "Read the PR and report.", Kind: kind, RepoPath: "/tmp/repo"})
	if err != nil {
		t.Fatal(err)
	}
	return a, task
}

func TestRunTaskHappyPathStoresRealIdentity(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	l := &fakeLauncher{}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon"}

	run, err := d.RunTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if run.Status != RunDone || run.PaneID != "w1:p1" || run.WorkspaceID != "w1" {
		t.Fatalf("run = %+v", run)
	}
	if l.provisions != 1 || l.works != 1 {
		t.Fatalf("side effects: provisions=%d works=%d", l.provisions, l.works)
	}
	got, _ := s.GetTask(task.ID)
	if got.State != TaskDone {
		t.Fatalf("task = %s", got.State)
	}
	// The brief reaches the Agent; the spec carries the run id as the marker.
	spec := l.specs[0]
	if !strings.Contains(spec.Prompt, "Read the PR and report.") || !strings.Contains(spec.Label, run.ID) {
		t.Fatalf("spec = %+v", spec)
	}
}

// A coding task never finishes as plain "done": a person must review it.
func TestCodingTaskEndsInReviewRequired(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindCoding)
	d := &Dispatcher{Store: s, Launcher: &fakeLauncher{}, Claimant: "daemon"}
	if _, err := d.RunTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetTask(task.ID)
	if got.State != TaskReviewRequired || !got.NeedsYou || got.ReviewState != "pending" {
		t.Fatalf("coding task after the agent finished: %+v", got)
	}
}

// S21-2046ff2c9693 (failed starts): a provision error that created nothing is a
// plain failure and the task may be retried deliberately.
func TestProvisionFailureWithNothingCreatedFails(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	l := &fakeLauncher{provisionErr: errors.New("herdr: worktree failed")}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon"}
	run, err := d.RunTask(t.Context(), task.ID)
	if err == nil {
		t.Fatal("expected the failure to be reported")
	}
	if run.Status != RunFailed {
		t.Fatalf("run = %s", run.Status)
	}
	got, _ := s.GetTask(task.ID)
	if got.State != TaskFailed || !got.NeedsYou {
		t.Fatalf("task = %+v", got)
	}
	// Deliberate retry makes a new attempt with a new run.
	retried, _, err := s.RetryTask("retry-1", task.ID, got.Revision)
	if err != nil || retried.State != TaskQueued || retried.Attempt != 2 {
		t.Fatalf("retry = %+v, %v", retried, err)
	}
}

// An error that may have created the pane is reconciled against Herdr by the
// run's marker: found → adopted and carried on, never provisioned twice.
func TestProvisionErrorButPaneExistsIsAdopted(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	l := &fakeLauncher{provisionErr: errors.New("timeout waiting for herdr"), found: map[string]Session{}}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon"}
	l.found = nil
	// Herdr did create it even though the answer was lost.
	d.OnProvisionError = func(run Run) {
		l.found = map[string]Session{run.ID: {HerdrSession: "default", WorkspaceID: "w9", PaneID: "w9:p1"}}
	}

	run, err := d.RunTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if run.PaneID != "w9:p1" || run.Status != RunDone {
		t.Fatalf("adopted run = %+v", run)
	}
	if l.provisions != 1 {
		t.Fatalf("provisioned %d times, want 1", l.provisions)
	}
}

// Herdr cannot be asked either: the outcome is unknown, so the run is
// uncertain and nothing is launched again.
func TestProvisionErrorAndUnknownOutcomeIsUncertain(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	l := &fakeLauncher{provisionErr: errors.New("socket closed"), findErr: errors.New("herdr unreachable")}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon"}
	run, err := d.RunTask(t.Context(), task.ID)
	if !errors.Is(err, ErrUncertain) {
		t.Fatalf("want ErrUncertain, got %v", err)
	}
	if run.Status != RunUncertain {
		t.Fatalf("run = %s", run.Status)
	}
	got, _ := s.GetTask(task.ID)
	if got.State != TaskUncertain || !got.NeedsYou {
		t.Fatalf("task = %+v", got)
	}
	// A second dispatch does nothing: no new Provision, no new Run.
	if _, err := d.RunTask(t.Context(), task.ID); err == nil {
		t.Fatal("an uncertain task must not be run again")
	}
	if l.provisions != 1 {
		t.Fatalf("provisions = %d, want 1", l.provisions)
	}
	runs, _ := s.ListRuns(task.ID)
	if len(runs) != 1 {
		t.Fatalf("runs = %d", len(runs))
	}
}

// A human resolves an uncertain run: the pane was found (adopt) or never
// existed (failed, retry allowed).
func TestResolveUncertain(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	l := &fakeLauncher{provisionErr: errors.New("x"), findErr: errors.New("y")}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon"}
	run, _ := d.RunTask(t.Context(), task.ID)

	// Reconcile while Herdr still cannot answer changes nothing.
	got, err := d.Reconcile(t.Context(), run.ID)
	if err == nil || got.Status != RunUncertain {
		t.Fatalf("reconcile while blind: %+v, %v", got, err)
	}
	// Herdr answers: no such pane → failed, so a retry is allowed.
	l.findErr = nil
	l.found = map[string]Session{}
	got, err = d.Reconcile(t.Context(), run.ID)
	if err != nil || got.Status != RunFailed {
		t.Fatalf("reconcile with nothing there: %+v, %v", got, err)
	}
	gt, _ := s.GetTask(task.ID)
	if gt.State != TaskFailed {
		t.Fatalf("task = %s", gt.State)
	}
}

func TestReconcileFindsPaneAndRecordsIt(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	l := &fakeLauncher{provisionErr: errors.New("x"), findErr: errors.New("y")}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon"}
	run, _ := d.RunTask(t.Context(), task.ID)
	l.findErr = nil
	l.found = map[string]Session{run.ID: {HerdrSession: "default", WorkspaceID: "w7", PaneID: "w7:p1"}}
	got, err := d.Reconcile(t.Context(), run.ID)
	if err != nil || got.Status != RunRunning || got.PaneID != "w7:p1" {
		t.Fatalf("reconcile found: %+v, %v", got, err)
	}
}

func TestBlockedAgentBecomesNeedsYou(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	l := &fakeLauncher{workErr: &BlockedError{Reason: "trust dialog"}}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon"}
	run, err := d.RunTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("a blocked agent is a state, not a failure: %v", err)
	}
	if run.Status != RunBlocked {
		t.Fatalf("run = %s", run.Status)
	}
	got, _ := s.GetTask(task.ID)
	if got.State != TaskBlocked || !got.NeedsYou {
		t.Fatalf("task = %+v", got)
	}
}

func TestCancelledWorkspaceIsNotAFailure(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	d := &Dispatcher{Store: s, Launcher: &fakeLauncher{workErr: ErrWorkCancelled}, Claimant: "daemon"}
	run, _ := d.RunTask(t.Context(), task.ID)
	if run.Status != RunCancelled {
		t.Fatalf("run = %s", run.Status)
	}
	got, _ := s.GetTask(task.ID)
	if got.State != TaskCancelled || got.NeedsYou {
		t.Fatalf("task = %+v", got)
	}
}

// SOLCO-W04: the Assistant is disabled between queueing and the claim.
func TestDisabledBeforeClaimLaunchesNothing(t *testing.T) {
	s, _ := openTest(t)
	a, task := newQueuedTask(t, s, KindResearch)
	if _, _, err := s.SetAssistantStatus("dis", a.ID, StatusDisabled, a.Revision); err != nil {
		t.Fatal(err)
	}
	l := &fakeLauncher{}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon"}
	if _, err := d.RunTask(t.Context(), task.ID); !errors.Is(err, ErrAssistantDisabled) {
		t.Fatalf("err = %v", err)
	}
	if l.provisions != 0 {
		t.Fatalf("provisioned %d times for a disabled assistant", l.provisions)
	}
}

// Pump claims every queued task of an active Assistant, bounded by
// MaxParallel, and leaves the rest queued for the next tick.
func TestPumpBoundsParallelism(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a", "Ada")
	gate := make(chan struct{})
	l := &fakeLauncher{workGate: gate}
	for i := range 4 {
		if _, _, err := s.CreateTask("t"+string(rune('a'+i)), CreateTaskInput{AssistantID: a.ID, Title: "t", Brief: "b", Kind: KindResearch}); err != nil {
			t.Fatal(err)
		}
	}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon", MaxParallel: 2}
	started := d.Pump(t.Context())
	if started != 2 {
		t.Fatalf("started %d, want 2", started)
	}
	deadline := time.After(2 * time.Second)
	for {
		l.mu.Lock()
		w := l.works
		l.mu.Unlock()
		if w == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("works = %d", w)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if again := d.Pump(t.Context()); again != 0 {
		t.Fatalf("pump over the limit started %d", again)
	}
	close(gate)
	d.Wait()
	if again := d.Pump(t.Context()); again != 2 {
		t.Fatalf("second wave started %d, want 2", again)
	}
	d.Wait()
	queued := 0
	tasks, _ := s.ListTasks(a.ID)
	for _, tk := range tasks {
		if tk.State == TaskQueued {
			queued++
		}
	}
	if queued != 0 {
		t.Fatalf("still queued: %d", queued)
	}
}

// The daemon must not relaunch itself (and so drop Agent waits) while a
// personal run is in flight: Busy mirrors runner.Runner.Busy.
func TestBusyWhileAnAgentIsWorking(t *testing.T) {
	s, _ := openTest(t)
	newQueuedTask(t, s, KindResearch)
	gate := make(chan struct{})
	d := &Dispatcher{Store: s, Launcher: &fakeLauncher{workGate: gate}, Claimant: "daemon"}
	if d.Busy() {
		t.Fatal("busy before anything started")
	}
	if n := d.Pump(t.Context()); n != 1 {
		t.Fatalf("pumped %d", n)
	}
	if !d.Busy() {
		t.Fatal("not busy while the Agent works")
	}
	close(gate)
	d.Wait()
	if d.Busy() {
		t.Fatal("still busy after the run ended")
	}
}

// A daemon killed between "workspace opened" and "agent started" leaves a
// running run whose pane holds no agent. Resume must not call that a
// cancellation (the workspace is alive) and must not guess: it parks the run
// for a person.
func TestResumeWithNoAgentInALiveWorkspaceIsUncertain(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	run, _, err := s.ClaimTask(task.ID, "daemon")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkStarting(run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLaunch(run.ID, Session{HerdrSession: "default", WorkspaceID: "w1", PaneID: "w1:p1"}); err != nil {
		t.Fatal(err)
	}
	l := &fakeLauncher{workErr: ErrAgentMissing}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon"}
	if n := d.Resume(t.Context()); n != 1 {
		t.Fatalf("resumed %d", n)
	}
	d.Wait()
	got, _ := s.GetRun(run.ID)
	if got.Status != RunUncertain {
		t.Fatalf("run = %s, want uncertain", got.Status)
	}
	tk, _ := s.GetTask(task.ID)
	if tk.State != TaskUncertain || !tk.NeedsYou {
		t.Fatalf("task = %+v", tk)
	}
}

// Once the daemon is shutting down (context cancelled) a queued task is never
// claimed or launched, even if its dependencies finish meanwhile.
// (Shepherdr core-source: "shutdown prevents queued tasks from receiving go".)
func TestShutdownClaimsAndLaunchesNothing(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a", "Ada")
	task, _, err := s.CreateTask("t1", CreateTaskInput{AssistantID: a.ID, Title: "t", Brief: "b", Kind: KindResearch})
	if err != nil {
		t.Fatal(err)
	}
	l := &fakeLauncher{}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon", MaxParallel: 2}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if n := d.Pump(ctx); n != 0 {
		t.Fatalf("a cancelled daemon started %d task(s)", n)
	}
	d.Wait()
	got, _ := s.GetTask(task.ID)
	if got.State != TaskQueued {
		t.Fatalf("task = %s, want it left queued", got.State)
	}
	if runs, _ := s.ListRuns(task.ID); len(runs) != 0 {
		t.Fatalf("a run was claimed during shutdown: %+v", runs)
	}
}
