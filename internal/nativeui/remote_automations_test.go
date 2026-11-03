package nativeui

import (
	"context"
	"testing"

	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/automations"
)

/**
 * [INPUT]: remote_automations.go、automations_inbox_test.go 的 stubInbox/inboxFixture
 * [OUTPUT]: 手机 Automations 快照回归——门关闭时无快照、列表落地后可读、已读即时翻转 unread、线格式不带提示词/理由
 * [POS]: remote_automations.go 的行为锁；Host 线程读到的永远是桌面当前列表的映射
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func TestPhoneSnapshotFollowsTheGateAndTheList(t *testing.T) {
	s := NewShell()
	if _, ok := s.hostAutomations(context.Background()); ok {
		t.Fatal("a snapshot exists before the plugin gate opened")
	}
	_, s = stubInbox(t)
	out, ok := s.hostAutomations(context.Background())
	if !ok || len(out.Automations) == 0 {
		t.Fatalf("snapshot after the first read: %v %+v", ok, out)
	}
	unread := 0
	for _, a := range out.Automations {
		if a.Unread {
			unread++
		}
	}
	if unread != 2 {
		t.Errorf("unread = %d, want the failed and the finished run", unread)
	}

	s.markAllAutomationsSeen()
	out, _ = s.hostAutomations(context.Background())
	for _, a := range out.Automations {
		if a.Unread {
			t.Errorf("%s still unread after Mark All Seen", a.Name)
		}
	}

	s.applyAutomationsAvailability(automations.Availability{}) // the plugin went away
	if _, ok := s.hostAutomations(context.Background()); ok {
		t.Error("the snapshot outlived a closed gate")
	}
}

func TestPhoneWireMapsStatesTimesAndCarriesNoPrompt(t *testing.T) {
	list := mustParseList(t, inboxFixture(false))
	out := remoteAutomationsOf(list, nil)
	states := map[string]string{}
	for _, a := range out.Automations {
		states[a.Name] = a.State
		if a.LastRun != nil && a.LastRun.AtMs == 0 && a.LastRun.ID != "" {
			t.Errorf("%s: run time lost", a.Name)
		}
	}
	if states["draft-sweep"] != "draft" || states["nightly-ci-triage"] != "active" {
		t.Errorf("states = %v", states)
	}
}

// The form's agent choices carry the id the plugin takes, not Solco's own: a
// phone that saved "claude-code" would be refused by Herdr.
func TestPhoneAgentChoicesUseThePluginsKindIds(t *testing.T) {
	_, s := stubInbox(t)
	s.launchKinds = []app.SessionKind{{ID: app.TerminalKind, Label: "Terminal"}, {ID: "claude-code", Label: "Claude Code"}}
	got := s.remoteAutomationAgents()
	if len(got) != 1 || got[0].ID != "claude" || got[0].Label != "Claude Code" {
		t.Fatalf("agents %+v", got)
	}
	s.syncRemoteAutomations()
	if list, _ := s.hostAutomations(context.Background()); len(list.Agents) != 1 {
		t.Fatalf("the list carries %+v", list.Agents)
	}
}
