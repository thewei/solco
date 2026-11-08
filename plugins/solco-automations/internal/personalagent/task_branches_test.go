package personalagent

import (
	"reflect"
	"testing"
)

// Gap L07: the worktree branches the core can name as its own — and only those
// of work that ended in a way that leaves nothing to inspect (merged and done,
// or cancelled). A failed task's checkout is the evidence the person reads.
func TestOwnedTaskBranchesNameOnlyFinishedCodingRuns(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a", "Ada")
	mk := func(req, kind, state, branch string) {
		t.Helper()
		task, _, err := s.CreateTask(req, CreateTaskInput{AssistantID: a.ID, Title: req, Brief: "b", Kind: kind, RepoPath: "/tmp/repo-" + req[:1]})
		if err != nil {
			t.Fatal(err)
		}
		run, _, err := s.ClaimTask(task.ID, "daemon")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MarkStarting(run.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordLaunch(run.ID, Session{HerdrSession: "default", WorkspaceID: "w1", PaneID: "w1:p1", Branch: branch}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE tasks SET state=? WHERE id=?`, state, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	mk("a-done", KindCoding, TaskDone, "auto/r-1")
	mk("a-cancelled", KindCoding, TaskCancelled, "auto/r-2")
	mk("b-failed", KindCoding, TaskFailed, "auto/r-3")
	mk("c-running", KindCoding, TaskRunning, "auto/r-4")
	mk("d-research", KindResearch, TaskDone, "")
	got, err := s.OwnedTaskBranches()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"/tmp/repo-a": {"auto/r-1", "auto/r-2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("owned = %v, want %v", got, want)
	}
}
