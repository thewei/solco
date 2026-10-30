package nativeui

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 mygo/ui 的 ButtonBase、Spinner，agent_visual 的品牌 mark 与状态点、git_meta 的 git 事实
 * [OUTPUT]: 对外提供 Shell.agentCardRow（侧栏 Agents 区与 Agent 活动浮层共享的两行卡片，可选行尾控件）与 agentCardTextInset（卡片文字起点，卡片下方的说明行对齐用）；第一行 = 会话标题（agentCardLabel 以标题为主名，多卡同项目时 a11y 标签追项目消歧），第二行 = 项目名 + git 事实（同一 muted 样式）
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
func (s *Shell) agentCardRow(c *ui.Context, card agent.AgentCardModel, selected bool, dupProjects map[string]bool, hideProject bool, trailing func()) ui.Element {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	mark := agentMark(card.Provider, t.Dark)
	label, _ := agentCardLabel(card, dupProjects)
	var row ui.Element
	ui.Box(c.Key("agent:" + card.PaneID)).Children(func() {
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
					s.agentFactsView(c, card)
				})
			})
			if card.Unread {
				ui.Box(c).Size(6, 6).Radius(3).Background(tokens.StatusColor(ToneAttention, t.Dark)).Shrink(0)
			}
			if n := len(card.Subagents); n > 0 && len(openSubagents(card, time.Now())) == 0 {
				tipped(c, ui.Text(c, "⤷"+itoa(n)).FontSize(Typography().Micro).TextColor(t.TextMuted).SingleLine().Shrink(0),
					pluralUnits(n, "sub-agent")+" ran")
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

// agentFactsView renders the git facts of the card's Pane cwd with
// distinct glyphs and colors (2026-10-05 round three): branch behind a
// branch glyph, the changed-file count behind a file glyph, and the
// additions/deletions in success/danger colors. It reports false until
// the background meta produced facts (the caller then falls back to the
// agent title).
func (s *Shell) agentFactsView(c *ui.Context, card agent.AgentCardModel) bool {
	t := c.Theme()
	cwd := ""
	if pane := paneByID(s.projection, card.PaneID); pane != nil {
		cwd = pane.CWD
	}
	branch, adds, dels, files, ok := s.gitMetaForCWD(cwd)
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

// subagentRowsShown caps the nested rows under one agent.
const subagentRowsShown = 6

// openSubagents are the sub-agent rows a card shows nested under it: the most
// recent ones, but only while at least one is still running (an all-done
// card folds them into its ⤷N chip instead).
func openSubagents(card agent.AgentCardModel, now time.Time) []history.Subagent {
	running := false
	for _, sub := range card.Subagents {
		if sub.Active(now) {
			running = true
			break
		}
	}
	if !running {
		return nil
	}
	subs := card.Subagents
	if len(subs) > subagentRowsShown {
		subs = subs[len(subs)-subagentRowsShown:]
	}
	return subs
}

// subagentRow is one nested sub-agent: a guide line from its parent, the
// kind in bold, the task muted, the same state slot at the end (a spinner
// while it writes, a ring once done). Clicking it opens the parent.
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
