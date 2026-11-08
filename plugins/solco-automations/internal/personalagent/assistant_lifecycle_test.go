package personalagent

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestArchiveRestoreAndDeleteOnlyWhenSafe(t *testing.T) {
	s, _ := openTest(t)
	anchorPrimary(t, s)
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
	if err != nil || len(list) != 1 || list[0].Name != "Lead" {
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
	anchorPrimary(t, s)
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
	anchorPrimary(t, s)
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
	anchorPrimary(t, s)
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
	anchorPrimary(t, s)
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

// A Run recorded before Herdr sessions were named carries no session. That is
// not an unreadable identity: the closure check receives the empty session
// (HerdrPaneGone then asks every running session) and archive/delete follow
// its verdict — gone archives, a live pane still refuses.
func TestArchiveAcceptsLegacyRunWithoutHerdrSession(t *testing.T) {
	s, _ := openTest(t)
	anchorPrimary(t, s)
	a := mustAssistant(t, s, "arch-legacy", "Ada")
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
	if err := s.RecordLaunch(run.ID, Session{HerdrSession: "", WorkspaceID: "w1", PaneID: "w1:p1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(run.ID, RunDone, ""); err != nil {
		t.Fatal(err)
	}
	a, _ = s.GetAssistant(a.ID)
	if _, _, err := s.ArchiveAssistant("no-verifier", a.ID, a.Revision); !errors.Is(err, ErrBadState) {
		t.Fatalf("without a verifier the closure stays unproven: %v", err)
	}
	live := func(sess, workspace, pane string) (bool, error) {
		if sess != "" || workspace != "w1" || pane != "w1:p1" {
			t.Fatalf("wrong target %q/%q/%q", sess, workspace, pane)
		}
		return false, nil
	}
	if _, _, err := s.ArchiveAssistant("live", a.ID, a.Revision, live); !errors.Is(err, ErrBadState) {
		t.Fatalf("a live pane must refuse: %v", err)
	}
	gone := func(string, string, string) (bool, error) { return true, nil }
	if _, _, err := s.ArchiveAssistant("gone", a.ID, a.Revision, gone); err != nil {
		t.Fatalf("a legacy run whose pane is gone must archive: %v", err)
	}
}

// A Coordinator/Group Agent whose terminal was closed by hand never reports
// back: its Run stays `blocked` and its Task open. Once Herdr proves the pane
// gone that record is settled instead of blocking the archive forever; a Run
// that is still launching (no pane yet) keeps refusing.
func TestArchiveSettlesWorkWhoseHerdrPaneIsGone(t *testing.T) {
	s, _ := openTest(t)
	anchorPrimary(t, s)
	a := mustAssistant(t, s, "arch-stale", "Ada")
	co, _, err := s.StartCoordinator("start", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.ClaimTask(co.TaskID, "daemon")
	if err != nil {
		t.Fatal(err)
	}
	// Claimed, no pane yet: unproven, so the archive refuses and never asks Herdr.
	asked := false
	probe := func(string, string, string) (bool, error) { asked = true; return true, nil }
	if _, _, err := s.ArchiveAssistant("launching", a.ID, a.Revision, probe); !errors.Is(err, ErrBadState) || asked {
		t.Fatalf("a launching run must refuse without a probe: %v asked=%v", err, asked)
	}
	if err := s.MarkStarting(run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLaunch(run.ID, Session{HerdrSession: "default", WorkspaceID: "w1", PaneID: "w1:p1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE runs SET status='blocked' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE tasks SET state='blocked' WHERE id=?`, co.TaskID); err != nil {
		t.Fatal(err)
	}
	a, _ = s.GetAssistant(a.ID)
	open := func(string, string, string) (bool, error) { return false, nil }
	if _, _, err := s.ArchiveAssistant("open", a.ID, a.Revision, open); !errors.Is(err, ErrBadState) {
		t.Fatalf("a pane that is still open must refuse: %v", err)
	}
	gone := func(string, string, string) (bool, error) { return true, nil }
	got, _, err := s.ArchiveAssistant("gone", a.ID, a.Revision, gone)
	if err != nil {
		t.Fatalf("a blocked run whose pane is gone must archive: %v", err)
	}
	if got.Status != StatusArchived {
		t.Fatalf("status %q", got.Status)
	}
	var runState, taskState string
	if err := s.db.QueryRow(`SELECT status FROM runs WHERE id=?`, run.ID).Scan(&runState); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT state FROM tasks WHERE id=?`, co.TaskID).Scan(&taskState); err != nil {
		t.Fatal(err)
	}
	if runState != RunCancelled || taskState != TaskCancelled {
		t.Fatalf("stale work not settled: run=%s task=%s", runState, taskState)
	}
}

// 2026-11-07, found on a real Assistant: deleting one that had a finished run failed with
// "FOREIGN KEY constraint failed" because run_results (and task_skills, task_limits, skill_grants,
// event_triggers, hook_keys, capability_grants, budget_events…) were not in the purge. Every table
// that holds an Assistant's data must be emptied, with foreign keys on, and leave nothing behind.
func TestDeleteArchivedAssistantRemovesEveryOwnedRow(t *testing.T) {
	s, _ := openTest(t)
	anchorPrimary(t, s)
	a := mustAssistant(t, s, "owned-rows", "Ada")
	tk, _, err := s.CreateTask("owned-task", CreateTaskInput{AssistantID: a.ID, Kind: KindResearch, Title: "Owned", Brief: "Owned contents"})
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
	seed := []struct {
		table, query string
		args         []any
	}{
		{"run_results", `INSERT INTO run_results(run_id,evidence,agent_session_id,message_id,sha256,body,collected_at) VALUES(?,'e','','','h','private result',1)`, []any{run.ID}},
		{"task_skills", `INSERT INTO task_skills(task_id,skill) VALUES(?,'sk')`, []any{tk.ID}},
		{"task_limits", `INSERT INTO task_limits(task_id,timeout_min,require_checks) VALUES(?,5,0)`, []any{tk.ID}},
		{"skill_grants", `INSERT INTO skill_grants(assistant_id,skill,granted_at) VALUES(?,'sk',1)`, []any{a.ID}},
		{"event_triggers", `INSERT INTO event_triggers(id,assistant_id,name,on_kind,task_title,task_brief,task_kind,state,revision,created_at,updated_at) VALUES('tr_1',?,'n','task.failed','t','b','research','active',1,1,1)`, []any{a.ID}},
		{"hook_keys", `INSERT INTO hook_keys(id,assistant_id,secret,sources,status,created_at) VALUES('hk_1',?,'s','[]','active',1)`, []any{a.ID}},
		{"hook_events", `INSERT INTO hook_events(id,assistant_id,key_id,source,delivery_id,payload_hash,routing,received_at) VALUES('he_1',?,'hk_1','github','d1','h','ignored',1)`, []any{a.ID}},
		{"capability_grants", `INSERT INTO capability_grants(id,assistant_id,capability,effect,issued_at,expires_at,created_at) VALUES('cg_1',?,'c','allow',1,2,1)`, []any{a.ID}},
		{"authorization_receipts", `INSERT INTO authorization_receipts(id,assistant_id,capability,request_id,decision,reason,created_at) VALUES('ar_1',?,'c','rq_1','allow','r',1)`, []any{a.ID}},
		{"budget_events", `INSERT INTO budget_events(assistant_id,day,kind,ref,outcome,reason,at,dedupe) VALUES(?,'2026-11-07','k','r','o','why',1,'d1')`, []any{a.ID}},
		{"assistant_checkpoints", `INSERT INTO assistant_checkpoints(assistant_id,last_seq) VALUES(?,1)`, []any{a.ID}},
	}
	for _, row := range seed {
		if _, err := s.db.Exec(row.query, row.args...); err != nil {
			t.Fatalf("seed %s: %v", row.table, err)
		}
	}
	a, _ = s.GetAssistant(a.ID)
	archived, _, err := s.ArchiveAssistant("archive", a.ID, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteArchivedAssistant(a.ID, archived.Revision); err != nil {
		t.Fatalf("delete with finished work: %v", err)
	}
	for _, row := range seed {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + row.table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s kept %d rows of a deleted Assistant (%v)", row.table, n, err)
		}
	}
	if rows, err := s.db.Query(`PRAGMA foreign_key_check`); err != nil {
		t.Fatal(err)
	} else {
		defer rows.Close()
		if rows.Next() {
			t.Fatal("deleting an Assistant left dangling foreign keys")
		}
	}
}

// The purge list is the one place that knows every table an Assistant owns. A table added later that
// points at an Assistant, its tasks, runs or goals must join it (or be one the delete refuses to
// run over) — otherwise the delete either fails on a constraint or leaves personal rows behind.
func TestPurgeCoversEveryAssistantTable(t *testing.T) {
	s, _ := openTest(t)
	text := strings.Join(assistantPurgeStatements, "\n")
	// The delete refuses while any of these exist (see DeleteArchivedAssistant), so they need no statement.
	refused := map[string]bool{"routines": true, "worktrees": true, "group_members": true, "routine_skills": true}
	rows, err := s.db.Query(`SELECT m.name FROM sqlite_master m WHERE m.type='table' AND m.name<>'assistants' AND (
		EXISTS (SELECT 1 FROM pragma_table_info(m.name) WHERE name='assistant_id') OR
		EXISTS (SELECT 1 FROM pragma_foreign_key_list(m.name) f WHERE f."table" IN ('assistants','tasks','runs','goals')))`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if refused[name] {
			continue
		}
		if !regexp.MustCompile(`\bFROM ` + name + `\b`).MatchString(text) {
			t.Errorf("table %s holds an Assistant's data but DeleteArchivedAssistant never empties it", name)
		}
	}
}
