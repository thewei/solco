package nativeui

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/automations"
	"github.com/thewei/solco/internal/commandcenter"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 automations_ui.go 的门与刷新缝、page_automations.go 的页面纯函数与状态、app_menu/command_center 的 ⌘6 接线、ui.Tester 无头渲染
 * [OUTPUT]: 门接线回归（去抖、关闭清列表、调色板/Go 菜单条目随门、⌘6 一致）、列表行分组与状态槽/chip 映射、history 目录 ±2 分钟唯一命中、无头渲染（关态引导、开态列表、抽屉）、确认删除与拒绝透传
 * [POS]: Automations 页的守卫测试；与 automations_ui.go / page_automations.go 同生共死；一切读取走 fake exec，绝不跑真插件 CLI
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// automationsListFixture is a small plugin answer: one enabled automation
// whose last run failed, one draft.
const automationsListFixture = `{"result":{"paused":false,"invalid":[],
 "automations":[
  {"name":"nightly-ci-triage","draft":false,"disabled":false,
   "cron":"0 9 * * 1-5","schedule_text":"Weekdays 09:00","once_at":null,
   "repo":"~/Workspaces/wh-studio/solco","workspace_mode":"worktree",
   "agent":"claude","model":"","prompt":"triage","rationale":"nightly",
   "on_overlap":"skip","timeout_minutes":60,"catch_up_minutes":120,"cleanup_ttl_hours":0,
   "next_run":"2026-11-03T09:00:00+08:00",
   "last_run":{"run_id":"r-1","status":"failed","started_at":"2026-11-01T09:00:00+08:00",
               "duration_seconds":3600,"worktree":"auto/x","workspace_id":"w-1","pane_id":"p-1"},
   "history30":{"done":3,"failed":1,"skipped":0}},
  {"name":"draft-sweep","draft":true,"disabled":false,
   "cron":"","schedule_text":"","once_at":null,
   "repo":"~/Workspaces/wh-studio/solco","workspace_mode":"worktree",
   "agent":"codex","model":"","prompt":"sweep","rationale":"",
   "on_overlap":"queue","timeout_minutes":0,"catch_up_minutes":0,"cleanup_ttl_hours":48,
   "next_run":"","last_run":null,"history30":{"done":0,"failed":0,"skipped":0}}]}}`

// stubAutomationsClient reroutes every client call through one fake and runs
// the background lane inline, the workboard test seams' way.
func stubAutomationsClient(t *testing.T, answer func(args []string) (string, error)) *[]string {
	t.Helper()
	var calls []string
	newAutomationsClient = func(string, string) *automations.Client {
		return automations.NewClientWithExec(func(_ context.Context, args ...string) (string, error) {
			calls = append(calls, strings.Join(args, " "))
			return answer(args)
		}).WithDaemonStarter(func(log string) error {
			calls = append(calls, "daemon "+log)
			return nil
		})
	}
	automationsGo = func(f func()) { f() }
	// The background lane runs inline here, so a poll timer would recurse.
	automationsPollGap, automationsStartSettle = 0, 0
	t.Cleanup(func() {
		newAutomationsClient = automations.NewClient
		automationsGo = func(f func()) { go f() }
		automationsPollGap, automationsStartSettle = 8*time.Second, 1500*time.Millisecond
	})
	return &calls
}

// openAutomationsShell is a shell whose gate is open, with the fixture list
// already applied. calls must come from stubAutomationsClient, installed
// first.
func openAutomationsShell(t *testing.T, calls *[]string) *Shell {
	t.Helper()
	if calls == nil {
		t.Fatal("openAutomationsShell needs the stub's calls slice")
	}
	s := NewShell()
	s.applyAutomationsAvailability(automations.Availability{
		Installed: true, Enabled: true, Version: "0.12.0", Supported: true, Root: "/plugin",
	})
	*calls = nil // drop the gate-opening read
	s.applyAutomationsList(mustParseList(t, automationsListFixture), nil)
	*calls = nil
	return s
}

// stubAutomationsFixture is the plain fixture answer.
func stubAutomationsFixture(t *testing.T) *[]string {
	t.Helper()
	return stubAutomationsClient(t, func([]string) (string, error) {
		return automationsListFixture, nil
	})
}

// mustParseList decodes one raw plugin answer through the client.
func mustParseList(t *testing.T, raw string) automations.ListResult {
	t.Helper()
	client := automations.NewClientWithExec(func(context.Context, ...string) (string, error) {
		return raw, nil
	})
	result, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// The gate wiring mirrors the workboard's: focus hand-backs are frequent;
// only a detection older than the gap runs again, an in-flight one is never
// doubled, and a closed gate drops the page state.
func TestAutomationsDetectionIsDebounced(t *testing.T) {
	s := NewShell()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	restore := timeNow
	timeNow = func() time.Time { return now }
	defer func() { timeNow = restore }()

	s.automations.lastDetect = now.Add(-automationsDetectGap / 2)
	s.detectAutomations(false)
	if s.automations.detecting {
		t.Fatal("a detection inside the gap must not start")
	}

	s.automations.detecting = true
	before := s.automations.lastDetect
	s.detectAutomations(true)
	if !s.automations.lastDetect.Equal(before) {
		t.Fatal("an in-flight detection must not be doubled, even when forced")
	}
}

func TestApplyAutomationsAvailabilityClearsTheInFlightFlag(t *testing.T) {
	stubAutomationsClient(t, func([]string) (string, error) { return automationsListFixture, nil })
	s := NewShell()
	s.automations.detecting = true
	s.applyAutomationsAvailability(automations.Availability{Installed: true, Enabled: true, Version: "0.12.0", Supported: true})
	if s.automations.detecting || !s.automationsAvailable() {
		t.Fatalf("after apply: detecting=%v available=%v", s.automations.detecting, s.automationsAvailable())
	}
	s.applyAutomationsAvailability(automations.Unreadable())
	if s.automationsAvailable() {
		t.Fatal("a later unreadable detection must close the gate again")
	}
	if len(s.automations.list.result.Automations) != 0 {
		t.Fatal("a closed gate must leave no list behind")
	}
}

// The palette entry and the Go menu item close with the gate; ⌘6 is bound
// once, in one place, and the palette prints the same key the menu binds.
func TestAutomationsEntryFollowsTheGate(t *testing.T) {
	stubAutomationsFixture(t) // the gate-open apply reads once
	s := NewShell()
	if action := actionOf(t, s, "app.automations"); action.Available {
		t.Fatal("no detection yet: the entry must be unavailable")
	}
	index := commandcenter.Build(s.buildCommandCenterActions())
	if got := index.Query("automations", commandcenter.ScopeAll); len(got) != 0 {
		t.Fatalf("an ungated palette surfaced the automations: %+v", got)
	}
	if item := menuItemNamed(menuNamed(t, s.MenuBar(nil), "Go").Submenu, "Automations"); item != nil {
		t.Fatal("a closed gate must leave the Go menu without the item")
	}
	if persistableRoute(routeAutomations) != routeAutomations {
		t.Fatal("/automations must be a persistable route")
	}

	s.applyAutomationsAvailability(automations.Availability{
		Installed: true, Enabled: true, Version: "0.12.0", Supported: true, Root: "/plugin",
	})
	if !actionOf(t, s, "app.automations").Available {
		t.Fatal("a supported plugin on a local instance must expose the entry")
	}
	item := menuItemNamed(menuNamed(t, s.MenuBar(nil), "Go").Submenu, "Automations")
	if item == nil {
		t.Fatal("an open gate must put the item in the Go menu")
	}
	if item.Accelerator != accelAutomations {
		t.Fatalf("menu accelerator = %q, want %q", item.Accelerator, accelAutomations)
	}
	if label := acceleratorLabel(accelAutomations); label != "⌘6" {
		t.Fatalf("⌘6 label = %q", label)
	}
	if palette := actionOf(t, s, "app.automations"); palette.Shortcut != "⌘6" {
		t.Fatalf("palette shortcut = %q, want the menu's ⌘6", palette.Shortcut)
	}
	// The shortcut sheet lists the binding even before the gate is open:
	// the sheet describes the app's keys, the menus list live destinations.
	left, _ := s.shortcutSections()
	found := false
	for _, section := range left {
		for _, r := range section.Rows {
			if r.Title == "Automations" && strings.Join(r.Keys, "") == "⌘6" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("the shortcut sheet has no Automations row with ⌘6")
	}
}

func actionOf(t *testing.T, s *Shell, id string) commandcenter.Action {
	t.Helper()
	for _, a := range s.buildCommandCenterActions() {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("no %s in the registry", id)
	return commandcenter.Action{}
}

// A remote Herdr instance never shows the page: plugins live on this machine.
func TestAutomationsHideOnRemoteInstances(t *testing.T) {
	s := NewShell()
	s.automations.avail = automations.Availability{Installed: true, Enabled: true, Version: "0.12.0", Supported: true}
	s.instances = []herdr.Instance{{Name: "inst-1", Machine: &herdr.Machine{ID: "m1"}}}
	s.activeInstance = "inst-1"
	if s.automationsAvailable() {
		t.Fatal("a remote instance must hide the automations page")
	}
}

// The segment filter and the row state mapping.
func TestAutomationsVisibleFiltersBySegment(t *testing.T) {
	calls := stubAutomationsFixture(t)
	s := openAutomationsShell(t, calls)
	st := &s.automations.list
	if got := len(s.automationsVisible()); got != 2 {
		t.Fatalf("all = %d rows, want 2", got)
	}
	st.segment = automationsEnabled
	if got := s.automationsVisible(); len(got) != 1 || got[0].Name != "nightly-ci-triage" {
		t.Fatalf("enabled = %+v", got)
	}
	st.segment = automationsDrafts
	if got := s.automationsVisible(); len(got) != 1 || got[0].Name != "draft-sweep" {
		t.Fatalf("drafts = %+v", got)
	}
	st.segment = automationsPaused
	if got := len(s.automationsVisible()); got != 0 {
		t.Fatalf("paused = %d rows, want 0", got)
	}
}

func TestAutomationStatusToneFollowsTheContract(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*automations.Automation)
		tone StatusTone
	}{
		{"running turns", func(a *automations.Automation) {
			a.LastRun = &automations.LastRun{Status: "running", StartedAt: "2026-11-02T11:00:00Z"}
		}, ToneWorking},
		{"draft waits for a look", func(a *automations.Automation) { a.Draft = true }, ToneInfo},
		{"failed wants attention", func(a *automations.Automation) {
			a.LastRun = &automations.LastRun{Status: "failed"}
		}, ToneError},
		{"paused is muted", func(a *automations.Automation) { a.Disabled = true }, ToneMuted},
		{"one-shot is muted", func(a *automations.Automation) {
			once := "2026-11-05T09:00:00+08:00"
			a.OnceAt = &once
		}, ToneMuted},
		{"enabled is a quiet ring", func(a *automations.Automation) {}, ToneNeutral},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := automations.Automation{Name: "x"}
			tc.mod(&a)
			if tone, _ := automationStatusTone(a); tone != tc.tone {
				t.Fatalf("tone = %v, want %v", tone, tc.tone)
			}
		})
	}
}

func TestAutomationChipsMarkDraftFailureAndLastRun(t *testing.T) {
	a := automations.Automation{Name: "x", Draft: true,
		LastRun: &automations.LastRun{Status: "failed", DurationSeconds: 3600}, TimeoutMinutes: 60}
	var texts []string
	for _, chip := range automationChips(a) {
		texts = append(texts, chip.text)
	}
	joined := strings.Join(texts, "|")
	if !strings.Contains(joined, "✦ Draft") || !strings.Contains(joined, "✗ Timed out") {
		t.Fatalf("chips = %q, want the draft and the timeout mark", joined)
	}

	a.Draft = false
	a.LastRun = &automations.LastRun{Status: "failed", DurationSeconds: 60}
	if chips := automationChips(a); len(chips) != 1 || chips[0].text != "✗ Failed" {
		t.Fatalf("a plain failure is no timeout: %+v", chips)
	}
}

// View in Chat exists only when the history catalog matches the run
// uniquely: same agent, same repository, started within ±2 minutes.
func TestAutomationChatSessionKeyNeedsAUniqueHit(t *testing.T) {
	plus8 := time.FixedZone("+08:00", 8*3600)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory to expand ~ against")
	}
	repo := home + "/autos-repo"
	started := "2026-11-02T09:00:00+08:00"
	sessions := []history.SessionSummary{
		{Meta: history.SessionMeta{Key: "k1", Agent: "claude-code", ProjectPath: repo,
			CreatedAt: time.Date(2026, 11, 2, 9, 0, 30, 0, plus8).UnixMilli()}},
	}
	if key := automationChatSessionKey("~/autos-repo", "claude", started, sessions); key != "k1" {
		t.Fatalf("key = %q, want the unique hit through the ~ form", key)
	}
	// Two candidates in the window: no link, never a guess.
	sessions = append(sessions, sessions[0])
	sessions[1].Meta.Key = "k2"
	if key := automationChatSessionKey("~/autos-repo", "claude", started, sessions); key != "" {
		t.Fatalf("key = %q, want empty when two sessions match", key)
	}

	// Outside the ±2min window, another agent, another repository.
	sessions = sessions[:1]
	late := "2026-11-02T09:03:00+08:00"
	if key := automationChatSessionKey("~/autos-repo", "claude", late, sessions); key != "" {
		t.Fatalf("key = %q, want empty past the window", key)
	}
	if key := automationChatSessionKey("~/autos-repo", "codex", started, sessions); key != "" {
		t.Fatalf("key = %q, want empty for another agent", key)
	}
	if key := automationChatSessionKey("~/other-repo", "claude", started, sessions); key != "" {
		t.Fatalf("key = %q, want empty for another repository", key)
	}
	if key := automationChatSessionKey("~/autos-repo", "claude", "not-a-time", sessions); key != "" {
		t.Fatalf("key = %q, want empty for an unparsable start", key)
	}
}

// Headless render: the closed gate draws the install guidance, the open gate
// draws the rows, and clicking a row opens the drawer.
func TestAutomationsPageRenders(t *testing.T) {
	t.Run("gate closed shows guidance", func(t *testing.T) {
		s := NewShell()
		s.automations.avail = automations.Availability{Installed: true, Reason: automations.ReasonNotInstalled}
		tester := ui.NewTester(s.automationsPage, 900, 600)
		tester.Frame()
		if !hasText(tester.Texts(), "Install the Automations plugin") {
			t.Fatalf("texts = %q, want the install guidance", tester.Texts())
		}
		if !hasText(tester.Texts(), "herdr plugin enable "+automations.PluginID) {
			t.Fatalf("texts = %q, want the enable command", tester.Texts())
		}
	})

	t.Run("gate open shows rows and the drawer", func(t *testing.T) {
		calls := stubAutomationsFixture(t)
		s := openAutomationsShell(t, calls)
		tester := ui.NewTester(s.automationsPage, 1100, 800)
		tester.Frame()
		if !hasText(tester.Texts(), "nightly-ci-triage") || !hasText(tester.Texts(), "draft-sweep") {
			t.Fatalf("texts = %q, want both rows", tester.Texts())
		}
		for _, label := range []string{"All", "Enabled", "Drafts", "Paused", "✦ Let an Agent Draft", "＋ New"} {
			if !hasText(tester.Texts(), label) {
				t.Fatalf("texts = %q, want %q", tester.Texts(), label)
			}
		}
		if !hasText(tester.Texts(), "✗ Timed out") {
			t.Fatalf("texts = %q, want the failed row's timeout chip", tester.Texts())
		}
		// A row click opens the drawer with the prompt card and actions.
		tester.Click("Automation draft-sweep")
		tester.Frame()
		for _, label := range []string{"Prompt", "Details", "Last 5 runs", "Approve", "Edit…", "Delete…"} {
			if !hasText(tester.Texts(), label) {
				t.Fatalf("drawer texts = %q, want %q", tester.Texts(), label)
			}
		}
		// A draft never fires, so run-now is not on offer; approval is.
		if hasText(tester.Texts(), "▶ Run Now") {
			t.Fatalf("drawer texts = %q, a draft must not offer run now", tester.Texts())
		}
		tester.Click("Approve")
		tester.Frame()
		if got := strings.Join(*calls, "|"); !strings.Contains(got, "approve draft-sweep") {
			t.Fatalf("calls = %q, want the draft's approve to reach the plugin", got)
		}
	})

	t.Run("an enabled automation offers run now", func(t *testing.T) {
		calls := stubAutomationsFixture(t)
		s := openAutomationsShell(t, calls)
		tester := ui.NewTester(s.automationsPage, 1100, 800)
		tester.Frame()
		tester.Click("Automation nightly-ci-triage")
		tester.Frame()
		if !hasText(tester.Texts(), "▶ Run Now") || hasText(tester.Texts(), "Approve") {
			t.Fatalf("drawer texts = %q, want run now and no approve", tester.Texts())
		}
	})
}

// The drawer's runs come from the plugin's history verb, loaded per name.
func TestAutomationsDrawerLoadsHistory(t *testing.T) {
	calls := stubAutomationsClient(t, func(args []string) (string, error) {
		if args[0] == "history" {
			return `{"result":{"runs":[{"run_id":"r-9","name":"draft-sweep","trigger":"manual",
			  "status":"done","started_at":"2026-11-02T08:00:00+08:00","finished_at":"","duration_seconds":120,
			  "worktree":"","workspace_id":"w-9","pane_id":"","agent":"codex"}]}}`, nil
		}
		return automationsListFixture, nil
	})
	s := openAutomationsShell(t, calls)
	s.openAutomationsDrawer("draft-sweep")
	if got := strings.Join(*calls, "|"); !strings.Contains(got, "history draft-sweep --json") {
		t.Fatalf("calls = %q, want the drawer's history read", got)
	}
	runs := s.automations.list.history["draft-sweep"].Runs
	if len(runs) != 1 || runs[0].RunID != "r-9" {
		t.Fatalf("runs = %+v", runs)
	}
}

// 删除 goes through the shared confirm; the confirmed verb is rm.
func TestAutomationsRemoveIsConfirmed(t *testing.T) {
	calls := stubAutomationsClient(t, func(args []string) (string, error) {
		return automationsListFixture, nil
	})
	s := openAutomationsShell(t, calls)
	s.openAutomationsDrawer("draft-sweep")
	s.openConfirm(automationsRemoveConfirm, "draft-sweep", "Delete “draft-sweep”?", "…")
	s.submitConfirm(s.confirmKind, s.confirmTarget)
	if got := strings.Join(*calls, "|"); !strings.Contains(got, "rm draft-sweep") {
		t.Fatalf("calls = %q, want the confirmed rm", got)
	}
	if s.automations.list.selected != "" {
		t.Fatal("a removed automation must close the drawer")
	}
}

// Writes carry the plugin's refusal verbatim into the toast, bounded.
func TestAutomationsRefusalBecomesTheToast(t *testing.T) {
	calls := stubAutomationsClient(t, func([]string) (string, error) {
		// The CLI wrapper's shape: "automations: <verb>: <exit>: <refusal>".
		return "", errors.New("automations: approve: exit status 1: draft-sweep is a draft — approve it first: solco-automations approve draft-sweep")
	})
	s := openAutomationsShell(t, calls)
	s.automationsWrite("approve", "draft-sweep", func(ctx context.Context, client *automations.Client) error {
		return client.Approve(ctx, "draft-sweep")
	}, nil)
	if s.pendingToast != "draft-sweep is a draft — approve it first: solco-automations approve draft-sweep" {
		t.Fatalf("toast = %q, want the plugin's own refusal", s.pendingToast)
	}
}

// A draft cannot be run or approved by the client without reaching the
// plugin: nothing here special-cases the state, the plugin refuses.
func TestAutomationsDraftVerbGuardIsOnlyANameGuard(t *testing.T) {
	calls := stubAutomationsClient(t, func(args []string) (string, error) {
		return "", nil
	})
	s := openAutomationsShell(t, calls)
	s.automationsWrite("run", "draft-sweep", func(ctx context.Context, client *automations.Client) error {
		return client.Run(ctx, "draft-sweep")
	}, nil)
	if got := strings.Join(*calls, "|"); !strings.Contains(got, "run draft-sweep") {
		t.Fatalf("calls = %q, want the run to reach the plugin", got)
	}
}

func hasText(texts []string, want string) bool {
	for _, text := range texts {
		if strings.Contains(text, want) {
			return true
		}
	}
	return false
}

// A fire time lies ahead of now: it reads as a clock time, never "just now".
func TestAutomationNextTextNamesTheFuture(t *testing.T) {
	loc := time.FixedZone("x", 8*3600)
	restore := timeNow
	timeNow = func() time.Time { return time.Date(2026, 11, 2, 23, 50, 0, 0, loc) }
	defer func() { timeNow = restore }()
	for _, tc := range []struct{ at, want string }{
		{"2026-11-02T23:59:00+08:00", "today 23:59"},
		{"2026-11-03T09:00:00+08:00", "tomorrow 09:00"},
		{"2026-11-03T01:00:00Z", "tomorrow 09:00"}, // another zone, same instant
		{"2026-11-09T09:00:00+08:00", "11-09 09:00"},
	} {
		at, err := time.Parse(time.RFC3339, tc.at)
		if err != nil {
			t.Fatal(err)
		}
		if got := automationNextText(at); got != tc.want {
			t.Errorf("automationNextText(%s) = %q, want %q", tc.at, got, tc.want)
		}
	}
}
