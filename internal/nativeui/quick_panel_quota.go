package nativeui

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/usage"
)

/**
 * [INPUT]: 依赖 usage_state 的 usageEnabled/Service.Result、page_usage 的 usageLookOf/usageBar/usageBadge/usageIn/usageMark（同一套读数外观与配速语义）、quickPanelHeaderHeight 所在的 quick_panel
 * [OUTPUT]: 对外提供 Shell.quickPanelQuota（浮层 Quota 页签：一行一张卡，与 /quota 同一个 usageCard(compact)，全部窗口 + 页脚）、quotaHeadline（一个标题数字）、quickPanelQuotaHeight（按窗口数估高）
 * [POS]: Header 浮层的额度列（2026-10-31 用户拍板改一行一张）：2026-11-01：浮层与内页同一张卡片（usageCard），只差密度，不再各画各的；页签打开时按 TTL 读一次（refreshUsage(false)），无定时器、不记录历史
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	quotaCardGap          = 8
	quickPanelQuotaFooter = 38
	// quotaCardChrome and quotaRowHeight estimate a compact card's height
	// (header, padding, and one 22pt row per window or note); the list
	// scrolls, so the estimate only sizes the flyout.
	quotaCardChrome = 52
	quotaRowHeight  = 22
)

// quotaHeadline picks the one window a mini card leads with: a counted
// allowance (Cursor's 500 requests) first, otherwise the tightest window.
// The flyout no longer draws a headline-only card; the headline stays the
// answer for surfaces that have room for one number (the sidebar, tooltips).
func quotaHeadline(snap usage.Snapshot) (usage.Window, bool) {
	for _, w := range snap.Windows {
		if w.Counted() && !w.Minor {
			return w, true
		}
	}
	return snap.Tightest()
}

// quotaCardEstimate is a compact card's height.
func (s *Shell) quotaCardEstimate(id usage.ID) int {
	res := s.usage.svc.Result(id)
	rows := 1
	if res.Has {
		rows = len(res.Snapshot.Windows) + len(res.Snapshot.Notes)
	}
	return quotaCardChrome + rows*quotaRowHeight
}

// quickPanelQuotaHeight sizes the tab to its cards: one per line.
func (s *Shell) quickPanelQuotaHeight() int {
	ids := s.usageEnabled()
	if len(ids) == 0 {
		return quickPanelHeaderHeight + quickPanelEmptyHeight + quickPanelQuotaFooter
	}
	h := quickPanelHeaderHeight + (len(ids)-1)*quotaCardGap + int(2*Spacing().S) + quickPanelQuotaFooter
	for _, id := range ids {
		h += s.quotaCardEstimate(id)
	}
	return min(max(h, quickPanelMinHeight), QuickPanelHeight)
}

// quickPanelQuota is the Quota tab: one mini card per switched-on provider,
// and a footer that says how fresh the readings are and opens the page.
func (s *Shell) quickPanelQuota(c *ui.Context) {
	t := c.Theme()
	sp := Spacing()
	ids := s.usageEnabled()
	now := time.Now()
	if len(ids) == 0 {
		ui.Column(c).Grow(1).MinHeight(0).Children(func() {
			emptyState(c, "No quota sources", "Switch on the agents whose limits you want to see.")
		})
	} else {
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().Gap(quotaCardGap).Padding(sp.S, sp.M).Children(func() {
				for _, id := range ids {
					s.usageCard(c, id, true)
				}
			})
		})
	}
	ui.Row(c).FillWidth().Height(quickPanelQuotaFooter).Padding(0, sp.M).Gap(sp.S).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Text(c, s.quotaFreshness(ids, now)).FontSize(Typography().Caption).TextColor(t.TextMuted).Grow(1).SingleLine()
		if regularButton(c, "Open Quota").Clicked() {
			s.hideQuickPanel()
			s.router.Push(routeQuota)
		}
	})
}

// quotaFreshness is the footer's left side: how old the oldest reading is,
// or that one is being read.
func (s *Shell) quotaFreshness(ids []usage.ID, now time.Time) string {
	var oldest time.Time
	for _, id := range ids {
		if s.usage.svc.Busy(id) {
			return "Reading…"
		}
		res := s.usage.svc.Result(id)
		if res.Has && !res.Snapshot.At.IsZero() && (oldest.IsZero() || res.Snapshot.At.Before(oldest)) {
			oldest = res.Snapshot.At
		}
	}
	if oldest.IsZero() {
		return ""
	}
	return "Updated " + usageAgo(now.Sub(oldest))
}
