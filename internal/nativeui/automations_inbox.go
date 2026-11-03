package nativeui

/**
 * [INPUT]: 依赖 internal/automations 的 LastRun.Seen / MarkSeen / MarkAllSeen（插件自己的 inbox 已读标记）、automationsWrite 通道、quick_panel 的分组头与行尺寸、statusIndicatorTone
 * [OUTPUT]: Shell.automationsUnread / automationsHeaderCounts（Header 触发器的 attention/review 计入：失败且未读 = attention，完成且未读 = review）、Shell.markAutomationSeen（打开抽屉即已读）、Shell.quickPanelAutomations（Header 浮层的 Automations 页签：未读 run 的只读列表 + Mark All Seen）
 * [POS]: 自动化 run 进入 Header 数字的唯一入口；页签只在 automations 插件门打开时出现；状态与已读都归插件，这里只读 list 里的 last_run 并发出 `seen`
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/automations"
)

// automationsBackgroundGap is how old the list may get before the runtime
// hook re-reads it for the Header count. The page and the panel read sooner.
const automationsBackgroundGap = time.Minute

// automationRunUnread is whether an automation's newest run finished and
// nobody opened it. A draft, a paused entry's old run and a run still going
// are not news.
func automationRunUnread(a automations.Automation) bool {
	if a.Draft || a.LastRun == nil || a.LastRun.Seen {
		return false
	}
	return a.LastRun.Status == "failed" || a.LastRun.Status == "done"
}

// automationsUnread splits the unread runs: failures need a look, finished
// runs have a result to read.
func (s *Shell) automationsUnread() (failed, finished []automations.Automation) {
	if !s.automationsAvailable() {
		return nil, nil
	}
	for _, a := range s.automations.list.result.Automations {
		if !automationRunUnread(a) {
			continue
		}
		if a.LastRun.Status == "failed" {
			failed = append(failed, a)
		} else {
			finished = append(finished, a)
		}
	}
	return failed, finished
}

// automationsHeaderCounts is what the Header trigger adds for automations:
// failed unread runs count as attention, finished unread runs as review.
func (s *Shell) automationsHeaderCounts() (attention, review int) {
	failed, finished := s.automationsUnread()
	return len(failed), len(finished)
}

// refreshAutomationsBackground keeps the Header count current without a
// timer: the runtime's projection hook calls it, and it re-reads at most once
// a minute (the page and the panel read sooner on their own).
func (s *Shell) refreshAutomationsBackground() {
	list := &s.automations.list
	if !s.automationsAvailable() || list.loading {
		return
	}
	gap := automationsBackgroundGap
	if list.busy(timeNow()) {
		gap = automationsPollGap
	}
	if !list.loadedAt.IsZero() && timeNow().Sub(list.loadedAt) < gap {
		return
	}
	s.refreshAutomations(true)
}

// markAutomationSeen records the newest run of one automation as opened. The
// row reads as seen at once; a refused or failed write is corrected by the
// next list read.
func (s *Shell) markAutomationSeen(name string) {
	a := s.automations.list.automation(name)
	if a == nil || !automationRunUnread(*a) {
		return
	}
	runID := a.LastRun.RunID
	a.LastRun.Seen = true
	s.syncRemoteAutomations()
	s.invalidateQuickPanel()
	s.automationsWrite("seen", name, func(ctx context.Context, client *automations.Client) error {
		return client.MarkSeen(ctx, runID)
	}, nil)
}

// markAllAutomationsSeen clears the inbox.
func (s *Shell) markAllAutomationsSeen() {
	for i := range s.automations.list.result.Automations {
		if a := &s.automations.list.result.Automations[i]; automationRunUnread(*a) {
			a.LastRun.Seen = true
		}
	}
	s.syncRemoteAutomations()
	s.automationsWrite("seen-all", "", func(ctx context.Context, client *automations.Client) error {
		return client.MarkAllSeen(ctx)
	}, nil)
}

// quickPanelAutomationsFooterHeight is the Mark All Seen / Open row.
const quickPanelAutomationsFooterHeight = 44

// quickPanelAutomationsHeight sizes the tab like the Tasks tab: a header per
// group, a row per run.
func (s *Shell) quickPanelAutomationsHeight() int {
	failed, finished := s.automationsUnread()
	groups, rows := 0, len(failed)+len(finished)
	if len(failed) > 0 {
		groups++
	}
	if len(finished) > 0 {
		groups++
	}
	return min(quickPanelListHeight(rows, groups, 0)+quickPanelAutomationsFooterHeight, QuickPanelHeight)
}

// quickPanelAutomations renders the Automations tab: runs nobody has opened,
// failures first. A row opens the automation's drawer on its page.
func (s *Shell) quickPanelAutomations(c *ui.Context) {
	sp := Spacing()
	t := c.Theme()
	failed, finished := s.automationsUnread()
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(0, sp.S, sp.M).Children(func() {
			if len(failed)+len(finished) == 0 {
				if s.automations.list.loadedAt.IsZero() && !s.automations.list.failed {
					return // first read in flight
				}
				emptyState(c, "No new runs", "")
				return
			}
			for _, group := range []struct {
				label string
				tone  StatusTone
				items []automations.Automation
			}{{"Failed", ToneError, failed}, {"Finished", ToneSuccess, finished}} {
				if len(group.items) == 0 {
					continue
				}
				tone := group.tone
				quickPanelGroupHeader(c, group.label, len(group.items), &tone)
				for _, a := range group.items {
					s.quickPanelAutomationRow(c, a, group.tone, t.TextMuted)
				}
			}
		})
	})
	ui.Row(c).FillWidth().Padding(sp.S, sp.M).Gap(sp.S).Justify(ui.End).Children(func() {
		if regularButton(c, "Mark All Seen").Clicked() {
			s.markAllAutomationsSeen()
			s.fitQuickPanel()
		}
		if regularButton(c, "Open Automations").Clicked() {
			s.hideQuickPanel()
			s.goAutomations()
		}
	})
}

// quickPanelAutomationRow is one unread run: name over "<how it ended> · when".
func (s *Shell) quickPanelAutomationRow(c *ui.Context, a automations.Automation, tone StatusTone, muted ui.Color) {
	tokens := designTokens(c.Theme().Dark)
	title := a.Name
	caption := "Finished"
	if a.LastRun.Status == "failed" {
		caption = "Failed"
	}
	if started, err := time.Parse(time.RFC3339, a.LastRun.StartedAt); err == nil {
		caption += " · " + gdRelativeTime(started)
	}
	var row ui.Element
	ui.Box(c.Key("auto:" + a.Name)).FillWidth().Children(func() {
		row = ui.ButtonBase(c).FillWidth().MinHeight(38).Radius(6).
			Padding(0, agentCardPadX).Gap(agentCardGap).AlignItems(ui.Center).Label(title)
		if bg, ok := rowTint(tokens, false, row.Hovered()); ok {
			row.Background(bg)
		}
		row.Transition(rowFade)
		row.Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
				tipped(c, ui.Text(c, title).FontSize(Typography().Body).SingleLine(), title)
				ui.Text(c, caption).FontSize(Typography().Micro).TextColor(muted).SingleLine()
			})
			statusIndicatorTone(c, tone, caption)
		})
	})
	if row.Clicked() {
		s.hideQuickPanel()
		s.goAutomations()
		s.openAutomationsDrawer(a.Name)
	}
}
