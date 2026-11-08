package nativeui

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/thewei/solco/internal/automations"
	"github.com/thewei/solco/internal/remotehost"
)

/**
 * [INPUT]: remote_automations_admin.go、automations_inbox_test.go 的 stubInbox/inboxFixture、page_automations_test.go 的 stubAutomationsClient
 * [OUTPUT]: 手机管理缝的行为回归——Open 随门、详情含提示词/理由与运行（未读只来自未看的 done/failed）、未知名字 404、每个动作走对插件命令并答新列表、插件拒绝原文透传（有界）而超时只给稳定码、起调度器经注入的 daemon starter
 * [POS]: remote_automations_admin.go 的行为锁；插件 CLI 由测试注入，绝不读真实插件
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const adminHistoryFixture = `{"result":{"runs":[
 {"run_id":"r-2","name":"nightly-ci-triage","trigger":"schedule","status":"failed","started_at":"2026-11-03T03:00:00+08:00","duration_seconds":31,"workspace_id":"w_1","pane_id":"p_9","agent":"claude","error":"SECRET trust prompt text"},
 {"run_id":"r-1","name":"nightly-ci-triage","trigger":"manual","status":"done","seen":true,"started_at":"2026-11-02T03:00:00+08:00","duration_seconds":412}]}}`

func TestAdminOpensAndClosesWithTheGate(t *testing.T) {
	s := NewShell()
	admin := s.hostAutomationsAdmin()
	if admin.Open() {
		t.Fatal("management is open before the plugin gate opened")
	}
	_, s = stubInbox(t)
	admin = s.hostAutomationsAdmin()
	if !admin.Open() {
		t.Fatal("management stays closed with the gate open")
	}
	s.applyAutomationsAvailability(automations.Availability{})
	if admin.Open() {
		t.Fatal("management outlived the gate")
	}
	if _, err := admin.Do(context.Background(), "x", remotehost.OpRunAutomation); err == nil {
		t.Fatal("a call went through a closed gate")
	}
}

func TestAdminDetailCarriesTheFullFormAndRunsWithoutErrorText(t *testing.T) {
	calls := stubAutomationsClient(t, func(args []string) (string, error) {
		if args[0] == "history" {
			return adminHistoryFixture, nil
		}
		return inboxFixture(false), nil
	})
	s := openAutomationsShell(t, calls)
	d, err := s.hostAutomationsAdmin().Detail(context.Background(), "nightly-ci-triage")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "nightly-ci-triage" || d.Prompt == "" || d.Cron == "" || len(d.Runs) != 2 {
		t.Fatalf("detail %+v", d)
	}
	if !d.Runs[0].Unread || d.Runs[0].PaneID != "p_9" || d.Runs[1].Unread {
		t.Errorf("runs %+v", d.Runs)
	}
	raw := strings.ToLower(strings.Join([]string{d.Runs[0].ID, d.Runs[0].Status, d.Runs[0].Trigger}, " "))
	if strings.Contains(raw, "secret") {
		t.Error("a run's error text reached the wire")
	}

	_, err = s.hostAutomationsAdmin().Detail(context.Background(), "ghost")
	var ae *remotehost.AutomationError
	if !errors.As(err, &ae) || ae.Code != "unknown_automation" || ae.Status != http.StatusNotFound {
		t.Fatalf("unknown name: %v", err)
	}
}

func TestAdminVerbsRunTheRightPluginCommandAndAnswerTheFreshList(t *testing.T) {
	calls, s := stubInbox(t)
	admin := s.hostAutomationsAdmin()
	for op, want := range map[remotehost.AutomationOp]string{
		remotehost.OpApproveAutomation: "approve recap",
		remotehost.OpRunAutomation:     "run recap --bg",
		remotehost.OpPauseAutomation:   "pause recap",
		remotehost.OpResumeAutomation:  "resume recap",
		remotehost.OpDeleteAutomation:  "rm recap",
	} {
		*calls = nil
		out, err := admin.Do(context.Background(), "recap", op)
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		if len(*calls) == 0 || (*calls)[0] != want {
			t.Errorf("%s ran %v, want %q first", op, *calls, want)
		}
		if len(out.Automations) == 0 || out.DraftPrompt == "" {
			t.Errorf("%s answered no fresh list: %+v", op, out)
		}
	}
	*calls = nil
	if _, err := admin.MarkSeen(context.Background(), "r-9", false); err != nil || (*calls)[0] != "seen r-9" {
		t.Fatalf("seen: %v %v", err, *calls)
	}
	*calls = nil
	if _, err := admin.MarkSeen(context.Background(), "", true); err != nil || (*calls)[0] != "seen --all" {
		t.Fatalf("seen all: %v %v", err, *calls)
	}
}

func TestAdminCreateIsAlwaysADraftAndRefusalsKeepThePluginsWords(t *testing.T) {
	calls, s := stubInbox(t)
	admin := s.hostAutomationsAdmin()
	f := remotehost.AutomationFields{Cron: "0 9 * * 1", Repo: "/r", Agent: "claude", Prompt: "do it"}
	if _, err := admin.Create(context.Background(), "weekly", f); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*calls, "\n")
	if !strings.Contains(joined, "add --name weekly --draft") {
		t.Fatalf("create did not draft: %s", joined)
	}

	stubAutomationsClient(t, func(args []string) (string, error) {
		return "", errors.New("automations: add: bad cron\nsecond line")
	})
	_, err := admin.Create(context.Background(), "weekly", f)
	var ae *remotehost.AutomationError
	if !errors.As(err, &ae) || ae.Code != "automation_refused" || ae.Status != http.StatusConflict || ae.Detail != "add: bad cron" {
		t.Fatalf("refusal %+v (%v)", ae, err)
	}
	if _, err := admin.Create(context.Background(), "", f); err == nil {
		t.Fatal("an empty name was accepted")
	}

	stubAutomationsClient(t, func(args []string) (string, error) { return "", context.DeadlineExceeded })
	_, err = admin.Do(context.Background(), "recap", remotehost.OpRunAutomation)
	if !errors.As(err, &ae) || ae.Code != "automation_failed" || ae.Detail != "" {
		t.Fatalf("a timeout must be a bare stable code: %+v", ae)
	}
}

func TestAdminStartSchedulerUsesTheDaemonStarterAndAnswersTheList(t *testing.T) {
	calls, s := stubInbox(t)
	*calls = nil
	out, err := s.hostAutomationsAdmin().StartScheduler(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) == 0 || !strings.HasPrefix((*calls)[0], "daemon ") {
		t.Fatalf("calls %v", *calls)
	}
	if len(out.Automations) == 0 {
		t.Fatal("no fresh list")
	}
}
