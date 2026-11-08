package nativeui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/automations"
	"github.com/thewei/solco/internal/herdr"
)

/**
 * [INPUT]: 官方 MyGo headless Tester、automations 的 stub 客户端（page_automations_test.go）
 * [OUTPUT]: 自动化 inbox 回归——未读 run 的分类与 Header 计数、Header 浮层 Automations 页签（门关闭时无痕）、打开抽屉即已读、Mark All Seen
 * [POS]: automations_inbox.go 的行为锁；插件门是否打开、seen 谁写、计数是否虚高都在这里失败
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// inboxFixture is the plugin's answer with one finished unread run added
// beside the failed one; readRuns marks them seen the way `seen` would.
func inboxFixture(readRuns bool) string {
	fixture := strings.Replace(automationsListFixture, `"automations":[`,
		`"automations":[{"name":"recap","draft":false,"disabled":false,"cron":"0 9 * * *","repo":"/r","agent":"claude",
		  "last_run":{"run_id":"r-9","status":"done","started_at":"2026-11-03T09:00:00+08:00"}},`, 1)
	if readRuns {
		fixture = strings.ReplaceAll(fixture, `"status":"done","started_at"`, `"status":"done","seen":true,"started_at"`)
		fixture = strings.ReplaceAll(fixture, `"status":"failed","started_at"`, `"status":"failed","seen":true,"started_at"`)
	}
	return fixture
}

// stubInbox answers like a plugin whose `seen` works: after one, every run
// reads as opened.
func stubInbox(t *testing.T) (*[]string, *Shell) {
	t.Helper()
	read := false
	calls := stubAutomationsClient(t, func(args []string) (string, error) {
		if args[0] == "seen" {
			read = true
		}
		return inboxFixture(read), nil
	})
	s := openAutomationsShell(t, calls)
	s.applyAutomationsList(mustParseList(t, inboxFixture(false)), nil)
	*calls = nil
	return calls, s
}

func TestUnreadRunsAreSplitIntoFailedAndFinished(t *testing.T) {
	_, s := stubInbox(t)

	failed, finished := s.automationsUnread()
	if len(failed) != 1 || failed[0].Name != "nightly-ci-triage" {
		t.Errorf("failed = %+v, want the failed unread run", failed)
	}
	if len(finished) != 1 || finished[0].Name != "recap" {
		t.Errorf("finished = %+v, want the finished unread run", finished)
	}
	if a, r := s.automationsHeaderCounts(); a != 1 || r != 1 {
		t.Errorf("header counts = %d/%d, want 1 attention, 1 review", a, r)
	}
}

func TestSeenRunsDraftsAndRunningRunsAreNotNews(t *testing.T) {
	_, s := stubInbox(t)
	list := mustParseList(t, inboxFixture(false))
	for i := range list.Automations {
		a := &list.Automations[i]
		switch a.Name {
		case "nightly-ci-triage":
			a.LastRun.Seen = true
		case "recap":
			a.LastRun.Status = "running"
		case "draft-sweep":
			a.LastRun = &automations.LastRun{RunID: "d", Status: "failed"} // a draft's stale run
		}
	}
	s.applyAutomationsList(list, nil)
	if a, r := s.automationsHeaderCounts(); a != 0 || r != 0 {
		t.Errorf("header counts = %d/%d, want nothing", a, r)
	}
}

func TestClosedGateLeavesNoTraceInTheHeaderOrThePanel(t *testing.T) {
	_, s := stubInbox(t)
	s.applyAutomationsAvailability(automations.Availability{})
	if a, r := s.automationsHeaderCounts(); a != 0 || r != 0 {
		t.Errorf("closed gate counts = %d/%d, want 0", a, r)
	}
	for _, item := range s.quickPanelTabItems() {
		if item.id == quickPanelAutomationsTab {
			t.Error("the Automations tab must not exist while the gate is closed")
		}
	}
	s.quickPanelTab = quickPanelAutomationsTab
	if got := s.quickPanelTabIndex(); got != quickPanelAgentsTab {
		t.Errorf("tab = %d, want it to fall back to Agents", got)
	}
}

func TestPanelTabListsUnreadRunsAndOpensTheDrawer(t *testing.T) {
	calls, s := stubInbox(t)
	found := false
	for _, item := range s.quickPanelTabItems() {
		found = found || item.id == quickPanelAutomationsTab
	}
	if !found {
		t.Fatal("the Automations tab should be offered while the gate is open")
	}
	s.quickPanelTab = quickPanelAutomationsTab

	tester := ui.NewTester(s.QuickPanelView, QuickPanelWidth, QuickPanelHeight)
	tester.Frame()
	tester.Frame()
	texts := tester.Texts()
	for _, want := range []string{"Automations", "Failed", "Finished", "nightly-ci-triage", "recap", "Mark All Seen"} {
		if !hasText(texts, want) {
			t.Errorf("panel is missing %q; texts=%q", want, texts)
		}
	}
	*calls = nil
	tester.Click("recap")
	tester.Frame()
	if s.automations.list.selected != "recap" {
		t.Errorf("selected = %q, want the row's drawer opened", s.automations.list.selected)
	}
}

func TestOpeningTheDrawerMarksTheRunSeenOnce(t *testing.T) {
	calls, s := stubInbox(t)

	s.openAutomationsDrawer("recap")
	got := strings.Join(*calls, "|")
	if !strings.Contains(got, "seen r-9") {
		t.Fatalf("calls = %q, want the plugin told the run was opened", got)
	}
	if _, r := s.automationsHeaderCounts(); r != 0 {
		t.Errorf("review = %d, want the opened run gone at once", r)
	}
	*calls = nil
	s.openAutomationsDrawer("recap")
	if strings.Contains(strings.Join(*calls, "|"), "seen") {
		t.Error("an already-seen run must not be marked again")
	}
}

func TestMarkAllSeenClearsTheInbox(t *testing.T) {
	calls, s := stubInbox(t)
	s.markAllAutomationsSeen()
	if !strings.Contains(strings.Join(*calls, "|"), "seen --all") {
		t.Fatalf("calls = %q, want seen --all", *calls)
	}
	if a, r := s.automationsHeaderCounts(); a != 0 || r != 0 {
		t.Errorf("counts = %d/%d, want an empty inbox", a, r)
	}
}

// TestOneWaitingNumberOnTheTrayAndTheDock pins the round-3 rule (2026-11-07):
// the menu-bar title and the Dock badge show the same number — status-center
// attention + review, the Assistants' "needs you" inbox and the unopened
// Automation runs (the Header's old inputs) — and nothing when nothing waits.
// A working agent never counts.
func TestOneWaitingNumberOnTheTrayAndTheDock(t *testing.T) {
	_, s := stubInbox(t) // 1 failed + 1 finished unread run
	trayPlatform := &fakeTrayPlatform{}
	dockPlatform := &fakeDockPlatform{}
	s.tray = newStatusTray(trayPlatform)
	s.AttachDock(dockPlatform)
	s.assistants.ready = true
	s.assistants.overview.NeedsYou = 2
	s.projection = herdr.Projection{Agents: []herdr.Agent{
		{TerminalID: "t1", PaneID: "p1", Status: "blocked"}, // attention
		{TerminalID: "t2", PaneID: "p2", Status: "working"}, // never counts
	}}
	s.reconcileWorkbench()

	s.republishWaiting()
	// 1 attention + 2 needs-you + 1 failed run + 1 finished run.
	want := "5"
	if got := trayPlatform.titles[len(trayPlatform.titles)-1]; got != want {
		t.Errorf("tray title = %q, want %q", got, want)
	}
	if got := dockPlatform.badges[len(dockPlatform.badges)-1]; got != want {
		t.Errorf("dock badge = %q, want %q", got, want)
	}

	// Opening every run and answering the Assistants leaves only the Agent.
	s.markAllAutomationsSeen()
	s.assistants.overview.NeedsYou = 0
	s.republishWaiting()
	if got := trayPlatform.titles[len(trayPlatform.titles)-1]; got != "1" {
		t.Errorf("tray title after the inbox cleared = %q, want 1", got)
	}
	if got := dockPlatform.badges[len(dockPlatform.badges)-1]; got != "1" {
		t.Errorf("dock badge after the inbox cleared = %q, want 1", got)
	}
}
