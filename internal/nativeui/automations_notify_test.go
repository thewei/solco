package nativeui

import (
	"testing"

	"github.com/thewei/solco/internal/automations"
)

/**
 * [INPUT]: automations_notify.go、automations_inbox_test.go 的 stubInbox
 * [OUTPUT]: 自动化通知回归——通知文案只含名称与步骤、ID 往返、点击回到抽屉、列表变化走 Watcher 而不阻塞无窗口的构建
 * [POS]: automations_notify.go 的行为锁；文案是否泄露提示词/错误、点击是否落到对的抽屉在这里失败
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func TestAutomationNotificationWordsTheStepAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		kind        automations.EventKind
		title, body string
	}{
		{automations.EventDraft, "Draft needs approval", "recap"},
		{automations.EventStarted, "recap", "Started"},
		{automations.EventDone, "recap", "Finished"},
		{automations.EventFailed, "recap", "Failed"},
	} {
		title, body := automationNotificationText(automations.Event{Name: "recap", Kind: tc.kind})
		if title != tc.title || body != tc.body {
			t.Errorf("%s: got %q / %q, want %q / %q", tc.kind, title, body, tc.title, tc.body)
		}
	}
}

func TestAutomationNotificationIDRoundTripsAndFailsClosed(t *testing.T) {
	if name, ok := parseAutomationNotificationID(automationNotificationID("nightly ci/triage")); !ok || name != "nightly ci/triage" {
		t.Errorf("round trip = %q %v", name, ok)
	}
	for _, id := range []string{"", "solco-automation/", "solco-agent/i/t", "other"} {
		if _, ok := parseAutomationNotificationID(id); ok {
			t.Errorf("%q parsed as an automation id", id)
		}
	}
}

func TestClickingANotificationOpensTheAutomationsDrawer(t *testing.T) {
	_, s := stubInbox(t)
	s.routeAgentNotification(automationNotificationID("recap"))
	if s.router.Path() != routeAutomations {
		t.Fatalf("route = %q, want the Automations page", s.router.Path())
	}
	if s.automations.list.selected != "recap" {
		t.Errorf("drawer = %q, want recap", s.automations.list.selected)
	}
	// A vanished automation still lands on the page, with no drawer.
	s.automations.list.selected = ""
	s.routeAgentNotification(automationNotificationID("gone"))
	if s.automations.list.selected != "" {
		t.Errorf("drawer = %q for a missing automation", s.automations.list.selected)
	}
}

func TestListChangesFeedTheWatcherAndNeverBlockAWindowlessShell(t *testing.T) {
	_, s := stubInbox(t) // the first read primed the watcher
	list := mustParseList(t, inboxFixture(false))
	for i := range list.Automations {
		if a := &list.Automations[i]; a.Name == "recap" {
			a.LastRun = &automations.LastRun{RunID: "r-10", Status: "running"}
		}
	}
	s.applyAutomationsList(list, nil) // would hang on MyGo's main-thread hop if it tried a notification
	if got := s.automations.watcher.Observe(list.Automations); got != nil {
		t.Errorf("the change was not consumed by applyAutomationsList: %v", got)
	}
	// Closing the gate makes the next read a first observation again.
	s.applyAutomationsAvailability(automations.Availability{})
	s.applyAutomationsList(list, nil)
	if got := s.automations.watcher.Observe(list.Automations); got != nil {
		t.Errorf("a closed gate kept the ledger: %v", got)
	}
}
