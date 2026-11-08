package nativeui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/usage"
)

// TestQuickPanelQuotaTabShowsMiniCards pins the Quota tab: the strip carries
// the tab, opening it reads the switched-on providers once (no timer), and
// each provider is the page's own card, whole.
func TestQuickPanelQuotaTabShowsMiniCards(t *testing.T) {
	codex := &fakeUsageFetcher{id: usage.Codex, snap: codexSnapshot()}
	s, _ := usageTestShell(t, codex)
	s.setUsageEnabled(usage.Codex, true)
	before := codex.calls.Load()

	tester := ui.NewTester(s.QuickPanelView, QuickPanelWidth, QuickPanelHeight)
	settleFrames(tester, 2)
	if !tester.HasText("Quota") {
		t.Fatalf("popover strip lacks the Quota tab; texts=%q", tester.Texts())
	}
	if err := tester.Click("Quota tab"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 3)
	if s.quickPanelTab != quickPanelQuotaTab {
		t.Fatalf("tab = %d, want Quota", s.quickPanelTab)
	}
	for _, want := range []string{"Codex", "Plus", "62%", "Open Quota"} {
		if !tester.HasText(want) {
			t.Errorf("Quota tab lacks %q; texts=%q", want, tester.Texts())
		}
	}
	if !tester.HasText("5-hour") || !tester.HasText("used") {
		t.Errorf("no window row; texts=%q", tester.Texts())
	}
	if !tester.HasText("Used") || !tester.HasText("Left") {
		t.Errorf("the tab lacks the Used | Left reading; texts=%q", tester.Texts())
	}
	// Opening within the TTL is a cache hit: at most the one read the first
	// open of the page made.
	if got := codex.calls.Load() - before; got > 1 {
		t.Fatalf("opening the tab read %d times, want at most 1", got)
	}
}

func TestQuotaHeadlinePrefersCountedAllowance(t *testing.T) {
	snap := usage.Snapshot{Windows: []usage.Window{
		{Name: "Weekly", UsedPct: 80},
		{Name: "Included", UsedPct: 25, Used: 124, Max: 500, Unit: "req"},
	}}
	if w, ok := quotaHeadline(snap); !ok || w.Name != "Included" {
		t.Fatalf("headline = %+v ok=%v, want the counted allowance", w, ok)
	}
	snap = usage.Snapshot{Windows: []usage.Window{{Name: "5-hour", UsedPct: 30}, {Name: "Weekly", UsedPct: 80}}}
	if w, ok := quotaHeadline(snap); !ok || w.Name != "Weekly" {
		t.Fatalf("headline = %+v ok=%v, want the tightest window", w, ok)
	}
}

func TestMigrateRouteRenamesUsageToQuota(t *testing.T) {
	for in, want := range map[string]string{
		"/usage":          routeQuota,
		"/settings/usage": usageSettingsPath,
		"/files":          "/files",
		"":                "",
	} {
		if got := migrateRoute(in); got != want {
			t.Fatalf("migrateRoute(%q) = %q, want %q", in, got, want)
		}
	}
}

// The flyout and the page draw the same card from the same reading: every
// window of a provider appears on both, with the same figure.
func TestQuotaFlyoutAndPageShowTheSameWindows(t *testing.T) {
	snap := usage.Snapshot{Plan: "Max 5×", Windows: []usage.Window{
		{Name: "5-hour", UsedPct: 62, Span: 5 * time.Hour, Resets: time.Now().Add(2 * time.Hour)},
		{Name: "Weekly", UsedPct: 41, Span: 7 * 24 * time.Hour, Resets: time.Now().Add(72 * time.Hour)},
		{Name: "Weekly · Sonnet", UsedPct: 18, Span: 7 * 24 * time.Hour, Minor: true},
	}}
	f := &fakeUsageFetcher{id: usage.Claude, snap: snap}
	s, _ := usageTestShell(t, f)
	s.setUsageEnabled(usage.Claude, true)
	s.quickPanelTab = quickPanelQuotaTab

	fly := ui.NewTester(s.QuickPanelView, QuickPanelWidth, QuickPanelHeight)
	page := ui.NewTester(s.usagePage, 1000, 900)
	settleFrames(fly, 3)
	settleFrames(page, 3)
	for _, want := range []string{"Claude Code", "Max 5×", "5-hour", "Weekly", "Weekly · Sonnet"} {
		if !fly.HasText(want) || !page.HasText(want) {
			t.Errorf("%q must be on both surfaces (flyout %v, page %v)", want, fly.HasText(want), page.HasText(want))
		}
	}
	// The pace footer (and the source line) were the data the compact card
	// dropped; the popover must carry whatever the page prints under a bar.
	for _, want := range []string{"Claude Code's sign-in · api.anthropic.com"} {
		if !fly.HasText(want) || !page.HasText(want) {
			t.Errorf("%q must be on both surfaces (flyout %v, page %v)", want, fly.HasText(want), page.HasText(want))
		}
	}
	if !containsPrefix(fly.Texts(), "Resets in") || !containsPrefix(page.Texts(), "Resets in") {
		t.Errorf("the reset/pace footer must be on both surfaces; flyout=%q", fly.Texts())
	}
	for _, figure := range []string{"62%", "41%", "18%"} {
		if !fly.HasText(figure) || !page.HasText(figure) {
			t.Errorf("figure %q differs between flyout and page", figure)
		}
	}
}

// Used | Left is one control in the title bar's tool cluster, not a part of
// the page body, and it is not drawn while nothing is switched on.
func TestUsageReadingToggleLivesInTheTitleBar(t *testing.T) {
	codex := &fakeUsageFetcher{id: usage.Codex, snap: codexSnapshot()}
	s, _ := usageTestShell(t, codex)
	s.setUsageEnabled(usage.Codex, true)
	s.router.Replace(routeQuota)
	spec, ok := s.routeHeader()
	if !ok || spec.tools == nil {
		t.Fatalf("/quota header = %+v, want a tools slot for Used | Left", spec)
	}
	page := ui.NewTester(s.usagePage, 1000, 800)
	settleFrames(page, 3)
	if page.HasText("Used") || page.HasText("Left") {
		t.Errorf("the page body draws the Used | Left switch; texts=%q", page.Texts())
	}
	bar := ui.NewTester(s.View, 1200, 800)
	settleFrames(bar, 3)
	if !bar.HasText("Used") || !bar.HasText("Left") {
		t.Fatalf("the title bar lacks Used | Left; texts=%q", bar.Texts())
	}
	if err := bar.Click("Left"); err != nil {
		t.Fatal(err)
	}
	if !s.usageShowLeft() {
		t.Fatal("clicking Left in the title bar did not switch the reading")
	}
}

// "Needs a look" lists a window about to run out and a provider that needs
// setup, and nothing for a healthy one.
func TestUsageAttentionListsWhatWillBite(t *testing.T) {
	hot := usage.Snapshot{Windows: []usage.Window{{Name: "5-hour", UsedPct: 95, Span: 5 * time.Hour, Resets: time.Now().Add(time.Hour)}}}
	calm := usage.Snapshot{Windows: []usage.Window{{Name: "5-hour", UsedPct: 5, Span: 5 * time.Hour, Resets: time.Now().Add(4 * time.Hour)}}}
	ag := &fakeUsageFetcher{id: usage.Antigravity, snap: hot}
	cx := &fakeUsageFetcher{id: usage.Codex, snap: calm}
	zh := &fakeUsageFetcher{id: usage.Zhipu, err: usage.ErrSetup}
	s, _ := usageTestShell(t, ag, cx, zh)
	ids := []usage.ID{usage.Antigravity, usage.Codex, usage.Zhipu}
	s.usage.svc.Refresh(context.Background(), ids, true, nil)

	items := s.usageAttention(ids, time.Now(), false)
	var joined []string
	for _, it := range items {
		joined = append(joined, it.text)
	}
	text := strings.Join(joined, "\n")
	if !strings.Contains(text, "Antigravity") || !strings.Contains(text, "95% used") {
		t.Errorf("a window at its limit is missing:\n%s", text)
	}
	if !strings.Contains(text, "Zhipu GLM — needs setup") {
		t.Errorf("a provider needing setup is missing:\n%s", text)
	}
	if strings.Contains(text, "Codex") {
		t.Errorf("a healthy provider is listed:\n%s", text)
	}
}

// TestQuickPanelTabContentFadesIn: switching tabs fades the new tab's body in
// instead of snapping it.
func TestQuickPanelTabContentFadesIn(t *testing.T) {
	codex := &fakeUsageFetcher{id: usage.Codex, snap: codexSnapshot()}
	s, _ := usageTestShell(t, codex)
	s.setUsageEnabled(usage.Codex, true)
	tester := ui.NewTester(s.QuickPanelView, QuickPanelWidth, QuickPanelHeight)
	settleFrames(tester, 2)
	tester.Advance(2 * motionRow)
	tester.Frame()
	if err := tester.Click("Quota tab"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	early := append([]byte(nil), tester.Image().Pix...)
	tester.Advance(2 * motionRow)
	tester.Frame()
	settled := tester.Image().Pix
	changed := 0
	body := 4 * QuickPanelWidth * 160 // skip the header: its tab colours ease too
	for i := body; i+3 < len(early); i += 4 {
		if early[i] != settled[i] || early[i+1] != settled[i+1] || early[i+2] != settled[i+2] {
			changed++
		}
	}
	if changed < 200 {
		t.Fatalf("the Quota body was already fully drawn on its first frame (%d px changed)", changed)
	}
}
