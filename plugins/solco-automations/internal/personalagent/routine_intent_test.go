package personalagent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
)

// A real subprocess dies with SIGKILL after the YAML commit and before
// routine.upsert. The parent process reopens the same durable files and must
// recover exactly one Owner approval request without enabling execution.
func TestRoutineSIGKILLAfterYAMLRecoversOnNewProcess(t *testing.T) {
	e := newRoutineEnv(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestRoutineSIGKILLChild$")
	cmd.Env = append(os.Environ(),
		"AUDIT_ROUTINE_DB="+e.s.Path(),
		"AUDIT_ROUTINE_ASSISTANT="+e.a.ID,
		"AUDIT_ROUTINE_REPO="+e.repo)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || !exit.Sys().(syscall.WaitStatus).Signaled() || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("child not killed at intended commit point: err=%v output=%s", err, out)
	}
	pending, err := e.s.pendingRoutineIntents()
	if err != nil || len(pending) != 1 {
		t.Fatalf("durable intent lost after SIGKILL: %+v %v", pending, err)
	}
	if _, err := e.s.GetRoutine("morning-check"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("child wrote SQLite before intended kill: %v", err)
	}
	if err := e.s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(e.s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.RecoverRoutineIntents()
	if err != nil || got != 1 {
		t.Fatalf("post-process recovery: %d %v", got, err)
	}
	r, err := reopened.GetRoutine("morning-check")
	if err != nil || r.State != RoutineDraft || r.Approved {
		t.Fatalf("unexpected routine after SIGKILL: %+v %v", r, err)
	}
	cards, err := reopened.ListApprovals(e.a.ID)
	if err != nil || len(cards) != 1 || cards[0].State != ApprovalPending {
		t.Fatalf("duplicate or missing Owner card: %+v %v", cards, err)
	}
}

func TestRoutineSIGKILLChild(t *testing.T) {
	dbPath := os.Getenv("AUDIT_ROUTINE_DB")
	if dbPath == "" {
		return
	}
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	api := &API{Store: store, ConfigPut: func(original string, x config.Automation) (config.PutResult, error) {
		res, err := config.Put(original, x, false)
		if err != nil || res.Problem != "" {
			t.Fatalf("YAML was not committed before SIGKILL: %+v %v", res, err)
		}
		if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		panic("SIGKILL was not delivered")
	}}
	_, _ = api.routineDraft("sigkill-request", RoutineDraftInput{
		AssistantID: os.Getenv("AUDIT_ROUTINE_ASSISTANT"), Name: "morning-check",
		Cron: "0 9 * * *", Repo: os.Getenv("AUDIT_ROUTINE_REPO"), Agent: "omp",
		Prompt:    "Check the repository for risky changes and summarise.",
		Rationale: "The user asked for a daily check.",
	})
	t.Fatal("child returned without its injected SIGKILL")
}

// Crash injection in tests returns after a durable step to simulate SIGKILL.
// All SQL/YAML files live under TempDir; no actual user data is accessed.
func interruptedRoutineDraft(t *testing.T, afterYAML func(*routineEnv) error) *routineEnv {
	t.Helper()
	e := newRoutineEnv(t)
	e.api.ConfigPut = func(original string, x config.Automation) (config.PutResult, error) {
		pending, err := e.s.pendingRoutineIntents()
		if err != nil || len(pending) != 1 {
			t.Fatalf("intent not durable before YAML: %+v %v", pending, err)
		}
		if afterYAML != nil {
			res, err := config.Put(original, x, false)
			if err != nil || res.Problem != "" {
				t.Fatalf("write before simulated crash: %+v %v", res, err)
			}
			if err := afterYAML(e); err != nil {
				t.Fatal(err)
			}
		}
		return config.PutResult{}, errors.New("injected process death")
	}
	if _, err := e.draft(t, "crash-request", nil); err == nil {
		t.Fatal("simulated crash unexpectedly completed")
	}
	e.api.ConfigPut = nil
	return e
}

func assertPendingIntents(t *testing.T, e *routineEnv, want int) {
	t.Helper()
	got, err := e.s.pendingRoutineIntents()
	if err != nil || len(got) != want {
		t.Fatalf("intents=%+v err=%v, want %d", got, err, want)
	}
}

// If a process died after writing the intent but before touching YAML,
// recovery must discard the no-op journal, not invent an automation.
func TestRoutineRecoveryBeforeYAMLIsNoOp(t *testing.T) {
	e := interruptedRoutineDraft(t, nil)
	assertPendingIntents(t, e, 1)
	n, err := e.s.RecoverRoutineIntents()
	if err != nil || n != 1 {
		t.Fatalf("recovered=%d err=%v", n, err)
	}
	assertPendingIntents(t, e, 0)
	cfg, err := config.Load()
	if err != nil || cfg.Find("morning-check") != nil {
		t.Fatalf("invented YAML routine after no-op: %+v %v", cfg, err)
	}
	if _, err := e.s.GetRoutine("morning-check"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invented SQLite routine: %v", err)
	}
	if _, err := e.draft(t, "crash-request", nil); err != nil {
		t.Fatalf("original requestId cannot be retried after no-op recovery: %v", err)
	}
}

// A process death immediately after YAML replacement is the formerly broken
// case: YAML exists but no SQLite Routine/ledger/card. Recovery must roll
// forward only to a DRAFT + one Owner approval request, never execute it.
func TestRoutineRecoveryAfterYAMLCompletesOneApprovalRequest(t *testing.T) {
	e := interruptedRoutineDraft(t, func(*routineEnv) error { return nil })
	assertPendingIntents(t, e, 1)
	if _, err := e.s.GetRoutine("morning-check"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unexpected routine before recovery: %v", err)
	}
	reserved, err := e.s.LegacyReservations()
	if err != nil || !reserved["morning-check"] {
		t.Fatalf("legacy runner could steal orphan draft: %+v %v", reserved, err)
	}
	n, err := e.s.RecoverRoutineIntents()
	if err != nil || n != 1 {
		t.Fatalf("recover journal=%d err=%v", n, err)
	}
	assertPendingIntents(t, e, 0)
	row, err := e.s.GetRoutine("morning-check")
	if err != nil || row.State != RoutineDraft || row.Approved {
		t.Fatalf("recovered Routine bypassed approval: %+v %v", row, err)
	}
	owned, err := e.s.OwnedLegacy()
	if err != nil || !owned[row.Name] {
		t.Fatalf("recovered Routine not adopted: %+v %v", owned, err)
	}
	cards, err := e.s.ListApprovals(e.a.ID)
	if err != nil || len(cards) != 1 || cards[0].State != ApprovalPending {
		t.Fatalf("recovered approval cards: %+v %v", cards, err)
	}
	again, err := e.s.RecoverRoutineIntents()
	if err != nil || again != 0 {
		t.Fatalf("duplicate recovery: %d %v", again, err)
	}
	replay, err := e.draft(t, "crash-request", nil)
	if err != nil || replay["replayed"] != true {
		t.Fatalf("retry did not replay original durable command: %+v %v", replay, err)
	}
	cards, _ = e.s.ListApprovals(e.a.ID)
	if len(cards) != 1 {
		t.Fatalf("duplicate Owner approval card: %+v", cards)
	}
	rule := &RuleScheduler{Store: e.s, Load: config.Load, Now: func() time.Time { return at(10, 0).Add(24*time.Hour + 5*time.Second) }}
	if report, err := rule.Tick(); err != nil || report.Fired != 0 {
		t.Fatalf("interrupted draft ran before approval: %+v %v", report, err)
	}
}

// Recovery after routine.upsert must replay the receipt, NOT bump the routine
// revision (which would invalidate the pending Owner approval's hash).
func TestRoutineRecoveryAfterSQLiteUpsertKeepsRevision(t *testing.T) {
	e := interruptedRoutineDraft(t, func(e *routineEnv) error {
		pending, _ := e.s.pendingRoutineIntents()
		_, _, err := e.s.UpsertRoutine("crash-request:r", pending[0].Input)
		return err
	})
	before, err := e.s.GetRoutine("morning-check")
	if err != nil || before.Revision != 1 {
		t.Fatalf("intermediate row: %+v %v", before, err)
	}
	if _, err := e.s.RecoverRoutineIntents(); err != nil {
		t.Fatal(err)
	}
	after, _ := e.s.GetRoutine("morning-check")
	if after.Revision != before.Revision {
		t.Fatalf("recovery created unapproved revision %d->%d", before.Revision, after.Revision)
	}
	cards, _ := e.s.ListApprovals(e.a.ID)
	if len(cards) != 1 || cards[0].ExpectedRevision != before.Revision {
		t.Fatalf("invalid pending approval: %+v", cards)
	}
}

// A crash after all three SQLite operations but before the intent is cleared
// must not create another approval card. The action is frozen even when the
// daemon restarts days after the original requested occurrence.
func TestRoutineRecoveryAfterApprovalReceiptIsIdempotent(t *testing.T) {
	e := interruptedRoutineDraft(t, func(e *routineEnv) error {
		pending, _ := e.s.pendingRoutineIntents()
		i := pending[0]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		_, _, _, err = e.s.completeRoutineIntent(i, cfg, nil, e.clk.t)
		return err
	})
	e.clk.t = e.clk.t.Add(72 * time.Hour)
	n, err := e.s.RecoverRoutineIntents()
	if err != nil || n != 1 {
		t.Fatalf("recovered=%d err=%v", n, err)
	}
	cards, _ := e.s.ListApprovals(e.a.ID)
	if len(cards) != 1 {
		t.Fatalf("duplicate cards after delayed recovery: %+v", cards)
	}
	replay, err := e.draft(t, "crash-request", nil)
	if err != nil || replay["replayed"] != true {
		t.Fatalf("frozen action did not survive a later replay: %+v %v", replay, err)
	}
}

// An external edit must not be overwritten with the old intent. While the
// journal is pending, that name remains reserved away from the legacy runner.
func TestRoutineRecoveryRefusesChangedDefinition(t *testing.T) {
	e := interruptedRoutineDraft(t, func(*routineEnv) error { return nil })
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := *cfg.Find("morning-check")
	a.Prompt = "Unreviewed replacement"
	a.Draft = false
	if _, err := config.Put(a.Name, a, false); err != nil {
		t.Fatal(err)
	}
	_, err = e.s.RecoverRoutineIntents()
	if !errors.Is(err, config.ErrDefinitionChanged) {
		t.Fatalf("stale intent silently overwrote YAML: %v", err)
	}
	assertPendingIntents(t, e, 1)
	reserved, err := e.s.LegacyReservations()
	if err != nil || !reserved[a.Name] {
		t.Fatalf("changed orphan draft was exposed to legacy scheduler: %+v %v", reserved, err)
	}
	after, _ := config.Load()
	if after.Find(a.Name).Prompt != "Unreviewed replacement" {
		t.Fatalf("recovery rewrote external modification: %+v", after.Find(a.Name))
	}
}

// A generic config.Approve that runs before crash recovery must not enable
// the draft without the SQLite Owner decision.
func TestRoutineRecoveryRestoresDraftFlag(t *testing.T) {
	e := interruptedRoutineDraft(t, func(*routineEnv) error { return nil })
	if err := config.Approve("morning-check"); err != nil {
		t.Fatal(err)
	}
	if n, err := e.s.RecoverRoutineIntents(); err != nil || n != 1 {
		t.Fatalf("recover cleared Draft instead of restoring it: %d %v", n, err)
	}
	cfg, _ := config.Load()
	if cfg.Find("morning-check").Draft != true {
		t.Fatalf("unapproved routine left enabled after recovery: %+v", cfg.Find("morning-check"))
	}
	r, _ := e.s.GetRoutine("morning-check")
	if r.Approved || r.State != RoutineDraft {
		t.Fatalf("unapproved Routine enabled: %+v", r)
	}
}

// A failed migration or missing SQLite intent schema must never silently
// enable execution; report a deterministic error rather than using YAML.
func TestRoutineIntentSchemaCreatedForNewStore(t *testing.T) {
	e := newRoutineEnv(t)
	var table string
	if err := e.s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='routine_intents'`).Scan(&table); err != nil {
		t.Fatal(fmt.Errorf("routine WAL migration missing: %w", err))
	}
}
