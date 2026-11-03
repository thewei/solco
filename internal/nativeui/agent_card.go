package nativeui

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 mygo/ui 的 ButtonBase、Spinner，agent_visual 的品牌 mark 与状态点、git_meta 的 git 事实、gorexIconButton 与 iconCheck
 * [OUTPUT]: 对外提供 Shell.agentCardRow（侧栏 Agents 区与 Agent 活动浮层共享的两行卡片，可选行尾控件）、Shell.agentReviewAction（共享的 Mark Reviewed 尾部勾号动作按钮）与 agentCardTextInset（卡片文字起点，卡片下方的说明行对齐用）；第一行 = 会话标题（agentCardLabel 以标题为主名，多卡同项目时 a11y 标签追项目消歧），第二行 = 项目名 + git 事实（同一 muted 样式）
 * [POS]: Agent 卡片的唯一呈现实现（2026-10-07；2026-10-30 用户拍板：标题上移第一行、项目名下到第二行，推翻 F118 的「项目名即标题」）：品牌 mark + 角落状态点 + 标题行 + 项目/事实行；工作中卡片尾随 Spinner（card.Working，与目录 Working 桶同一语义）；点击语义留给调用方
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// agentCardLabel builds the card's accessibility label. The session title
// is the primary name (2026-10-30 user request — the project name moved to
// the second line, reversing F118); when several visible cards share one
// project, the project name is appended so the rows stay tellable apart
// (F169). The second result reports whether the label already carries the
// project, so callers need no duplicate disambiguation line.
func agentCardLabel(card agent.AgentCardModel, dupProjects map[string]bool) (string, bool) {
	if card.Title == "" {
		return card.ProjectName, false
	}
	if card.ProjectName != "" && dupProjects[card.ProjectName] {
		return card.Title + " · " + card.ProjectName, true
	}
	return card.Title, false
}

// duplicateAgentProjectLabels reports project labels shared by more than
// one of the visible cards — the set agentCardLabel disambiguates against.
func duplicateAgentProjectLabels(cards []agent.AgentCardModel) map[string]bool {
	counts := map[string]int{}
	for _, card := range cards {
		if card.ProjectName != "" {
			counts[card.ProjectName]++
		}
	}
	dups := map[string]bool{}
	for name, count := range counts {
		if count > 1 {
			dups[name] = true
		}
	}
	return dups
}

// The card's horizontal metrics. agentCardTextInset is where its text
// starts, so a caption set under the card lines up with the title above.
const (
	agentCardPadX      float32 = 8
	agentCardGap       float32 = 8
	agentCardMark      float32 = 18
	agentCardTextInset         = agentCardPadX + agentCardMark + agentCardGap
)

// agentCardRow is the two-line Agent card shared by the sidebar's Agents
// section and the floating agent activity panel (2026-10-07): the brand
// mark with the operational state as a corner dot, the project (or agent
// title) on the first line, and the git facts (or the agent title) on the
// second. Status words are deliberately absent — the dot is the state,
// and a working agent spins at the row's trailing edge so mid-flight work
// reads at a glance. Click handling stays with the caller; the returned
// element carries the Clicked/ContextMenu surface. A caller may add one
// control at the trailing edge (trailing), which draws inside the row after
// the spinner and the unread dot.
func (s *Shell) agentCardRow(c *ui.Context, card agent.AgentCardModel, selected bool, dupProjects map[string]bool, hideProject, hideFacts bool, trailing func()) ui.Element {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	mark := agentMark(card.Provider, t.Dark)
	label, _ := agentCardLabel(card, dupProjects)
	var row ui.Element
	ui.Box(c.Key("agent:" + card.PaneID)).Transition(treeRowMotion).Children(func() { // an Agent that starts grows in; one that exits fades out
		row = ui.ButtonBase(c).
			FillWidth().
			MinHeight(38).
			Radius(6).
			Padding(0, agentCardPadX).
			Gap(agentCardGap).
			AlignItems(ui.Center).
			Label(label)
		if tint, ok := rowTint(tokens, selected, row.Hovered()); ok {
			row.Background(tint)
		}
		row.Transition(rowFade)
		row.Children(func() {
			ui.Box(c).Size(agentCardMark, agentCardMark).Shrink(0).Children(func() {
				markView(c, mark, agentCardMark, t.TextMuted)
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
				// First line: the agent's own session title (2026-10-30
				// user request); the project name and the git facts share
				// the second line in the same muted style. Neither present
				// keeps the card single-line.
				tipped(c, ui.Text(c, card.Title).FontSize(Typography().Body).SingleLine(), card.Title)
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					if card.ProjectName != "" && !hideProject {
						tipped(c, ui.Text(c, card.ProjectName).FontSize(Typography().Micro).TextColor(t.TextMuted).SingleLine(), card.ProjectName)
					}
					if !hideFacts {
						s.agentFactsView(c, card)
					}
				})
			})
			if card.Unread {
				ui.Box(c).Size(6, 6).Radius(3).Background(tokens.StatusColor(ToneAttention, t.Dark)).Shrink(0)
			}
			if len(card.Subagents) > 0 {
				// The disclosure of the rows nested under the card (the
				// sidebar and the Header popover draw them alike).
				s.subagentToggle(c, card)
			}
			// The row ends with when the Agent last worked and its state
			// slot (2026-10-30): spinner, dot and ring are one indicator
			// (status_indicator.go), never at the icon any more.
			if ago := agentAgo(card.LastActive, time.Now()); ago != "" {
				ui.Text(c, ago).FontSize(Typography().Micro).TextColor(t.TextMuted).SingleLine().Shrink(0).PassThrough()
			}
			statusIndicator(c, agentIndicatorState(card))
			if trailing != nil {
				trailing()
			}
		})
	})
	return row
}

// agentReviewAction renders the shared "Mark Reviewed" check button at the trailing
// edge of an Agent card row when the card is in ReviewPending state.
// If onReviewed is non-nil, it is called after markAgentReviewed finishes.
func (s *Shell) agentReviewAction(c *ui.Context, card agent.AgentCardModel, onReviewed func()) {
	if !card.ReviewPending {
		return
	}
	k := gorexColorsOf(c.Theme().Dark)
	if gorexIconButton(c, k, iconCheck, "Mark Reviewed", "Mark Reviewed", false, gorexRowBtn, gorexRowGlyph).Clicked() {
		s.markAgentReviewed(card.Key)
		if onReviewed != nil {
			onReviewed()
		}
	}
}

// agentFactsView renders the git facts of the card's Pane cwd with
// distinct glyphs and colors (2026-10-05 round three): branch behind a
// branch glyph, the changed-file count behind a file glyph, and the
// additions/deletions in success/danger colors. It reports false until
// the background meta produced facts (the caller then falls back to the
// agent title).
func (s *Shell) agentFactsView(c *ui.Context, card agent.AgentCardModel) bool {
	branch, adds, dels, files, ok := s.gitMetaForCWD(s.agentCWD(card))
	return gitFactsView(c, branch, adds, dels, files, ok)
}

// agentCWD is the card's Pane cwd ("" when the Pane is gone).
func (s *Shell) agentCWD(card agent.AgentCardModel) string {
	if pane := paneByID(s.projection, card.PaneID); pane != nil {
		return pane.CWD
	}
	return ""
}

// agentGitRoot is the repository root of the card's Pane cwd, "" until the
// background lane resolved it. Cards sharing a root share their git facts.
func (s *Shell) agentGitRoot(card agent.AgentCardModel) string {
	cwd := s.agentCWD(card)
	if s.git == nil || cwd == "" {
		return ""
	}
	return s.git.rootOf[cwd]
}

// gitFactsView draws one set of git facts; it reports false (drawing
// nothing) when they are unknown or empty.
func gitFactsView(c *ui.Context, branch string, adds, dels, files int, ok bool) bool {
	t := c.Theme()
	if !ok || (branch == "" && adds == 0 && dels == 0 && files == 0) {
		return false
	}
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		if branch != "" {
			ui.Icon(c, iconBranch).Size(9, 9).TextColor(t.TextMuted)
			tipped(c, ui.Text(c, branch).FontSize(Typography().Micro).TextColor(t.TextMuted).SingleLine(), branch)
		}
		if files != 0 {
			ui.Icon(c, iconFile).Size(9, 9).TextColor(t.TextMuted)
			ui.Text(c, itoa(files)).FontSize(Typography().Micro).TextColor(t.TextMuted)
		}
		if adds != 0 {
			ui.Text(c, "+"+itoa(adds)).FontSize(Typography().Micro).TextColor(t.Success)
		}
		if dels != 0 {
			ui.Text(c, "-"+itoa(dels)).FontSize(Typography().Micro).TextColor(t.Danger)
		}
	})
	return true
}

// agentIndicatorState is the state a card's slot draws: a working (or
// launching) card spins whatever its attention projection says, otherwise
// the attention state itself.
func agentIndicatorState(card agent.AgentCardModel) operationalState {
	if card.Working() {
		return opWorking
	}
	return operationalState(card.Attention)
}

// agentAgo is the short "last worked" tag: "<1m", "7m", "3h", "2d"; empty
// when the instant is unknown.
func agentAgo(at, now time.Time) string {
	if at.IsZero() {
		return ""
	}
	d := now.Sub(at)
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return itoa(int(d/time.Minute)) + "m"
	case d < 48*time.Hour:
		return itoa(int(d/time.Hour)) + "h"
	default:
		return itoa(int(d/(24*time.Hour))) + "d"
	}
}

// runningSubagents counts the sub-agents still writing.
func runningSubagents(card agent.AgentCardModel, now time.Time) int {
	n := 0
	for _, sub := range card.Subagents {
		if sub.Active(now) {
			n++
		}
	}
	return n
}

// subagentRowsShown caps the nested rows under one agent.
const subagentRowsShown = 6

// agentFoldKey names a sidebar fold in s.foldedAgents: kind is "group" (a
// Workspace's Agents, id = project id) or "sub" (a parent's sub-agent rows,
// id = its terminal id, the pane id before one is known).
func agentFoldKey(kind, id string) string { return kind + ":" + id }

func subagentFoldKey(card agent.AgentCardModel) string {
	id := card.Key.TerminalID
	if id == "" {
		id = card.PaneID
	}
	return agentFoldKey("sub", id)
}

// toggleAgentFold flips one Workspace group's fold; open is the default.
func (s *Shell) toggleAgentFold(key string) {
	if s.foldedAgents == nil {
		s.foldedAgents = map[string]bool{}
	}
	if s.foldedAgents[key] {
		delete(s.foldedAgents, key)
	} else {
		s.foldedAgents[key] = true
	}
}

// subagentAutoFoldAfter is how long a parent's sub-agents must have been all
// done before their rows fold on their own.
const subagentAutoFoldAfter = 10 * time.Minute

// subagentsFolded is whether a parent's nested rows are folded: the user's
// own choice when they made one (true = folded, false = opened on purpose,
// which also overrides the automatic fold), otherwise folded once every
// sub-agent has been done for subagentAutoFoldAfter.
func (s *Shell) subagentsFolded(card agent.AgentCardModel, now time.Time) bool {
	if folded, chosen := s.foldedAgents[subagentFoldKey(card)]; chosen {
		return folded
	}
	var newest time.Time
	for _, sub := range card.Subagents {
		if sub.Active(now) {
			return false
		}
		newest = laterOf(newest, sub.UpdatedAt)
	}
	return len(card.Subagents) > 0 && !newest.IsZero() && now.Sub(newest) >= subagentAutoFoldAfter
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// toggleSubagentFold flips a parent's sub-agent rows from whatever they show
// now, recording the choice.
func (s *Shell) toggleSubagentFold(card agent.AgentCardModel) {
	if s.foldedAgents == nil {
		s.foldedAgents = map[string]bool{}
	}
	s.foldedAgents[subagentFoldKey(card)] = !s.subagentsFolded(card, time.Now())
}

// nestedSubagents are the rows nested under a card (sidebar and Header
// popover): the newest few, running or not (2026-11-01 — they used to appear
// only while one was still writing, then fold into a ⤷N chip), unless the
// user folded this parent.
func (s *Shell) nestedSubagents(card agent.AgentCardModel) []history.Subagent {
	if s.subagentsFolded(card, time.Now()) {
		return nil
	}
	subs := card.Subagents
	if len(subs) > subagentRowsShown {
		subs = subs[len(subs)-subagentRowsShown:]
	}
	return subs
}

// groupIndicatorState is the state a folded Workspace label draws: whatever
// needs a person wins; otherwise working when any card works or any of its
// sub-agents still writes (the fold hides both); otherwise the strongest
// card's state (the cards arrive actionable-first).
func groupIndicatorState(cards []agent.AgentCardModel, now time.Time) operationalState {
	state := agentIndicatorState(cards[0])
	if state == opNeedsAttention || state == opReadyForReview {
		return state
	}
	for _, card := range cards {
		if card.Working() || runningSubagents(card, now) > 0 {
			return opWorking
		}
	}
	return state
}

// setAgentFolds folds or unfolds every Workspace group ("group") or every
// parent's sub-agent rows ("sub") of the listing at once.
func (s *Shell) setAgentFolds(groups []agentGroup, kind string, fold bool) {
	if s.foldedAgents == nil {
		s.foldedAgents = map[string]bool{}
	}
	for _, group := range groups {
		if kind == "group" && group.Name != "" {
			key := agentFoldKey("group", group.Key)
			if fold {
				s.foldedAgents[key] = true
			} else {
				delete(s.foldedAgents, key)
			}
		}
		for _, card := range group.Cards {
			if kind == "sub" && len(card.Subagents) > 0 {
				// Unfolding is a choice too: it keeps a long-finished
				// parent open against the automatic fold.
				s.foldedAgents[subagentFoldKey(card)] = fold
			}
		}
	}
}

// earlierSubagents is how many of a card's sub-agents the nested rows leave
// out (they keep the newest subagentRowsShown); 0 while the parent is folded.
func (s *Shell) earlierSubagents(card agent.AgentCardModel) int {
	if s.subagentsFolded(card, time.Now()) {
		return 0
	}
	return max(len(card.Subagents)-subagentRowsShown, 0)
}

// nestedSubagentRows draws a card's nested rows: a "+N earlier" row first when
// the list was cut, then the newest sub-agents. openSub runs on a sub-agent
// row's click; openAll on the "earlier" row's (Chat's roster lists them all).
// nav registers the rows with the sidebar's keyboard model (nil: none).
func (s *Shell) nestedSubagentRows(c *ui.Context, card agent.AgentCardModel, nav *listNav, openSub func(history.Subagent), openAll func()) {
	subs := s.nestedSubagents(card)
	if n := s.earlierSubagents(card); n > 0 {
		t := c.Theme()
		tokens := designTokens(t.Dark)
		var row ui.Element
		ui.Box(c.Key("subearlier:" + subagentFoldKey(card))).FillWidth().Children(func() {
			row = tipped(c, ui.ButtonBase(c).FillWidth().Height(20).Radius(6).Padding(0, agentCardPadX).AlignItems(ui.Center).
				Label("Show earlier sub-agents"), "Open Chat to see all "+itoa(len(card.Subagents)))
			if tint, ok := rowTint(tokens, false, row.Hovered()); ok {
				row.Background(tint)
			}
			row.Transition(rowFade)
			row.Draw(func(p *ui.Painter, r ui.Rect) {
				x := r.X + agentCardPadX + agentCardMark/2
				p.Line(x, r.Y, x, r.Y+r.H, 1, tokens.TreeLine)
			})
			row.Children(func() {
				ui.Box(c).Width(agentCardMark + 4).Shrink(0).PassThrough()
				ui.Text(c, "+"+itoa(n)+" earlier").FontSize(Typography().Micro).TextColor(t.TextMuted).SingleLine().PassThrough()
			})
		})
		if row.Clicked() {
			openAll()
		}
	}
	svc, hasSvc := s.cardService(card)
	for i, sub := range subs {
		row := s.subagentRow(c, card, sub, i == len(subs)-1 && !hasSvc)
		nav.row(c, row, navRow{Key: "sub:" + card.PaneID + ":" + sub.ID, Level: 1}) // nil nav: the popover has no keyboard model
		if nav != nil {
			row.FocusRing(false)
			if nav.showsCursor(row) {
				row.Background(designTokens(c.Theme().Dark).SidebarSelect)
			}
		}
		if row.Clicked() {
			openSub(sub)
		}
	}
	if hasSvc {
		// The service the Agent's pane is running hangs beside its
		// sub-agents as one more nested row (2026-11-02): same guide line,
		// the service shape (rounded square) instead of the Agent circle.
		row := s.serviceRow(c, card, svc)
		nav.row(c, row, navRow{Key: "svc:" + card.PaneID, Level: 1})
		if nav != nil {
			row.FocusRing(false)
			if nav.showsCursor(row) {
				row.Background(designTokens(c.Theme().Dark).SidebarSelect)
			}
		}
		if row.Clicked() {
			s.rememberPaneSurface(card.PaneID, WorkspaceSurfaceTerminal)
			s.openAgentCard(card)
		}
	}
}

// cardService is the service running in the card's pane (a dev server the
// Agent started), shown nested like a sub-agent; none while the parent's
// nested rows are folded or the pane has no listener.
func (s *Shell) cardService(card agent.AgentCardModel) (paneServiceActivity, bool) {
	if card.PaneID == "" || s.subagentsFolded(card, time.Now()) {
		return paneServiceActivity{}, false
	}
	act, ok := s.serviceIndex.Panes[card.PaneID]
	return act, ok && len(act.Ports) > 0
}

// serviceRow is one nested service: the process in bold, its ports muted, the
// service-shaped slot at the end. A click opens the pane's Terminal, where it
// runs (v1 is read-only: no Stop).
func (s *Shell) serviceRow(c *ui.Context, parent agent.AgentCardModel, act paneServiceActivity) ui.Element {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	name := firstText(procFriendlyName(act.Proc), act.Proc, "service")
	ports := servicePortsText(act)
	var row ui.Element
	ui.Box(c.Key("svc:" + parent.PaneID)).FillWidth().Children(func() {
		row = ui.ButtonBase(c).FillWidth().Height(24).Radius(6).Padding(0, agentCardPadX).Gap(6).
			AlignItems(ui.Center).Label("Service " + serviceActivityDetail(act))
		if tint, ok := rowTint(tokens, false, row.Hovered()); ok {
			row.Background(tint)
		}
		row.Transition(rowFade)
		row.Draw(func(p *ui.Painter, r ui.Rect) {
			x := r.X + agentCardPadX + agentCardMark/2
			p.Line(x, r.Y, x, r.Y+r.H/2, 1, tokens.TreeLine)
			p.Line(x, r.Y+r.H/2, x+9, r.Y+r.H/2, 1, tokens.TreeLine)
		})
		row.Children(func() {
			if mark, ok := procServiceMark(act); ok {
				ui.Box(c).Width(agentCardMark + 4).Shrink(0).Children(func() {
					markView(c, mark, agentCardMark, t.TextMuted)
				})
			} else {
				ui.Box(c).Width(agentCardMark + 4).Shrink(0).PassThrough()
			}
			ui.Text(c, name).FontSize(Typography().BodySmall).FontWeight(WeightSemibold).SingleLine().Shrink(0).PassThrough()
			ui.Text(c, ports).FontSize(Typography().Micro).TextColor(t.TextMuted).
				SingleLine().Ellipsis("…").Shrink(1).MinWidth(0).Grow(1).PassThrough()
			statusIndicatorService(c, healthToneOf(act), serviceActivityDetail(act))
		})
	})
	return row
}

// subagentToggle is the parent card's disclosure for its sub-agent rows: a
// chevron and the count, ending on the running tally in the tooltip. It sits
// inside the card's row, so a click folds without opening the Agent.
func (s *Shell) subagentToggle(c *ui.Context, card agent.AgentCardModel) {
	t := c.Theme()
	key := subagentFoldKey(card)
	folded := s.subagentsFolded(card, time.Now())
	n, running := len(card.Subagents), runningSubagents(card, time.Now())
	tip := pluralUnits(n, "sub-agent")
	if running > 0 {
		tip += ", " + itoa(running) + " running"
	}
	if folded {
		tip += " — click to show"
	} else {
		tip += " — click to fold"
	}
	var btn ui.Element
	ui.Box(c.Key("subfold:" + key)).Shrink(0).Children(func() {
		btn = tipped(c, ui.ButtonBase(c).Height(20).Padding(0, 4, 0, 0).Radius(treeRowRadius).Gap(0).AlignItems(ui.Center).
			Label("Toggle sub-agents of "+card.Title), tip)
		if btn.Hovered() {
			btn.Background(t.SurfaceHover)
		}
		btn.Transition(rowFade)
		btn.Children(func() {
			treeChevron(c, true, !folded, t.TextMuted)
			ui.Text(c, itoa(n)).FontSize(Typography().Micro).TextColor(t.TextMuted).SingleLine().Shrink(0).PassThrough()
			if folded && running > 0 {
				// A fold hides the rows that show the work: the same
				// spinner slot keeps it visible.
				statusIndicator(c, opWorking)
			}
		})
	})
	if btn.Clicked() {
		s.toggleSubagentFold(card)
		s.fitQuickPanel() // the Header popover's height follows its rows
	}
}

// subagentRow is one nested sub-agent: a guide line from its parent, the
// kind in bold, the task muted, the same state slot at the end (a spinner
// while it writes, a ring once done). Clicking it opens the sub-agent's own
// drawer in Chat (openSubagentDrawer), like the page's cards do.
func (s *Shell) subagentRow(c *ui.Context, parent agent.AgentCardModel, sub history.Subagent, last bool) ui.Element {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	name := sub.Type
	if name == "" {
		name = "Sub-agent"
	}
	var row ui.Element
	ui.Box(c.Key("sub:" + parent.PaneID + ":" + sub.ID)).FillWidth().Children(func() {
		row = ui.ButtonBase(c).FillWidth().Height(24).Radius(6).Padding(0, agentCardPadX).Gap(6).
			AlignItems(ui.Center).Label(name + " " + sub.Description)
		if tint, ok := rowTint(tokens, false, row.Hovered()); ok {
			row.Background(tint)
		}
		row.Transition(rowFade)
		row.Draw(func(p *ui.Painter, r ui.Rect) {
			x := r.X + agentCardPadX + agentCardMark/2
			bottom := r.Y + r.H
			if last {
				bottom = r.Y + r.H/2
			}
			p.Line(x, r.Y, x, bottom, 1, tokens.TreeLine)
			p.Line(x, r.Y+r.H/2, x+9, r.Y+r.H/2, 1, tokens.TreeLine)
		})
		row.Children(func() {
			ui.Box(c).Width(agentCardMark + 4).Shrink(0).PassThrough()
			ui.Text(c, name).FontSize(Typography().BodySmall).FontWeight(WeightSemibold).SingleLine().Shrink(0).PassThrough()
			ui.Text(c, sub.Description).FontSize(Typography().Micro).TextColor(t.TextMuted).
				SingleLine().Ellipsis("…").Shrink(1).MinWidth(0).Grow(1).PassThrough()
			if sub.Active(time.Now()) {
				statusIndicator(c, opWorking)
			} else {
				statusIndicator(c, opIdle)
			}
		})
	})
	return row
}
