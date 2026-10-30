package nativeui

import (
	"time"

	"github.com/egoist/mygo/ui"
)

/**
 * [INPUT]: 依赖 mygo/ui 的 Context/Theme/Popover, gorex_style（gorexToggleFace 芯片脸）, tooltip.go 的 tipped（被截断祖先与末级目录的 tooltip）, design_system 的 Typography/Spacing/Radius/Controls, agent_visual, proc_brands, brands 的 agentProvider/agentMark 与 Herdr Pane layout 排序
 * [OUTPUT]: 对外提供 crumbSegment, Shell.breadcrumbSegments, Shell.breadcrumbSegmentsWithTheme, Shell.breadcrumbRow（shadcn Breadcrumb 结构的 ghost 芯片面包屑，返回是否画出路径）, crumbItemRow, crumbPathTip（末级 crumb 的目录悬停弹层）, switcherListMaxHeight（项目列表在切换弹层内的最高高度，超出后行在面板内滚动；crumb 弹层与侧栏项目切换器共用）
 * [POS]: nativeui 标题栏面包屑与快速切换弹层所有者，展示工作区/Tab/Pane层次并呈现 Agent 与进程品牌图标；活 Agent 的末级 crumb（多 Pane Tab 的 pane crumb、单 Pane Tab 的 tab crumb）显示 Agents 区同款标题（agentCardTitleForPane，会话标题优先，死绑定回落原标签）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Breadcrumb switchers (2026-10-05 header review): every segment of the
// title-bar path is a button that opens a popover listing its siblings —
// Projects, the selected Project's Tabs, the selected Tab's Panes — for
// one-click switching, mirroring the Rust Solco behavior. Pane rows
// carry the bound Agent's provider glyph with the state dot at its corner.
// One line (2026-10-28): the selected Pane's directory, which used to sit
// under the path as a second row, is the last crumb's hover popup.
// shadcn shape (2026-10-29): ghost chips joined by chevrons that belong to
// no button; ancestors muted, the current crumb in full ink with no
// permanent pill, a chip face on hover and while the crumb's popover is open.

// Crumb chip widths: an ancestor ends in an ellipsis at crumbMaxWidth and
// is the first to give way in a narrow bar; the current crumb has more room
// and yields last; no chip narrows below its mark and a few letters.
const (
	crumbMaxWidth     = 200
	crumbLastMaxWidth = 320
	crumbMinWidth     = 56
)

type crumbSegment struct {
	label string
	kind  string // "project" | "tab" | "pane"
	id    string
	mark  visualMark
}

// breadcrumbSegments resolves the title-bar path with identities using default theme.
func (s *Shell) breadcrumbSegments() []crumbSegment {
	return s.breadcrumbSegmentsWithTheme(false)
}

// breadcrumbSegmentsWithTheme resolves the title-bar path with identities and visual marks.
func (s *Shell) breadcrumbSegmentsWithTheme(dark bool) []crumbSegment {
	var path []crumbSegment
	if p := s.selectedProject(); p != nil {
		path = append(path, crumbSegment{label: p.Label, kind: "project", id: p.ID, mark: visualMark{svg: iconFolder}})
	}
	if t := s.selectedTab(); t != nil {
		tabMark := visualMark{svg: iconTab}
		tabLabel := t.Label
		panes := panesForTab(s.projection, t.ID)
		if len(panes) == 1 {
			if a := s.paneAgent(panes[0].ID); a != nil {
				tabMark, _ = s.agentMarkForPane(panes[0].ID, dark)
				// A live agent's crumb speaks its conversation title, the
				// same one the sidebar rows and Agents section carry.
				if title, ok := s.agentCardTitleForPane(panes[0].ID); ok {
					tabLabel = title
				} else if tabLabel == "" || tabLabel == "Tab" {
					tabLabel = a.Name
					if tabLabel == "" {
						if provider := agentProvider(a); provider != "" {
							tabLabel = provider.DisplayName()
						}
					}
				}
			} else if brand, ok := s.procMarkForPaneWithTheme(panes[0].ID, dark); ok {
				tabMark = brand
				if tabLabel == "" || tabLabel == "Tab" {
					if act, ok := s.serviceIndex.Panes[panes[0].ID]; ok && act.Proc != "" {
						tabLabel = procFriendlyName(act.Proc)
					}
				}
			}
		}
		path = append(path, crumbSegment{label: tabLabel, kind: "tab", id: t.ID, mark: tabMark})
	}
	if p := s.selectedPane(); p != nil && s.selectedTab() != nil && s.selectedTab().PaneCount > 1 {
		paneMark := visualMark{svg: iconTerminal}
		paneLabel := p.Label
		if a := s.paneAgent(p.ID); a != nil {
			paneMark, _ = s.agentMarkForPane(p.ID, dark)
			// A live agent's crumb speaks its conversation title, the
			// same one the sidebar rows and Agents section carry.
			if title, ok := s.agentCardTitleForPane(p.ID); ok {
				paneLabel = title
			} else if paneLabel == "" || paneLabel == "Pane" || paneLabel == "Terminal" {
				paneLabel = a.Name
				if paneLabel == "" {
					if provider := agentProvider(a); provider != "" {
						paneLabel = provider.DisplayName()
					}
				}
			}
		} else if brand, ok := s.procMarkForPaneWithTheme(p.ID, dark); ok {
			paneMark = brand
			if paneLabel == "" || paneLabel == "Pane" || paneLabel == "Terminal" {
				if act, ok := s.serviceIndex.Panes[p.ID]; ok && act.Proc != "" {
					paneLabel = procFriendlyName(act.Proc)
				}
			}
		}
		path = append(path, crumbSegment{label: paneLabel, kind: "pane", id: p.TabID, mark: paneMark})
	}
	return path
}

// breadcrumbRow renders the switcher path as shadcn's Breadcrumb: ghost
// chips (ancestors muted and turning to full ink on hover, the current crumb
// in full ink with no permanent pill, the neutral chip face while hovered or
// while the crumb's own popover is open) joined by chevrons that are not
// buttons. It reports whether it drew a path — the workspace surface Actions
// then belong to the bar (titlebarTools).
func (s *Shell) breadcrumbRow(c *ui.Context) bool {
	t := c.Theme()
	sp := Spacing()
	k := gorexColorsOf(t.Dark)
	segments := s.breadcrumbSegmentsWithTheme(t.Dark)
	if len(segments) == 0 {
		ui.Text(c, "Herdr").TextColor(t.TextMuted).Grow(1).SingleLine()
		return false
	}
	glyph := Controls().IconRegular
	ui.Row(c).MinWidth(0).Gap(sp.XS).AlignItems(ui.Center).Children(func() {
		for i, segment := range segments {
			segment := segment
			last := i == len(segments)-1
			if i > 0 {
				ui.Icon(c, iconChevron).Size(glyph, glyph).TextColor(t.TextMuted).Shrink(0)
			}
			open := s.crumbOpen[i]
			b := ui.ButtonBase(c).
				Height(Controls().RegularHeight).
				MinWidth(crumbMinWidth).
				Radius(Radius().Row).
				Padding(0, sp.M).
				Gap(sp.S).
				Label(segment.label)
			if last {
				b.MaxWidth(crumbLastMaxWidth).Shrink(1)
			} else {
				b.MaxWidth(crumbMaxWidth).Shrink(4)
			}
			// Clickable affordance is the chip face and the popover (the
			// toggle faces' one neutral chip, held while the popover is
			// open), with ink going from muted to full as the pointer
			// arrives; the current crumb is simply always in full ink.
			gorexToggleFace(b, k, open)
			b.Transition(ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond})
			ink := t.TextMuted
			if last || open || b.Hovered() {
				ink = t.Text
			}
			if !last && crumbClipped(c, segment.label) {
				tipped(c, b, segment.label)
			}
			b.Children(func() {
				if segment.mark.bmp != nil || segment.mark.svg != nil {
					ui.Box(c).Size(glyph, glyph).Shrink(0).Children(func() {
						markView(c, segment.mark, glyph, ink)
					})
				}
				ui.Text(c, segment.label).FontSize(Typography().Section).FontWeight(WeightRegular).
					TextColor(ink).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			})
			if last {
				if cwd := s.selectedTabCWD(); cwd != "" {
					crumbPathTip(c, b, cwd)
				}
			}
			if b.Clicked() {
				// One popover at a time.
				s.crumbOpen = [3]bool{}
				s.crumbOpen[i] = true
			}
			if s.crumbOpen[i] {
				s.crumbPopover(c, b, i, segment)
			}
		}
	})
	return true
}

// crumbClipped reports whether an ancestor crumb's label would end in an
// ellipsis at its widest chip; the label is then also in its tooltip, as
// every truncated text's is.
func crumbClipped(c *ui.Context, label string) bool {
	sp := Spacing()
	room := crumbMaxWidth - 2*sp.M - Controls().IconRegular - sp.S
	width, _ := c.MeasureText(0, ui.Span{Text: label, Size: Typography().Section})
	return width > room
}

// crumbPathTip is the last crumb's hover popup: the working directory the
// switcher path stands for, in full from ~, in the shared tooltip face with
// a folder glyph and room for a long path (tipped owns the look). The
// pointer resting on the last crumb shows it; pressing the crumb — the
// siblings popover — hides it again, as tooltips do.
func crumbPathTip(c *ui.Context, anchor ui.Element, cwd string) {
	tipped(c, anchor, tildePath(cwd), tipIcon(iconFolder), tipMaxWidth(520))
}

// switcherListMaxHeight is how tall a project list may grow in a switcher
// panel — the sidebar head's and the path's alike — before its rows scroll
// inside the panel; it leaves a pull-down room under the title bar of the
// smallest window.
const switcherListMaxHeight = 280

// crumbPopover lists the segment's siblings for quick switching. The rows
// scroll inside the panel past switcherListMaxHeight; a MaxHeight on the
// column alone left a long list running out of the panel (2026-10-29).
func (s *Shell) crumbPopover(c *ui.Context, anchor ui.Element, index int, segment crumbSegment) {
	ui.Popover(c, anchor, &s.crumbOpen[index], func() {
		sp := Spacing()
		ui.Column(c).Width(300).Padding(sp.S).Children(func() {
			ui.Scroll(c).FillWidth().MaxHeight(switcherListMaxHeight).Children(func() {
				ui.Column(c).FillWidth().Gap(1).Children(func() {
					switch segment.kind {
					case "project":
						s.crumbProjectRows(c)
					case "tab":
						// The segment id is the selected Tab; its siblings list is
						// scoped by the selected Project.
						s.crumbTabRows(c, s.selectedProjectID)
					case "pane":
						s.crumbPaneRows(c, segment.id)
					}
				})
			})
		})
	})
}

func (s *Shell) crumbProjectRows(c *ui.Context) {
	for _, project := range s.projection.Projects {
		project := project
		row := crumbItemRow(c, visualMark{svg: iconFolder}, project.Label, opUnknown, project.ID == s.selectedProjectID)
		if row.Clicked() {
			s.crumbOpen = [3]bool{}
			s.setProjectExpanded(project.ID, true)
			s.selectProject(project.ID)
		}
	}
}

func (s *Shell) crumbTabRows(c *ui.Context, projectID string) {
	dark := c.Theme().Dark
	for _, tab := range tabsForProject(s.projection, projectID) {
		tab := tab
		mark := visualMark{svg: iconTab}
		label := tab.Label
		panes := panesForTab(s.projection, tab.ID)
		if len(panes) == 1 {
			if a := s.paneAgent(panes[0].ID); a != nil {
				mark, _ = s.agentMarkForPane(panes[0].ID, dark)
				// Popover rows speak the same title as the crumb and the
				// sidebar rows: the Agents section's card title.
				if title, ok := s.agentCardTitleForPane(panes[0].ID); ok {
					label = title
				} else if label == "" || label == "Tab" {
					label = a.Name
					if label == "" {
						if p := agentProvider(a); p != "" {
							label = p.DisplayName()
						}
					}
				}
			} else if brand, ok := s.procMarkForPaneWithTheme(panes[0].ID, dark); ok {
				mark = brand
			}
		}
		row := crumbItemRow(c, mark, label, opUnknown, tab.ID == s.selectedTabID)
		if row.Clicked() {
			s.crumbOpen = [3]bool{}
			s.selectTab(tab.ID)
		}
	}
}

func (s *Shell) crumbPaneRows(c *ui.Context, tabID string) {
	panes := panesForTab(s.projection, tabID)
	// Mirror the Herdr split order, including user-driven pane swaps.
	sortPanesByLayout(s.projection, tabID, panes)
	for _, pane := range panes {
		pane := pane
		dark := c.Theme().Dark
		mark, isAgent := s.agentMarkForPane(pane.ID, dark)
		if !isAgent {
			if brand, ok := s.procMarkForPane(pane.ID); ok {
				mark = brand
			}
		}
		dot := opUnknown
		if isAgent {
			if a := s.paneAgent(pane.ID); a != nil {
				dot = normalizeRuntimeStatus(a.Status)
			}
		}
		label := pane.Label
		if title, ok := s.agentCardTitleForPane(pane.ID); ok {
			label = title
		}
		row := crumbItemRow(c, mark, label, dot, pane.ID == s.selectedPaneID)
		if row.Clicked() {
			s.crumbOpen = [3]bool{}
			s.selectPane(pane.ID)
		}
	}
}

// crumbItemRow is one switcher row: glyph (with the agent state dot at
// its corner when bound), label, selected tint.
func crumbItemRow(c *ui.Context, mark visualMark, label string, dot operationalState, selected bool) ui.Element {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	var row ui.Element
	ui.Box(c).Children(func() {
		row = ui.ButtonBase(c).
			FillWidth().
			Height(28).
			Radius(Radius().Row).
			Padding(0, 8).
			Gap(7).
			Label(label)
		if tint, ok := rowTint(tokens, selected, row.Hovered()); ok {
			row.Background(tint)
		}
		row.Children(func() {
			if dot != opUnknown {
				ui.Box(c).Size(14, 14).Shrink(0).Children(func() {
					markView(c, mark, 14, t.TextMuted)
					ui.Box(c).Absolute().Left(9).Top(7).Children(func() {
						statusDot(c, dot)
					})
				})
			} else {
				ui.Box(c).Shrink(0).Children(func() { markView(c, mark, 14, t.TextMuted) })
			}
			ui.Text(c, label).FontSize(Typography().Body).Grow(1).SingleLine()
		})
	})
	return row
}
