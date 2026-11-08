package personalagent

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
)

// L03/AC-13: a consistent backup (database + automations.yaml) taken while data
// exists restores into a store that migrates forward without losing a goal, a
// running routine, a pending approval, or the truth of an uncertain run, and an
// old request replays instead of duplicating.
func TestGapL03BackupRestoresAcrossAnUpgradeWithoutLosingState(t *testing.T) {
	e := newRoutineEnv(t)
	// A routine that runs (approved) and one still waiting for a person.
	first, err := e.draft(t, "l03-r1", nil)
	if err != nil {
		t.Fatal(err)
	}
	ap := first["approval"].(map[string]any)
	e.approve(t, ap["id"].(string), ap["actionHash"].(string))
	if _, err := e.draft(t, "l03-r2", func(m map[string]any) { m["name"] = "second-check" }); err != nil {
		t.Fatal(err)
	}
	// Two goals with plans, and an uncertain run.
	g1 := activeGoalWithPlan(t, e.s, e.a, twoStepPlan())
	g2 := activeGoalWithPlan(t, e.s, mustAssistant(t, e.s, "l03-b", "Bob"), twoStepPlan())
	task, _, err := e.s.CreateTask("l03-task", CreateTaskInput{AssistantID: e.a.ID, Kind: KindResearch, Title: "Look into it", Brief: "x"})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := e.s.ClaimTask(task.ID, "l03")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.parkUncertain(run.ID, "Herdr stopped answering"); err != nil {
		t.Fatal(err)
	}
	count := func(s *Store, q string) int {
		var n int
		if err := s.db.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	wantGoals := count(e.s, `SELECT COUNT(*) FROM goals`)
	wantApprovals := count(e.s, `SELECT COUNT(*) FROM approvals WHERE state='approved'`)
	wantPending := count(e.s, `SELECT COUNT(*) FROM approvals WHERE state='pending'`)
	if wantGoals != 2 || wantApprovals != 1 || wantPending != 1 {
		t.Fatalf("fixture: goals=%d approved=%d pending=%d", wantGoals, wantApprovals, wantPending)
	}

	dest := filepath.Join(t.TempDir(), "backup")
	if err := e.s.Backup(dest); err != nil {
		t.Fatalf("backup: %v", err)
	}
	for _, name := range []string{"assistants.sqlite", "automations.yaml"} {
		st, err := os.Stat(filepath.Join(dest, name))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s must exist and be private: %v %v", name, st, err)
		}
	}
	if err := e.s.Backup(dest); err == nil {
		t.Fatal("a backup must never overwrite an earlier one")
	}
	// Life goes on after the backup; the backup does not change.
	if _, _, err := e.s.CreateGoal("l03-g3", CreateGoalInput{AssistantID: e.a.ID, Title: "Later goal", Objective: "later"}); err != nil {
		t.Fatalf("a later goal: %v", err)
	}

	// An older binary wrote this file: the columns of the migrations after v28 did not exist yet.
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dest, "assistants.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`ALTER TABLE runs DROP COLUMN fail_code`,
		`ALTER TABLE task_limits DROP COLUMN require_checks`,
		`ALTER TABLE persona_applied DROP COLUMN brief_hash`,
		`ALTER TABLE persona_applied DROP COLUMN brief_session`,
		`ALTER TABLE persona_applied DROP COLUMN brief_at`,
		`ALTER TABLE coordinators DROP COLUMN stop_reason`,
		`ALTER TABLE assistant_runtime DROP COLUMN idle_sleep_min`,
		`UPDATE meta SET value='28' WHERE key='schema_version'`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = raw.Close()

	r, err := Open(filepath.Join(dest, "assistants.sqlite"))
	if err != nil {
		t.Fatalf("the upgraded binary must open the restored file: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if v, _ := r.SchemaVersion(); v != len(migrations) {
		t.Fatalf("schema = %d, want %d", v, len(migrations))
	}
	if got := count(r, `SELECT COUNT(*) FROM goals`); got != wantGoals {
		t.Fatalf("goals = %d, want %d (the later goal is not in the backup)", got, wantGoals)
	}
	if count(r, `SELECT COUNT(*) FROM approvals WHERE state='approved'`) != wantApprovals || count(r, `SELECT COUNT(*) FROM approvals WHERE state='pending'`) != wantPending {
		t.Fatal("approvals changed across backup and upgrade")
	}
	for _, id := range []string{g1.ID, g2.ID} {
		if g, err := r.GetGoal(id); err != nil || g.State != GoalActive {
			t.Fatalf("goal %s: %+v %v", id, g, err)
		}
	}
	got, err := r.GetRun(run.ID)
	if err != nil || got.Status != RunUncertain {
		t.Fatalf("an uncertain run must stay uncertain, got %+v %v", got, err)
	}
	if rt, err := r.GetRoutine("morning-check"); err != nil || rt.State != RoutineActive || !rt.Approved {
		t.Fatalf("the running routine: %+v %v", rt, err)
	}
	if rt, err := r.GetRoutine("second-check"); err != nil || rt.State != RoutineDraft {
		t.Fatalf("the waiting routine: %+v %v", rt, err)
	}
	// The definitions the routines point at came along.
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", dest)
	cfg, err := config.Load()
	if err != nil || cfg.Find("morning-check") == nil || cfg.Find("second-check") == nil {
		t.Fatalf("automations.yaml in the backup: %v", err)
	}
	// A request from before the backup replays; it does not create a second goal.
	if _, replayed, err := r.CreateGoal("l03-g3-replay-check", CreateGoalInput{AssistantID: e.a.ID, Title: "X", Objective: "x"}); err != nil || replayed {
		t.Fatalf("fresh request: replayed=%v err=%v", replayed, err)
	}
	if _, replayed, err := r.CreateTask("l03-task", CreateTaskInput{AssistantID: e.a.ID, Kind: KindResearch, Title: "Look into it", Brief: "x"}); err != nil || !replayed {
		t.Fatalf("an old request must replay, replayed=%v err=%v", replayed, err)
	}
}

func TestGapL03BackupCommandWritesAConsistentCopyAndRefusesToOverwrite(t *testing.T) {
	e := newRoutineEnv(t)
	dir := filepath.Join(t.TempDir(), "b1")
	got, err := call(t, e.api, ``, "assistant", "backup", "--to", dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := got["backup"].(map[string]any); b["path"] != dir {
		t.Fatalf("answer = %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "assistants.sqlite")); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, e.api, ``, "assistant", "backup", "--to", dir); err == nil {
		t.Fatal("the second backup into the same folder must be refused")
	}
}
