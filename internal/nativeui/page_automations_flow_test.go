package nativeui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/automations"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 stubAutomationsClient/openAutomationsShell（page_automations_test.go）、page_automations.go 的表单/模板/调度器入口、automations_ui.go 的轮询与历史重读
 * [OUTPUT]: 真机巡检（2026-11-03）暴露的缺口回归：无调度器横幅与 Start Scheduler、New 表单交给插件的完整 add 参数（--name/--workspace/必填项）、Edit 走 put、运行失败原文、抽屉历史随末次运行变化重读、运行中轮询的开关、模板只读
 * [POS]: Automations 页流程测试；与 page_automations_test.go 同属一套 fake exec 缝
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// withScheduler inserts the daemon fields the plugin prints into the fixture.
func withScheduler(pid, state string) string {
	return strings.Replace(automationsListFixture, `{"result":{"paused":false,`,
		`{"result":{"paused":false,"scheduler":`+pid+`,"state":"`+state+`",`, 1)
}

func TestAutomationsWithoutASchedulerOffersToStartIt(t *testing.T) {
	calls := stubAutomationsClient(t, func([]string) (string, error) { return withScheduler("0", "/state"), nil })
	s := openAutomationsShell(t, calls)
	s.applyAutomationsList(mustParseList(t, withScheduler("0", "/state")), nil)

	tester := ui.NewTester(s.automationsPage, 1100, 800)
	tester.Frame()
	if !hasText(tester.Texts(), "The scheduler is not running") || !hasText(tester.Texts(), "Start Scheduler") {
		t.Fatalf("texts = %q, want the banner with its action", tester.Texts())
	}
	*calls = nil
	tester.Click("Start Scheduler")
	tester.Frame()
	got := strings.Join(*calls, "|")
	if !strings.Contains(got, "daemon /state/daemon.log") {
		t.Fatalf("calls = %q, want the daemon started with its log beside the plugin state", got)
	}
	if !strings.Contains(got, "list --json") {
		t.Fatalf("calls = %q, want the list re-read so the banner can clear", got)
	}
}

func TestSchedulerBannerStaysAwayWhenItIsNotTrue(t *testing.T) {
	calls := stubAutomationsClient(t, func([]string) (string, error) { return automationsListFixture, nil })
	s := openAutomationsShell(t, calls)

	// A running daemon.
	s.applyAutomationsList(mustParseList(t, withScheduler("4242", "/state")), nil)
	if s.automations.list.schedulerMissing() {
		t.Error("a daemon with a pid is running; no banner")
	}
	// Nothing waiting on a schedule: a daemon would have nothing to do.
	idle := mustParseList(t, withScheduler("0", "/s"))
	idle.Automations[0].Disabled = true
	s.applyAutomationsList(idle, nil)
	if s.automations.list.schedulerMissing() {
		t.Error("only drafts and paused automations: nothing should be firing, no banner")
	}
	// A failed read says nothing about the daemon.
	s.applyAutomationsList(mustParseList(t, withScheduler("0", "/s")), nil)
	s.automations.list.failed = true
	if s.automations.list.schedulerMissing() {
		t.Error("a failed read must not claim the scheduler is down")
	}
}

func claudeKinds() []app.SessionKind {
	// Solco's own ids: Claude Code is "claude-code" here and "claude" to Herdr.
	return []app.SessionKind{{ID: app.TerminalKind, Label: "Terminal"},
		{ID: string(history.AgentClaudeCode), Label: "Claude Code"},
		{ID: string(history.AgentCodex), Label: "Codex"},
		{ID: string(history.AgentAntigravity), Label: "Antigravity CLI"}}
}

// The New form hands the plugin everything its CLI would otherwise ask for.
func TestNewDraftSubmitsWhatTheCLIRequires(t *testing.T) {
	added := false
	calls := stubAutomationsClient(t, func(args []string) (string, error) {
		if args[0] == "add" {
			added = true
		}
		if added { // the plugin now lists the new draft
			return strings.Replace(automationsListFixture, `"automations":[`,
				`"automations":[{"name":"recap","draft":true,"cron":"0 9 * * *","repo":"/work/repo","agent":"codex","prompt":"summarize"},`, 1), nil
		}
		return automationsListFixture, nil
	})
	s := openAutomationsShell(t, calls)
	s.launchKinds = claudeKinds()

	s.openAutomationsNew()
	d := &s.automations.list.newDraft
	if d.agent != "Claude Code" {
		t.Errorf("default agent = %q, want the first launchable one", d.agent)
	}
	d.name, d.repo, d.prompt = "recap", "", "summarize"
	if s.automationsDraftReady(d) {
		t.Fatal("a draft without a repository cannot run and must not be submittable")
	}
	d.repo, d.agent, d.model, d.mode, d.headless = " /work/repo ", "Codex", "gpt-x", 1, 1
	if !s.automationsDraftReady(d) {
		t.Fatal("the form is complete")
	}
	*calls = nil
	s.submitAutomationsDraft(d, "automations-add")

	want := "add --name recap --draft --cron 0 9 * * * --repo /work/repo --workspace root --agent codex " +
		"--model gpt-x --prompt summarize --on-overlap skip --headless"
	if got := strings.Join(*calls, "|"); !strings.HasPrefix(got, want) {
		t.Fatalf("calls = %q, want it to start with %q", got, want)
	}
	if s.automations.list.selected != "recap" {
		t.Errorf("selected = %q, the new draft should open in the drawer", s.automations.list.selected)
	}
}

func TestEditKeepsTheNameAndSavesThroughPut(t *testing.T) {
	calls := stubAutomationsClient(t, func(args []string) (string, error) {
		if args[0] == "put" {
			return `{"next":[]}`, nil
		}
		return automationsListFixture, nil
	})
	s := openAutomationsShell(t, calls)
	s.launchKinds = claudeKinds()

	s.openAutomationsEdit("nightly-ci-triage")
	d := &s.automations.list.edit
	if d.original != "nightly-ci-triage" || d.agent != "Claude Code" || d.mode != 0 {
		t.Fatalf("edit draft = %+v, want the saved automation (agent shown by label)", *d)
	}
	d.prompt = "triage harder"
	*calls = nil
	s.submitAutomationsDraft(d, "automations-put")
	got := strings.Join(*calls, "|")
	if !strings.Contains(got, "put --original nightly-ci-triage") {
		t.Fatalf("calls = %q, want put against the original name", got)
	}
	if s.pendingToast != "Saved." {
		t.Fatalf("toast = %q", s.pendingToast)
	}
}

func TestTemplatesSeedAReadOnlyDraft(t *testing.T) {
	calls := stubAutomationsClient(t, func([]string) (string, error) { return automationsListFixture, nil })
	s := openAutomationsShell(t, calls)
	for _, tpl := range automationTemplates {
		if !strings.Contains(tpl.prompt, "Do not change any files") {
			t.Errorf("template %q must start read-only: %q", tpl.name, tpl.prompt)
		}
		if presetCron(tpl.preset) == "" {
			t.Errorf("template %q names a schedule preset that does not exist: %q", tpl.name, tpl.preset)
		}
	}
	s.openAutomationsTemplate(automationTemplates[0])
	d := s.automations.list.newDraft
	if !d.open || d.name != "ci-triage" || d.cronSel != "Weekdays 09:00" || d.rationale == "" {
		t.Fatalf("template draft = %+v", d)
	}
}

func TestFailedRunsShowThePluginsOwnReason(t *testing.T) {
	failed := automations.Run{Status: "failed", Error: "  Claude Code is asking\n whether to trust this folder "}
	if got := automationRunProblem(failed); got != "Claude Code is asking whether to trust this folder" {
		t.Fatalf("problem = %q", got)
	}
	if got := automationRunProblem(automations.Run{Status: "done", Error: "ignored"}); got != "" {
		t.Fatalf("a run that went well has no problem line, got %q", got)
	}
	long := automationRunProblem(automations.Run{Status: "failed", Error: strings.Repeat("x", 500)})
	if r := []rune(long); len(r) != 281 {
		t.Fatalf("problem has %d runes, want it bounded at 280 + ellipsis", len(r))
	}
}

// The drawer used to keep the history it read when it opened.
func TestDrawerHistoryRereadsWhenTheLastRunChanges(t *testing.T) {
	calls := stubAutomationsClient(t, func(args []string) (string, error) {
		if args[0] == "history" {
			return `{"result":{"runs":[]}}`, nil
		}
		return automationsListFixture, nil
	})
	s := openAutomationsShell(t, calls)
	s.openAutomationsDrawer("nightly-ci-triage")
	*calls = nil

	// Same list again: nothing changed, nothing re-read.
	s.applyAutomationsList(mustParseList(t, automationsListFixture), nil)
	if got := strings.Join(*calls, "|"); strings.Contains(got, "history") {
		t.Fatalf("calls = %q, an unchanged list must not re-read history", got)
	}

	// The last run moved from failed r-1 to a running r-2.
	moved := strings.Replace(automationsListFixture, `"run_id":"r-1","status":"failed"`, `"run_id":"r-2","status":"running"`, 1)
	s.applyAutomationsList(mustParseList(t, moved), nil)
	if got := strings.Join(*calls, "|"); !strings.Contains(got, "history nightly-ci-triage --json") {
		t.Fatalf("calls = %q, want the drawer's history re-read", got)
	}
}

// While a run is going and the page is open, the list is re-read on a timer;
// otherwise it is not.
func TestPollingFollowsAnActiveRunOnlyWhilePageIsOpen(t *testing.T) {
	calls := stubAutomationsClient(t, func([]string) (string, error) { return automationsListFixture, nil })
	s := openAutomationsShell(t, calls)
	running := mustParseList(t, strings.Replace(automationsListFixture, `"status":"failed"`, `"status":"running"`, 1))

	var armed []func()
	automationsGo = func(f func()) { armed = append(armed, f) }
	automationsPollGap = time.Millisecond

	s.router.Push(routeAutomations)
	s.applyAutomationsList(running, nil)
	if !s.automations.list.pollPending || len(armed) != 1 {
		t.Fatalf("pollPending=%v armed=%d, want one poll armed for the active run", s.automations.list.pollPending, len(armed))
	}
	// A second apply while armed does not double the timer.
	s.applyAutomationsList(running, nil)
	if len(armed) != 1 {
		t.Fatalf("armed=%d, want the single timer to stand", len(armed))
	}

	// Off the page, no timer.
	s2 := openAutomationsShell(t, calls)
	automationsGo = func(f func()) { armed = append(armed, f) }
	armed = nil
	s2.router.Push(routeSettings)
	s2.applyAutomationsList(running, nil)
	if len(armed) != 0 {
		t.Fatalf("armed=%d, a run must not keep the list polling from another page", len(armed))
	}
}

// The sheet shipped with .Label only — an accessibility name, not a caption —
// so the user saw a bare dropdown and an empty box. Every field is captioned.
func TestNewSheetCaptionsEveryField(t *testing.T) {
	calls := stubAutomationsClient(t, func([]string) (string, error) { return automationsListFixture, nil })
	s := openAutomationsShell(t, calls)
	s.launchKinds = claudeKinds()
	s.openAutomationsNew()
	s.automations.list.newDraft.advanced = true

	tester := ui.NewTester(s.automationsDialogs, 900, 1000)
	tester.Frame()
	for _, caption := range []string{"Name", "Schedule", "Repository", "Each run starts in", "Agent", "Model", "Prompt",
		"Rationale", "On overlap", "Timeout (min)", "Catch-up (min)", "Cleanup (hours)"} {
		if !hasText(tester.Texts(), caption) {
			t.Errorf("sheet is missing the caption %q; texts = %q", caption, tester.Texts())
		}
	}
}

// The plugin takes Herdr's agent kind, not Solco's agent id. The first real
// New Draft saved "claude-code", which `herdr agent start --kind` refuses.
func TestAgentChoiceTravelsAsTheHerdrKind(t *testing.T) {
	kinds := claudeKinds()
	for label, want := range map[string]string{"Claude Code": "claude", "Codex": "codex", "Antigravity CLI": "agy", "something-typed": "something-typed"} {
		if got := automationAgentID(kinds, label); got != want {
			t.Errorf("automationAgentID(%q) = %q, want %q", label, got, want)
		}
	}
	for kind, want := range map[string]string{"claude": "Claude Code", "agy": "Antigravity CLI", "unknown": "unknown"} {
		if got := automationAgentLabel(kinds, kind); got != want {
			t.Errorf("automationAgentLabel(%q) = %q, want %q", kind, got, want)
		}
	}
}

// Run as: Headless is the plugin's field, carried by both writes and shown in
// the drawer; Edit reopens with the saved choice.
func TestRunAsHeadlessRoundTripsThroughEditAndTheDrawer(t *testing.T) {
	list := strings.Replace(automationsListFixture, `"agent":"claude","model":""`, `"agent":"claude","headless":true,"model":""`, 1)
	calls := stubAutomationsClient(t, func(args []string) (string, error) {
		if args[0] == "put" {
			return `{"next":[]}`, nil
		}
		return list, nil
	})
	s := openAutomationsShell(t, calls)
	s.applyAutomationsList(mustParseList(t, list), nil)
	s.launchKinds = claudeKinds()

	s.openAutomationsEdit("nightly-ci-triage")
	d := &s.automations.list.edit
	if d.headless != 1 {
		t.Fatalf("edit draft headless = %d, want the saved choice", d.headless)
	}
	d.headless = 0
	*calls = nil
	s.submitAutomationsDraft(d, "automations-put")
	if got := strings.Join(*calls, "|"); !strings.Contains(got, "put --original nightly-ci-triage") {
		t.Fatalf("calls = %q", got)
	}
	if runAsLabel(true) != "Headless" || runAsLabel(false) != "Interactive" {
		t.Error("the drawer's Run as fact must name both modes")
	}
}
