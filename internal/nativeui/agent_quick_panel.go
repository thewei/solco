package nativeui

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖共享 StatusCenterSnapshot/StatusCenterEntry、agentCardRow、gorexToggleFace（过滤芯片与图标按钮同一状态脸）、surface_header 的 Chat/Terminal 记忆与 quick panel 的 Flyout 关闭
 * [OUTPUT]: 提供 Agent 活动浮层的分组表（quickPanelGroupSpecs/quickPanelBucketGroup）、过滤芯片（quickPanelChip/quickPanelChipCounts）、分组（quickPanelGroups）、分组标题（quickPanelGroupHeader）、条目行（quickPanelEntryRow：点击直达对应面板、行尾「标记已审阅」、右键菜单）与 openQuickPanelEntry/openAgentTerminal 直达动作
 * [POS]: quick_panel 的内容构件；状态由同一目录投影生成，点击不再进入第二层浮层，无第二套弹层窗口或运行时状态
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// quickPanelGroupSpecs are the panel's status groups in priority order: the
// Agents tab's filter chips and the list's section headers share these
// labels and dot colours.
var quickPanelGroupSpecs = []struct {
	Label string
	Tone  StatusTone
}{
	{"Needs you", ToneAttention},
	{"Working", ToneWorking},
	{"Idle", ToneMuted},
}

// The group indexes of quickPanelGroupSpecs (the chip filter's values).
const (
	quickPanelNeedsYouGroup = 0
	quickPanelWorkingGroup  = 1
	quickPanelIdleGroup     = 2
)

// quickPanelBucketGroup places a directory bucket in its panel group: 0 is
// "Needs you" (attention then review: the same set the header's waiting
// count counts), 1 Working, 2 Idle.
func quickPanelBucketGroup(bucket agent.AgentDirectoryBucket) int {
	switch bucket {
	case agent.BucketNeedsAttention, agent.BucketReadyForReview:
		return quickPanelNeedsYouGroup
	case agent.BucketWorking:
		return quickPanelWorkingGroup
	}
	return quickPanelIdleGroup
}

// quickPanelChipCounts counts every group's entries in a snapshot — the
// chips show all three counts, the list drops the empty groups.
func quickPanelChipCounts(entries []StatusCenterEntry) [3]int {
	var counts [3]int
	for _, entry := range entries {
		counts[quickPanelBucketGroup(entry.Bucket)]++
	}
	return counts
}

// quickPanelChip is one filter chip: the group's name and its count in a
// pill. The chosen chip wears the shared toggle face; the label names the
// whole chip ("Working 2") so tests and readers click it as one.
func quickPanelChip(c *ui.Context, group, count int, selected bool) bool {
	t := c.Theme()
	spec := quickPanelGroupSpecs[group]
	label := fmt.Sprintf("%s %d", spec.Label, count)
	b := ui.ButtonBase(c.Key("qp-chip:"+spec.Label)).Height(22).Padding(0, 9).
		Radius(11).FocusRing(false).AlignItems(ui.Center).Gap(4).Label(label)
	gorexToggleFace(b, gorexColorsOf(t.Dark), selected)
	b.Border(1, designTokens(t.Dark).BorderSubtle)
	col := t.TextMuted
	if selected {
		col = t.Text
	}
	b.Children(func() {
		ui.Text(c, spec.Label).FontSize(Typography().BodySmall).TextColor(col).SingleLine()
		ui.Text(c, fmt.Sprintf("%d", count)).FontSize(Typography().BodySmall).TextColor(col).SingleLine()
	})
	return b.Clicked()
}

// quickPanelGroup is one status section of the Agents list: the entries of a
// directory bucket, under the label and dot colour that name the state.
type quickPanelGroup struct {
	Label   string
	Tone    StatusTone
	Entries []StatusCenterEntry
}

// quickPanelGroups splits the snapshot's entries into status sections in the
// directory's priority order — Needs you (attention then review: the same set
// the header's waiting count counts), Working, Idle — leaving out the empty
// ones. Each section keeps the snapshot's own order.
func quickPanelGroups(entries []StatusCenterEntry) []quickPanelGroup {
	groups := make([]quickPanelGroup, len(quickPanelGroupSpecs))
	for i, spec := range quickPanelGroupSpecs {
		groups[i] = quickPanelGroup{Label: spec.Label, Tone: spec.Tone}
	}
	// Attention rows lead Review rows inside "Needs you".
	for _, bucket := range []agent.AgentDirectoryBucket{agent.BucketNeedsAttention, agent.BucketReadyForReview, agent.BucketWorking} {
		g := quickPanelBucketGroup(bucket)
		for _, entry := range entries {
			if entry.Bucket == bucket {
				groups[g].Entries = append(groups[g].Entries, entry)
			}
		}
	}
	for _, entry := range entries {
		if g := quickPanelBucketGroup(entry.Bucket); g == quickPanelIdleGroup {
			groups[g].Entries = append(groups[g].Entries, entry)
		}
	}
	kept := groups[:0]
	for _, group := range groups {
		if len(group.Entries) > 0 {
			kept = append(kept, group)
		}
	}
	return kept
}

// quickPanelGroupHeader is the panel's one section header: an optional state
// dot, the label and a count. The Services tab's sections wear it too, so
// both tabs read the same.
func quickPanelGroupHeader(c *ui.Context, label string, count int, dot *StatusTone) {
	t := c.Theme()
	sp := Spacing()
	ui.Row(c).FillWidth().Padding(sp.M, sp.M, sp.XS).Gap(sp.S).AlignItems(ui.Center).Children(func() {
		if dot != nil {
			ui.Box(c).Size(7, 7).Radius(3.5).Shrink(0).
				Background(designTokens(t.Dark).StatusColor(*dot, t.Dark))
		}
		ui.Text(c, label).FontSize(Typography().BodySmall).FontWeight(WeightMedium).
			TextColor(t.TextMuted).SingleLine()
		ui.Text(c, itoa(count)).FontSize(Typography().Caption).TextColor(t.TextMuted).SingleLine()
	})
}

// quickPanelEntryRow is one Agent in the panel: the shared card, a click that
// goes straight to the panel it belongs to (no second layer), the usage line
// beneath when the History meta has one, and the rest of the actions in the
// right-click menu. A row waiting for review carries its one quick action —
// mark reviewed — at the trailing edge.
func (s *Shell) quickPanelEntryRow(c *ui.Context, entry StatusCenterEntry, dupProjects map[string]bool) {
	t := c.Theme()
	card := entry.Card
	var row ui.Element
	ui.Box(c).FillWidth().Children(func() {
		row = s.agentCardRow(c, card, false, dupProjects, false, false, func() {
			if !card.ReviewPending {
				return
			}
			k := gorexColorsOf(t.Dark)
			if gorexIconButton(c, k, iconCheck, "Mark Reviewed", "Mark Reviewed", false, gorexRowBtn, gorexRowGlyph).Clicked() {
				s.markAgentReviewed(card.Key)
				s.fitQuickPanel()
			}
		})
		// Sub-agents hang under the agent that spawned them, running or
		// done (the sidebar's rows; the chevron folds them, a click opens
		// the sub-agent's own drawer in Chat, then closes the panel).
		s.nestedSubagentRows(c, card, nil,
			func(sub history.Subagent) {
				s.hideQuickPanel()
				s.openSubagentDrawer(card, sub)
			},
			func() {
				s.hideQuickPanel()
				s.openChat(card)
			})
		// §17.2: the usage caption renders under the card when the cached
		// History-meta projection has facts, lined up with the card's text.
		if usage := s.usageLineFor(card.Key); usage != "" {
			ui.Box(c).Padding(0, 0, Spacing().XS, agentCardTextInset).Children(func() {
				ui.Text(c, usage).FontSize(Typography().Caption).
					TextColor(t.TextMuted).SingleLine()
			})
		}
	})
	if row.Clicked() {
		s.openQuickPanelEntry(entry)
	}
	row.ContextMenu(func(m *ui.Menu) { s.quickPanelEntryMenu(m, entry) })
}

// quickPanelEntryMenu holds what a row's click does not: the other panel, the
// inspector and the review mark.
func (s *Shell) quickPanelEntryMenu(m *ui.Menu, entry StatusCenterEntry) {
	card := entry.Card
	if m.Item("Open Terminal").Chosen() {
		s.hideQuickPanel()
		s.openAgentTerminal(card)
	}
	chat := "Open Chat"
	if entry.HasInteraction {
		chat = "Answer in Chat"
	}
	if m.Item(chat).Chosen() {
		s.hideQuickPanel()
		s.openChat(card)
	}
	if card.PaneID != "" && m.Item("Open Inspector").Chosen() {
		s.hideQuickPanel()
		s.router.Push("/inspector/" + card.PaneID)
	}
	if card.ReviewPending {
		m.Separator()
		if m.Item("Mark Reviewed").Chosen() {
			s.markAgentReviewed(card.Key)
			s.fitQuickPanel()
		}
	}
}

// openQuickPanelEntry is a row click: close the panel and land on the panel
// the Agent needs you at — its Chat when it waits on an interaction, its
// Terminal when it is blocked or failed, otherwise the surface you last used
// on that pane (the Terminal by default, as a sidebar click does).
func (s *Shell) openQuickPanelEntry(entry StatusCenterEntry) {
	card := entry.Card
	s.hideQuickPanel()
	switch statusCenterRecommendedDestination(entry) {
	case actionOpenChat:
		s.openChat(card)
	case agent.ActionOpenTerminal:
		s.openAgentTerminal(card)
	default:
		if s.panePrefersChat(card.PaneID) {
			s.openChat(card)
			return
		}
		s.openAgentTerminal(card)
	}
}

// openAgentTerminal selects the Agent's pane and shows the Terminal — from
// whichever page or surface the panel was opened on.
func (s *Shell) openAgentTerminal(card agent.AgentCardModel) {
	s.rememberPaneSurface(card.PaneID, WorkspaceSurfaceTerminal)
	s.openAgentCard(card)
	s.showViewTerminal()
}

// quickPanelSubagentRows counts the nested sub-agent rows the entries show
// (the height budget of the rows quickPanelEntryRow hangs under them).
func (s *Shell) quickPanelSubagentRows(entries []StatusCenterEntry) int {
	rows := 0
	for _, entry := range entries {
		rows += len(s.nestedSubagents(entry.Card))
		if s.earlierSubagents(entry.Card) > 0 {
			rows++ // the "+N earlier" row
		}
	}
	return rows
}
