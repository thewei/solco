package nativeui

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/usage"
)

// TestQuickPanelQuotaTabShowsMiniCards pins the 2026-10-30 Quota tab: the
// strip carries the tab, opening it reads the switched-on providers once (no
// timer), and each provider is one mini card with its tightest window.
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
	if !containsPrefix(tester.Texts(), "5-hour · resets ") {
		t.Errorf("no window · reset line; texts=%q", tester.Texts())
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

func TestHeaderTallyNeverPassesItsDenominator(t *testing.T) {
	if w, total := headerTally(1, 1, 4, 0); w != 2 || total != 4 {
		t.Fatalf("tally = %d/%d, want 2/4", w, total)
	}
	// Two waiting tasks with no agent of their own count on both sides.
	if w, total := headerTally(2, 0, 3, 2); w != 2 || total != 5 {
		t.Fatalf("tally with tasks = %d/%d, want 2/5", w, total)
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
