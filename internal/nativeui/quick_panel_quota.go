package nativeui

import (
	"math"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/usage"
)

/**
 * [INPUT]: 依赖 usage_state 的 usageEnabled/Service.Result、page_usage 的 usageLookOf/usageBar/usageBadge/usageIn/usageMark（同一套读数外观与配速语义）、quickPanelHeaderHeight 所在的 quick_panel
 * [OUTPUT]: 对外提供 Shell.quickPanelQuota（浮层 Quota 页签：一行一张迷你卡片 + 页脚）、quotaHeadline（卡片的一个标题数字）、quickPanelQuotaHeight（内容驱动高度）
 * [POS]: Header 浮层的额度列（2026-10-31 用户拍板改一行一张）：每家一张卡，只放最紧的那个窗口与重置时间，细节留在 /quota 页；页签打开时按 TTL 读一次（refreshUsage(false)），无定时器、不记录历史
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	quotaCardHeight       = 96
	quotaCardGap          = 8
	quickPanelQuotaFooter = 38
)

// quotaHeadline picks the one window a mini card leads with: a counted
// allowance (Cursor's 500 requests) first, otherwise the tightest window.
func quotaHeadline(snap usage.Snapshot) (usage.Window, bool) {
	for _, w := range snap.Windows {
		if w.Counted() && !w.Minor {
			return w, true
		}
	}
	return snap.Tightest()
}

// quickPanelQuotaHeight sizes the tab to its cards: one per line.
func (s *Shell) quickPanelQuotaHeight() int {
	n := len(s.usageEnabled())
	if n == 0 {
		return quickPanelHeaderHeight + quickPanelEmptyHeight + quickPanelQuotaFooter
	}
	h := quickPanelHeaderHeight + n*quotaCardHeight + (n-1)*quotaCardGap + int(2*Spacing().S) + quickPanelQuotaFooter
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
			ui.Row(c).FillWidth().Wrap().Gap(quotaCardGap).Padding(sp.S, sp.M).Children(func() {
				for _, id := range ids {
					s.quotaMiniCard(c, id, now)
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

// quotaMiniCard is one provider: mark and name, the headline number, a thin
// bar, and the window with its reset.
func (s *Shell) quotaMiniCard(c *ui.Context, id usage.ID, now time.Time) {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	typ := Typography()
	sp := Spacing()
	res := s.usage.svc.Result(id)
	busy := s.usage.svc.Busy(id)
	left := s.usageShowLeft()

	ui.Column(c.Key("quota:"+string(id))).FillWidth().Height(quotaCardHeight).
		Radius(Radius().Row).Background(tokens.Panel).Border(1, tokens.BorderSubtle).
		Padding(sp.M, sp.M).Gap(sp.XS).Children(func() {
		ui.Row(c).FillWidth().Gap(sp.S).AlignItems(ui.Center).Children(func() {
			usageMark(c, id, 14)
			ui.Text(c, id.Title()).FontSize(typ.Body).FontWeight(WeightSemibold).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
			if res.Has && res.Snapshot.Plan != "" {
				ui.Text(c, res.Snapshot.Plan).FontSize(typ.Caption).TextColor(t.TextMuted).SingleLine().Shrink(0)
			}
		})
		w, ok := quotaHeadline(res.Snapshot)
		if !res.Has || !ok {
			badge, _ := usageBadge(res, busy, now)
			ui.Text(c, badge).FontSize(typ.BodySmall).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0)
			return
		}
		stale := res.Stale()
		look := usageLookOf(c, w, left, stale, now)
		big, small := usageShownText(w.UsedPct, left), ""
		if w.Counted() && !w.Minor {
			count, word := w.Used, "used"
			if left {
				count, word = math.Max(0, w.Max-w.Used), "left"
			}
			unit := w.Unit
			if unit == "" {
				unit = "units"
			}
			big = strconv.FormatFloat(math.Round(count), 'f', 0, 64)
			small = "of " + strconv.FormatFloat(math.Round(w.Max), 'f', 0, 64) + " " + unit + " " + word
		} else {
			// "87% used" splits into the number and its word.
			big = strconv.Itoa(usageShown(w.UsedPct, left)) + "%"
			small = map[bool]string{true: "left", false: "used"}[left]
		}
		ui.Row(c).FillWidth().Gap(sp.XS).AlignItems(ui.End).Children(func() {
			ui.Text(c, big).FontSize(typ.Heading).FontWeight(WeightSemibold).TextColor(look.value).SingleLine().Shrink(0)
			ui.Text(c, small).FontSize(typ.Caption).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
		})
		usageBar(c, look, usageCompactBar)
		foot := w.Name
		if !w.Resets.IsZero() {
			if d := w.Resets.Sub(now); d > 0 {
				foot += " · resets " + usageIn(d)
			}
		}
		ui.Text(c, foot).FontSize(typ.Caption).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0)
	})
}
