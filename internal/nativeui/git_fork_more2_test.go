package nativeui

import (
	"strings"
	"testing"
	"time"
)

func settleRows(shellTester interface {
	Frame()
	Advance(time.Duration)
}) {
	shellTester.Frame()
	shellTester.Advance(time.Second) // new rows finish their enter motion before they can be hit
	shellTester.Frame()
}

func menuText(items []string) string { return strings.Join(items, "|") }

func TestIntegrationNoticesCoverTheNewOps(t *testing.T) {
	shell, _, _ := treeTestShell(t)
	for op, title := range map[string]string{
		"Pull and Rebase": "Rebase Stopped on Conflicts",
		"Squash Merge":    "Squash Merge Cancelled",
	} {
		if !integrationOps[op] {
			t.Fatalf("%s must be an integration op", op)
		}
		shell.openGitNotice(op, errGitOp("x"), []string{"a.txt"}, false)
		if shell.gitNotice.title != title {
			t.Errorf("%s notice = %q, want %q", op, shell.gitNotice.title, title)
		}
	}
	if !integrationOps["Pull and Merge"] {
		t.Fatal("Pull and Merge may pause on conflicts")
	}
	shell.openGitNotice("Pull and Merge", errGitOp("x"), []string{"a.txt"}, true)
	if shell.gitNotice.title != "Merge Needs Your Help" {
		t.Fatalf("paused merge notice = %q", shell.gitNotice.title)
	}
}

func TestCommitOptionsResetWithTheSurface(t *testing.T) {
	shell, _, _ := treeTestShell(t)
	shell.openQuickCommit(false)
	shell.surface.commit.SignOff, shell.surface.commit.NoVerify = true, true
	shell.cancelCommitSurface()
	shell.openQuickCommit(false)
	if shell.surface.commit.SignOff || shell.surface.commit.NoVerify {
		t.Fatal("Sign off and Skip hooks are per commit and must not persist")
	}
}

func TestGitConfirmButtonsNameTheirAction(t *testing.T) {
	for kind, want := range map[string]string{
		"git-discard": "Discard", "git-delete-untracked": "Delete", "git-delete-branch": "Delete",
		"git-merge-squash": "Squash", "git-sequencer-abort": "Abort",
		"history-trash-forever": "Confirm", "": "Confirm",
	} {
		if got := confirmActionText(kind); got != want {
			t.Errorf("confirmActionText(%q) = %q, want %q", kind, got, want)
		}
	}
	// Every git confirm still opened in this package has its own verb.
	for _, kind := range []string{"git-merge-ff", "git-merge-no-ff"} {
		if confirmActionText(kind) == "Confirm" {
			t.Errorf("%s has no action label", kind)
		}
	}
}
