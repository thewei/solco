package nativeui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thewei/solco/internal/gitworkbench"
)

/**
 * [INPUT]: 真实仓库 fixture、Save/Apply/Rename Stash 对话框
 * [OUTPUT]: 对齐 Fork 的 stash 操作面回归（部分 stash、Apply 并删除、重命名、菜单）
 * [POS]: 2026-11-03 用户要求"Stashes 列表与 Changes 里的 stash/unstash 全部对齐 Fork"
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func (f *graphFixture) reloadStashes(t *testing.T) {
	t.Helper()
	list, err := f.shell.git.runner.StashList(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	f.shell.git.stashes = list
}

func (f *graphFixture) dirty(t *testing.T, names ...string) {
	t.Helper()
	var files []gitworkbench.ChangeFile
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(f.root, n), []byte(n+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, gitworkbench.ChangeFile{Path: n, Status: gitworkbench.StatusAdded, Untracked: true})
	}
	f.shell.git.snapshot = &gitworkbench.ChangesSnapshot{Root: f.root, Branch: "main", Signature: "sig-stash", Files: files}
}

// Save Stash lists every changed file ticked; unticking one stashes only the
// others and leaves it in the working tree (Fork's partial stash).
func TestSaveStashStashesOnlyTheTickedFiles(t *testing.T) {
	f := newGraphFixture(t)
	f.dirty(t, "a.txt", "b.txt")
	f.shell.openSaveStashDialog()
	f.tester.Frame()
	if !f.tester.HasText("Save Stash") || !f.tester.HasText("a.txt") || !f.tester.HasText("b.txt") {
		t.Fatalf("the dialog should list the files: %q", f.tester.Texts())
	}
	for i := range f.shell.gitMore.stash.files {
		if !f.shell.gitMore.stash.files[i].on {
			t.Fatal("every file starts ticked")
		}
	}
	f.shell.gitMore.stash.files[1].on = false // untick b.txt (the label also sits in the tree behind the sheet)
	f.tester.Frame()
	f.shell.gitMore.stash.message = "only a"
	if err := f.tester.Click("Stash"); err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	f.reloadStashes(t)
	if len(f.shell.git.stashes) != 1 || !strings.Contains(f.shell.git.stashes[0].Subject, "only a") {
		t.Fatalf("stashes = %+v", f.shell.git.stashes)
	}
	if _, err := os.Stat(filepath.Join(f.root, "a.txt")); err == nil {
		t.Fatal("a.txt should have gone into the stash")
	}
	if _, err := os.Stat(filepath.Join(f.root, "b.txt")); err != nil {
		t.Fatal("the unticked b.txt must stay in the working tree")
	}
}

func TestSaveStashWithEverythingTickedStashesTheWholeSet(t *testing.T) {
	f := newGraphFixture(t)
	f.dirty(t, "a.txt", "b.txt")
	f.shell.openSaveStashDialog()
	f.tester.Frame()
	if err := f.tester.Click("Stash"); err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	f.reloadStashes(t)
	if len(f.shell.git.stashes) != 1 {
		t.Fatalf("stashes = %+v", f.shell.git.stashes)
	}
	for _, n := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(f.root, n)); err == nil {
			t.Fatalf("%s should be stashed", n)
		}
	}
}

// Apply Stash is one sheet: unchecked it applies and keeps the stash, checked
// it also deletes it.
func TestApplyStashSheetKeepsOrDeletes(t *testing.T) {
	for _, deleteAfter := range []bool{false, true} {
		f := newGraphFixture(t)
		f.dirty(t, "a.txt")
		f.shell.openSaveStashDialog()
		f.tester.Frame()
		if err := f.tester.Click("Stash"); err != nil {
			t.Fatal(err)
		}
		f.drain(t)
		f.reloadStashes(t)
		f.shell.git.snapshot = &gitworkbench.ChangesSnapshot{Root: f.root, Branch: "main", Signature: "sig-clean"}
		f.shell.openApplyStashDialog(f.shell.git.stashes[0])
		f.tester.Frame()
		if !f.tester.HasText("Apply Stash") || !f.tester.HasText("Delete stash after applying") {
			t.Fatalf("sheet texts = %q", f.tester.Texts())
		}
		if deleteAfter {
			if err := f.tester.Click("Delete stash after applying"); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.tester.Click("Apply"); err != nil {
			t.Fatal(err)
		}
		f.drain(t)
		f.reloadStashes(t)
		if _, err := os.Stat(filepath.Join(f.root, "a.txt")); err != nil {
			t.Fatalf("delete=%v: the stash should have been applied", deleteAfter)
		}
		if kept := len(f.shell.git.stashes) == 1; kept == deleteAfter {
			t.Fatalf("delete=%v: stashes left = %d", deleteAfter, len(f.shell.git.stashes))
		}
	}
}

func TestRenameStashFromTheSidebarMenu(t *testing.T) {
	f := newGraphFixture(t)
	f.dirty(t, "a.txt")
	f.shell.openSaveStashDialog()
	f.tester.Frame()
	if err := f.tester.Click("Stash"); err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	f.reloadStashes(t)
	f.tester.Frame()
	f.shell.openRenameStash(f.shell.git.stashes[0])
	if !f.shell.dialogOpen || f.shell.dialogKind != "git-rename-stash" {
		t.Fatalf("dialog = %v %q", f.shell.dialogOpen, f.shell.dialogKind)
	}
	f.shell.submitTextDialog(f.shell.dialogKind, f.shell.dialogTarget, "fresh name")
	f.drain(t)
	f.reloadStashes(t)
	if len(f.shell.git.stashes) != 1 || stashTitle(f.shell.git.stashes[0].Subject) != "fresh name" {
		t.Fatalf("stashes = %+v toast=%q", f.shell.git.stashes, f.shell.pendingToast)
	}
}

func TestHideStashesDropsTheStashChips(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	base := strings.Repeat("b", 40)
	s.git.stashes = []gitworkbench.Stash{{Ref: "stash@{0}", Hash: strings.Repeat("a", 40), Base: base, Subject: "WIP"}}
	if got := len(s.newGitChipIndex().stashes[base]); got != 1 {
		t.Fatalf("stash chips = %d, want 1", got)
	}
	s.settings.Workbench.HideStashes = true
	if got := len(s.newGitChipIndex().stashes[base]); got != 0 {
		t.Fatalf("hidden stashes must not chip the commit list, got %d", got)
	}
}
