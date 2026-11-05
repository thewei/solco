package personalagent

import (
	"os"
	"path/filepath"
	"testing"
)

// W11: a repo-less task gets its Assistant's private folder, never a folder
// shared with other Assistants, and a task with a repository keeps that one.
func TestRepoLessTasksWorkInTheirAssistantsPrivateFolder(t *testing.T) {
	root := t.TempDir()
	l := &HostLauncher{WorkDirRoot: root}
	a := l.automation(LaunchSpec{Kind: KindResearch, AssistantID: "ada", RunID: "r1", Label: "assistant: x (r1)"})
	b := l.automation(LaunchSpec{Kind: KindManual, AssistantID: "bob", RunID: "r2", Label: "assistant: y (r2)"})
	if a.Repo == b.Repo {
		t.Fatalf("two Assistants share %s", a.Repo)
	}
	want := filepath.Join(root, "personal", "tasks", "ada")
	if a.Repo != want {
		t.Fatalf("repo = %s, want %s", a.Repo, want)
	}
	if st, err := os.Stat(want); err != nil || !st.IsDir() {
		t.Fatalf("folder not created: %v", err)
	}
	repo := t.TempDir()
	if got := l.automation(LaunchSpec{Kind: KindResearch, AssistantID: "ada", RepoPath: repo, RunID: "r3"}).Repo; got != repo {
		t.Fatalf("a task's own repository is kept, got %s", got)
	}
}

// W12: a failed OMP run is retried with OMP, the same model and the same task;
// nothing quietly hands the task to another provider.
func TestRetryNeverSwitchesAgentOrModel(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	task, _, err := s.CreateTask("w12", CreateTaskInput{AssistantID: a.ID, Title: "t", Brief: "b", Kind: KindResearch, Agent: "omp", Model: "fixture/model"})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeLauncher{provisionErr: errString("omp crashed")}
	d := &Dispatcher{Store: s, Launcher: f, Claimant: "daemon"}
	_, _ = d.RunTask(t.Context(), task.ID)
	failed, _ := s.GetTask(task.ID)
	if failed.State != TaskFailed {
		t.Fatalf("state = %s", failed.State)
	}
	if _, _, err := s.RetryTask("w12-retry", task.ID, failed.Revision); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.provisionErr = nil
	f.mu.Unlock()
	if _, err := d.RunTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.specs) != 2 {
		t.Fatalf("launches = %d", len(f.specs))
	}
	for i, sp := range f.specs {
		if sp.Agent != "omp" || sp.Model != "fixture/model" || sp.TaskID != task.ID {
			t.Fatalf("attempt %d changed identity: %+v", i+1, sp)
		}
	}
}
