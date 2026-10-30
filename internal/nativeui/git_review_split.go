package nativeui

/**
 * [INPUT]: Git commit/file selections, staged/unstaged tree, existing Godiff and Commit modal
 * [OUTPUT]: Fork-style Changes dual columns and Commits history-over-review (commit message card + changed files | read-only diff, no sub-tabs); the one-line commit composer opens on focus; the file navigator closes by itself in a narrow center, and a toolbar toggle brings it back
 * [POS]: Single native Git workspace surface; Herdr/runtime and Git mutations keep their owners
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
)

const (
	gitSplitMinTop          float32 = 170
	gitSplitMinBottom       float32 = 220
	gitSplitDefaultTop      float32 = 420
	gitFilePaneDefaultWidth float32 = 292
	gitFilePaneMinWidth     float32 = 205
	gitFileSplitterWidth    float32 = 6
	// gitFilePaneCollapseBelow is the width of the center (the window less
	// the sidebar) under which the file navigator closes by itself: open at
	// its default width, it would leave the diff under ~450 px.
	gitFilePaneCollapseBelow float32 = 750
)

// gitSplitReviewSurface intentionally has two different spatial hierarchies:
// Changes: file navigator | (diff + compact commit composer)
// Commits: commit history above (commit message + changed files | read-only diff).
func (s *Shell) gitSplitReviewSurface(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		if s.commitsMode {
			_, windowH := c.Size()
			maxTop := windowH - gitSplitMinBottom - 85
			if maxTop < gitSplitMinTop {
				maxTop = gitSplitMinTop
			}
			if s.gitUpperHeight == 0 {
				s.gitUpperHeight = gitSplitDefaultTop
			}
			upper := clampF(s.gitUpperHeight, gitSplitMinTop, maxTop)
			ui.Column(c).FillWidth().Height(upper).Shrink(0).MinHeight(0).Children(func() {
				s.gitCommitHistoryPane(c)
			})
			handle := ui.Row(c).Height(8).FillWidth().Shrink(0).Cursor(ui.CursorResizeNS).
				Label("Resize Git history and diff panes").Center().Children(func() {
				ui.Box(c).FillWidth().Height(1).Background(s.tokens(t.Dark).BorderSubtle)
			})
			if _, dy, held := handle.Dragged(); held {
				s.gitUpperHeight = clampF(upper+dy, gitSplitMinTop, maxTop)
			}
			s.gitCommitReview(c)
			return
		}
		s.gitLocalChangesReview(c)
	})
}

func (s *Shell) gitLocalChangesReview(c *ui.Context) {
	ui.Row(c).Grow(1).MinHeight(0).MinWidth(0).AlignItems(ui.Stretch).Children(func() {
		s.gitFileNavigationColumn(c, func() { s.sidebarFilesView(c) })
		ui.Column(c).Grow(1).MinHeight(0).MinWidth(0).Children(func() {
			ui.Column(c).Grow(1).MinHeight(0).MinWidth(0).Children(func() {
				s.gdDiffSurface(c)
			})
			s.gitQuickCommitComposer(c)
		})
	})
}

// gitFilePaneLimits is the navigator's width in a window this wide, and the
// widest it may be: the user's drag, kept clear of the sidebar and the diff.
func (s *Shell) gitFilePaneLimits(windowW float32) (width, maxWidth float32) {
	width = s.gitFilePaneWidth
	if width == 0 {
		width = gitFilePaneDefaultWidth
	}
	maxWidth = windowW - s.sidebarW - 270
	if maxWidth < gitFilePaneMinWidth {
		maxWidth = gitFilePaneMinWidth
	}
	return clampF(width, gitFilePaneMinWidth, maxWidth), maxWidth
}

// gitCenterWidth is the room the Git surface shares between its file
// navigator and its diff: the window less the sidebar, when it shows.
func (s *Shell) gitCenterWidth(windowW float32) float32 {
	if s.sidebarCollapsed {
		return windowW
	}
	return windowW - s.sidebarW
}

// gitFilePaneOpen tells whether the file navigator shows in a window this
// wide. A narrow center closes it by itself, to give the diff its width. The
// user's say is kept per width class: they open it in a narrow window, and
// close it in a wide one.
func (s *Shell) gitFilePaneOpen(windowW float32) bool {
	if s.gitCenterWidth(windowW) < gitFilePaneCollapseBelow {
		return s.gitFilePaneOpenNarrow
	}
	return !s.gitFilePaneClosedWide
}

// toggleGitFilePane opens or closes the navigator in the width class the
// window is in.
func (s *Shell) toggleGitFilePane(windowW float32) {
	if s.gitCenterWidth(windowW) < gitFilePaneCollapseBelow {
		s.gitFilePaneOpenNarrow = !s.gitFilePaneOpenNarrow
	} else {
		s.gitFilePaneClosedWide = !s.gitFilePaneClosedWide
	}
}

// gitFilePaneToggle is the toolbar button that shows or hides the navigator.
func (s *Shell) gitFilePaneToggle(c *ui.Context) {
	windowW, _ := c.Size()
	label := "Show file list"
	if s.gitFilePaneOpen(windowW) {
		label = "Hide file list"
	}
	if gdIconButton(c, iconPanel, label).Clicked() {
		s.toggleGitFilePane(windowW)
	}
}

// gitFileNavigationColumn is the reusable resize seam for the middle-left
// file tree. It does not reuse (or compete with) the app's Git reference rail.
// Closed, it leaves the whole width to the diff beside it.
func (s *Shell) gitFileNavigationColumn(c *ui.Context, body func()) {
	t := c.Theme()
	windowW, _ := c.Size()
	if !s.gitFilePaneOpen(windowW) {
		return
	}
	width, maxWidth := s.gitFilePaneLimits(windowW)
	ui.Column(c).Width(width).FillHeight().Shrink(0).MinHeight(0).Children(body)
	handle := ui.Box(c).Width(gitFileSplitterWidth).Shrink(0).Cursor(ui.CursorResizeEW).
		Label("Resize Git file navigator").Children(func() {
		ui.Box(c).Width(1).FillHeight().Background(s.tokens(t.Dark).BorderSubtle)
	})
	if dx, _, held := handle.Dragged(); held {
		s.gitFilePaneWidth = clampF(width+dx, gitFilePaneMinWidth, maxWidth)
	}
}

// Quick composer is an entry point into the SINGLE existing fenced Commit
// modal. It never calls git itself or duplicates the commit transaction.
func (s *Shell) gitQuickCommitComposer(c *ui.Context) {
	t := c.Theme()
	snap := s.gitSnapshot()
	if snap == nil || s.gdReviewingCommit() {
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
		s.piQuickRunning || s.piQuickError != "" || form.FocusWithin()
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
			if s.piQuickRunning {
				ui.Spinner(c).Size(13, 13)
				ui.Text(c, "Generating subject…").FontSize(Typography().Micro).TextColor(t.TextMuted)
				if compactButton(c, "Cancel").Clicked() {
					s.cancelQuickAICommit()
				}
			}
			ui.Spacer(c)
			ui.Text(c, pluralFiles(len(snap.Files))).FontSize(Typography().Micro).TextColor(t.TextMuted)
			s.gitQuickCommitActions(c, blocked)
		})
		if s.piQuickError != "" {
			ui.Text(c, s.piQuickError).FontSize(Typography().BodySmall).TextColor(t.Warning).MaxWidth(600)
		}
	})
}

// gitQuickCommitActions is the Commit button and its options pull-down.
func (s *Shell) gitQuickCommitActions(c *ui.Context, blocked bool) {
	t := c.Theme()
	button := compactPrimaryButton(c, "Commit")
	button.Disabled(blocked || s.piQuickRunning)
	if button.Clicked() {
		s.openCommitSurface()
		s.surface.commit.Subject = s.gitQuickSubject
		s.surface.commit.Body = s.gitQuickBody
		s.surface.commit.Amend = s.gitQuickAmend
		// Do not overwrite a user-written amend message with HEAD.
		s.surface.commit.AmendPrefilled = s.gitQuickAmend &&
			(s.gitQuickSubject != "" || s.gitQuickBody != "")
	}
	// The dropdown is independent of Commit: it only requests a Pi
	// candidate and fills Subject after root/snapshot/draft checks.
	dropdown := gorexIconButton(c, gorexColorsOf(t.Dark), iconChevronDown, "Commit options", "Commit options",
		false, gorexPaneBtn, gorexPaneGlyph)
	dropdown.Menu(func(m *ui.Menu) {
		if m.Item("Draft Subject with Pi").Disabled(blocked || s.piQuickRunning).Chosen() {
			s.startQuickAICommit()
		}
	})
}

// showGitCommits switches both the upper navigator and lower reviewer.
func (s *Shell) showGitCommits() {
	s.commitsMode = true
	if s.git != nil && !s.gdReviewingCommit() && len(s.git.commits) > 0 {
		s.reviewCommit(s.git.commits[0].Hash)
	}
}

// gitCommitHistoryPane is the wide top table of the Commits view: a one-row
// head (search button + count) over the graph list.
func (s *Shell) gitCommitHistoryPane(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		s.gitListHead(c, &headerSearch{
			Query: &s.gitCommitFilter, Open: &s.gitCommitSearchOpen, Label: "Search commits", Button: "Search commit list",
		}, 12, func() {
			summary := itoa(len(s.git.commits)) + " commits"
			if strings.TrimSpace(s.gitCommitFilter) != "" {
				summary = itoa(len(s.filteredCommits())) + " of " + summary
			}
			ui.Text(c, summary).FontSize(Typography().BodySmall).TextColor(t.TextMuted).SingleLine()
		})
		s.commitsPane(c, gdPaletteFor(t))
	})
}

// gitCommitReview is the lower half of the Commits view: the commit's message
// and changed files on the left, its diff on the right. A narrow center closes
// the left column; the toolbar button above the diff brings it back.
func (s *Shell) gitCommitReview(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinHeight(0).MinWidth(0).Children(func() {
		if !s.gdReviewingCommit() {
			msg := "Select a commit to review its changes"
			if !s.git.commitsLoading && len(s.git.commits) == 0 {
				msg = "This repository has no commits yet"
			}
			s.gdEmptyPanel(c, gdPaletteFor(t), "No commit selected", msg, func() {
				if compactPrimaryButton(c, "Open Terminal").Clicked() {
					s.showSurface(WorkspaceSurfaceTerminal)
				}
			})
			return
		}
		ui.Row(c).Grow(1).MinHeight(0).MinWidth(0).AlignItems(ui.Stretch).Children(func() {
			s.gitFileNavigationColumn(c, func() {
				ui.Column(c).Grow(1).MinHeight(0).MinWidth(0).Children(func() {
					s.gitCommitCard(c)
					s.gitCommitFiles(c)
				})
			})
			ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				ui.Row(c).FillWidth().Padding(6, 12, 0).Gap(Spacing().S).AlignItems(ui.Center).Children(func() {
					s.gitFilePaneToggle(c)
					ui.Spacer(c)
					s.gdLayoutControl(c)
				})
				s.gdDiffSurface(c)
			})
		})
	})
}

func (s *Shell) gitCommitFiles(c *ui.Context) {
	t := c.Theme()
	snap := s.git.commitSnap
	ui.Column(c).Grow(1).MinHeight(0).MinWidth(0).Children(func() {
		s.gitListHead(c, &headerSearch{
			Query: &s.gitCommitFilesFilter, Open: &s.gitCommitFilesSearchOpen, Label: "Filter commit files", Button: "Search commit files",
		}, 10, func() {
			ui.Text(c, "Changed Files").FontSize(Typography().BodySmall).FontWeight(WeightSemibold).SingleLine()
			if snap != nil {
				ui.Textf(c, "%d", len(snap.Files)).FontSize(Typography().Micro).TextColor(t.TextMuted)
			}
		})
		if snap == nil {
			ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c).Size(15, 15) })
			return
		}
		rows := gitworkbench.Flatten(gitworkbench.FilterTree(
			gitworkbench.BuildTree(snap.Files, true), s.gitCommitFilesFilter), s.git.collapsedDirs)
		list := s.gdChangedTreeList(c, gdPaletteFor(t), rows, &s.changesList, &s.changesSelected,
			nil, gdViewCombined, func(row gitworkbench.FlatRow) {
				if row.Node.Dir {
					s.git.collapsedDirs[row.Node.Path] = !s.git.collapsedDirs[row.Node.Path]
				} else {
					s.revealDiffFile(row.Node.Path)
				}
			})
		list.Children(func() {
			if len(rows) == 0 {
				ui.Text(c, "No matching files").Padding(12).FontSize(Typography().BodySmall).TextColor(t.TextMuted)
			}
		})
	})
}

// gitCommitBodyFold is the body's height, in lines, while it is folded.
const gitCommitBodyFold = 3

// splitCommitMessage cuts a full message into its subject and body; the
// subject falls back to the list's when the message could not be read.
func splitCommitMessage(message, subject string) (string, string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return subject, ""
	}
	head, body, _ := strings.Cut(message, "\n")
	return strings.TrimSpace(head), strings.TrimSpace(body)
}

// gitCommitCard is the commit under review: its message — selectable, the
// body folded past three lines — who and when, the short hash, and the
// commit's own actions.
func (s *Shell) gitCommitCard(c *ui.Context) {
	t := c.Theme()
	cm := s.git.commitMeta
	ui.Column(c).FillWidth().Shrink(0).Padding(8, 10, 10).Gap(6).
		BorderWidth(0, 0, 1, 0).BorderColor(s.tokens(t.Dark).BorderSubtle).Children(func() {
		if cm == nil {
			ui.Text(c, "Reading commit…").FontSize(Typography().BodySmall).TextColor(t.TextMuted)
			return
		}
		subject, body := splitCommitMessage(s.git.commitBody, cm.Subject)
		message := subject
		if body != "" {
			message += "\n\n" + body
		}
		ui.Column(c).FillWidth().Gap(4).Selectable().Children(func() {
			ui.Text(c, subject).FontSize(Typography().Body).FontWeight(WeightSemibold)
			if body != "" {
				text := ui.Text(c, body).FontSize(Typography().BodySmall).TextColor(t.TextMuted)
				if !s.gitCommitBodyOpen {
					text.MaxLines(gitCommitBodyFold)
				}
			}
		})
		if strings.Count(body, "\n") >= gitCommitBodyFold || len(body) > 180 {
			label := "Show more"
			if s.gitCommitBodyOpen {
				label = "Show less"
			}
			fold := ui.ButtonBase(c).Padding(0).FocusRing(false).Label(label).Children(func() {
				ui.Text(c, label).FontSize(Typography().Caption).TextColor(t.Accent)
			})
			if fold.Clicked() {
				s.gitCommitBodyOpen = !s.gitCommitBodyOpen
			}
		}
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			tipped(c, ui.Text(c, cm.Author+" · "+gdRelativeTime(cm.Time)).FontSize(Typography().Caption).TextColor(t.TextMuted).SingleLine().Grow(1).MinWidth(0), gdCommitTime(cm.Time))
			ui.Text(c, cm.Short).Font(gdCodeFont()).FontSize(Typography().Caption).TextColor(t.TextMuted).Shrink(0).Selectable()
			if gorexIconButton(c, gorexColorsOf(t.Dark), iconCopy, "Copy hash", "Copy Commit Hash", false, gorexRowBtn, gorexRowGlyph).Clicked() {
				s.copyToClipboard(cm.Hash)
			}
		})
		ui.Row(c).FillWidth().Gap(6).Wrap().Children(func() {
			if compactButton(c, "Copy Message").Clicked() {
				s.copyToClipboard(message)
				s.pendingToast = "Copied commit message"
			}
			if compactButton(c, "Cherry-pick…").Clicked() {
				s.confirmCherryPickCommit(cm.Hash, cm.Subject)
			}
			if compactButton(c, "Revert…").Clicked() {
				s.confirmRevertCommit(cm.Hash, cm.Subject)
			}
		})
	})
}
