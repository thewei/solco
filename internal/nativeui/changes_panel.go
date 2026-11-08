package nativeui

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/gitworkbench"
)

// changesPanelState is the Changes tool's presentation state (filter,
// footer totals are derived).
type changesPanelState struct {
	filter string
}

// gdChangedTreeList renders one Godiff-style changed-file tree list over the
// given state (the Right Panel tool and the Diff-mode sidebar panes each
// hold their own list state over the same tree data). onClick receives the
// activated row. choice, when not nil, lets Cmd-click and Shift-click choose
// several rows, held by path in the caller's selection.
func (s *Shell) gdChangedTreeList(c *ui.Context, pal *gdPalette, rows []gitworkbench.FlatRow, state *ui.ListState, selected *int, choice *ui.Selection[string], pane gdViewSide, onClick func(gitworkbench.FlatRow)) ui.Element {
	if choice != nil {
		// Rows identified by path: the choice and the list's place follow
		// their files as rows come and go.
		state.Key = func(i int) any {
			if i >= 0 && i < len(rows) {
				return rows[i].Node.Path
			}
			return i
		}
		state.Selection = choice
	}
	// MyGo paints a chosen row in the theme's accent (opaque blue while the
	// list has focus), which sat far brighter than the neutral selection the
	// Commits list wears (user report 2026-11-03). The list is built under a
	// theme whose accent is clear, so the row's own SidebarSelect/Strong tint
	// (renderChangesRow) is the only face; every other control is built
	// outside this block and keeps the real accent.
	real := c.Theme()
	quiet := *real
	quiet.Accent, quiet.AccentText = ui.RGBA(0, 0, 0, 0), real.Text
	c.SetTheme(&quiet)
	list := ui.List(c, state, len(rows), func(i int) {
		if i < 0 || i >= len(rows) {
			return
		}
		s.renderChangesRow(c, pal, rows[i], state, pane, choice, onClick)
	}).Grow(1).MinHeight(0).Padding(3, sidebarGutter)
	c.SetTheme(real)
	// SetTheme also resets the window root's background to the theme canvas,
	// which silently switched the sidebar's vibrancy off whenever a changes
	// list was on screen (the sidebar flashed opaque on every commit click,
	// user report 2026-11-03). Put back what Shell.View sets.
	if c.Vibrancy() {
		c.Root().Background(ui.Transparent)
	}
	// A plain click opens the file: the list replaced the choice with that
	// one row. Choice-growing clicks (Cmd, Shift) stay preview-free.
	if changed := list.Changed(); changed && *selected >= 0 && *selected < len(rows) {
		row := rows[*selected]
		if !row.Node.Dir && (choice == nil || (choice.Len() == 1 && choice.Has(row.Node.Path))) {
			onClick(row)
		}
	}
	return list
}

// currentChangesTree builds (or reuses) the filtered changed-path tree.
func (s *Shell) currentChangesTree(snap *gitworkbench.ChangesSnapshot) *gitworkbench.TreeNode {
	if s.changesTree != nil {
		return s.changesTree
	}
	tree := gitworkbench.BuildTree(snap.Files, true)
	s.changesTree = gitworkbench.FilterTree(tree, s.changes.filter)
	return s.changesTree
}

// renderChangesRow draws one compact tree row on the shared sidebar grid:
// chevron slot · icon · name · hover action · +N -N · status letter
// (GWB-141..143). Folders and files of one level share their icon and name
// columns, and guides run through the ancestors' chevron slots.
// state is the owning list, for the focused-selection tint. choice is the
// pane's multi-selection, whose rows tint like an extended selection.
func (s *Shell) renderChangesRow(c *ui.Context, pal *gdPalette, row gitworkbench.FlatRow, state *ui.ListState, pane gdViewSide, choice *ui.Selection[string], onClick func(gitworkbench.FlatRow)) {
	t := c.Theme()
	node := row.Node

	chosen := false
	if choice != nil && !node.Dir && node.File != nil {
		chosen = choice.Has(node.Path)
	}
	selected := false
	if !node.Dir && node.File != nil {
		selected = node.Path == s.surface.diff.SelectedPath
	}
	focused := state.FocusWithin(c)

	r := ui.Row(c).Height(s.treeRowHeight()).Padding(0, treeRowPad, 0, treeRowPad).AlignItems(ui.Center).Radius(treeRowRadius).MinWidth(0)
	textColor := t.Text
	muted := t.TextMuted
	switch {
	case chosen && focused:
		r.Background(designTokens(t.Dark).SidebarSelectStrong)
		textColor = t.Text
		muted = t.TextMuted
	case chosen:
		r.Background(designTokens(t.Dark).SidebarSelect)
	case selected && focused:
		r.Background(designTokens(t.Dark).SidebarSelectStrong)
		textColor = t.Text
		muted = t.TextMuted
	case selected:
		r.Background(designTokens(t.Dark).SidebarSelect)
	case r.Hovered():
		r.Background(designTokens(t.Dark).SidebarHover)
	}
	if timeSince(s.git.dirOpenedAt) < changesDirGrowWindow {
		r.Transition(changesRowGrow)
	} else {
		r.Transition(rowFade)
	}
	treeGuides(r, row.Depth, designTokens(t.Dark).TreeLine)
	r.TextColor(textColor).Children(func() {
		if row.Depth > 0 {
			ui.Box(c).Width(float32(row.Depth) * TreeIndentWidth).Shrink(0).PassThrough()
		}
		treeChevron(c, node.Dir, !s.git.collapsedDirs[node.Path], muted)
		ui.Box(c).Width(treeGlyphGap).Shrink(0).PassThrough()
		if node.Dir {
			ui.Icon(c, iconFolder).FontSize(treeGlyphW).TextColor(muted).Shrink(0)
			ui.Box(c).Width(treeTitleGap).Shrink(0).PassThrough()
			tipped(c, ui.Text(c, node.Name).FontSize(Typography().Body).FontWeight(WeightMedium).SingleLine().Grow(1).Shrink(1).MinWidth(0), node.Path)
			if pane == gdViewUnstaged || pane == gdViewStaged {
				paths := gitDirectoryPaths(s.gitSnapshot(), node.Path, pane)
				if len(paths) > 0 {
					glyph, verb, action := iconPlus, "Stage", s.stageFiles
					if pane == gdViewStaged {
						glyph, verb, action = iconMinus, "Unstage", s.unstageFiles
					}
					button := gorexIconButton(c, gorexColorsOf(t.Dark), glyph, verb+" folder "+node.Path,
						verb+" "+node.Path+" ("+pluralFiles(len(paths))+")", false, gorexRowBtn, gorexRowGlyph).Shrink(0)
					if !r.Hovered() && !button.Hovered() {
						button.Opacity(0)
					}
					button.Disabled(s.git.opBusy)
					if button.Clicked() {
						action(paths)
					}
				}
			}
			return
		}
		cf := *node.File
		viewed := s.git.gdViewed[cf.Path] != "" && s.git.gdViewed[cf.Path] == cf.Fingerprint
		ui.Icon(c, iconFile).FontSize(treeGlyphW).TextColor(muted).Shrink(0)
		ui.Box(c).Width(treeTitleGap).Shrink(0).PassThrough()
		// Full path on hover (F113): the 250px pane truncates names so
		// service_index.go and service_index_test.go read as the same row.
		name := fileNameLabel(c, node.Name, node.Path)
		if viewed && !(selected && focused) {
			name.TextColor(t.TextMuted)
		}
		ui.Row(c).Gap(5).AlignItems(ui.Center).Shrink(0).Children(func() {
			// The per-file Stage/Unstage button shows while the row is hovered,
			// as Fork's +/- does; which one depends on the pane.
			if action, glyph, verb := s.gdRowAction(&cf, pane); action != nil {
				b := gorexIconButton(c, gorexColorsOf(t.Dark), glyph, verb+" "+cf.Path, verb, false, gorexRowBtn, gorexRowGlyph).Shrink(0)
				if !r.Hovered() && !b.Hovered() {
					b.Opacity(0)
				}
				if b.Clicked() {
					action([]string{cf.Path})
				}
			}
			if gdCountable(&cf) && (cf.Additions > 0 || cf.Deletions > 0) {
				tipped(c, ui.Textf(c, "+%s -%s", gdCompact(cf.Additions), gdCompact(cf.Deletions)).
					Font(gdCodeFont()).FontSize(Typography().BodySmall).FontWeight(WeightSemibold).TextColor(muted).Shrink(0), gdLines(cf.Additions, "added")+", "+gdLines(cf.Deletions, "removed"))
			}
			letter := gdStatusLetterColor(cf.Status, pal, t)
			if viewed {
				letter = t.TextMuted
			}
			// A small tinted tile instead of a bare letter: the status reads as
			// a mark at a glance, the letter is the accessible detail. It sits
			// flush right, where the pane header's Stage/Unstage ends.
			ui.Box(c).Size(16, 16).Shrink(0).Radius(treeRowRadius).Background(letter.Alpha(0.16)).Center().Children(func() {
				ui.Text(c, gdStatusLetter(&cf)).Font(gdCodeFont()).FontSize(Typography().BodySmall).FontWeight(WeightSemibold).
					TextColor(letter).PassThrough()
			})
		})
	})
	// Directory disclosure is independent of List.Changed: selecting the
	// same directory twice does not change selection, so the old handler
	// could never reliably reopen a collapsed folder.
	if node.Dir && r.Clicked() {
		onClick(row)
	}
	if node.Dir && (pane == gdViewUnstaged || pane == gdViewStaged) {
		paths := gitDirectoryPaths(s.gitSnapshot(), node.Path, pane)
		r.ContextMenu(func(m *ui.Menu) {
			if choice != nil && m.Item("Select All Files in Folder").Disabled(len(paths) == 0).Chosen() {
				for _, path := range paths {
					choice.Add(path)
				}
			}
			if pane == gdViewUnstaged && m.Item("Stage Folder ("+itoa(len(paths))+" Files)").Disabled(len(paths) == 0 || s.git.opBusy).Chosen() {
				s.stageFiles(paths)
			}
			if pane == gdViewStaged && m.Item("Unstage Folder ("+itoa(len(paths))+" Files)").Disabled(len(paths) == 0 || s.git.opBusy).Chosen() {
				s.unstageFiles(paths)
			}
		})
	}
	if !node.Dir && node.File != nil {
		cf := *node.File
		r.ContextMenu(func(m *ui.Menu) {
			s.gdRowContextMenu(m, &cf)
		})
	}
}

// gdRowAction returns the hover action of a pane's file row: Stage in the
// unstaged pane, Unstage in the staged pane; nil in the combined tree.
func (s *Shell) gdRowAction(cf *gitworkbench.ChangeFile, pane gdViewSide) (func([]string), *ui.SVG, string) {
	switch pane {
	case gdViewUnstaged:
		return s.stageFiles, iconPlus, "Stage"
	case gdViewStaged:
		return s.unstageFiles, iconMinus, "Unstage"
	default:
		if cf.Unstaged {
			return s.stageFiles, iconPlus, "Stage"
		}
		if cf.Staged {
			return s.unstageFiles, iconMinus, "Unstage"
		}
		return nil, nil, ""
	}
}

// gdRowContextMenu is a tree row's right-click menu: the working-tree file
// operations, as Fork's lists offer them.
func (s *Shell) gdRowContextMenu(m *ui.Menu, cf *gitworkbench.ChangeFile) {
	if cf.Untracked {
		if m.Item("Stage").Chosen() {
			s.stageFiles([]string{cf.Path})
		}
		if m.Item("Delete…").Chosen() {
			s.discardChanges([]string{cf.Path})
		}
	} else {
		if cf.Unstaged {
			if m.Item("Stage").Chosen() {
				s.stageFiles([]string{cf.Path})
			}
		}
		if cf.Staged {
			if m.Item("Unstage").Chosen() {
				s.unstageFiles([]string{cf.Path})
			}
		}
		if m.Item("Discard Changes…").Chosen() {
			s.discardChanges([]string{cf.Path})
		}
		m.Separator()
	}
	if cf.Status != gitworkbench.StatusDeleted {
		if m.Item("Open in Editor").Chosen() {
			s.openFileInEditor(cf.Path)
		}
	}
	s.gdFileItems(m, cf)
	if m.Item("Copy Path").Chosen() {
		s.copyToClipboard(cf.Path)
	}
}

// changesDirGrowWindow is how long after a folder opens its new rows may grow
// in. The Changes list is virtualized, so a row built by scrolling would
// otherwise animate too; only rows born in this window are the folder's.
const changesDirGrowWindow = 300 * time.Millisecond

// changesRowGrow is treeRowMotion without the exit: a collapsed folder's rows
// leave at once (the list cannot hold a removed row open).
var changesRowGrow = ui.ElementTransition{Colors: true, Duration: motionRow, Enter: &ui.Motion{Collapse: true}}

// toggleChangesDir opens or closes a folder of the Changes tree.
func (s *Shell) toggleChangesDir(path string) {
	open := s.git.collapsedDirs[path]
	s.git.collapsedDirs[path] = !open
	if open {
		s.git.dirOpenedAt = timeNow()
	}
}

// syncChangesSelectionFromDiff expands ancestors and scrolls the selected
// tree row into view (center→right, GWB-181) — guarded against oscillation.
func (s *Shell) syncChangesSelectionFromDiff(topPath string) {
	if s.git == nil || topPath == "" || s.diffScrollGuardActive() {
		return
	}
	if topPath == s.surface.diff.SelectedPath {
		return
	}
	s.surface.diff.SelectedPath = topPath
	gitworkbench.EnsureAncestorsOpen(s.git.collapsedDirs, topPath)
	s.changesTree = nil
	if snap := s.gitSnapshot(); snap != nil {
		rows := gitworkbench.Flatten(s.currentChangesTree(snap), s.git.collapsedDirs)
		for i, row := range rows {
			if !row.Node.Dir && row.Node.Path == topPath {
				s.changesList.ScrollIntoView(i)
				break
			}
		}
	}
}

// diffScrollGuardActive suppresses center→panel sync for a short window
// after a programmatic scroll (oscillation guard, GWB-182).
func (s *Shell) diffScrollGuardActive() bool {
	return s.git != nil && timeSince(s.git.revealAt) < DiffRevealGuardWindow
}
