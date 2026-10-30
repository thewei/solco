package nativeui

import (
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
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
func (s *Shell) agentCardRow(c *ui.Context, card agent.AgentCardModel, selected bool, dupProjects map[string]bool, trailing func()) ui.Element {
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
				// Always shown; gray means idle (round three follow-up).
				ui.Box(c).Absolute().Left(11).Top(10).Children(func() {
					statusDot(c, operationalState(card.Attention))
				})
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
				// First line: the agent's own session title (2026-10-30
				// user request); the project name and the git facts share
				// the second line in the same muted style. Neither present
				// keeps the card single-line.
				tipped(c, ui.Text(c, card.Title).FontSize(Typography().Body).SingleLine(), card.Title)
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					if card.ProjectName != "" {
						tipped(c, ui.Text(c, card.ProjectName).FontSize(Typography().Micro).TextColor(t.TextMuted).SingleLine(), card.ProjectName)
					}
					s.agentFactsView(c, card)
				})
			})
			// A working (or launching) agent spins at the trailing edge:
			// mid-flight work reads at a glance, with no status words —
			// the row stays word-free by contract, so the spinner is
			// unnamed; AT already reads it as a progress indicator inside
			// the labeled row. The spinner paints only while its row
			// shows (mygo drives the frames), so idle agents cost nothing.
			if card.Working() {
				ui.Spinner(c)
			}
			if card.Unread {
				ui.Box(c).Size(6, 6).Radius(3).Background(tokens.StatusColor(ToneAttention, t.Dark)).Shrink(0)
			}
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
