package personalagent

import (
	"errors"
	"testing"
)

func TestArchiveRestoreAndDeleteOnlyWhenSafe(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "arch-create", "Archivist")
	first, _ := s.GetInstructions(a.ID)
	if first.Body == "" {
		t.Fatal("precondition: instructions must exist")
	}
	archived, replayed, err := s.ArchiveAssistant("archive1", a.ID, a.Revision)
	if err != nil || replayed || archived.Status != StatusArchived {
		t.Fatalf("archive: %+v %v", archived, err)
	}
	if again, rep, err := s.ArchiveAssistant("archive1", a.ID, a.Revision); err != nil || !rep || again.Revision != archived.Revision {
		t.Fatalf("replay: %+v %v", again, err)
	}
	list, err := s.ListAssistants()
	if err != nil || len(list) != 0 {
		t.Fatalf("active leaked: %+v %v", list, err)
	}
	hidden, err := s.ListArchivedAssistants()
	if err != nil || len(hidden) != 1 || hidden[0].ID != a.ID {
		t.Fatalf("archive list: %+v %v", hidden, err)
	}
	if err := s.DeleteArchivedAssistant(a.ID, a.Revision); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale delete = %v", err)
	}
	restored, _, err := s.RestoreAssistant("restore1", a.ID, archived.Revision)
	if err != nil || restored.Status != StatusActive {
		t.Fatalf("restore: %+v %v", restored, err)
	}
	if role, _ := s.GetInstructions(a.ID); role.Body != first.Body {
		t.Fatalf("instructions lost: %+v", role)
	}
	archived, _, err = s.ArchiveAssistant("archive2", a.ID, restored.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteArchivedAssistant(a.ID, archived.Revision); err != nil {
		t.Fatalf("delete empty Assistant: %v", err)
	}
	if _, err := s.GetAssistant(a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Assistant survived deletion: %v", err)
	}
	if _, err := s.GetInstructions(a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("instructions survived deletion: %v", err)
	}
}

func TestArchiveKeepsTasksAndRefusesRunningAgent(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "task-owner", "Ada")
	co, _, err := s.StartCoordinator("start", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.ClaimTask(co.TaskID, "daemon")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ArchiveAssistant("archive", a.ID, a.Revision); !errors.Is(err, ErrBadState) {
		t.Fatalf("active run should block archiving, got %v", err)
	}
	if err := s.MarkStarting(run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLaunch(run.ID, Session{HerdrSession: "default", WorkspaceID: "w1", PaneID: "w1:p1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(run.ID, RunDone, ""); err != nil {
		t.Fatal(err)
	}
	current, _ := s.GetAssistant(a.ID)
	if _, _, err := s.ArchiveAssistant("archive2", a.ID, current.Revision); !errors.Is(err, ErrBadState) {
		t.Fatalf("cannot hide a possibly live Herdr pane: %v", err)
	}
	archived, _, err := s.ArchiveAssistant("archive2", a.ID, current.Revision, func(sess, ws, pane string) (bool, error) {
		return true, nil // Herdr has confirmed the pane closed.
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteArchivedAssistant(a.ID, archived.Revision); !errors.Is(err, ErrBadState) {
		t.Fatalf("real pane must not be deleted blindly, got %v", err)
	}
	if tk, err := s.GetTask(co.TaskID); err != nil || tk.ID != co.TaskID {
		t.Fatalf("archive removed task: %+v %v", tk, err)
	}
}

// A queued task has no Run yet but would still be claimed by the scheduler.
// Archiving must never leave work that can continue unnoticed.
func TestArchiveRefusesQueuedWork(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "queued", "Ada")
	_, _, err := s.CreateTask("queue", CreateTaskInput{AssistantID: a.ID, Title: "Pending", Brief: "wait", Kind: KindResearch})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ArchiveAssistant("archive", a.ID, a.Revision); !errors.Is(err, ErrBadState) {
		t.Fatalf("queued task must block archival: %v", err)
	}
}

// An Assistant with settled private work can be purged without leaving task,
// run or idempotency-receipt rows behind.
func TestDeleteArchivedAssistantWithSettledTaskPurgesReceipts(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "receipt-owner", "Ada")
	tk, _, err := s.CreateTask("create-private", CreateTaskInput{AssistantID: a.ID, Kind: KindResearch, Title: "Private", Brief: "Private contents"})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.ClaimTask(tk.ID, "daemon")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(run.ID, RunDone, "ok"); err != nil {
		t.Fatal(err)
	}
	a, _ = s.GetAssistant(a.ID)
	archived, _, err := s.ArchiveAssistant("archive", a.ID, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteArchivedAssistant(a.ID, archived.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTask(tk.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("task survived purge: %v", err)
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM receipts WHERE result LIKE ?", "%"+tk.ID+"%").Scan(&count); err != nil || count != 0 {
		t.Fatalf("receipts still retain task: %d %v", count, err)
	}
}

// A recorded pane may be removed after Herdr confirms it was closed. An
// unavailable provider cannot authorize irreversible deletion.
func TestArchivedDeleteRequiresPositivePaneClosure(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "pane-owner", "Ada")
	co, _, err := s.StartCoordinator("start", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.ClaimTask(co.TaskID, "daemon")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkStarting(run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLaunch(run.ID, Session{HerdrSession: "default", WorkspaceID: "w1", PaneID: "w1:p1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(run.ID, RunDone, ""); err != nil {
		t.Fatal(err)
	}
	a, _ = s.GetAssistant(a.ID)
	archived, _, err := s.ArchiveAssistant("archive", a.ID, a.Revision, func(string, string, string) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	closed := func(sess, workspace, pane string) (bool, error) {
		calls++
		if sess != "default" || workspace != "w1" || pane != "w1:p1" {
			t.Fatalf("wrong target %q/%q/%q", sess, workspace, pane)
		}
		return true, nil
	}
	if err := s.DeleteArchivedAssistant(a.ID, archived.Revision, func(string, string, string) (bool, error) { return false, errors.New("Herdr offline") }); !errors.Is(err, ErrBadState) {
		t.Fatalf("offline must refuse: %v", err)
	}
	if err := s.DeleteArchivedAssistant(a.ID, archived.Revision, closed); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("closure probe called %d times", calls)
	}
}

// A Herdr closure check is not a permanent capability: the Run can change
// between the external read and the SQLite transaction. Never archive if it
// now points at a different pane.
func TestArchiveRejectsPaneChangedDuringHerdrVerification(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "arch-race", "Ada")
	co, _, err := s.StartCoordinator("start", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.ClaimTask(co.TaskID, "daemon")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkStarting(run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLaunch(run.ID, Session{HerdrSession: "default", WorkspaceID: "w1", PaneID: "w1:p1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(run.ID, RunDone, ""); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.ArchiveAssistant("archive", a.ID, a.Revision, func(sess, workspace, pane string) (bool, error) {
		if _, err := s.db.Exec("UPDATE runs SET pane_id=? WHERE id=?", "w1:p2", run.ID); err != nil {
			t.Fatal(err)
		}
		return true, nil // The old pane is closed, but a new one appeared.
	})
	if !errors.Is(err, ErrBadState) {
		t.Fatalf("changed Pane must invalidate archival proof: %v", err)
	}
	if active, _ := s.GetAssistant(a.ID); active.Status != StatusActive {
		t.Fatalf("failed archive hid the Assistant: %+v", active)
	}
}

// Archiving a safely closed Coordinator must not leave a stale 'ready' state
// that prevents the user starting it after Restore.
func TestArchiveResetsSettledCoordinatorAfterHerdrClosure(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "arch-coordinator", "Ada")
	co, _, err := s.StartCoordinator("start", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	launcher := &fakeLauncher{}
	d := &Dispatcher{Store: s, Launcher: launcher, Claimant: "daemon"}
	if _, err := d.RunTask(t.Context(), co.TaskID); err != nil {
		t.Fatal(err)
	}
	if current, _ := s.GetCoordinator(a.ID); current.State != CoordinatorReady {
		t.Fatalf("expected live Coordinator: %+v", current)
	}
	closed := func(sess, workspace, pane string) (bool, error) { return true, nil }
	archived, _, err := s.ArchiveAssistant("archive", a.ID, a.Revision, closed)
	if err != nil {
		t.Fatal(err)
	}
	if current, _ := s.GetCoordinator(a.ID); current.State != CoordinatorStopped {
		t.Fatalf("archive left a stale live Coordinator: %+v", current)
	}
	if prior, replayed, err := s.ArchiveAssistant("archive", a.ID, a.Revision); err != nil || !replayed || prior.Revision != archived.Revision {
		t.Fatalf("archive receipt replay should not recheck Herdr: %+v %v %t", prior, err, replayed)
	}
	restored, _, err := s.RestoreAssistant("restore", a.ID, archived.Revision)
	if err != nil {
		t.Fatal(err)
	}
	next, _, err := s.StartCoordinatorAs("restart", restored.ID, "omp", "", closed)
	if err != nil || next.TaskID == co.TaskID {
		t.Fatalf("restored Coordinator could not restart after closure: %+v %v", next, err)
	}
}
