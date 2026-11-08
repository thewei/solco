package nativeui

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/herdr"
)

// TestQuickPanelHeightFor pins the content-driven height: the panel grows
// with its group, row and usage-caption counts up to the fixed cap, sizes to
// the empty state when there are no rows, and never exceeds the cap (the
// list scrolls inside beyond it). The Agents tab's chip row rides on the
// shared list budget; the Tasks tab reuses the budget without the chips.
func TestQuickPanelHeightFor(t *testing.T) {
	empty := quickPanelListHeight(0, 0, 0) + quickPanelChipsHeight
	if got := QuickPanelHeightFor(0, 0, 0); got != empty {
		t.Fatalf("empty height = %d, want %d", got, empty)
	}
	if got := QuickPanelHeightFor(1, 1, 0); got != quickPanelListHeight(1, 1, 0)+quickPanelChipsHeight {
		t.Fatalf("one-row height = %d", got)
	}
	five := quickPanelHeaderHeight + quickPanelChipsHeight + 2*quickPanelGroupHeight +
		5*quickPanelRowHeight + 2*quickPanelUsageHeight
	if got := QuickPanelHeightFor(5, 2, 2); got != five {
		t.Fatalf("five-row height = %d, want %d", got, five)
	}
	if got := QuickPanelHeightFor(50, 4, 50); got != QuickPanelHeight {
		t.Fatalf("capped height = %d, want %d", got, QuickPanelHeight)
	}
	if !(QuickPanelHeightFor(5, 1, 0) < QuickPanelHeightFor(5, 3, 0)) {
		t.Fatal("height must grow with the group count")
	}
	if !(QuickPanelHeightFor(4, 2, 0) < QuickPanelHeightFor(6, 2, 0)) {
		t.Fatal("height must grow with the row count")
	}
	if !(QuickPanelHeightFor(5, 2, 0) < QuickPanelHeightFor(5, 2, 3)) {
		t.Fatal("height must grow with the usage-caption count")
	}
	if got := quickPanelListHeight(2, 1, 0); got != max(quickPanelHeaderHeight+quickPanelGroupHeight+2*quickPanelRowHeight, quickPanelMinHeight) {
		t.Fatalf("tasks list height = %d, want header + one group + two rows (min-clamped)", got)
	}
}

// TestTrayToolTipSharesTheAttentionSegments pins the one summary source the
// tray tooltip builds on (§17.4): wording per surface, quiet reads Ready,
// disconnection wins.
func TestTrayToolTipSharesTheAttentionSegments(t *testing.T) {
	counts := TrayCounts{Attention: 2, Review: 1, Working: 3}
	if got := TrayToolTip(counts, true); got != "Solco — 2 need attention, 1 for review, 3 working" {
		t.Fatalf("tray tooltip = %q", got)
	}
}

// quickPanelShell builds a shell whose projection carries a working, a
// blocked and an idle agent — the full list the panel's tab group filters.
func quickPanelShell(t *testing.T) *Shell {
	t.Helper()
	shell := NewShell()
	shell.activeInstance = "inst-1"
	shell.projection = herdr.Projection{
		Agents: []herdr.Agent{
			{TerminalID: "term-1", PaneID: "p1", Status: "working", Name: "Scout"},
			{TerminalID: "term-2", PaneID: "p2", Status: "blocked", Name: "Ranger"},
			{TerminalID: "term-3", PaneID: "p3", Status: "idle", Name: "Sleeper"},
		},
	}
	shell.reconcileWorkbench()
	return shell
}

// TestQuickPanelViewGroupsAgentsByState pins the panel contract (the chrome
// is Agents-first since 2026-10-31): the tab's name leads, the other tabs
// are trailing icons, filter chips carry the group labels, and the sidebar's
// compact agent cards keep the group order — no complex per-row action
// buttons, no status words in the rows themselves.
func TestQuickPanelViewGroupsAgentsByState(t *testing.T) {
	shell := quickPanelShell(t)
	tester := ui.NewTester(shell.QuickPanelView, QuickPanelWidth, QuickPanelHeight)
	for _, want := range []string{"Agents", "Services", "Needs you", "Working", "Idle", "Scout", "Ranger", "Sleeper"} {
		if !tester.HasText(want) {
			t.Fatalf("panel missing %q; texts=%q", want, tester.Texts())
		}
	}
	for _, gone := range []string{"Agents & Services", "Open Terminal", "Open Agent", "Open Chat", "Mark Reviewed", "agents"} {
		if tester.HasText(gone) {
			t.Fatalf("panel still shows %q; texts=%q", gone, tester.Texts())
		}
	}
	if tester.HasText("No active agents") {
		t.Fatalf("panel shows the empty state despite live agents; texts=%q", tester.Texts())
	}
	// The chips lead (they carry the labels first in tree order), the rows
	// follow in group order — the fixture has one row per group.
	y := func(text string) float32 {
		rect, ok := tester.Find(text)
		if !ok {
			t.Fatalf("%q not found; texts=%q", text, tester.Texts())
		}
		return rect.Y
	}
	if !(y("Needs you") < y("Ranger") && y("Ranger") < y("Scout") && y("Scout") < y("Sleeper")) {
		t.Fatalf("rows are not grouped Needs you › Working › Idle; texts=%q", tester.Texts())
	}
}

// TestQuickPanelChipsFilterTheList pins the filter chips: a chip narrows the
// list to its group, clicking it again lists every group.
func TestQuickPanelChipsFilterTheList(t *testing.T) {
	shell := quickPanelShell(t)
	tester := ui.NewTester(shell.QuickPanelView, QuickPanelWidth, QuickPanelHeight)
	if err := tester.Click("Working 1"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 2)
	if !tester.HasText("Scout") || tester.HasText("Ranger") || tester.HasText("Sleeper") {
		t.Fatalf("the Working chip did not narrow the list: %q", tester.Texts())
	}
	if shell.workbench.filterAll || shell.workbench.filter != quickPanelWorkingGroup {
		t.Fatalf("chip state = all=%v group=%d", shell.workbench.filterAll, shell.workbench.filter)
	}
	if err := tester.Click("Working 1"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 2)
	if !tester.HasText("Ranger") || !tester.HasText("Sleeper") {
		t.Fatalf("re-clicking the chip did not restore the list: %q", tester.Texts())
	}
	if !shell.workbench.filterAll {
		t.Fatal("the re-clicked chip must clear the filter")
	}
}

// TestQuickPanelHeightHoldsTheContent pins the height budget against the real
// layout: at the content-driven height every row is on screen (nothing
// scrolls away) and the panel is not left mostly empty below the last row —
// with and without the usage caption under each card.
func TestQuickPanelHeightHoldsTheContent(t *testing.T) {
	shell := quickPanelShell(t)
	fits := func(label, last string) {
		t.Helper()
		height := shell.quickPanelContentHeight()
		tester := ui.NewTester(shell.QuickPanelView, QuickPanelWidth, height)
		rect, ok := tester.Find(last)
		if !ok {
			t.Fatalf("%s: last line %q missing; texts=%q", label, last, tester.Texts())
		}
		bottom := int(rect.Y + rect.H)
		if bottom > height {
			t.Errorf("%s: last line ends at %d, below the %d-high panel", label, bottom, height)
		} else if height-bottom > 20 { // the list's own bottom padding plus the caption's are structural
			t.Errorf("%s: last line ends at %d in a %d-high panel: %d of blank slack", label, bottom, height, height-bottom)
		}
	}
	fits("plain cards", "Sleeper")

	shell.usageByAgent = map[agent.AgentKey]agent.AgentUsageSnapshot{}
	for _, card := range shell.workbenchCards() {
		shell.usageByAgent[card.Key] = agent.AgentUsageSnapshot{Model: card.Title + "-model"}
	}
	fits("cards with a usage caption", "Sleeper-model")
}

// TestQuickPanelRowClickGoesStraightToThePanel pins the one-layer panel: a
// row click selects the Agent's pane, shows its Terminal from whatever page
// the panel was opened on, and swaps nothing inside the panel — the old
// detail layer (← All Agents, Open Terminal, Context) is gone.
func TestQuickPanelRowClickGoesStraightToThePanel(t *testing.T) {
	shell := quickPanelShell(t)
	shell.router.Replace("/history")
	tester := ui.NewTester(shell.QuickPanelView, QuickPanelWidth, QuickPanelHeight)

	if err := tester.Click("Ranger"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if shell.router.Path() != routeWorkspace || shell.selectedPaneID != "p2" ||
		shell.surface.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("a blocked Agent's row landed on %q/%v pane %q, want its Terminal at /workspace pane p2",
			shell.router.Path(), shell.surface.current(), shell.selectedPaneID)
	}
	for _, gone := range []string{"← All Agents", "Open Terminal", "Context", "Details"} {
		if tester.HasText(gone) {
			t.Fatalf("the panel grew a second layer (%q); texts=%q", gone, tester.Texts())
		}
	}

	// From another Workspace surface the click still lands on the Terminal.
	shell = quickPanelShell(t)
	shell.router.Replace(routeWorkspace)
	shell.surface.openChat(shell.workspaceContext()) // another pane's Chat is up
	if shell.surface.current() != WorkspaceSurfaceChat {
		t.Fatalf("fixture: surface = %v, want Chat", shell.surface.current())
	}
	tester = ui.NewTester(shell.QuickPanelView, QuickPanelWidth, QuickPanelHeight)
	if err := tester.Click("Ranger"); err != nil {
		t.Fatal(err)
	}
	if shell.surface.current() != WorkspaceSurfaceTerminal || shell.selectedPaneID != "p2" {
		t.Fatalf("a row click from Chat landed on %v pane %q, want the Terminal on p2", shell.surface.current(), shell.selectedPaneID)
	}
}

// TestQuickPanelRowClickFollowsTheRecommendedPanel pins where a click lands
// when it is not the Terminal: the Chat for an Agent waiting on an
// interaction, and the surface a pane was last used on.
func TestQuickPanelRowClickFollowsTheRecommendedPanel(t *testing.T) {
	// A pending interaction sends the click to Chat (the answer goes there).
	shell := quickPanelShell(t)
	shell.interactionBroker.Publish(conversation.PendingInteraction{
		ID: "i1", ConversationID: "live:codex:s1", AgentPaneID: "p1",
		Kind: conversation.InteractionPermission, Prompt: "Allow workspace write?", Options: []string{"Approve", "Deny"},
	})
	tester := ui.NewTester(shell.QuickPanelView, QuickPanelWidth, QuickPanelHeight)
	if err := tester.Click("Scout"); err != nil {
		t.Fatal(err)
	}
	if shell.chatPaneID != "p1" || shell.selectedPaneID != "p1" {
		t.Fatalf("an interaction row opened pane %q / chat %q, want Chat on p1", shell.selectedPaneID, shell.chatPaneID)
	}

	// A working Agent whose pane was last left in Chat reopens in Chat.
	shell = quickPanelShell(t)
	shell.rememberPaneSurface("p1", WorkspaceSurfaceChat)
	tester = ui.NewTester(shell.QuickPanelView, QuickPanelWidth, QuickPanelHeight)
	if err := tester.Click("Scout"); err != nil {
		t.Fatal(err)
	}
	if shell.chatPaneID != "p1" {
		t.Fatalf("a pane left in Chat opened chat %q, want p1", shell.chatPaneID)
	}

	// Anything else is the Terminal, and the pane is remembered as such.
	shell = quickPanelShell(t)
	tester = ui.NewTester(shell.QuickPanelView, QuickPanelWidth, QuickPanelHeight)
	if err := tester.Click("Sleeper"); err != nil {
		t.Fatal(err)
	}
	if shell.chatPaneID != "" || shell.selectedPaneID != "p3" || shell.paneSurfaceMemo["p3"] != WorkspaceSurfaceTerminal {
		t.Fatalf("an idle row opened pane %q / chat %q / memo %v, want Terminal on p3",
			shell.selectedPaneID, shell.chatPaneID, shell.paneSurfaceMemo["p3"])
	}
}

// TestQuickPanelRowMenuHoldsTheOtherActions pins what moved off the detail
// layer: the other panel, the inspector and — only for an Agent waiting on
// review — the review mark, in the row's right-click menu.
func TestQuickPanelRowMenuHoldsTheOtherActions(t *testing.T) {
	shell := quickPanelShell(t)
	tester := ui.NewTester(shell.QuickPanelView, QuickPanelWidth, QuickPanelHeight)
	if err := tester.RightClick("Ranger"); err != nil {
		t.Fatal(err)
	}
	menu := tester.Menu()
	for _, want := range []string{"Open Terminal", "Open Chat", "Open Inspector"} {
		if !containsText(menu, want) {
			t.Fatalf("row menu %q missing %q", menu, want)
		}
	}
	if containsText(menu, "Mark Reviewed") {
		t.Fatalf("row menu %q offers a review mark with nothing to review", menu)
	}
	// The item is named after the page it opens, the title bar's "Agent Inspector".
	if err := tester.ChooseMenuItem("Open Inspector"); err != nil {
		t.Fatal(err)
	}
	if got := shell.router.Path(); got != "/inspector/p2" {
		t.Fatalf("Open Inspector landed on %q, want /inspector/p2", got)
	}

	// Review pending: the mark joins the menu, and the row carries the same
	// action as a trailing icon that marks without navigating.
	shell.projection.Agents[0].Status = "done"
	shell.reconcileWorkbench()
	shell.router.Replace("/history")
	tester = ui.NewTester(shell.QuickPanelView, QuickPanelWidth, QuickPanelHeight)
	if !tester.HasText("Needs you") {
		t.Fatalf("a finished Agent is not under Needs you; texts=%q", tester.Texts())
	}
	if err := tester.RightClick("Scout"); err != nil {
		t.Fatal(err)
	}
	if menu = tester.Menu(); !containsText(menu, "Mark Reviewed") {
		t.Fatalf("row menu %q lacks the review mark", menu)
	}
	tester.CloseMenu()
	if err := tester.Click("Mark Reviewed"); err != nil {
		t.Fatal(err)
	}
	for _, card := range shell.workbenchCards() {
		if card.Title == "Scout" && card.ReviewPending {
			t.Fatal("the trailing mark did not clear the review")
		}
	}
	if shell.router.Path() != "/history" {
		t.Fatalf("marking reviewed navigated to %q", shell.router.Path())
	}
}

// TestQuickPanelViewEmptyWithoutAgents pins the empty state: only a panel
// with no agents at all shows it.
func TestQuickPanelViewEmptyWithoutAgents(t *testing.T) {
	shell := NewShell()
	shell.activeInstance = "inst-1"
	shell.reconcileWorkbench()
	tester := ui.NewTester(shell.QuickPanelView, QuickPanelWidth, QuickPanelHeight)
	if !tester.HasText("No active agents") {
		t.Fatalf("empty panel missing the empty state; texts=%q", tester.Texts())
	}
}

// TestQuickPanelToggleWithoutPanel pins the §17.1 adapter rule: without a
// bound tray the tray click is a safe no-op — the deleted in-window Status Center is no
// longer a fallback.
func TestQuickPanelToggleWithoutPanel(t *testing.T) {
	shell := quickPanelShell(t)
	shell.router.Replace("/history")
	shell.ToggleQuickPanel()
	if shell.router.Path() != "/history" {
		t.Fatalf("fallback route = %q, want no navigation", shell.router.Path())
	}

	// hideQuickPanel is nil-safe without an open panel.
	bare := NewShell()
	bare.hideQuickPanel()
}

// TestQuickPanelToggleCloses pins the §17.1 anchor toggle against the
// pass-through click: on macOS the outside press that dismissed the flyout
// goes on to the element under it, so a click landing within the dismiss
// grace must still close (stay closed) rather than re-open.
func TestQuickPanelToggleCloses(t *testing.T) {
	now := time.Now()
	if !quickPanelToggleCloses(&mygo.Window{}, time.Time{}, now) {
		t.Fatal("open panel must toggle off")
	}
	if quickPanelToggleCloses(nil, time.Time{}, now) {
		t.Fatal("no open panel with no recent dismissal must toggle on")
	}
	if !quickPanelToggleCloses(nil, now.Add(-quickPanelDismissGrace/2), now) {
		t.Fatal("a click inside the dismiss grace must toggle off")
	}
	if quickPanelToggleCloses(nil, now.Add(-2*quickPanelDismissGrace), now) {
		t.Fatal("a click after the dismiss grace must toggle on")
	}
}

// TestFilterStatusEntries keeps a direct pin on the chip projection:
// snapshot order survives, the chosen chip group narrows it, and "Needs
// you" spans both the attention and review buckets.
func TestFilterStatusEntries(t *testing.T) {
	entry := func(name string, bucket agent.AgentDirectoryBucket) StatusCenterEntry {
		return StatusCenterEntry{Card: agent.AgentCardModel{Title: name}, Bucket: bucket}
	}
	shell := quickPanelShell(t)
	snapshot := shell.BuildStatusCenterSnapshot()
	if got := len(shell.filterStatusEntries(snapshot)); got != 3 {
		t.Fatalf("all entries = %d, want 3", got)
	}
	snapshot.Entries = append(snapshot.Entries, entry("done-1", agent.BucketReadyForReview))
	shell.workbench.filterAll, shell.workbench.filter = false, quickPanelNeedsYouGroup
	entries := shell.filterStatusEntries(snapshot)
	if len(entries) != 2 || entries[0].Card.Title != "Ranger" || entries[1].Card.Title != "done-1" {
		t.Fatalf("needs-you entries = %+v", entries)
	}
	shell.workbench.filter = quickPanelIdleGroup
	entries = shell.filterStatusEntries(snapshot)
	if len(entries) != 1 || entries[0].Card.Title != "Sleeper" {
		t.Fatalf("idle entries = %+v", entries)
	}
	shell.workbench.filterAll = true
	if got := len(shell.filterStatusEntries(snapshot)); got != 4 {
		t.Fatalf("all entries = %d, want 4", got)
	}
}
