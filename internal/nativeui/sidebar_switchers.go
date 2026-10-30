package nativeui

/**
 * [INPUT]: Herdr 项目投影与当前选择、本地分支目录、既有 selectProject/confirmSwitchBranch/创建分支流程、共享 sidebar 网格与 breadcrumb_switcher 的 switcherListMaxHeight
 * [OUTPUT]: Git、Files 与 Sessions 三个侧栏共用的头部 sidebarProjectHeader：单行：项目名与分支名各自是可搜索的 pull-down 快速切换，外加可选的搜索钮（headerSearch，点开才展开字段，Esc/再点收起并清空）与刷新图标；侧栏 ≤236pt 时项目只留文件夹图标
 * [POS]: 侧栏头部呈现与切换交互；项目选择权威仍在 selection.go，分支变更仍走 git_service_mutation
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
)

// sidebarProjectTitle names the sidebar head: the selected Herdr project,
// else the repository folder.
func (s *Shell) sidebarProjectTitle() string {
	if project := s.selectedProject(); project != nil && project.Label != "" {
		return project.Label
	}
	return s.gitRepoName()
}

// switcherChip is a pull-down title: glyph, text and a chevron hugging the
// text, with a hover face that spans only the content. Its glyph and text sit
// on the tree grid, under the chevron and title columns of the rows below.
func switcherChip(c *ui.Context, key, label, text, tooltip string, glyph *ui.SVG, size float32, weight int, color ui.Color) ui.Element {
	t := c.Theme()
	chip := tipped(c, ui.ButtonBase(c.Key(key)).Height(22).Padding(0, treeRowPad).Radius(4).FocusRing(false).
		AlignItems(ui.Center).Label(label), tooltip)
	if chip.Hovered() {
		chip.Background(t.SurfaceHover)
	}
	chip.Children(func() {
		ui.Icon(c, glyph).FontSize(treeGlyphW).TextColor(t.TextMuted).Shrink(0)
		ui.Box(c).Width(treeGlyphGap).Shrink(0).PassThrough()
		if text != "" {
			ui.Text(c, text).FontSize(size).FontWeight(weight).TextColor(color).SingleLine().Shrink(1).MinWidth(0)
		}
		ui.Box(c).Width(3).Shrink(0).PassThrough()
		ui.Icon(c, iconChevronDown).FontSize(9).TextColor(t.TextMuted).Shrink(0)
	})
	return chip
}

// sidebarHeaderNarrow is the sidebar width at and under which the project
// chip keeps only its folder glyph (its name in the tooltip), so the branch
// keeps its name.
const sidebarHeaderNarrow float32 = 236

// headerSearch is the search a sidebar head may carry: a magnifier button
// that opens a field under the head. Closing it (the button again, or Esc)
// clears the query, so no hidden filter outlives its field.
type headerSearch struct {
	Query *string
	Open  *bool
	// Label names the field; Button names the magnifier that opens it.
	Label, Button string
	// Changed runs when the query is edited or cleared by closing.
	Changed func()
}

// close empties the search and hides its field.
func (h *headerSearch) close() {
	*h.Open = false
	if *h.Query != "" {
		*h.Query = ""
		if h.Changed != nil {
			h.Changed()
		}
	}
}

// button is the magnifier that opens and closes the field; it wears the
// selected face while the field shows.
func (h *headerSearch) button(c *ui.Context, k *gorexColors) {
	label := h.Button
	if *h.Open {
		label = "Close " + strings.ToLower(h.Button)
	}
	if gorexIconButton(c, k, iconSearch, label, label, *h.Open, gorexPaneBtn, gorexPaneGlyph).Clicked() {
		if *h.Open {
			h.close()
		} else {
			*h.Open = true
		}
	}
}

// field is the opened search: focused as it appears, Esc closes it.
func (h *headerSearch) field(c *ui.Context) {
	f := searchField(c, h.Query).FillWidth().AutoFocus().Label(h.Label)
	if f.Changed() && h.Changed != nil {
		h.Changed()
	}
	if f.Shortcut(0, ui.KeyEscape) {
		h.close()
	}
}

// sidebarProjectHeader is the head of the Git, Files and Sessions sidebars,
// one row: the project and its branch — each a pull-down that switches in
// place — then the search button (when search is given) and the refresh icon.
// root is the folder the sidebar shows, offered as the project's tooltip.
func (s *Shell) sidebarProjectHeader(c *ui.Context, root, refreshLabel string, refresh func(), search *headerSearch) {
	t := c.Theme()
	title, tooltip := s.sidebarProjectTitle(), root
	branch := s.currentBranchName()
	k := gorexColorsOf(t.Dark)
	narrow := s.sidebarW <= sidebarHeaderNarrow
	ui.Column(c).FillWidth().Children(func() {
		ui.Row(c).FillWidth().Padding(6, sidebarGutter, 4).Gap(2).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Children(func() {
				switch {
				case title == "":
					ui.Text(c, "No project").FontSize(Typography().Body).FontWeight(WeightSemibold).TextColor(t.TextMuted)
				case len(s.projection.Projects) == 0:
					tipped(c, ui.Text(c, title).FontSize(Typography().Body).FontWeight(WeightSemibold).SingleLine().Shrink(1).MinWidth(0), tooltip)
				default:
					chipTitle, chipTip := title, tooltip
					if narrow {
						chipTitle, chipTip = "", title
					}
					chip := switcherChip(c, "sidebar-project", "Switch project", chipTitle, chipTip, iconFolder,
						Typography().Body, WeightSemibold, t.Text)
					wasOpen := s.projectMenuOpen
					toggleMenu(&s.projectMenuOpen, chip)
					if s.projectMenuOpen && !wasOpen {
						s.projectFilter = ""
					}
					if s.projectMenuOpen {
						ui.Popover(c, chip, &s.projectMenuOpen, func() { s.projectSwitcherPanel(c) })
					}
				}
				if s.git != nil && s.git.root != "" && branch != "" {
					if !narrow && title != "" {
						ui.Text(c, "/").FontSize(Typography().BodySmall).TextColor(t.TextMuted).Shrink(0)
					}
					chip := switcherChip(c, "sidebar-branch", "Switch branch", branch, branch, iconBranch,
						Typography().BodySmall, 400, t.TextMuted)
					wasOpen := s.branchMenuOpen
					toggleMenu(&s.branchMenuOpen, chip)
					if s.branchMenuOpen && !wasOpen {
						s.branchFilter = ""
					}
					if s.branchMenuOpen {
						ui.Popover(c, chip, &s.branchMenuOpen, func() { s.branchSwitcherPanel(c) })
					}
				}
			})
			if search != nil {
				search.button(c, k)
			}
			if refresh != nil {
				if gorexIconButton(c, k, iconRefresh, refreshLabel, refreshLabel, false, gorexPaneBtn, gorexPaneGlyph).Clicked() {
					refresh()
				}
			}
		})
		if search != nil && *search.Open {
			ui.Row(c).FillWidth().Padding(0, sidebarGutter, 6).Children(func() { search.field(c) })
		}
	})
}

// switcherSearch is the search field every switcher panel opens with. It
// reports Enter, which picks the first match.
func switcherSearch(c *ui.Context, text *string, label string) (submitted bool) {
	field := searchField(c, text).FillWidth().AutoFocus().Label(label)
	return field.Submitted()
}

// projectSwitcherPanel lists the Herdr projects for a one-click switch.
func (s *Shell) projectSwitcherPanel(c *ui.Context) {
	t := c.Theme()
	sp := Spacing()
	ui.Column(c).Width(300).Padding(sp.S).Gap(sp.XS).Children(func() {
		var matches []herdr.Project
		for _, project := range s.projection.Projects {
			if gitRefMatches(s.projectFilter, project.Label) {
				matches = append(matches, project)
			}
		}
		if switcherSearch(c, &s.projectFilter, "Search projects") && len(matches) > 0 {
			s.switchProject(matches[0].ID)
			return
		}
		ui.Scroll(c).FillWidth().MaxHeight(switcherListMaxHeight).Children(func() {
			ui.Column(c).FillWidth().Gap(1).Children(func() {
				if len(matches) == 0 {
					ui.Text(c, "No matching projects").FontSize(Typography().Caption).TextColor(t.TextMuted).Padding(sp.S)
				}
				for _, project := range matches {
					id := project.ID
					row := crumbItemRow(c, visualMark{svg: iconFolder}, project.Label, opUnknown, id == s.selectedProjectID)
					if row.Clicked() {
						s.switchProject(id)
					}
				}
			})
		})
	})
}

// switchProject selects a project from a sidebar head. A context change
// resets the center to Terminal, so the Git view is reopened for the new
// project's repository; the Files route follows the selection by itself.
func (s *Shell) switchProject(id string) {
	s.projectMenuOpen = false
	if id == "" || id == s.selectedProjectID {
		return
	}
	inDiff := s.surface.current() == WorkspaceSurfaceDiff
	s.setProjectExpanded(id, true)
	s.selectProject(id)
	if inDiff && s.selectedProjectID == id {
		s.showSurface(WorkspaceSurfaceDiff)
	}
}

// branchSwitcherPanel lists the local branches, current first, for a
// one-click switch. Merge and delete stay on each row's context menu; a
// footer row starts a new branch.
func (s *Shell) branchSwitcherPanel(c *ui.Context) {
	t := c.Theme()
	sp := Spacing()
	ui.Column(c).Width(320).Padding(sp.S).Gap(sp.XS).Children(func() {
		matches := matchingLocalBranches(s.git.branches, s.branchFilter)
		if switcherSearch(c, &s.branchFilter, "Search branches") {
			for _, b := range matches {
				if !b.Current {
					s.branchMenuOpen = false
					s.confirmSwitchBranch(b.Name)
					return
				}
			}
		}
		ui.Scroll(c).FillWidth().MaxHeight(260).Children(func() {
			ui.Column(c).FillWidth().Gap(1).Children(func() {
				if s.git.branchesLoading && len(matches) == 0 {
					ui.Row(c).FillWidth().Padding(sp.XS, sp.S).Gap(sp.XS).AlignItems(ui.Center).Children(func() {
						ui.Spinner(c).Size(12, 12)
						ui.Text(c, "Loading branches…").FontSize(Typography().Caption).TextColor(t.TextMuted)
					})
					return
				}
				if len(matches) == 0 {
					label := "No local branches"
					if strings.TrimSpace(s.branchFilter) != "" {
						label = "No matching branches"
					}
					ui.Text(c, label).FontSize(Typography().Caption).TextColor(t.TextMuted).Padding(sp.S)
				}
				for _, branch := range matches {
					b := branch
					row := crumbItemRow(c, visualMark{svg: iconBranch}, b.Name, opUnknown, b.Current)
					if row.Clicked() && !b.Current {
						s.branchMenuOpen = false
						s.confirmSwitchBranch(b.Name)
					}
					if !b.Current {
						row.ContextMenu(func(m *ui.Menu) {
							if m.Item("Merge into Current…").Chosen() {
								s.branchMenuOpen = false
								s.openMergeDialog(b.Name)
							}
							if m.Item("Delete Branch…").Chosen() {
								s.branchMenuOpen = false
								s.deleteBranch(b.Name)
							}
						})
					}
				}
			})
		})
		ui.Divider(c)
		create := crumbItemRow(c, visualMark{svg: iconPlus}, "New Branch…", opUnknown, false)
		if create.Clicked() {
			s.branchMenuOpen = false
			s.openNewBranchDialog()
		}
	})
}
