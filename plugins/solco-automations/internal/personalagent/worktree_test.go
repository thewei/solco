package personalagent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitT runs git in dir for a fixture; failures are test failures.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo makes a real repository with one baseline commit on main.
func newRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-m", "baseline")
	return repo
}

func wtFixture(t *testing.T) (*Worktrees, *Store, string) {
	t.Helper()
	s, _ := openTest(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Worktrees{Store: s, Root: filepath.Join(root, "worktrees")}, s, newRepo(t)
}

func codingTask(t *testing.T, s *Store, repo string) Task {
	t.Helper()
	a := mustAssistant(t, s, "a-wt", "Ada")
	task, _, err := s.CreateTask("t-wt", CreateTaskInput{AssistantID: a.ID, Title: "Fix the bug", Brief: "Fix it.", Kind: KindCoding, RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// E02: a coding task works in its own branch and folder, from a recorded
// baseline, and the main checkout is never touched.
func TestPrepareIsolatesTheTaskFromTheMainCheckout(t *testing.T) {
	w, s, repo := wtFixture(t)
	task := codingTask(t, s, repo)
	// The user's own uncommitted work in the main checkout.
	if err := os.WriteFile(filepath.Join(repo, "wip.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	headBefore := gitT(t, repo, "rev-parse", "HEAD")

	info, err := w.Prepare(task)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(info.Path) != w.Root || filepath.Base(info.Path) != task.ID {
		t.Fatalf("path = %s", info.Path)
	}
	if info.BaseHead != headBefore || info.TargetBranch != "main" || !strings.HasPrefix(info.Branch, "solco/"+task.ID) {
		t.Fatalf("info = %+v", info)
	}
	if got := gitT(t, info.Path, "rev-parse", "--abbrev-ref", "HEAD"); got != info.Branch {
		t.Fatalf("worktree is on %q", got)
	}
	if got := gitT(t, info.Path, "rev-parse", "HEAD"); got != headBefore {
		t.Fatalf("worktree starts at %s, baseline is %s", got, headBefore)
	}
	// The main checkout: same branch, same commit, the user's file intact, and
	// the untracked file did not leak into the worktree.
	if gitT(t, repo, "rev-parse", "HEAD") != headBefore || gitT(t, repo, "rev-parse", "--abbrev-ref", "HEAD") != "main" {
		t.Fatal("the main checkout moved")
	}
	if raw, err := os.ReadFile(filepath.Join(repo, "wip.txt")); err != nil || string(raw) != "mine\n" {
		t.Fatalf("the user's file changed: %q %v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(info.Path, "wip.txt")); err == nil {
		t.Fatal("uncommitted main-checkout work leaked into the task worktree")
	}

	// Retry and continue reuse the same place instead of making a second one.
	again, err := w.Prepare(task)
	if err != nil || again.Path != info.Path || again.BaseHead != info.BaseHead {
		t.Fatalf("second prepare = %+v %v", again, err)
	}
}

func TestPrepareRefusesWhatItCannotMakeSafe(t *testing.T) {
	w, s, repo := wtFixture(t)
	mk := func(path string) Task {
		task := codingTask(t, s, repo)
		task.RepoPath = path
		return task
	}
	plain := t.TempDir()
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, empty, "init", "-b", "main")
	detached := newRepo(t)
	gitT(t, detached, "checkout", "--detach")
	bare := filepath.Join(t.TempDir(), "bare.git")
	gitT(t, t.TempDir(), "init", "--bare", bare)
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"not a repository":   plain,
		"no commits":         empty,
		"detached head":      detached,
		"bare":               bare,
		"a subfolder":        sub,
		"missing":            filepath.Join(plain, "nope"),
		"repository in Root": "", // set below
	}
	inRoot := filepath.Join(w.Root, "inner")
	if err := os.MkdirAll(inRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, inRoot, "init", "-b", "main")
	cases["repository in Root"] = inRoot
	for name, path := range cases {
		if _, err := w.Prepare(mk(path)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestPrepareRefusesUnsafeLocations(t *testing.T) {
	w, s, repo := wtFixture(t)
	task := codingTask(t, s, repo)

	// A task id is never a path.
	for _, id := range []string{"../escape", "/abs", "a/b", "", ".", ".."} {
		bad := task
		bad.ID = id
		if _, err := w.Prepare(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("id %q: err = %v", id, err)
		}
	}
	// Root itself reached through a symlink.
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	via := &Worktrees{Store: s, Root: filepath.Join(link, "wt")}
	if _, err := via.Prepare(task); !errors.Is(err, ErrInvalid) {
		t.Errorf("symlinked root: err = %v", err)
	}
	// Something already sits where the worktree would go: never adopt or
	// overwrite it.
	if err := os.MkdirAll(filepath.Join(w.Root, task.ID), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Root, task.ID, "keep.txt"), []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Prepare(task); !errors.Is(err, ErrBadState) {
		t.Errorf("occupied path: err = %v", err)
	}
	if raw, _ := os.ReadFile(filepath.Join(w.Root, task.ID, "keep.txt")); string(raw) != "precious" {
		t.Fatal("an unrelated folder was touched")
	}
}

// Repository hooks and config callbacks are code execution; the task's git
// calls never run them.
func TestGitCallsDoNotRunRepositoryHooksOrCallbacks(t *testing.T) {
	w, s, repo := wtFixture(t)
	marker := filepath.Join(t.TempDir(), "ran")
	hook := "#!/bin/sh\ntouch " + marker + "\n"
	for _, name := range []string{"post-checkout", "reference-transaction"} {
		p := filepath.Join(repo, ".git", "hooks", name)
		if err := os.WriteFile(p, []byte(hook), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cb := filepath.Join(t.TempDir(), "fsmon.sh")
	if err := os.WriteFile(cb, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "config", "core.fsmonitor", cb)
	task := codingTask(t, s, repo)
	info, err := w.Prepare(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Status(info); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a repository hook or config callback ran")
	}
}

func TestCleanupKeepsWorkUnlessForced(t *testing.T) {
	w, s, repo := wtFixture(t)
	task := codingTask(t, s, repo)
	info, err := w.Prepare(task)
	if err != nil {
		t.Fatal(err)
	}
	// Uncommitted work: refused, nothing removed.
	if err := os.WriteFile(filepath.Join(info.Path, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.Cleanup(task.ID, false); !errors.Is(err, ErrBadState) {
		t.Fatalf("dirty cleanup err = %v", err)
	}
	if _, err := os.Stat(info.Path); err != nil {
		t.Fatal("a dirty worktree was removed")
	}
	// Committed but unmerged work: refused too.
	gitT(t, info.Path, "add", ".")
	gitT(t, info.Path, "commit", "-m", "work")
	if err := w.Cleanup(task.ID, false); !errors.Is(err, ErrBadState) {
		t.Fatalf("unmerged cleanup err = %v", err)
	}
	// A tampered row can never aim removal at the main checkout.
	if _, err := s.db.Exec(`UPDATE worktrees SET path=? WHERE task_id=?`, repo, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.Cleanup(task.ID, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered path err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "README.md")); err != nil {
		t.Fatal("the main checkout was damaged")
	}
	if _, err := s.db.Exec(`UPDATE worktrees SET path=? WHERE task_id=?`, info.Path, task.ID); err != nil {
		t.Fatal(err)
	}
	// Forced: gone, branch included; asking again is a no-op, not an error.
	if err := w.Cleanup(task.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(info.Path); err == nil {
		t.Fatal("force did not remove the worktree")
	}
	if out := gitT(t, repo, "branch", "--list", info.Branch); out != "" {
		t.Fatalf("branch survived: %q", out)
	}
	if err := w.Cleanup(task.ID, false); err != nil {
		t.Fatalf("second cleanup: %v", err)
	}
}

func TestCleanupRemovesMergedCleanWorktree(t *testing.T) {
	w, s, repo := wtFixture(t)
	task := codingTask(t, s, repo)
	info, _ := w.Prepare(task)
	// Nothing was committed: the branch is already contained in main.
	if err := w.Cleanup(task.ID, false); err != nil {
		t.Fatalf("clean merged cleanup: %v", err)
	}
	if _, err := os.Stat(info.Path); err == nil {
		t.Fatal("still there")
	}
}

// E02 × dispatcher: a coding task launches inside its own worktree; a failure
// to make one fails the run before anything is provisioned.
func TestDispatcherLaunchesCodingTasksInTheirWorktree(t *testing.T) {
	w, s, repo := wtFixture(t)
	task := codingTask(t, s, repo)
	f := &fakeLauncher{}
	d := &Dispatcher{Store: s, Launcher: f, Claimant: "t", Worktrees: w}
	run, err := d.RunTask(context.Background(), task.ID)
	if err != nil || run.Status != RunDone {
		t.Fatalf("run = %+v %v", run, err)
	}
	info, ok, _ := w.Get(task.ID)
	if !ok || len(f.specs) != 1 || f.specs[0].WorkDir != info.Path {
		t.Fatalf("spec = %+v info = %+v", f.specs, info)
	}
	if f.specs[0].Auto != nil || !strings.Contains(f.specs[0].Prompt, info.Branch) {
		t.Fatalf("prompt must tell the agent its branch; prompt=%q", f.specs[0].Prompt)
	}

	// A repo that cannot host a worktree: the run fails, no workspace exists.
	a := mustAssistantByID(t, s, task.AssistantID)
	bad, _, err := s.CreateTask("t-bad", CreateTaskInput{AssistantID: a.ID, Title: "Nope", Brief: "x", Kind: KindCoding, RepoPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	before := f.provisions
	run2, err := d.RunTask(context.Background(), bad.ID)
	if err == nil || run2.Status != RunFailed || f.provisions != before {
		t.Fatalf("run2 = %+v err=%v provisions %d→%d", run2, err, before, f.provisions)
	}
	if got, _ := s.GetTask(bad.ID); got.State != TaskFailed || !got.NeedsYou {
		t.Fatalf("task = %+v", got)
	}
}
