package nativeui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/gitworkbench"
)

// pushFixture grows tempGitRepo into a repo with a bare origin whose branch
// is one commit behind the local HEAD: exactly the state the Changes view's
// Push actions exist for.
func pushFixture(t *testing.T) (root, origin, branch string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root = tempGitRepo(t)
	origin = filepath.Join(t.TempDir(), "origin.git")
	run := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run(root, "init", "-q", "--bare", origin)
	run(root, "remote", "add", "origin", origin)
	out, err := exec.Command("git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatalf("branch name: %v", err)
	}
	branch = strings.TrimSpace(string(out))
	run(root, "push", "-q", "-u", "origin", branch)
	if err := os.WriteFile(filepath.Join(root, "second.txt"), []byte("ahead\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(root, "add", "second.txt")
	run(root, "commit", "-q", "-m", "unpushed")
	return root, origin, branch
}

// clickAndWaitPush clicks one Push control and waits for the operation lane
// to finish, returning the toast the op reported.
func clickAndWaitPush(t *testing.T, shell *Shell, tt *ui.Tester, label string) (toast string) {
	t.Helper()
	var mu sync.Mutex
	shell.uiApplyOverride = func(fn func()) {
		mu.Lock()
		defer mu.Unlock()
		fn()
	}
	tt.Frame()
	if err := tt.Click(label); err != nil {
		t.Fatalf("%s button not clickable: %v", label, err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		mu.Lock()
		busy := shell.git.opBusy
		toast = shell.pendingToast
		mu.Unlock()
		if !busy {
			return toast
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s click never completed; opBusy stuck", label)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// pushPublished asserts the bare origin now carries root's HEAD.
func pushPublished(t *testing.T, root, origin, branch string) {
	t.Helper()
	read := func(dir, rev string) string {
		out, err := exec.Command("git", "-C", dir, "rev-parse", rev).Output()
		if err != nil {
			t.Fatalf("rev-parse %s in %s: %v", rev, dir, err)
		}
		return strings.TrimSpace(string(out))
	}
	if got, want := read(origin, branch), read(root, "HEAD"); got != want {
		t.Fatalf("origin/%s = %s, local HEAD = %s: the push never published", branch, got, want)
	}
}

// TestCleanStatePushButtonPublishesToOrigin drives the clean Changes view's
// "Push N commits" primary action against a real repository: the click must
// actually publish HEAD to origin, report success, and leave the operation
// lane free. (The title bar's Push icon retired with the Git page, 2026-11-08:
// pushing is this one button.)
func TestCleanStatePushButtonPublishesToOrigin(t *testing.T) {
	root, origin, branch := pushFixture(t)
	shell := gitSurfaceTestShell(t, root)
	shell.git.snapshot = &gitworkbench.ChangesSnapshot{Root: root, Branch: branch, Signature: "sig-clean"}
	shell.git.upstream = gitworkbench.UpstreamStatus{OK: true, Remote: "origin", Branch: branch, Ahead: 1}
	showChangesPanel(shell)
	tt := ui.NewTester(shell.View, 1280, 760)

	toast := clickAndWaitPush(t, shell, tt, "Push 1 commit")
	if toast != "Pushed origin/"+branch {
		t.Fatalf("toast = %q, want %q", toast, "Pushed origin/"+branch)
	}
	pushPublished(t, root, origin, branch)
}

// TestGitOpBusyClickAnswersInsteadOfVanishing pins the fix for the dead
// Push report: a click that lands while another Git operation holds the
// lane must say so, never silently do nothing.
func TestGitOpBusyClickAnswersInsteadOfVanishing(t *testing.T) {
	root := tempGitRepo(t)
	shell := gitSurfaceTestShell(t, root)
	shell.git.opBusy = true
	shell.pushRepo()
	if shell.pendingToast == "" {
		t.Fatal("a click while an operation runs vanished without a word")
	}
	if !shell.git.opBusy {
		t.Fatal("busy click must not clear the running op's flag")
	}
}

// TestSyncButtonWaitsForTheRunningOp pins the busy face of the clean view's
// sync button: while a Git operation runs, the primary action is dimmed, so a
// second click never starts a second push behind a slow first one.
func TestSyncButtonWaitsForTheRunningOp(t *testing.T) {
	root := tempGitRepo(t)
	shell := gitSurfaceTestShell(t, root)
	shell.git.snapshot = &gitworkbench.ChangesSnapshot{Root: root, Branch: "main", Signature: "sig-clean"}
	shell.git.upstream = gitworkbench.UpstreamStatus{OK: true, Remote: "origin", Branch: "main", Ahead: 2}
	showChangesPanel(shell)
	tt := ui.NewTester(shell.View, 1280, 760)
	tt.Frame()
	if node, ok := tt.Access("Push 2 commits"); !ok || node.Disabled {
		t.Fatalf("Push 2 commits idle: found=%v disabled=%v", ok, node.Disabled)
	}
	shell.git.opBusy = true
	tt.Frame()
	if node, ok := tt.Access("Push 2 commits"); !ok || !node.Disabled {
		t.Fatalf("Push 2 commits while an operation runs: found=%v disabled=%v — it must wait", ok, node.Disabled)
	}
}

// TestPushOutlivesTheWorkingTreeBudget pins the network budget: a push that
// needs longer than the 30s working-tree budget must still complete, because
// the Runner only ever tightens a parent deadline. The fake git sleeps 31s
// only for the push command, not the preceding remote/branch reads; under
// the old shared 30s parent this exact push was killed with "signal: killed".
func TestPushOutlivesTheWorkingTreeBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("sleeps 31s")
	}
	root, _, _ := pushFixture(t)
	shell := gitSurfaceTestShell(t, root)
	fake := filepath.Join(t.TempDir(), "slowgit")
	script := "#!/bin/sh\nif [ \"$1\" = \"rev-parse\" ]; then echo main; exit 0; fi\nif [ \"$1\" = \"push\" ]; then sleep 31; fi\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	shell.git.runner.GitBinary = fake

	var mu sync.Mutex
	shell.uiApplyOverride = func(fn func()) {
		mu.Lock()
		defer mu.Unlock()
		fn()
	}
	shell.pushRepo()
	deadline := time.Now().Add(60 * time.Second)
	for {
		mu.Lock()
		busy := shell.git.opBusy
		toast := shell.pendingToast
		mu.Unlock()
		if !busy {
			if !strings.HasPrefix(toast, "Pushed origin/main") {
				t.Fatalf("toast = %q, want the push to complete past the 30s budget", toast)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the 31s push never completed")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestPushRejectedToastSaysWhy: the remote moved after the last fetch, so the
// stale "ahead 1" Push is refused — the toast must carry Git's reason, not
// "exit status 1" (user report 2026-11-03: Push "did not pass", no reason).
func TestPushRejectedToastSaysWhy(t *testing.T) {
	root, origin, branch := pushFixture(t)
	clone := filepath.Join(t.TempDir(), "clone")
	for _, args := range [][]string{
		{"clone", "-q", origin, clone},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	for _, args := range [][]string{
		{"-C", clone, "config", "user.email", "t@example.com"},
		{"-C", clone, "config", "user.name", "t"},
		{"-C", clone, "commit", "-q", "--allow-empty", "-m", "remote moved"},
		{"-C", clone, "push", "-q", "origin", "HEAD:" + branch},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	shell := gitSurfaceTestShell(t, root)
	shell.git.snapshot = &gitworkbench.ChangesSnapshot{Root: root, Branch: branch, Signature: "sig-clean"}
	shell.git.upstream = gitworkbench.UpstreamStatus{OK: true, Remote: "origin", Branch: branch, Ahead: 1}
	showChangesPanel(shell)
	tt := ui.NewTester(shell.View, 1280, 760)

	toast := clickAndWaitPush(t, shell, tt, "Push 1 commit")
	if !strings.HasPrefix(toast, "Push failed: ") || !strings.Contains(toast, "pull first") || strings.Contains(toast, "exit status") {
		t.Fatalf("toast = %q, want the pull-first reason", toast)
	}
}

// TestDivergedBranchBlocksPushWithAHint: ahead and behind means Git refuses
// the push for sure, so the clean view offers the merge instead of a Push.
func TestDivergedBranchBlocksPushWithAHint(t *testing.T) {
	root, _, branch := pushFixture(t)
	shell := gitSurfaceTestShell(t, root)
	shell.git.upstream = gitworkbench.UpstreamStatus{OK: true, Remote: "origin", Branch: branch, Ahead: 1, Behind: 2}
	shell.git.snapshot = &gitworkbench.ChangesSnapshot{Root: root, Branch: branch, Signature: "sig-clean"}
	showChangesPanel(shell)
	tt := ui.NewTester(shell.View, 1280, 760)
	tt.Frame()
	if _, ok := tt.Find("Merge origin/" + branch + "…"); !ok {
		t.Fatal("the clean diverged view must offer the merge")
	}
	if _, ok := tt.Find("Push 1 commit"); ok {
		t.Fatal("the clean diverged view still offers a guaranteed-to-fail Push")
	}
}

// TestRebaseConflictOpensTheExplainingDialog: an integration op that cannot
// land opens a dialog (not a toast) that says what happened and names files.
func TestRebaseConflictOpensTheExplainingDialog(t *testing.T) {
	root := t.TempDir()
	shell := gitSurfaceTestShell(t, root)
	shell.uiApplyOverride = func(fn func()) { fn() }
	done := make(chan struct{})
	shell.runGitOp("Rebase", func(context.Context, string) (string, error) {
		defer close(done)
		return "", &gitworkbench.Error{Class: gitworkbench.ErrConflict, Op: "rebase", Paths: []string{"a.txt", "b/c.txt"},
			Stderr: "boom"}
	})
	<-done
	deadline := time.Now().Add(5 * time.Second)
	for !shell.gitNotice.open && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	n := shell.gitNotice
	if !n.open || n.title != "Rebase Stopped on Conflicts" || len(n.paths) != 2 || shell.pendingToast != "" {
		t.Fatalf("notice = %+v, toast %q", n, shell.pendingToast)
	}
}
