package nativeui

import (
	"strings"
	"testing"
	"time"

	"github.com/thewei/solco/internal/gitworkbench"
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

func TestActionsMenuCarriesTheThirdBatch(t *testing.T) {
	shell, tester, _ := treeTestShell(t)
	shell.git.remotes = []gitworkbench.Remote{{Name: "origin", FetchURL: "https://github.com/a/b.git"}}
	tester.Frame()
	if err := tester.Click("Actions"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	menu := menuText(tester.Menu())
	for _, want := range []string{"Fetch & Pull Options", "Apply Patch…", "Reflog…", "Add Submodule…"} {
		if !strings.Contains(menu, want) {
			t.Errorf("Actions lost %s: %s", want, menu)
		}
	}
	tester.CloseMenu()
}

func TestWorktreeRowsOfferRemoveOnlyForLinkedTrees(t *testing.T) {
	for _, c := range []struct {
		row    string
		remove bool
	}{{"trunk", false}, {"side", true}} {
		shell, tester, root := treeTestShell(t)
		shell.git.worktrees = []gitworkbench.Worktree{{Path: root, Branch: "trunk"}, {Path: root + "/side", Branch: "side"}}
		settleRows(tester)
		if err := tester.RightClick(c.row); err != nil {
			t.Fatalf("%s: %v", c.row, err)
		}
		tester.Frame()
		menu := menuText(tester.Menu())
		if !strings.Contains(menu, "Open as Workspace") {
			t.Errorf("%s menu = %q, missing Open as Workspace", c.row, menu)
		}
		if got := strings.Contains(menu, "Remove Worktree…"); got != c.remove {
			t.Errorf("%s menu = %q, remove offered = %v want %v", c.row, menu, got, c.remove)
		}
	}
}

func TestTagMenuDeletesFromTheRemoteAndTheDialogOpens(t *testing.T) {
	shell, tester, _ := treeTestShell(t)
	shell.git.remotes = []gitworkbench.Remote{{Name: "upstream"}, {Name: "origin"}}
	settleRows(tester)
	if err := tester.RightClick("v1.0"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if menu := menuText(tester.Menu()); !strings.Contains(menu, "Delete Tag from Remote…") {
		t.Fatalf("tag menu = %q", menu)
	}
	tester.CloseMenu()

	shell.confirmDeleteRemoteTag("v1.0")
	if !shell.confirmOpen || shell.confirmKind != "git-delete-remote-tag" || shell.confirmTarget != "origin\x00v1.0" {
		t.Fatalf("confirm = %v %q %q (origin wins over the first remote)", shell.confirmOpen, shell.confirmKind, shell.confirmTarget)
	}
	shell.git.upstream.OK, shell.git.upstream.Remote = true, "upstream"
	if shell.tagRemote() != "upstream" {
		t.Fatal("the branch's own remote wins")
	}

	hash := strings.Repeat("b", 40)
	shell.openTagDialog(hash)
	if d := shell.gitMore.tag; !d.open || d.hash != hash || d.root != shell.git.root {
		t.Fatalf("tag dialog = %+v", d)
	}
	shell.gitMore.tag.open = false
	shell.openTagDialog("HEAD")
	if shell.gitMore.tag.open {
		t.Fatal("a non-hash commit is refused")
	}
}

func TestStashSectionMenuCarriesMessageAndDropAll(t *testing.T) {
	shell, tester, _ := treeTestShell(t)
	settleRows(tester)
	if err := tester.RightClick("Toggle Stashes"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	menu := menuText(tester.Menu())
	for _, want := range []string{"Stash All Changes", "Save Stash…", "Drop All Stashes…"} {
		if !strings.Contains(menu, want) {
			t.Errorf("stash section menu = %q, missing %q", menu, want)
		}
	}
	tester.CloseMenu()
	shell.confirmDropAllStashes()
	if !shell.confirmOpen || shell.confirmKind != "git-stash-clear" || !strings.Contains(shell.confirmTitle, "1 Stashes") {
		t.Fatalf("confirm = %v %q %q", shell.confirmOpen, shell.confirmKind, shell.confirmTitle)
	}
	shell.openSaveStashDialog()
	if !shell.gitMore.stash.open || shell.gitMore.stash.kind != "save" || shell.gitMore.stash.root != shell.git.root {
		t.Fatalf("dialog = %+v", shell.gitMore.stash)
	}
}

func TestSubmoduleRowOffersRemoveAndAddDialogGuessesTheFolder(t *testing.T) {
	shell, tester, _ := treeTestShell(t)
	shell.git.submodules = []gitworkbench.Submodule{{Path: "vendor/lib", Commit: strings.Repeat("a", 40)}}
	settleRows(tester)
	if err := tester.RightClick("lib"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if menu := menuText(tester.Menu()); !strings.Contains(menu, "Remove Submodule…") {
		t.Fatalf("submodule menu = %q", menu)
	}
	tester.CloseMenu()
	shell.confirmRemoveSubmodule("vendor/lib")
	if shell.confirmKind != "git-remove-submodule" || shell.confirmTarget != "vendor/lib" {
		t.Fatalf("confirm = %q %q", shell.confirmKind, shell.confirmTarget)
	}
	for in, want := range map[string]string{
		"https://github.com/a/lib.git": "lib", "git@host:team/tools.git": "tools", "https://x.io/a/b/": "b", "plain": "plain",
	} {
		if got := submoduleFolder(in); got != want {
			t.Errorf("submoduleFolder(%q) = %q, want %q", in, got, want)
		}
	}
	shell.openAddSubmoduleDialog()
	if !shell.gitMore.submodule.open {
		t.Fatal("Add Submodule opens its dialog")
	}
}

func TestConfirmsForCommitLevelActionsNeedAFullHash(t *testing.T) {
	shell, _, _ := treeTestShell(t)
	hash := strings.Repeat("c", 40)
	shell.confirmCheckoutCommit(hash, "subject")
	if !shell.confirmOpen || shell.confirmKind != "git-checkout-detached" || shell.confirmTarget != hash {
		t.Fatalf("checkout confirm = %v %q %q", shell.confirmOpen, shell.confirmKind, shell.confirmTarget)
	}
	shell.confirmOpen = false
	shell.confirmRestoreFile(hash, "src/a.go")
	if !shell.confirmOpen || shell.confirmKind != "git-restore-file" || shell.confirmTarget != hash+"\x00src/a.go" {
		t.Fatalf("restore confirm = %v %q %q", shell.confirmOpen, shell.confirmKind, shell.confirmTarget)
	}
	shell.confirmOpen = false
	shell.confirmCheckoutCommit("abc", "x")
	shell.confirmRestoreFile("abc", "a")
	if shell.confirmOpen {
		t.Fatal("a short hash is refused")
	}
	shell.confirmMergeSquash("develop")
	if !shell.confirmOpen || shell.confirmKind != "git-merge-squash" || shell.confirmTarget != "develop" {
		t.Fatalf("squash confirm = %v %q %q", shell.confirmOpen, shell.confirmKind, shell.confirmTarget)
	}
	shell.confirmOpen = false
	shell.confirmMergeSquash("main") // the current branch
	if shell.confirmOpen {
		t.Fatal("squashing the current branch into itself is refused")
	}
}

func TestCommitLinkItemsFollowTheRemote(t *testing.T) {
	shell, _, _ := treeTestShell(t)
	if _, ok := shell.commitWebRepo(); ok {
		t.Fatal("no remote, no link")
	}
	shell.git.remotes = []gitworkbench.Remote{{Name: "origin", FetchURL: "/Users/me/origin.git"}}
	if _, ok := shell.commitWebRepo(); ok {
		t.Fatal("a local-path remote has no web page")
	}
	shell.git.remotes = []gitworkbench.Remote{{Name: "origin", FetchURL: "git@github.com:a/b.git"}}
	web, ok := shell.commitWebRepo()
	if !ok || web.Name() != "GitHub" || web.CommitURL("H") != "https://github.com/a/b/commit/H" {
		t.Fatalf("web = %+v ok=%v", web, ok)
	}
}

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

func TestReflogSheetOpensLoading(t *testing.T) {
	shell, _, _ := treeTestShell(t)
	shell.openReflogSheet()
	if r := shell.gitMore.reflog; !r.open || !r.loading {
		t.Fatalf("reflog = %+v", r)
	}
}

func TestReflogPartsSplitsKindFromDetail(t *testing.T) {
	for _, c := range []struct{ in, kind, detail string }{
		{"commit: fix typo", "commit", "fix typo"},
		{"checkout: moving from a to b", "checkout", "moving from a to b"},
		{"rebase (finish): returning to refs/heads/main", "rebase", "(finish) · returning to refs/heads/main"},
		{"pull --rebase origin main (pick): feat: x", "pull", "--rebase origin main (pick) · feat: x"},
		{"reset: moving to HEAD", "reset", "moving to HEAD"},
		{"", "head", ""},
	} {
		if k, d := reflogParts(c.in); k != c.kind || d != c.detail {
			t.Errorf("reflogParts(%q) = %q, %q; want %q, %q", c.in, k, d, c.kind, c.detail)
		}
	}
}

func TestGitConfirmButtonsNameTheirAction(t *testing.T) {
	for kind, want := range map[string]string{
		"git-remove-submodule": "Remove", "git-discard": "Discard", "git-reset-hard": "Reset",
		"git-stash-clear": "Drop All", "history-trash-forever": "Confirm", "": "Confirm",
	} {
		if got := confirmActionText(kind); got != want {
			t.Errorf("confirmActionText(%q) = %q, want %q", kind, got, want)
		}
	}
	// Every git confirm opened in this package has its own verb.
	for _, kind := range []string{"git-merge-squash", "git-checkout-detached", "git-restore-file", "git-delete-remote-tag", "git-remove-worktree"} {
		if confirmActionText(kind) == "Confirm" {
			t.Errorf("%s has no action label", kind)
		}
	}
}
