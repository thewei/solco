package nativeui

/**
 * [INPUT]: git_quick_commit.go 的一行提交栏与 Commit 弹窗、headless MyGo Tester、gitSurfaceTestShell 夹具
 * [OUTPUT]: 提交栏契约——两处 Commit 按钮取自同一尺寸档；提交栏的草稿原样交给唯一的 Commit 表面
 * [POS]: 右侧面板 Changes 页提交栏的回归测试（2026-11-08 G5 起取代 git_review_split_test.go：Git 页的分栏/引用树/历史评审测试随页面退役）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The two Commit buttons take their size from a shared tier instead of an
// inline font size: the dense Git strip's composer wears the compact tier,
// and the Commit dialog's footer pairs the primary with Cancel Commit on the
// regular one — the two were the only Buttons restating Body / BodySmall.
func TestCommitPrimaryButtonsTakeTheirSizeFromASharedTier(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	showChangesPanel(s)

	composer := ui.NewTester(func(c *ui.Context) { s.gitQuickCommitForm(c, s.git.snapshot) }, 600, 200)
	composer.Frame()
	reference := ui.NewTester(func(c *ui.Context) { compactPrimaryButton(c, "Commit") }, 200, 60)
	reference.Frame()
	got, ok := composer.Find("Commit")
	want, refOK := reference.Find("Commit")
	if !ok || !refOK {
		t.Fatalf("missing Commit button; composer=%q", composer.Texts())
	}
	if got.H != want.H {
		t.Fatalf("composer Commit text is %.2f high, the compact primary tier's is %.2f", got.H, want.H)
	}

	s.openCommitSurface()
	footer := ui.NewTester(s.commitActionFooter, 800, 100)
	footer.Frame()
	selected := len(s.selectedCommitPaths())
	cancel, cancelOK := footer.Find("Cancel Commit")
	primary, primaryOK := footer.Find("Commit " + itoa(selected) + " file" + pluralS(selected))
	if !cancelOK || !primaryOK {
		t.Fatalf("footer lacks its buttons; texts=%q", footer.Texts())
	}
	if cancel.H != primary.H {
		t.Fatalf("Cancel Commit text is %.2f high but the primary beside it is %.2f", cancel.H, primary.H)
	}
}

func TestGitQuickComposerHandoffUsesExistingCommitModal(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	s.git.refreshing = true
	showChangesPanel(s)
	s.gitQuickSubject = "feat: new native Git layout"
	s.gitQuickBody = "Prepared in bottom composer"
	s.gitQuickAmend = true
	tt := ui.NewTester(s.View, 1200, 800)
	tt.Frame()
	if err := tt.Click("Commit"); err != nil {
		t.Fatal(err)
	}
	if s.surface.current() != WorkspaceSurfaceCommit || !s.commitDialogOpen {
		t.Fatal("bottom composer must open the sole fenced Commit surface")
	}
	if s.surface.commit.Subject != s.gitQuickSubject ||
		s.surface.commit.Body != s.gitQuickBody ||
		!s.surface.commit.Amend || !s.surface.commit.AmendPrefilled {
		t.Fatalf("draft not preserved across composer handoff: %+v", s.surface.commit)
	}
}
