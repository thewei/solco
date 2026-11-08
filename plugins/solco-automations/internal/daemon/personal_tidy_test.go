package daemon

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/cleanup"
	"github.com/thewei/solco/plugins/solco-automations/internal/personalagent"
)

// Gap L07: the daily chore hands the core's own task branches to the cleanup
// rules with the fixed retention, and does nothing when it owns none.
func TestDailyChoreTidiesOnlyTheCoresOwnTaskBranches(t *testing.T) {
	s, err := personalagent.Open(filepath.Join(t.TempDir(), "p.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var calls int
	var gotOwned map[string][]string
	var gotTTL time.Duration
	old := tidyWorktrees
	tidyWorktrees = func(owned map[string][]string, ttl time.Duration, _ time.Time, _ func(cleanup.Candidate, error)) (int, error) {
		calls++
		gotOwned, gotTTL = owned, ttl
		return 0, nil
	}
	t.Cleanup(func() { tidyWorktrees = old })
	p := &personal{store: s}

	p.tidyTaskWorktrees()
	if calls != 0 {
		t.Fatalf("with nothing owned the cleanup was called %d time(s)", calls)
	}

	a, _, err := s.CreateAssistant("a1", personalagent.CreateAssistantInput{Name: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := s.CreateTask("t1", personalagent.CreateTaskInput{AssistantID: a.ID, Title: "t", Brief: "b", Kind: personalagent.KindCoding, RepoPath: "/tmp/repo"})
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
	if err := s.RecordLaunch(run.ID, personalagent.Session{HerdrSession: "default", WorkspaceID: "w1", PaneID: "w1:p1", Branch: "auto/r-1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(run.ID, personalagent.RunCancelled, "stopped"); err != nil {
		t.Fatal(err)
	}
	p.tidyTaskWorktrees()
	if calls != 1 || !reflect.DeepEqual(gotOwned, map[string][]string{"/tmp/repo": {"auto/r-1"}}) || gotTTL != 14*24*time.Hour {
		t.Fatalf("calls=%d owned=%v ttl=%v", calls, gotOwned, gotTTL)
	}
}
