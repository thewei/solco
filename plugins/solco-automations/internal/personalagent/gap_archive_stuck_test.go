package personalagent

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"
)

// An Assistant must never be left unarchivable by a launch that provably
// never started anything (user report: "Archive phone-e2e" refused forever —
// the Coordinator's launch ran when `herdr` was not on the daemon's PATH and
// was parked "uncertain" with no pane to ever reconcile).

func failCodeOf(t *testing.T, s *Store, runID string) string {
	t.Helper()
	var c string
	if err := s.db.QueryRow(`SELECT fail_code FROM runs WHERE id=?`, runID).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func missingHerdr(step string) error {
	return fmt.Errorf("%s: %w", step, &exec.Error{Name: "herdr", Err: exec.ErrNotFound})
}

// Forward: no binary at the first call means nothing was created, so the run
// fails (retry allowed) instead of parking uncertain.
func TestAMissingHerdrBinaryFailsTheLaunchInsteadOfParkingIt(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	l := &fakeLauncher{provisionErr: missingHerdr("workspace create"), findErr: missingHerdr("workspace list")}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon"}
	run, _ := d.RunTask(t.Context(), task.ID)
	if run.Status != RunFailed || failCodeOf(t, s, run.ID) != FailCodeHerdrUnreachable {
		t.Fatalf("run = %s / %q", run.Status, failCodeOf(t, s, run.ID))
	}
}

// Existing rows: a run parked before that rule, with no pane and the missing
// binary written in its own error, is settled by the daemon pass; the
// Assistant can then be archived.
func TestAnOldMissingBinaryParkSettlesAndTheAssistantArchives(t *testing.T) {
	s, _ := openTest(t)
	anchorPrimary(t, s)
	asst := mustAssistant(t, s, "stuck", "phone-e2e")
	coord, _, err := s.StartCoordinator("first", asst.ID)
	if err != nil {
		t.Fatal(err)
	}
	l := &fakeLauncher{provisionErr: errors.New("x"), findErr: errors.New("y")}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon"}
	run, _ := d.RunTask(t.Context(), coord.TaskID)
	if run.Status != RunUncertain {
		t.Fatalf("setup: %s", run.Status)
	}
	legacy := `launch outcome unknown: workspace create: exec: "herdr": executable file not found in $PATH; lookup: workspace list: exec: "herdr": executable file not found in $PATH`
	if _, err := s.db.Exec(`UPDATE runs SET error=? WHERE id=?`, legacy, run.ID); err != nil {
		t.Fatal(err)
	}
	gone := func(string, string, string) (bool, error) { return true, nil }
	if _, _, err := s.ArchiveAssistant("a1", asst.ID, asst.Revision, gone); err == nil {
		t.Fatal("setup: the stuck run should block archiving")
	}
	if _, err := d.ReconcilePending(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetRun(run.ID); got.Status != RunFailed || failCodeOf(t, s, run.ID) != FailCodeHerdrUnreachable {
		t.Fatalf("run = %s / %q", got.Status, failCodeOf(t, s, run.ID))
	}
	cur, _ := s.GetAssistant(asst.ID)
	if _, _, err := s.ArchiveAssistant("a2", asst.ID, cur.Revision, gone); err != nil {
		t.Fatalf("archive after settling: %v", err)
	}
}

// Any other uncertain run is untouched: only the recorded missing binary at a
// create step, with no pane identity, is proof enough.
func TestOnlyAProvenNeverStartedParkIsSettled(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	l := &fakeLauncher{provisionErr: errors.New("x"), findErr: errors.New("y")}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon"}
	run, _ := d.RunTask(t.Context(), task.ID)
	l.findErr = nil
	if _, err := d.ReconcilePending(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetRun(run.ID); got.Status != RunUncertain {
		t.Fatalf("an unproven park was settled: %s", got.Status)
	}
}
