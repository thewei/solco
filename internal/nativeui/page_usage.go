package nativeui

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/usage"
)

/**
 * [INPUT]: 依赖 internal/usage 的 Result/Snapshot/Window（额度读数与配速）、history.DayTokens（近 30 天本地 token）、Shell.usage 状态与 settings.Usage 偏好、components/ds_components/controls 共享件
 * [OUTPUT]: 对外提供 Shell.usagePage（/usage：首次启用视图 / 一条摘要 + Needs a look + 一行一张卡）、usageReadingToggle（Used|Left，标题栏工具簇与浮层 Quota 页共用）、usageCard（页与浮层同一张卡：提供方标题 + usageFace 每窗口一张浮起卡，手机端 T3 Usage 样式，2026-11-08）、usageMenu（标题栏 Actions：设置入口）、usageAgo/usageIn/usageTokens 等格式化纯函数
 * [POS]: Sessions 区的第三个入口页；只呈现，读取与偏好在 usage_state.go，额度语义在 internal/usage；页面打开时读一次、手动刷新才再读，没有定时器，也不记录历史
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	usagePageMaxWidth  float32 = 820
	usageBarHeight     float32 = 6
	usageMeterHeight   float32 = 16
	usageSparkHeight   float32 = 28
	usageCompactBar    float32 = 4
	usageCompactDetail float32 = 124
	// usageNameWide is the name column of the compact card — the flyout's and
	// every card's sub-limit rows — wide enough for "Weekly · Sonnet".
	usageNameWide float32 = 108
)

// usageAgent maps a provider to the Agent identity whose brand mark it wears;
// Zhipu has no brand asset and draws a letter tile instead.
func usageAgent(id usage.ID) history.AgentID {
	switch id {
	case usage.Claude:
		return history.AgentClaudeCode
	case usage.Codex:
		return history.AgentCodex
	case usage.Cursor:
		return history.AgentCursor
	case usage.Copilot:
		return history.AgentCopilot
	case usage.Antigravity:
		return history.AgentAntigravity
	}
	return ""
}

// usageMark draws a provider's identity at size.
func usageMark(c *ui.Context, id usage.ID, size float32) {
	t := c.Theme()
	if agent := usageAgent(id); agent != "" {
		markView(c, agentMark(agent, t.Dark), size, t.TextMuted)
		return
	}
	ui.Box(c).Size(size, size).Shrink(0).Radius(size * 0.26).
		Background(designTokens(t.Dark).PanelHover).Center().Children(func() {
		ui.Text(c, "Z").FontSize(size * 0.6).FontWeight(WeightBold).TextColor(t.TextMuted)
	})
}

// usageHasToken reports a provider that can take a token of its own.
func usageHasToken(id usage.ID) bool {
	switch id {
	case usage.Cursor, usage.Zhipu, usage.Copilot:
		return true
	}
	return false
}

// usageAgo reads an age: "12 s ago".
func usageAgo(d time.Duration) string {
	switch {
	case d < 5*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%d s ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d d ago", int(d.Hours()/24))
}

// usageIn reads a span ahead: "2h 14m", "3d 4h", "<1m".
func usageIn(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		h, m := int(d.Hours()), int(d.Minutes())%60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	}
	days, h := int(d.Hours()/24), int(d.Hours())%24
	if h == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd %dh", days, h)
}

// usageTokens abbreviates a token count: 1.2M, 340K.
func usageTokens(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%dK", n/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

// usageShown is the percentage a window shows: used, or what is left.
func usageShown(pct float64, left bool) int {
	used := int(math.Round(pct))
	if left {
		return max(0, 100-used)
	}
	return used
}

func usageShownText(pct float64, left bool) string {
	if left {
		return fmt.Sprintf("%d%% left", usageShown(pct, true))
	}
	return fmt.Sprintf("%d%% used", usageShown(pct, false))
}

// usageTone maps a bar level to a status tone; ToneNeutral is the plain bar.
func usageTone(l usage.Level) StatusTone {
	switch l {
	case usage.LevelBad:
		return ToneError
	case usage.LevelWarn:
		return ToneAttention
	}
	return ToneNeutral
}

// usageBadge is the one-word standing of a card.
func usageBadge(res usage.Result, busy bool, now time.Time) (string, StatusTone) {
	age := ""
	if res.Has && !res.Snapshot.At.IsZero() {
		age = " · last seen " + usageAgo(now.Sub(res.Snapshot.At))
	}
	switch res.State {
	case usage.StateLive:
		return "Live · " + usageAgo(now.Sub(res.Snapshot.At)), ToneSuccess
	case usage.StateSetup:
		return "Needs setup" + age, ToneAttention
	case usage.StateExpired:
		return "Sign-in expired" + age, ToneAttention
	case usage.StateRateLimited:
		return "Rate limited" + age, ToneAttention
	case usage.StateUnavailable:
		return "Unavailable" + age, ToneAttention
	case usage.StateCustomEndpoint:
		return "Custom endpoint", ToneNeutral
	}
	if busy {
		return "Reading…", ToneNeutral
	}
	return "Not read yet", ToneNeutral
}

// usageSource is the card's footer: where the reading comes from.
func (s *Shell) usageSource(id usage.ID) string {
	pasted := s.usage.tokens[id]
	switch id {
	case usage.Claude:
		return "Claude Code's sign-in · api.anthropic.com"
	case usage.Codex:
		return "Codex app-server on this Mac · no sign-in read"
	case usage.Cursor:
		if pasted {
			return "Your token (stored encrypted) · cursor.com"
		}
		return "Cursor's own sign-in · cursor.com"
	case usage.Zhipu:
		if s.settings.Usage.ZhipuSite == usage.ZhipuChina {
			return "Your API key (stored encrypted) · open.bigmodel.cn"
		}
		return "Your API key (stored encrypted) · api.z.ai"
	case usage.Copilot:
		if pasted {
			return "Your token (stored encrypted) · api.github.com"
		}
		return "GitHub CLI sign-in · api.github.com"
	case usage.Antigravity:
		if s.settings.Usage.SignInAllowed[string(id)] {
			return "Antigravity's saved sign-in · cloudcode-pa.googleapis.com"
		}
		return "Antigravity's local server · this Mac only"
	}
	return ""
}

// usageRouteHeader is the title bar of /quota: Sessions › Quota, with the
// Used | Left reading beside the page's Actions (2026-11-01: it left the
// summary row for the bar's tool cluster, where Git's own toggles live).
func (s *Shell) usageRouteHeader() routeHeaderSpec {
	return routeHeaderSpec{parent: "Sessions", onParent: s.showHistoryIndex, title: "Quota", actions: s.usageMenu, tools: s.usageReadingToggle}
}

// usageReadingToggle is the Used | Left switch, drawn by the title bar on
// /quota and by the flyout's Quota tab: one control for both surfaces.
func (s *Shell) usageReadingToggle(c *ui.Context) {
	if !s.settings.Usage.Configured || len(s.usageEnabled()) == 0 {
		return
	}
	idx := 0
	if s.usageShowLeft() {
		idx = 1
	}
	if segmentedControl(c, &idx, "Used", "Left").Label("Reading").Changed() {
		s.setUsageShowLeft(idx == 1)
	}
}

// usageMenu is the page's Actions pull-down.
func (s *Shell) usageMenu(m *ui.Menu) {
	if m.Item("Quota Settings…").Chosen() {
		s.router.Push(usageSettingsPath)
	}
}

// usagePage is /usage.
func (s *Shell) usagePage(c *ui.Context) {
	st := s.usage
	if !st.pageOpen {
		st.pageOpen = true
		s.refreshUsage(false)
	}
	ui.Column(c).Grow(1).MinWidth(0).Children(func() {
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			contentColumn(c, usagePageMaxWidth, func() {
				ui.Column(c).FillWidth().Padding(pageTopInset(), Spacing().XL, Spacing().XL).Gap(Spacing().L).Children(func() {
					switch ids := s.usageEnabled(); {
					case !s.settings.Usage.Configured:
						s.usageFirstRun(c)
					case len(ids) == 0:
						ui.Column(c).FillWidth().Padding(Spacing().XL*2, 0).Children(func() {
							emptyState(c, "No agents turned on", "Choose which agents Quota may read.")
							ui.Row(c).FillWidth().Padding(Spacing().L, 0, 0).Justify(ui.Center).Children(func() {
								if regularButton(c, "Quota Settings…").Clicked() {
									s.router.Push(usageSettingsPath)
								}
							})
						})
					default:
						s.usageSummary(c, ids)
						s.usageAttentionCard(c, ids)
						for _, id := range ids {
							s.usageCard(c, id)
						}
					}
				})
			})
		})
	})
}

// usageFirstRun asks which agents Quota may read before it contacts any.
func (s *Shell) usageFirstRun(c *ui.Context) {
	t := c.Theme()
	contentColumn(c, formColumnWidth, func() {
		ui.Column(c).FillWidth().Gap(Spacing().L).Children(func() {
			ui.Column(c).FillWidth().Gap(Spacing().XS).Children(func() {
				ui.Text(c, "Turn on the agents you use").FontSize(Typography().Heading).FontWeight(WeightSemibold)
				ui.Text(c, "Quota reads each agent's own limits. Nothing is contacted until you switch it on.").
					FontSize(Typography().BodySmall).TextColor(t.TextMuted)
			})
			var rows []settingsRow
			for _, id := range usage.All {
				id := id
				r := row(id.Title(), usageConsentNote(id), func() {
					on := s.usageProviderOn(id)
					if on2, changed := settingSwitch(c, id.Title(), on); changed {
						s.requestUsageProvider(id, on2)
					}
				})
				r.lead = func() { usageMark(c, id, 20) }
				rows = append(rows, r)
			}
			settingsGroup(c, "", rows...)
			ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
				if regularPrimaryButton(c, "Show Quota").Clicked() {
					s.commitUsageFirstRun()
				}
			})
		})
	})
}

// usageConsentNote is the quiet line under a provider's switch.
func usageConsentNote(id usage.ID) string {
	switch id {
	case usage.Claude:
		return "Reads Claude Code's sign-in from the Keychain, after you allow it"
	case usage.Codex:
		return "Asks the local codex app-server; reads no sign-in"
	case usage.Cursor:
		return "Reads Cursor's own sign-in, after you allow it — or use your token"
	case usage.Zhipu:
		return "Needs your API key (Settings)"
	case usage.Copilot:
		return "Uses your GitHub CLI sign-in after you allow it — or your token"
	case usage.Antigravity:
		return "Uses Antigravity's saved sign-in, after you allow it — works with the app closed"
	}
	return ""
}

// usageSummary is the one strip above the cards: the tightest limit, the next
// reset and how many sources are live, as three facts of one panel.
func (s *Shell) usageSummary(c *ui.Context, ids []usage.ID) {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	typ := Typography()
	sp := Spacing()
	now := time.Now()
	left := s.usageShowLeft()

	var (
		tight        usage.Window
		tightOwner   usage.ID
		haveTight    bool
		next         usage.Window
		nextOwner    usage.ID
		haveNext     bool
		live, stale  int
		needSetup    int
		nextInterval time.Duration
		oldest       time.Time
	)
	for _, id := range ids {
		res := s.usage.svc.Result(id)
		switch {
		case res.State == usage.StateLive:
			live++
		case res.Stale():
			stale++
		case res.State == usage.StateSetup || res.State == usage.StateExpired:
			needSetup++
		}
		if !res.Has {
			continue
		}
		if !res.Snapshot.At.IsZero() && (oldest.IsZero() || res.Snapshot.At.Before(oldest)) {
			oldest = res.Snapshot.At
		}
		for _, w := range res.Snapshot.Windows {
			if !haveTight || w.UsedPct > tight.UsedPct {
				tight, tightOwner, haveTight = w, id, true
			}
			if d := w.Resets.Sub(now); !w.Resets.IsZero() && d > 0 && (!haveNext || d < nextInterval) {
				next, nextOwner, haveNext, nextInterval = w, id, true, d
			}
		}
	}

	stat := func(label, value, detail string, tone StatusTone) {
		ui.Column(c).Grow(1).Basis(160).MinWidth(0).Gap(sp.XXS).Children(func() {
			ui.Text(c, label).FontSize(typ.Micro).FontWeight(WeightBold).TextColor(t.TextMuted)
			color := t.Text
			if tone != ToneNeutral {
				color = tokens.StatusColor(tone, t.Dark)
			}
			ui.Text(c, value).FontSize(typ.Heading).FontWeight(WeightSemibold).TextColor(color).SingleLine().Ellipsis("…")
			ui.Text(c, detail).FontSize(typ.Caption).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
		})
	}
	ui.Row(c).FillWidth().Wrap().Gap(sp.XL).AlignItems(ui.Start).Padding(sp.M, sp.L).
		Radius(Radius().Card).Background(tokens.Panel).Border(1, tokens.BorderSubtle).Children(func() {
		if haveTight {
			tone, paceText := usageWindowAlert(tight, now)
			detail := tightOwner.Title() + " · " + tight.Name
			if paceText != "" {
				detail += " · " + paceText
			}
			stat("TIGHTEST", usageShownText(tight.UsedPct, left), detail, tone)
		} else {
			stat("TIGHTEST", "—", "Nothing read yet", ToneNeutral)
		}
		if haveNext {
			stat("NEXT RESET", usageIn(nextInterval), nextOwner.Title()+" · "+next.Name, ToneNeutral)
		} else {
			stat("NEXT RESET", "—", "No reset time known", ToneNeutral)
		}
		detail := fmt.Sprintf("%d stale · %d need setup", stale, needSetup)
		if !oldest.IsZero() {
			detail = "oldest read " + usageAgo(now.Sub(oldest))
			if needSetup > 0 {
				detail += fmt.Sprintf(" · %d need setup", needSetup)
			}
		}
		stat("SOURCES", fmt.Sprintf("%d live", live), detail, ToneNeutral)
	})
}

// usageWindowAlert is how worried to be about one window: its level's tone,
// raised by the pace, and the pace sentence ("runs out in 2h at this pace",
// "limit reached") when there is one. The strip and "Needs a look" share it.
func usageWindowAlert(w usage.Window, now time.Time) (StatusTone, string) {
	pace, runsOut := w.Pace(now)
	tone := usageTone(w.Level())
	text := ""
	switch pace {
	case usage.PaceExceeded:
		tone, text = ToneError, "limit reached"
	case usage.PaceRunsOut:
		text = "runs out in " + usageIn(runsOut) + " at this pace"
		if tone == ToneNeutral {
			tone = ToneAttention
		}
	}
	return tone, text
}

// usageAttentionItem is one line of "Needs a look": a provider that cannot be
// read, or a window near its limit.
type usageAttentionItem struct {
	id   usage.ID
	win  string // the window's name; empty for a setup/sign-in line
	text string
	tone StatusTone
}

// usageAttention lists what deserves a look, worst first: windows at their
// limit or that will run out before they reset (pace), then providers that
// need setup or a fresh sign-in.
func (s *Shell) usageAttention(ids []usage.ID, now time.Time, left bool) []usageAttentionItem {
	var windows, setups []usageAttentionItem
	for _, id := range ids {
		res := s.usage.svc.Result(id)
		switch res.State {
		case usage.StateSetup:
			setups = append(setups, usageAttentionItem{id: id, text: id.Title() + " — needs setup", tone: ToneAttention})
			continue
		case usage.StateExpired:
			setups = append(setups, usageAttentionItem{id: id, text: id.Title() + " — sign-in expired", tone: ToneAttention})
			continue
		}
		if !res.Has || res.Stale() {
			continue
		}
		for _, w := range res.Snapshot.Windows {
			tone, paceText := usageWindowAlert(w, now)
			if tone == ToneNeutral {
				continue
			}
			parts := []string{id.Title() + " · " + w.Name + "  " + usageShownText(w.UsedPct, left)}
			if paceText != "" {
				parts = append(parts, paceText)
			}
			if d := w.Resets.Sub(now); !w.Resets.IsZero() && d > 0 {
				parts = append(parts, "resets "+usageIn(d))
			}
			windows = append(windows, usageAttentionItem{id: id, win: w.Name, text: strings.Join(parts, " · "), tone: tone})
		}
	}
	sort.SliceStable(windows, func(i, j int) bool { return windows[i].tone == ToneError && windows[j].tone != ToneError })
	return append(windows, setups...)
}

// usageAttentionCard is "Needs a look": the answer to what will bite soon.
// It is not drawn when nothing does.
func (s *Shell) usageAttentionCard(c *ui.Context, ids []usage.ID) {
	// The summary above already names the tightest window; listing it again
	// here would say the same thing twice.
	var tightOwner usage.ID
	var tightName string
	var tightPct float64
	for _, id := range ids {
		if res := s.usage.svc.Result(id); res.Has {
			for _, w := range res.Snapshot.Windows {
				if tightName == "" || float64(w.UsedPct) > tightPct {
					tightOwner, tightName, tightPct = id, w.Name, float64(w.UsedPct)
				}
			}
		}
	}
	var items []usageAttentionItem
	for _, it := range s.usageAttention(ids, time.Now(), s.usageShowLeft()) {
		if it.win != "" && it.id == tightOwner && it.win == tightName {
			continue
		}
		items = append(items, it)
	}
	if len(items) == 0 {
		return
	}
	t := c.Theme()
	tokens := designTokens(t.Dark)
	typ := Typography()
	sp := Spacing()
	ui.Column(c).FillWidth().Gap(sp.XS).Padding(sp.M, sp.L).
		Radius(Radius().Card).Background(tokens.Panel).Border(1, tokens.BorderSubtle).Children(func() {
		ui.Text(c, "NEEDS A LOOK").FontSize(typ.Micro).FontWeight(WeightBold).TextColor(t.TextMuted)
		for _, it := range items {
			ui.Row(c).FillWidth().Gap(sp.S).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(6, 6).Shrink(0).Radius(3).Background(tokens.StatusColor(it.tone, t.Dark))
				ui.Text(c, it.text).FontSize(typ.BodySmall).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
			})
		}
	})
}

// usageCard is one provider's card, one to a row: the page and the Header
// popover's Quota tab draw this same card (2026-11-01: the popover used to
// drop the pace footers, the 30-day strip and the source line), so the two
// surfaces cannot disagree.
func (s *Shell) usageCard(c *ui.Context, id usage.ID) {
	t := c.Theme()
	typ := Typography()
	sp := Spacing()
	now := time.Now()
	res := s.usage.svc.Result(id)
	busy := s.usage.svc.Busy(id)
	left := s.usageShowLeft()
	badge, tone := usageBadge(res, busy, now)

	// The phone's Usage screen (designs/mobile-t3-redesign, 2026-11-07), in the
	// pointer tier: a provider header, then one raised card per window, then
	// the notes, the reason a reading is stale and where it came from. The
	// provider itself is not wrapped in a card.
	ui.Column(c.Key("quota-card:" + string(id))).FillWidth().Gap(sp.M).Children(func() {
		ui.Row(c).FillWidth().Gap(sp.M).AlignItems(ui.Center).Padding(0, sp.XS).Children(func() {
			usageMark(c, id, 20)
			ui.Text(c, id.Title()).FontSize(typ.Section).FontWeight(WeightMedium).SingleLine()
			if res.Has && res.Snapshot.Plan != "" {
				ui.Text(c, res.Snapshot.Plan).FontSize(typ.BodySmall).TextColor(t.TextMuted).SingleLine()
			}
			ui.Box(c).Grow(1)
			if busy {
				ui.Spinner(c).Size(12, 12)
			}
			statusPill(c, badge, tone)
		})

		if res.Has {
			stale := res.Stale()
			s.usageWindows(c, id, res.Snapshot.Windows, left, stale, now)
			s.usageNotes(c, res.Snapshot.Notes)
			if stale {
				s.usageProblem(c, id, res, now)
			}
			s.usageActivity(c, id)
		} else if busy && res.State == usage.StateIdle {
			usageSkeleton(c)
		} else {
			s.usageProblem(c, id, res, now)
		}

		ui.Text(c, s.usageSource(id)).FontSize(typ.Caption).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0).Padding(0, sp.XS)
	})
}

// usageFaceCard is the raised surface every window sits on (the phone's
// `.ucard`): the same fill, radius and padding on the page and in the flyout.
func usageFaceCard(c *ui.Context, key string) ui.Element {
	tokens := designTokens(c.Theme().Dark)
	sp := Spacing()
	return ui.Column(c.Key(key)).FillWidth().Radius(Radius().Card).Background(tokens.Panel).Border(1, tokens.BorderSubtle).
		Padding(sp.L, sp.L).Gap(sp.XXS)
}

// usageSkeleton holds the place of the first reading: one window's name,
// figure and meter as quiet blocks on a face card, so the card does not jump
// when it lands.
func usageSkeleton(c *ui.Context) {
	tokens := designTokens(c.Theme().Dark)
	sp := Spacing()
	block := func(w, h float32) {
		ui.Box(c).Width(w).Height(h).Radius(h / 2).Background(tokens.PanelHover)
	}
	usageFaceCard(c, "usage-skeleton").Gap(sp.M).Children(func() {
		block(72, 10)
		block(96, 20)
		ui.Box(c).FillWidth().Height(usageMeterHeight).Radius(usageMeterRadius(usageMeterHeight)).Background(tokens.PanelHover)
	})
}

// usageProblem is the note a card shows when it has no fresh reading — the
// reason and the one action that fixes it.
func (s *Shell) usageProblem(c *ui.Context, id usage.ID, res usage.Result, now time.Time) {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	typ := Typography()
	sp := Spacing()

	message := res.Detail
	if message == "" {
		message = "No reading yet."
	}
	if id == usage.Zhipu && res.State == usage.StateSetup {
		message = "Add your API key to see these limits."
	}
	if res.Has && !res.Snapshot.At.IsZero() {
		message = strings.TrimRight(message, ".") + ". Showing the last values from " + usageAgo(now.Sub(res.Snapshot.At)) + "."
	}
	color := t.TextMuted
	if res.State == usage.StateSetup || res.State == usage.StateExpired || res.State == usage.StateUnavailable {
		color = tokens.StatusColor(ToneAttention, t.Dark)
	}
	if res.State == usage.StateIdle && !s.usage.svc.Busy(id) {
		message = "Waiting for the first reading."
	}
	if res.State == usage.StateIdle && s.usage.svc.Busy(id) {
		return
	}
	ui.Column(c).FillWidth().Gap(sp.S).Children(func() {
		ui.Text(c, message).FontSize(typ.BodySmall).TextColor(color)
		if res.State == usage.StateCustomEndpoint {
			return
		}
		ui.Row(c).Gap(sp.S).Children(func() {
			switch {
			case id == usage.Antigravity && (res.State == usage.StateUnavailable || res.State == usage.StateSetup || res.State == usage.StateExpired):
				if !s.settings.Usage.SignInAllowed[string(id)] {
					if compactPrimaryButton(c, "Allow Sign-In…").Clicked() {
						s.usage.consentOpen, s.usage.consentFor = true, id
					}
				}
				if compactButton(c, "Open Antigravity").Clicked() {
					s.openUsageApp(id)
				}
				if compactButton(c, "Retry Now").Clicked() {
					s.refreshUsageIDs([]usage.ID{id}, true)
				}
			case (res.State == usage.StateSetup || res.State == usage.StateExpired) && usageHasToken(id):
				label := "Set Up…"
				if res.Has {
					label = "Update Token…"
				}
				if compactButton(c, label).Clicked() {
					s.router.Push(usageSettingsPath)
				}
			default:
				if compactButton(c, "Retry Now").Clicked() {
					s.refreshUsageIDs([]usage.ID{id}, true)
				}
			}
		})
	})
}

// usageLook is how one window reads right now: bar colors, the filled share,
// the pace tick and the pace verdict.
type usageLook struct {
	fill, value ui.Color
	share, tick float64
	pace        usage.PaceState
	runsOutIn   time.Duration
}

func usageLookOf(c *ui.Context, w usage.Window, left, stale bool, now time.Time) usageLook {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	look := usageLook{fill: usageMeterFill(t.Dark), value: t.Text, tick: -1}
	if tone := usageTone(w.Level()); tone != ToneNeutral {
		look.fill = tokens.StatusColor(tone, t.Dark)
		look.value = look.fill
	}
	if stale {
		look.fill = t.TextMuted.Alpha(0.6)
	}
	look.share = w.UsedPct
	if left {
		look.share = 100 - w.UsedPct
	}
	look.share = math.Max(0, math.Min(100, look.share))
	if !stale {
		if elapsed, ok := w.Elapsed(now); ok && elapsed >= 0.15 {
			look.tick = elapsed * 100
			if left {
				look.tick = 100 - look.tick
			}
		}
		look.pace, look.runsOutIn = w.Pace(now)
	}
	return look
}

// usageBarKey names one bar for usageBar: the provider, the bar's kind and
// the window it shows.
func usageBarKey(id usage.ID, kind string, w usage.Window) string {
	return string(id) + "|" + kind + "|" + w.Group + "|" + w.Name
}

// usageBar draws the track, the fill and (when the window has run long
// enough to tell) the pace tick. The fill grows from empty the first time the
// bar is drawn and eases to each new reading after that.
func (s *Shell) usageBar(c *ui.Context, key string, look usageLook, height float32) {
	t := c.Theme()
	box := ui.Box(c).FillWidth().Height(height).Shrink(0)
	if !s.usage.barSeen[key] {
		if s.usage.barSeen == nil {
			s.usage.barSeen = map[string]bool{}
		}
		s.usage.barSeen[key] = true
		_ = box.Animate("fill", 0, motionPanel) // prime at empty: the value starts at its first target
	}
	share := box.Animate("fill", float32(look.share/100), motionPanel)
	radius := usageMeterRadius(height)
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, usageMeterTrack(t.Dark), radius)
		if width := share * r.W; width > 0 {
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: max(width, min(height, radius*2)), H: r.H}, look.fill, radius)
		}
		if look.tick >= 0 {
			x := r.X + float32(look.tick/100)*r.W
			if height >= usageMeterHeight {
				// The meter's even-rate tick: a full-height 2pt mark.
				p.Fill(ui.Rect{X: min(max(x-1, r.X), r.X+r.W-2), Y: r.Y, W: 2, H: r.H}, t.Text, 0)
			} else {
				p.Line(x, r.Y-2, x, r.Y+r.H+2, 1.5, t.Text.Alpha(0.55))
			}
		}
	})
}

// usageWindows lays a provider's windows out as the phone does: one raised
// card per window, the major windows first and the minor (sub-limit) ones
// after them. A model group (Antigravity: a title and a dozen sub-limits) is
// the exception — its rows share one card, since a card each would bury the
// page.
func (s *Shell) usageWindows(c *ui.Context, id usage.ID, windows []usage.Window, left, stale bool, now time.Time) {
	typ := Typography()
	sp := Spacing()
	ordered := make([]usage.Window, 0, len(windows))
	for _, w := range windows {
		if !w.Minor || w.Group != "" {
			ordered = append(ordered, w)
		}
	}
	for _, w := range windows {
		if w.Minor && w.Group == "" {
			ordered = append(ordered, w)
		}
	}
	for i := 0; i < len(ordered); i++ {
		w := ordered[i]
		if w.Group == "" {
			s.usageFace(c, id, w, left, stale, now)
			continue
		}
		j := i
		for j < len(ordered) && ordered[j].Group == w.Group {
			j++
		}
		block := ordered[i:j]
		i = j - 1
		usageFaceCard(c, "usage-group:"+string(id)+"|"+w.Group).Gap(sp.S).Children(func() {
			ui.Text(c, w.Group).FontSize(typ.Section).FontWeight(WeightMedium).SingleLine()
			for _, gw := range block {
				s.usageCompact(c, id, gw, left, stale, now, usageNameWide)
			}
		})
	}
}

// usageVerdict is the phone's pace verdict (right of the window's name) and
// the one line under the number, derived from the same Pace the desktop has
// always used. Stale readings give no verdict: their pace is not live.
func usageVerdict(w usage.Window, look usageLook, stale bool, now time.Time) (text string, tone StatusTone, line string) {
	if stale {
		return "", ToneNeutral, "Last reading"
	}
	switch look.pace {
	case usage.PaceRunsOut:
		return "Runs out in " + usageIn(look.runsOutIn), ToneAttention, "Burning faster than the window allows"
	case usage.PaceExceeded:
		return "Allowance spent", ToneError, "Nothing left in this window"
	case usage.PaceOn:
		if elapsed, ok := w.Elapsed(now); ok && elapsed > 0 {
			return "Under pace", ToneNeutral, fmt.Sprintf("+%d%% headroom at this rate", max(1, int(math.Round(100-w.UsedPct/elapsed))))
		}
		return "Under pace", ToneNeutral, "On pace at this rate"
	}
	switch {
	case w.UsedPct <= 0:
		return "Under pace", ToneNeutral, "No usage yet in this window"
	case w.UsedPct < 5:
		return "Under pace", ToneNeutral, "Barely touched"
	}
	return "", ToneNeutral, "Too early to tell the pace"
}

// usageFace is one window on its card (the phone's `.ucard`): the name and the
// pace verdict, the share in the display size, the pace line, the meter with
// its even-rate tick, and a foot with what is used and when it resets. The
// Used | Left toggle still picks which reading is the big one.
func (s *Shell) usageFace(c *ui.Context, id usage.ID, w usage.Window, left, stale bool, now time.Time) {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	typ := Typography()
	sp := Spacing()
	look := usageLookOf(c, w, left, stale, now)
	verdict, vtone, line := usageVerdict(w, look, stale, now)
	vcolor := t.TextMuted
	if vtone != ToneNeutral {
		vcolor = tokens.StatusColor(vtone, t.Dark)
	}
	caption := "used"
	if left {
		caption = "left"
	}
	// The foot's left side says the other reading (or the counted amount).
	foot := usageShownText(w.UsedPct, !left)
	switch {
	case w.Counted():
		unit := w.Unit
		if unit != "" {
			unit = " " + unit
		}
		foot = fmt.Sprintf("%s of %s%s", strconv.FormatFloat(math.Round(w.Used), 'f', 0, 64), strconv.FormatFloat(math.Round(w.Max), 'f', 0, 64), unit)
	case w.Amount != "":
		foot = w.Amount
	}
	usageFaceCard(c, "usage-window:"+usageBarKey(id, "face", w)).Children(func() {
		ui.Row(c).FillWidth().Gap(sp.L).AlignItems(ui.Center).Children(func() {
			ui.Text(c, w.Name).FontSize(typ.Section).FontWeight(WeightMedium).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
			if verdict != "" {
				ui.Text(c, verdict).FontSize(typ.BodySmall).TextColor(vcolor).SingleLine().Shrink(0)
			}
		})
		ui.Row(c).Gap(sp.S).AlignItems(ui.End).Children(func() {
			ui.Text(c, strconv.Itoa(usageShown(w.UsedPct, left))+"%").FontSize(typ.Heading).FontWeight(WeightBold).TextColor(look.value).SingleLine()
			ui.Text(c, caption).FontSize(typ.BodySmall).TextColor(t.TextMuted).SingleLine()
		})
		ui.Text(c, line).FontSize(typ.Caption).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0)
		ui.Column(c).FillWidth().Padding(sp.M, 0, 0, 0).Children(func() {
			s.usageBar(c, usageBarKey(id, "face", w), look, usageMeterHeight)
		})
		ui.Row(c).FillWidth().Gap(sp.L).AlignItems(ui.Center).Padding(sp.S, 0, 0, 0).Children(func() {
			ui.Text(c, foot).FontSize(typ.Caption).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
			if !w.Resets.IsZero() {
				if d := w.Resets.Sub(now); d > 0 {
					ui.Row(c).Shrink(0).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "Resets in ").FontSize(typ.Caption).TextColor(t.TextMuted).SingleLine()
						ui.Text(c, usageIn(d)).FontSize(typ.Caption).FontWeight(WeightMedium).SingleLine()
					})
				}
			}
		})
	})
}

// usageMeterRadius is the meter's corner: a pill for the thin compact bars,
// a squarer block (the phone's `radii.block`) once it is tall enough to read
// as a gauge.
func usageMeterRadius(height float32) float32 {
	if height >= usageMeterHeight {
		return Radius().Control
	}
	return height / 2
}

// usageMeterTrack and usageMeterFill are the gauge's two neutrals (the
// phone's `meterTrack` / `meterFill`, 2026-11-07): the fill stays neutral
// while a window is under its warning level and turns amber from 80 % and red
// from 95 %.
func usageMeterTrack(dark bool) ui.Color {
	if dark {
		return ui.Hex("#262626")
	}
	return ui.Hex("#e6e6e9")
}

func usageMeterFill(dark bool) ui.Color {
	if dark {
		return ui.Hex("#4a4a4a")
	}
	return ui.Hex("#a9a9b0")
}

// usageNotes are the facts that have no bar (reset credits, extra usage,
// unlimited) as quiet chips.
func (s *Shell) usageNotes(c *ui.Context, notes []string) {
	if len(notes) == 0 {
		return
	}
	t := c.Theme()
	tokens := designTokens(t.Dark)
	typ := Typography()
	sp := Spacing()
	ui.Row(c).FillWidth().Gap(sp.XS).Wrap().AlignItems(ui.Center).Children(func() {
		for _, note := range notes {
			ui.Row(c).Radius(Radius().Row).Padding(sp.XXS, sp.S).Background(tokens.PanelHover).Shrink(0).Children(func() {
				ui.Text(c, note).FontSize(typ.Caption).TextColor(t.TextMuted).SingleLine()
			})
		}
	})
}

// usageCompact is a sub-limit on one line: name, a thin bar, share and reset.
func (s *Shell) usageCompact(c *ui.Context, id usage.ID, w usage.Window, left, stale bool, now time.Time, nameWidth float32) {
	typ := Typography()
	sp := Spacing()
	look := usageLookOf(c, w, left, stale, now)
	detail := usageShownText(w.UsedPct, left)
	if !w.Resets.IsZero() {
		if d := w.Resets.Sub(now); d > 0 {
			detail += " · " + usageIn(d)
		}
	}
	if w.Amount != "" && w.Group == "" {
		detail = w.Amount
	}
	ui.Row(c).FillWidth().Gap(sp.M).AlignItems(ui.Center).Children(func() {
		ui.Text(c, w.Name).FontSize(typ.BodySmall).SingleLine().Ellipsis("…").Width(nameWidth).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() { s.usageBar(c, usageBarKey(id, "compact", w), look, usageCompactBar) })
		ui.Text(c, detail).FontSize(typ.Caption).TextColor(look.value).SingleLine().Width(usageCompactDetail).Shrink(0).
			TextAlign(ui.End)
	})
}

// usageActivity is the 30-day token strip of the agents whose session files
// Solco already indexes (Claude Code, Codex). It is recomputed from those
// files, never stored.
func (s *Shell) usageActivity(c *ui.Context, id usage.ID) {
	days := s.usage.activity[id]
	total := history.SumDays(days)
	if len(days) == 0 || total == 0 {
		return
	}
	t := c.Theme()
	typ := Typography()
	sp := Spacing()
	peak := history.Busiest(days)
	today := days[len(days)-1].Tokens
	ui.Column(c).FillWidth().Gap(sp.XS).Children(func() {
		ui.Row(c).FillWidth().Gap(sp.M).AlignItems(ui.Center).Children(func() {
			ui.Text(c, fmt.Sprintf("Last %d days · %s tokens", len(days), usageTokens(total))).
				FontSize(typ.Caption).TextColor(t.TextMuted).SingleLine().Grow(1)
			ui.Text(c, "today "+usageTokens(today)).FontSize(typ.Caption).TextColor(t.TextMuted).SingleLine()
		})
		ui.Box(c).FillWidth().Height(usageSparkHeight).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
			n := float32(len(days))
			slot := r.W / n
			gap := float32(2)
			barW := max(slot-gap, 1)
			for i, d := range days {
				h := float32(2)
				if peak > 0 && d.Tokens > 0 {
					h = max(2, float32(float64(d.Tokens)/float64(peak))*r.H)
				}
				color := t.Accent.Alpha(0.45)
				if i == len(days)-1 {
					color = t.Accent
				}
				if d.Tokens == 0 {
					color = designTokens(t.Dark).PanelHover
				}
				p.Fill(ui.Rect{X: r.X + float32(i)*slot, Y: r.Y + r.H - h, W: barW, H: h}, color, 1)
			}
		})
	})
}

// usageConsentDialog is the "may Solco read this sign-in?" confirmation.
func (s *Shell) usageConsentDialog(c *ui.Context) {
	st := s.usage
	if st == nil || !st.consentOpen {
		return
	}
	title, message := usageConsentText(st.consentFor)
	// Allow is index 0 and Cancel last: AlertDialog focuses the last button,
	// so Enter dismisses safely.
	if ui.AlertDialog(c, &st.consentOpen, title, message, "Allow", "Cancel") == 0 {
		s.usageConsentAllowed()
	}
}

func usageConsentText(id usage.ID) (title, message string) {
	switch id {
	case usage.Claude:
		return "Allow Solco to read Claude Code's sign-in?",
			"Solco reads the sign-in Claude Code keeps in your Keychain, only to ask Anthropic for your usage; it goes nowhere else. Solco keeps a copy in its own encrypted store, so later reads no longer touch Claude Code's item — macOS asks only when that copy is missing or no longer accepted. macOS will now ask you to confirm (Allow Once or Always Allow)."
	case usage.Cursor:
		return "Allow Solco to read Cursor's sign-in?",
			"Solco reads the sign-in Cursor Agent keeps on this Mac, only to ask cursor.com for your usage; it goes nowhere else. Solco keeps a copy in its own encrypted store, so later reads no longer touch Cursor Agent's item — macOS asks only when that copy is missing or no longer accepted. macOS may ask you to confirm now."
	case usage.Copilot:
		return "Allow Solco to use your GitHub CLI sign-in?",
			"Solco asks the gh command for your GitHub token, only to ask api.github.com for your Copilot usage. It is never stored or sent anywhere else."
	case usage.Antigravity:
		return "Allow Solco to use Antigravity's saved sign-in?",
			"Solco reads the Google sign-in Antigravity saved on this Mac (a file in ~/.gemini, or its Keychain item) and uses it only to ask Google's cloudcode-pa.googleapis.com for your quota, so limits show even while Antigravity is closed. A refreshed token stays in memory. Google does not offer this as a supported API for other apps; using it is at your own risk, and your account could be restricted."
	}
	return "Allow Solco to read this sign-in?", ""
}
