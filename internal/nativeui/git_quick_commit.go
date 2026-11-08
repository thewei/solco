package nativeui

/**
 * [INPUT]: gitService 的变更快照/sequencer 状态、Commit 表面草稿（surface.commit）、commit_ai 的 AI 主题候选
 * [OUTPUT]: 右侧面板 Changes 页的提交条：gitQuickCommitComposer（一行主题 + Commit，聚焦才展开描述与选项；两个按钮 KeepFocus，按下不触发展开）、gitQuickCommitForm/gitQuickCommitActions、openQuickCommit（把草稿交给唯一的 Commit 表面）、gitCenterWidth（Commit 背景卡的可用宽度）
 * [POS]: 提交条只是进入唯一 Commit 表面的入口，不自己调 git、不复制提交事务；原 Git 整页（Diff 表面 + Commits 视图）于 2026-11-08 G5 退役，此文件只留面板用到的这一块（原 git_review_split.go）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/gitworkbench"
)

// gitCenterWidth is the room the diff has in the center — the Commit editor's
// backdrop and the Detail card: the window less the sidebar, when it shows,
// and less a docked right panel.
func (s *Shell) gitCenterWidth(windowW float32) float32 {
	width := windowW - s.sidePanelDockedWidth(windowW)
	if !s.sidebarCollapsed {
		width -= s.sidebarW
	}
	return width
}

// Quick composer is an entry point into the SINGLE existing fenced Commit
// modal. It never calls git itself or duplicates the commit transaction.
func (s *Shell) gitQuickCommitComposer(c *ui.Context) {
	t := c.Theme()
	snap := s.gitSnapshot()
	if snap == nil {
		return
	}
	// A clean tree has nothing to commit: the bar goes unless a draft or an
	// amend is in progress (2026-10-30 UX audit — the empty state is one block).
	if len(snap.Files) == 0 && s.gitQuickSubject == "" && s.gitQuickBody == "" && !s.gitQuickAmend {
		return
	}
	// A merge/rebase/cherry-pick in progress owns the next step (the banner's
	// Continue/Abort): a disabled Commit bar beneath it only invites a typed
	// message that nothing can send (device E2E 2026-11-03).
	if s.git.sequencerChecked && s.git.sequencer.Active() {
		return
	}
	ui.Column(c).FillWidth().Shrink(0).Padding(8, 12).
		BorderWidth(1, 0, 0, 0).BorderColor(s.tokens(t.Dark).BorderSubtle).Children(func() {
		contentColumn(c, formColumnWidth, func() {
			s.gitQuickCommitForm(c, snap)
		})
	})
}

// gitQuickCommitForm is the composer's fields and actions; the caller bounds
// its width so the inputs never run across a wide diff. Idle it is one line —
// the subject and the Commit action; focusing it (or a draft, an amend, a
// running draft) opens the description and the options row.
func (s *Shell) gitQuickCommitForm(c *ui.Context, snap *gitworkbench.ChangesSnapshot) {
	t := c.Theme()
	blocked := s.git.opBusy || s.git.committing || s.git.branchBusy ||
		len(snap.Files) == 0 || (s.git.sequencerChecked && s.git.sequencer.Active())
	form := ui.Column(c).FillWidth().Gap(5)
	open := s.gitQuickSubject != "" || s.gitQuickBody != "" || s.gitQuickAmend ||
		s.commitQuickRunning || s.commitQuickError != "" || form.FocusWithin()
	form.Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			textField(c.Key("git-quick-subject"), &s.gitQuickSubject).Grow(1).MinWidth(0).Height(Controls().RegularHeight).
				Placeholder("Commit subject").Label("Commit subject draft")
			if !open {
				s.gitQuickCommitActions(c, blocked)
			}
		})
		if !open {
			return
		}
		textField(c.Key("git-quick-body"), &s.gitQuickBody).FillWidth().Height(Controls().RegularHeight).
			Placeholder("Description (optional)").Label("Commit description draft")
		ui.Row(c).FillWidth().Height(28).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Checkbox(c, &s.gitQuickAmend, "Amend")
			if s.commitQuickRunning {
				ui.Spinner(c).Size(13, 13)
				ui.Text(c, "Generating subject…").FontSize(Typography().Micro).TextColor(t.TextMuted)
				if compactButton(c, "Cancel").Clicked() {
					s.cancelQuickAICommit()
				}
			}
			ui.Spacer(c)
			ui.Text(c, pluralFiles(len(snap.Files))).FontSize(Typography().BodySmall).TextColor(t.TextMuted)
			s.gitQuickCommitActions(c, blocked)
		})
		if s.commitQuickError != "" {
			ui.Text(c, s.commitQuickError).FontSize(Typography().BodySmall).TextColor(t.Warning).MaxWidth(600)
		}
	})
}

// gitQuickCommitActions is the Commit button and its options pull-down.
func (s *Shell) gitQuickCommitActions(c *ui.Context, blocked bool) {
	t := c.Theme()
	// KeepFocus: a press must not move the focus onto the button. Focus in the
	// form opens it and moves the buttons to the options row mid-click, and
	// the release then lands on another button — the first click only opened
	// the form (2026-11-08 run). Tab still reaches the buttons.
	button := compactPrimaryButton(c, "Commit").KeepFocus()
	button.Disabled(blocked || s.commitQuickRunning)
	if button.Clicked() {
		s.openQuickCommit(false)
	}
	// The dropdown is independent of Commit: it only requests an AI
	// candidate and fills Subject after root/snapshot/draft checks.
	dropdown := gorexIconButton(c, gorexColorsOf(t.Dark), iconChevronDown, "Commit options", "Commit options",
		false, gorexPaneBtn, gorexPaneGlyph).KeepFocus()
	dropdown.Menu(func(m *ui.Menu) {
		if m.Item("Commit and Push…").Disabled(blocked || s.commitQuickRunning).Chosen() {
			s.openQuickCommit(true)
		}
		m.Separator()
		if m.Item("Draft Subject with AI").Disabled(blocked || s.commitQuickRunning || !s.commitGeneratorUsable()).Chosen() {
			s.startQuickAICommit()
		}
	})
}

// openQuickCommit opens the Commit dialog with the composer's draft; push
// makes its success continue with a Push (Commit and Push…).
func (s *Shell) openQuickCommit(push bool) {
	s.openCommitSurface()
	s.surface.commit.Subject = s.gitQuickSubject
	s.surface.commit.Body = s.gitQuickBody
	s.surface.commit.Amend = s.gitQuickAmend
	// Do not overwrite a user-written amend message with HEAD.
	s.surface.commit.AmendPrefilled = s.gitQuickAmend &&
		(s.gitQuickSubject != "" || s.gitQuickBody != "")
	s.gitMore.pushAfterCommit = push
}
