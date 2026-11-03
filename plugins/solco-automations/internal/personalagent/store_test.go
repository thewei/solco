package personalagent

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTest(t *testing.T, opts ...Option) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "personal.sqlite")
	s, err := Open(path, opts...)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func mustAssistant(t *testing.T, s *Store, req, name string) Assistant {
	t.Helper()
	a, _, err := s.CreateAssistant(req, CreateAssistantInput{Name: name})
	if err != nil {
		t.Fatalf("create assistant %q: %v", name, err)
	}
	return a
}

func TestOpenRecordsSchemaVersion(t *testing.T) {
	s, path := openTest(t)
	if v, err := s.SchemaVersion(); err != nil || v < 1 {
		t.Fatalf("schema version = %d, %v", v, err)
	}
	// Re-opening an existing database keeps its data and its version.
	mustAssistant(t, s, "r1", "My Assistant")
	_ = s.Close()
	again, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer again.Close()
	list, err := again.ListAssistants()
	if err != nil || len(list) != 1 || list[0].Name != "My Assistant" {
		t.Fatalf("after reopen: %+v, %v", list, err)
	}
}

// SOLCO-A05: the same requestId and body is one Assistant; a different body
// under the same requestId is a conflict, never a second Assistant.
func TestReceiptReplayAndConflict(t *testing.T) {
	s, _ := openTest(t)
	first, replayed, err := s.CreateAssistant("req-1", CreateAssistantInput{Name: "Ada"})
	if err != nil || replayed {
		t.Fatalf("first create: replayed=%v err=%v", replayed, err)
	}
	again, replayed, err := s.CreateAssistant("req-1", CreateAssistantInput{Name: "Ada"})
	if err != nil || !replayed {
		t.Fatalf("replay: replayed=%v err=%v", replayed, err)
	}
	if again.ID != first.ID {
		t.Fatalf("replay returned another assistant: %s vs %s", again.ID, first.ID)
	}
	if _, _, err := s.CreateAssistant("req-1", CreateAssistantInput{Name: "Different"}); !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("different body under the same request id: %v", err)
	}
	list, _ := s.ListAssistants()
	if len(list) != 1 {
		t.Fatalf("assistants = %d, want 1", len(list))
	}
}

// SOLCO-A01: renaming keeps the ID and the things hanging off it.
func TestRenameKeepsIdentityAndEnforcesRevision(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "r1", "Ada")
	task, _, err := s.CreateTask("t1", CreateTaskInput{AssistantID: a.ID, Title: "look", Brief: "look at it", Kind: KindResearch})
	if err != nil {
		t.Fatal(err)
	}
	renamed, _, err := s.RenameAssistant("r2", a.ID, "Grace", a.Revision)
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if renamed.ID != a.ID || renamed.Name != "Grace" || renamed.Revision != a.Revision+1 {
		t.Fatalf("rename result: %+v", renamed)
	}
	got, _ := s.GetTask(task.ID)
	if got.AssistantID != a.ID {
		t.Fatalf("task lost its assistant: %+v", got)
	}
	// SOLCO-A06: a stale revision is refused.
	if _, _, err := s.RenameAssistant("r3", a.ID, "Stale", a.Revision); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale rename: %v", err)
	}
}

// SOLCO-A04: a disabled Assistant accepts no new work.
func TestDisabledAssistantTakesNoNewTasks(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "r1", "Ada")
	if _, _, err := s.SetAssistantStatus("r2", a.ID, StatusDisabled, a.Revision); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.CreateTask("t1", CreateTaskInput{AssistantID: a.ID, Title: "x", Brief: "y", Kind: KindResearch})
	if !errors.Is(err, ErrAssistantDisabled) {
		t.Fatalf("task on a disabled assistant: %v", err)
	}
}

// S21-52da6419d3cb family (core-database-lock): a short lock is waited out; a
// lock that outlasts the wait fails within a bounded time and leaves nothing
// half-written behind.
func TestStoreWaitsOutShortLock(t *testing.T) {
	s, path := openTest(t, WithBusyTimeout(2*time.Second))
	holder, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	conn, err := holder.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(250 * time.Millisecond)
		_, _ = conn.ExecContext(t.Context(), "ROLLBACK")
		_ = conn.Close()
	}()
	began := time.Now()
	if _, _, err := s.CreateAssistant("r1", CreateAssistantInput{Name: "Ada"}); err != nil {
		t.Fatalf("create under a short lock: %v", err)
	}
	if took := time.Since(began); took < 100*time.Millisecond {
		t.Fatalf("returned in %s: it did not wait for the lock", took)
	}
}

func TestStoreLockOutlastingWaitFailsBounded(t *testing.T) {
	s, path := openTest(t, WithBusyTimeout(300*time.Millisecond))
	holder, _ := sql.Open("sqlite", path)
	defer holder.Close()
	conn, _ := holder.Conn(t.Context())
	if _, err := conn.ExecContext(t.Context(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	_, _, err := s.CreateAssistant("r1", CreateAssistantInput{Name: "Ada"})
	took := time.Since(began)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("want ErrBusy, got %v", err)
	}
	if took < 250*time.Millisecond || took > 2*time.Second {
		t.Fatalf("lock wait took %s, want about 300ms", took)
	}
	_, _ = conn.ExecContext(t.Context(), "ROLLBACK")
	_ = conn.Close()
	// Nothing was left half-done: the same request now succeeds exactly once.
	if _, replayed, err := s.CreateAssistant("r1", CreateAssistantInput{Name: "Ada"}); err != nil || replayed {
		t.Fatalf("retry after the lock: replayed=%v err=%v", replayed, err)
	}
	list, _ := s.ListAssistants()
	if len(list) != 1 {
		t.Fatalf("assistants after retry = %d", len(list))
	}
}

// Claiming is the durable step before any Agent is launched: of N racing
// claimers exactly one wins, and the others see the same Run.
func TestClaimTaskExactlyOnce(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "r1", "Ada")
	task, _, err := s.CreateTask("t1", CreateTaskInput{AssistantID: a.ID, Title: "x", Brief: "y", Kind: KindResearch})
	if err != nil {
		t.Fatal(err)
	}
	var won atomic.Int32
	var wg sync.WaitGroup
	runs := make([]Run, 8)
	for i := range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, fresh, err := s.ClaimTask(task.ID, "worker")
			if err != nil {
				t.Errorf("claim %d: %v", i, err)
				return
			}
			if fresh {
				won.Add(1)
			}
			runs[i] = run
		}()
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Fatalf("winners = %d, want 1", won.Load())
	}
	for _, r := range runs {
		if r.ID != runs[0].ID {
			t.Fatalf("claimers saw different runs: %s vs %s", r.ID, runs[0].ID)
		}
	}
	got, _ := s.GetTask(task.ID)
	if got.State != TaskClaimed {
		t.Fatalf("task state = %s", got.State)
	}
}

// S21-27ec9a665622 / -26f951c53c8d / -0524107496f5 (durable receipt before the
// side effect): a claim that survives a crash is never launched a second time.
func TestDurableReceiptBeforeLaunch(t *testing.T) {
	s, path := openTest(t)
	a := mustAssistant(t, s, "r1", "Ada")
	task, _, _ := s.CreateTask("t1", CreateTaskInput{AssistantID: a.ID, Title: "x", Brief: "y", Kind: KindResearch})
	run, fresh, err := s.ClaimTask(task.ID, "worker")
	if err != nil || !fresh {
		t.Fatalf("claim: fresh=%v err=%v", fresh, err)
	}
	if err := s.MarkStarting(run.ID); err != nil {
		t.Fatal(err)
	}
	// "Crash": the process dies between the claim and the launch result.
	_ = s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	recovered, err := s2.Recover()
	if err != nil || recovered != 1 {
		t.Fatalf("recover = %d, %v", recovered, err)
	}
	got, _ := s2.GetRun(run.ID)
	if got.Status != RunUncertain {
		t.Fatalf("run status after a crash = %s, want uncertain", got.Status)
	}
	gt, _ := s2.GetTask(task.ID)
	if gt.State != TaskUncertain {
		t.Fatalf("task state after a crash = %s, want uncertain", gt.State)
	}
	// A second claim never creates a new Run for an uncertain task.
	again, fresh, err := s2.ClaimTask(task.ID, "worker")
	if err == nil && fresh {
		t.Fatalf("uncertain task was claimed again: %+v", again)
	}
}

func TestRecordLaunchStoresRealIdentity(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "r1", "Ada")
	task, _, _ := s.CreateTask("t1", CreateTaskInput{AssistantID: a.ID, Title: "x", Brief: "y", Kind: KindResearch})
	run, _, _ := s.ClaimTask(task.ID, "worker")
	_ = s.MarkStarting(run.ID)
	if err := s.RecordLaunch(run.ID, Session{HerdrSession: "default", WorkspaceID: "w1", PaneID: "w1:p1"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetRun(run.ID)
	if got.Status != RunRunning || got.PaneID != "w1:p1" || got.WorkspaceID != "w1" || got.HerdrSession != "default" {
		t.Fatalf("run after launch: %+v", got)
	}
	gt, _ := s.GetTask(task.ID)
	if gt.State != TaskRunning {
		t.Fatalf("task = %s", gt.State)
	}
	if err := s.FinishRun(run.ID, RunDone, "all fine"); err != nil {
		t.Fatal(err)
	}
	gt, _ = s.GetTask(task.ID)
	got, _ = s.GetRun(run.ID)
	if gt.State != TaskDone || got.Status != RunDone || got.Result != "all fine" {
		t.Fatalf("after finish: task=%s run=%+v", gt.State, got)
	}
}

// SOLCO-E03: an upgrade that dies half-way leaves the database at the version it
// had (each migration is one transaction) with every row intact, and the next
// start, with a working migration, finishes the job.
func TestAnInterruptedUpgradeRollsBackAndTheNextStartFinishesIt(t *testing.T) {
	s, path := openTest(t)
	a := mustAssistant(t, s, "r-up", "Ada")
	before, _ := s.SchemaVersion()
	_ = s.Close()

	good := migrations
	t.Cleanup(func() { migrations = good })
	migrations = append(append([]string{}, good...),
		`CREATE TABLE half_done (id TEXT); INSERT INTO nope_missing VALUES (1);`)
	if _, err := Open(path); err == nil {
		t.Fatal("a failing migration must fail Open")
	}

	migrations = good
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after the failed upgrade: %v", err)
	}
	defer s2.Close()
	if v, _ := s2.SchemaVersion(); v != before {
		t.Fatalf("version moved to %d after a rolled-back upgrade (was %d)", v, before)
	}
	if _, err := s2.db.Exec(`SELECT 1 FROM half_done`); err == nil {
		t.Fatal("the failed migration left a table behind")
	}
	if got, err := s2.GetAssistant(a.ID); err != nil || got.Name != "Ada" {
		t.Fatalf("rows lost across the failed upgrade: %+v %v", got, err)
	}

	migrations = append(append([]string{}, good...), `CREATE TABLE later_ok (id TEXT)`)
	s3, err := Open(path)
	if err != nil {
		t.Fatalf("finishing the upgrade: %v", err)
	}
	defer s3.Close()
	if v, _ := s3.SchemaVersion(); v != before+1 {
		t.Fatalf("version = %d, want %d", v, before+1)
	}
}

// Identity is never reused: every Assistant/Task id is a fresh opaque value
// (no per-prefix counters to rewind), a request replay returns the same id and
// allocates nothing, and a renamed Assistant keeps its id.
// (Shepherdr core-ids: "renamed/replaced machines and deleted tasks never reuse an ID".)
func TestIdentityIsOpaqueAndNeverReused(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a", "Ada")
	seen := map[string]bool{a.ID: true}
	for i := range 150 {
		task, replay, err := s.CreateTask("req-"+strconv.Itoa(i), CreateTaskInput{AssistantID: a.ID, Title: "t", Brief: "b", Kind: KindResearch})
		if err != nil || replay {
			t.Fatalf("task %d: replay=%v err=%v", i, replay, err)
		}
		if !strings.HasPrefix(task.ID, "t_") || seen[task.ID] {
			t.Fatalf("task id %q reused or malformed", task.ID)
		}
		seen[task.ID] = true
	}
	again, replay, err := s.CreateTask("req-7", CreateTaskInput{AssistantID: a.ID, Title: "t", Brief: "b", Kind: KindResearch})
	if err != nil || !replay || !seen[again.ID] {
		t.Fatalf("a replayed request must return its original task: %+v replay=%v err=%v", again, replay, err)
	}
	if tasks, _ := s.ListTasks(a.ID); len(tasks) != 150 {
		t.Fatalf("a replay allocated a new task: %d", len(tasks))
	}
	b := mustAssistant(t, s, "b", "Bob")
	if seen[b.ID] {
		t.Fatal("assistant id reused")
	}
}
