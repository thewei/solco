package nativeui

import (
	"strings"
	"testing"
	"time"

	"github.com/thewei/solco/internal/gitworkbench"
	"github.com/thewei/solco/internal/settings"
)

func TestRemoteAndTrackingMenus(t *testing.T) {
	for _, c := range []struct {
		row   string
		items []string
	}{
		{"origin", []string{"Fetch", "Edit Remote URL…", "Rename Remote…", "Remove Remote…"}},
		{"develop", []string{"Tracking"}},
	} {
		shell, tester, _ := treeTestShell(t)
		shell.git.remoteBranches = []gitworkbench.RemoteBranch{{Remote: "origin", Name: "main"}}
		shell.git.remotes = []gitworkbench.Remote{{Name: "origin", FetchURL: "https://example.com/a.git"}}
		tester.Frame()
		tester.Advance(time.Second) // new rows finish their enter motion before they can be hit
		tester.Frame()
		if err := tester.RightClick(c.row); err != nil {
			t.Fatalf("%s: %v", c.row, err)
		}
		tester.Frame()
		menu := strings.Join(tester.Menu(), "|")
		for _, item := range c.items {
			if !strings.Contains(menu, item) {
				t.Errorf("%s menu = %q, missing %q", c.row, menu, item)
			}
		}
	}
	// a leaf under the remote carries the delete
	shell, tester, _ := treeTestShell(t)
	shell.git.remoteBranches = []gitworkbench.RemoteBranch{{Remote: "origin", Name: "main"}}
	tester.Frame()
	if err := tester.RightClick("main"); err == nil {
		tester.Frame()
	}
	shell.confirmDeleteRemoteBranch("origin/main")
	if !shell.confirmOpen || shell.confirmKind != "git-delete-remote-branch" || shell.confirmTarget != "origin/main" {
		t.Fatalf("confirm = %v %q %q", shell.confirmOpen, shell.confirmKind, shell.confirmTarget)
	}
}

func TestRemotesWithoutBranchesAreRowsAndAddRemoteOpens(t *testing.T) {
	shell, tester, _ := treeTestShell(t)
	shell.git.remotes = []gitworkbench.Remote{{Name: "upstream", FetchURL: "https://example.com/u.git"}}
	tester.Frame()
	if !tester.HasText("upstream") {
		t.Fatalf("a configured remote with no fetched branch shows as a row: %q", tester.Texts())
	}
	shell.openRemoteDialog("url", "upstream")
	d := shell.gitMore.remote
	if !d.open || d.mode != "url" || d.url != "https://example.com/u.git" {
		t.Fatalf("dialog = %+v", d)
	}
	shell.openRemoteDialog("add", "")
	if shell.gitMore.remote.mode != "add" || shell.gitMore.remote.url != "" {
		t.Fatalf("add dialog = %+v", shell.gitMore.remote)
	}
}

func TestResetConfirmWordsEachMode(t *testing.T) {
	shell, _, _ := treeTestShell(t)
	hash := strings.Repeat("a", 40)
	for mode, title := range map[gitworkbench.ResetMode]string{
		gitworkbench.ResetSoft: "(Soft)", gitworkbench.ResetMixed: "(Mixed)", gitworkbench.ResetHard: "(Hard)"} {
		shell.confirmOpen = false
		shell.confirmResetTo(hash, "subject", mode)
		if !shell.confirmOpen || shell.confirmKind != "git-reset-"+string(mode) || !strings.Contains(shell.confirmTitle, title) || shell.confirmTarget != hash {
			t.Fatalf("%s: %v %q %q %q", mode, shell.confirmOpen, shell.confirmKind, shell.confirmTitle, shell.confirmTarget)
		}
	}
	shell.confirmOpen = false
	shell.confirmResetTo("abc", "x", gitworkbench.ResetHard)
	if shell.confirmOpen {
		t.Fatal("a short hash is not accepted")
	}
}

func TestRebaseSheetOrderingAndValidity(t *testing.T) {
	shell, _, _ := treeTestShell(t)
	r := &shell.gitMore.rebase
	mk := func(h, subject string) rebaseRow {
		return rebaseRow{commit: gitworkbench.RebaseCommit{CommitInfo: gitworkbench.CommitInfo{Hash: h, Short: h, Subject: subject}}, action: "pick", message: subject}
	}
	r.rows = []rebaseRow{mk("a", "one"), mk("b", "two"), mk("c", "three")}
	r.move(2, -1)
	r.move(0, -1) // off the top: ignored
	if r.rows[1].commit.Hash != "c" || r.rows[2].commit.Hash != "b" {
		t.Fatalf("order = %v", r.rows)
	}
	if !shell.rebaseValid() {
		t.Fatal("all picks are valid")
	}
	r.rows[0].action = "squash"
	if shell.rebaseValid() {
		t.Fatal("squashing the first kept commit is invalid")
	}
	r.rows[0].action = "drop"
	r.rows[1].action = "squash"
	if shell.rebaseValid() {
		t.Fatal("squash right after a drop at the top is invalid")
	}
	r.rows[1].action = "reword"
	r.rows[1].message = " "
	if shell.rebaseValid() {
		t.Fatal("a reword needs a message")
	}
	r.rows[1].message = "new"
	steps := r.steps()
	if !shell.rebaseValid() || steps[0].Action != gitworkbench.RebaseDrop || steps[1].Message != "new" {
		t.Fatalf("steps = %+v", steps)
	}
}

func TestCommitAndPushFlagLifecycle(t *testing.T) {
	shell, _, _ := treeTestShell(t)
	shell.openQuickCommit(true)
	if !shell.gitMore.pushAfterCommit {
		t.Fatal("Commit and Push… sets the flag")
	}
	shell.cancelCommitSurface()
	if shell.gitMore.pushAfterCommit {
		t.Fatal("cancelling clears it")
	}
	shell.openQuickCommit(true)
	shell.commitDialogOpen = false
	shell.openQuickCommit(false)
	if shell.gitMore.pushAfterCommit {
		t.Fatal("a plain Commit never pushes")
	}
	shell.gitMore.pushAfterCommit = true
	shell.afterCommitPush()
	if shell.gitMore.pushAfterCommit {
		t.Fatal("the push continuation is consumed")
	}
}

func TestAutoFetchCadence(t *testing.T) {
	shell := NewShell()
	if shell.autoFetchInterval() != 0 {
		t.Fatal("auto-fetch is off by default")
	}
	for minutes, want := range map[int]time.Duration{5: 5 * time.Minute, 60: time.Hour, 7: 0, -1: 0} {
		shell.settings.Workbench.AutoFetchMinutes = minutes
		if got := shell.autoFetchInterval(); got != want {
			t.Errorf("%d minutes -> %v, want %v", minutes, got, want)
		}
	}
	if autoFetchLabel(15) != "Every 15 minutes" || autoFetchLabel(3) != "Off" {
		t.Fatal("labels")
	}
	shell.settings.Workbench.AutoFetchMinutes = 5
	shell.rearmAutoFetch()
	if shell.gitMore.autoFetchTimer == nil {
		t.Fatal("a cadence arms the timer")
	}
	shell.settings.Workbench.AutoFetchMinutes = 0
	shell.rearmAutoFetch()
	if shell.gitMore.autoFetchTimer != nil {
		t.Fatal("off disarms it")
	}
	var _ settings.Settings
}

func TestSubmoduleBadge(t *testing.T) {
	if submoduleBadge(gitworkbench.SubmoduleClean) != "" || submoduleBadge(gitworkbench.SubmoduleUninitialized) != "not set up" ||
		submoduleBadge(gitworkbench.SubmoduleModified) != "moved" || submoduleBadge(gitworkbench.SubmoduleConflict) != "conflict" {
		t.Fatal("badges")
	}
}

func TestSubmodulesSectionAppearsOnlyWithSubmodules(t *testing.T) {
	shell, tester, _ := treeTestShell(t)
	if tester.HasText("Submodules") {
		t.Fatal("no submodules, no section")
	}
	shell.git.submodules = []gitworkbench.Submodule{{Path: "vendor/lib", Commit: strings.Repeat("a", 40), State: gitworkbench.SubmoduleUninitialized}}
	tester.Frame()
	if !tester.HasText("Submodules") || !tester.HasText("lib") || !tester.HasText("not set up") {
		t.Fatalf("section missing: %q", tester.Texts())
	}
}
