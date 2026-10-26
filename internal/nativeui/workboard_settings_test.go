package nativeui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/workboard"
)

// settingsShow mirrors 1.2.26's `show --json` rows (kinds and shapes from the
// sandbox); the voice/web-UI/keys/machines rows exist there and must stay hidden.
const settingsShow = `{"settings":[
 {"name":"worker.agent","kind":"work","value":"claude","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"worker.fast","kind":"work","value":true,"from":"you","place":"you","own":true,"note":"does nothing: claude has no fast mode"},
 {"name":"merge.method","kind":"work","value":"squash","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"board.open","kind":"board","value":"tab","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"worker.access","kind":"work","value":"full","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"worker.effort","kind":"work","value":"default","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"reviewer.agent","kind":"work","value":"codex/gpt-6-sol","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"coordinator.agent","kind":"coordinator","value":"none","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"coordinator.effort","kind":"coordinator","value":"default","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"check.timeout","kind":"work","value":"15m","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"limit.tokens","kind":"work","value":"none","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"review.rounds","kind":"work","value":3,"from":"built in","place":"builtin","own":false,"note":""},
 {"name":"attempts.by","kind":"work","value":1,"from":"built in","place":"builtin","own":false,"note":""},
 {"name":"plan.approve","kind":"work","value":"sideways","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"board.theme","kind":"board","value":"auto","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"board.webUi","kind":"board","value":false,"from":"built in","place":"builtin","own":false,"note":""},
 {"name":"board.keys","kind":"board","value":"x","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"board.machines","kind":"board","value":"none","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"voice.calls","kind":"board","value":false,"from":"built in","place":"builtin","own":false,"note":""}
],"problems":[]}`

// settingsModels mirrors `models --json`: codex has a model with its own effort list.
const settingsModels = `[
 {"agent":"claude","models":["default","opus"],"efforts":{"levels":["low","high"],"by":{}}},
 {"agent":"codex","models":["gpt-6-sol","gpt-6-luna"],"efforts":{"levels":["low","high","ultra"],"by":{"gpt-6-sol":["low","ultra"]}}},
 {"agent":"agy","models":null,"efforts":{"levels":[],"by":{}}}
]`

func settingsShell(t *testing.T, ran *[]string, setErr error) *Shell {
	t.Helper()
	newWorkboardClient = func(string, string) *workboard.Client {
		return workboard.NewClientWithExec(func(_ context.Context, args ...string) (string, error) {
			switch {
			case args[0] == "list" && len(args) > 1 && args[1] == "--pinned":
				return `{"machine":null,"projects":[],"tasks":[]}`, nil
			case args[0] == "list":
				return boardList, nil
			case args[0] == "details":
				return `{"task":{"id":"` + args[1] + `"}}`, nil
			case args[0] == "automations" && len(args) == 2, args[0] == "autopilot" && len(args) == 2:
				return `[]`, nil
			case args[0] == "coordinator" && args[len(args)-2] == "status":
				return `{"state":"none"}`, nil
			case args[0] == "models":
				*ran = append(*ran, strings.Join(args, " "))
				return settingsModels, nil
			case args[0] == "show":
				*ran = append(*ran, strings.Join(args, " "))
				return settingsShow, nil
			case args[0] == "set" && setErr != nil:
				return "", setErr
			case args[0] == "set":
				*ran = append(*ran, strings.Join(args, " "))
				return "worker.fast: false  (you)\ndoes nothing: claude has no fast mode\n", nil
			}
			*ran = append(*ran, strings.Join(args, " "))
			return "", nil
		})
	}
	workboardGo = func(f func()) { f() }
	t.Cleanup(func() { newWorkboardClient, workboardGo = workboard.NewClient, func(f func()) { go f() } })
	s := supportedWorkboardShell()
	loadBoard(t, s)
	return s
}

func TestWorkboardSettingsNavExistsOnlyWithThePlugin(t *testing.T) {
	var ran []string
	s := settingsShell(t, &ran, nil)
	nav := func() *ui.Tester { return ui.NewTester(s.settingsNav, 300, 500) }
	if !nav().HasText("Work Board") {
		t.Fatalf("the plugin is available: %q", nav().Texts())
	}
	s.workboard.avail.Supported = false
	if tt := nav(); tt.HasText("Work Board") {
		t.Fatalf("gate closed, so no trace: %q", tt.Texts())
	}
	if tt := ui.NewTester(s.settingsWorkboard, 900, 600); !tt.HasText("The work board isn't available") || s.workboard.settings.loading {
		t.Fatalf("closed gate page: %q", tt.Texts())
	}
	if got := settingsSectionFor("/settings/workboard").label; got != "Work Board" {
		t.Fatalf("title bar label = %q", got)
	}
}

func TestSettingsPageShowsWhatThePluginReturnsGroupedAndFiltered(t *testing.T) {
	var ran []string
	s := settingsShell(t, &ran, nil)
	tt := ui.NewTester(s.settingsWorkboard, 900, 1400)
	if got := strings.Join(ran, "|"); got != "show --json|models --json" {
		t.Fatalf("reads = %q: one show for the open scope, one models for the choice lists", got)
	}
	tt = ui.NewTester(s.settingsWorkboard, 900, 1400)
	for _, want := range []string{"Scope", "Everywhere", "Worker", "worker.agent", "worker.fast", "Merge", "merge.method", "Board", "board.theme",
		"you · does nothing: claude has no fast mode", "built in", "Reset"} {
		if !tt.HasText(want) {
			t.Fatalf("settings page missing %q: %q", want, tt.Texts())
		}
	}
	for _, hidden := range []string{"board.webUi", "board.keys", "board.machines", "voice.calls", "Voice"} {
		if tt.HasText(hidden) {
			t.Fatalf("%q is not integrated and must stay hidden: %q", hidden, tt.Texts())
		}
	}
	s.workboard.settings.filter = "merge"
	tt = ui.NewTester(s.settingsWorkboard, 900, 1400)
	if !tt.HasText("merge.method") || tt.HasText("worker.agent") {
		t.Fatalf("filter: %q", tt.Texts())
	}
	s.workboard.settings.filter = "zzz"
	if tt = ui.NewTester(s.settingsWorkboard, 900, 1400); !tt.HasText("No settings match") {
		t.Fatalf("no match: %q", tt.Texts())
	}
}

func TestSettingsWritesFollowTheScopeAndReload(t *testing.T) {
	var ran []string
	s := settingsShell(t, &ran, nil)
	ui.NewTester(s.settingsWorkboard, 900, 1400) // first read
	ran = nil
	s.setWorkboardSetting("worker.fast", "false", false)
	if got := strings.Join(ran, "|"); got != "set worker.fast false" {
		t.Fatalf("everywhere: %q", got)
	}
	if s.pendingToast != "worker.fast: false  (you) · does nothing: claude has no fast mode" || s.workboard.settings.loaded {
		t.Fatalf("toast=%q loaded=%v: the plugin's words show, and the page re-reads", s.pendingToast, s.workboard.settings.loaded)
	}
	ran = nil
	s.workboard.settings.scope, s.workboard.settings.project = wbScopeProject, "r"
	s.setWorkboardSetting("merge.method", "rebase", false)
	s.setWorkboardSetting("merge.method", "", true)
	s.workboard.settings.scope = wbScopeCoordinator
	s.setWorkboardSetting("coordinator.agent", "codex", false)
	want := "set merge.method rebase --repo r|unset merge.method --repo r|set coordinator.agent codex --project-coordinator r"
	if got := strings.Join(ran, "|"); got != want {
		t.Fatalf("scoped writes:\n got %q\nwant %q", got, want)
	}
	// Changing the scope reads that scope's own set.
	ran = nil
	s.workboard.settings.scope, s.workboard.settings.loaded = wbScopeProject, false
	ui.NewTester(s.settingsWorkboard, 900, 1400)
	if got := strings.Join(ran, "|"); got != "show --json --repo r" {
		t.Fatalf("scope read = %q", got)
	}
}

func TestSettingsRefusalIsThePluginsOwnWords(t *testing.T) {
	var ran []string
	refusal := errors.New(`workboard: set: exit status 1: merge.method must be squash, merge, rebase, not "nonsense".`)
	s := settingsShell(t, &ran, refusal)
	s.setWorkboardSetting("merge.method", "nonsense", false)
	if s.pendingToast != `merge.method must be squash, merge, rebase, not "nonsense".` {
		t.Fatalf("toast = %q", s.pendingToast)
	}
}

func TestSettingsPageSurvivesAFailedRead(t *testing.T) {
	var ran []string
	s := settingsShell(t, &ran, nil)
	s.workboard.settings = workboardSettingsState{failed: true}
	tt := ui.NewTester(s.settingsWorkboard, 900, 600)
	if !tt.HasText("Couldn't read the settings") || !tt.HasText("Retry") {
		t.Fatalf("failed read: %q", tt.Texts())
	}
	if persistableRoute("/settings/workboard") != "/settings/workboard" {
		t.Fatal("a settings section is a stable route")
	}
}

// press clicks a control on the settings page and returns the plugin calls it made.
func pressSetting(t *testing.T, s *Shell, ran *[]string, label string) string {
	t.Helper()
	tt := ui.NewTester(s.settingsWorkboard, 900, 2600)
	tt = ui.NewTester(s.settingsWorkboard, 900, 2600)
	*ran = nil
	if err := tt.Click(label); err != nil {
		t.Fatalf("click %q: %v (%q)", label, err, tt.Texts())
	}
	var writes []string
	for _, call := range *ran {
		if strings.HasPrefix(call, "set ") || strings.HasPrefix(call, "unset ") {
			writes = append(writes, call)
		}
	}
	return strings.Join(writes, "|")
}

func TestSettingsChoicesAreSelectedNotTyped(t *testing.T) {
	var ran []string
	s := settingsShell(t, &ran, nil)
	ui.NewTester(s.settingsWorkboard, 900, 2600)
	tt := ui.NewTester(s.settingsWorkboard, 900, 2600)
	for _, want := range []string{"squash", "merge", "rebase", "tab", "popup", "full", "ask", "auto", "none", "s", "m", "h"} {
		if !tt.HasText(want) {
			t.Fatalf("a choice control must show %q: %q", want, tt.Texts())
		}
	}
	if got := pressSetting(t, s, &ran, "rebase"); got != "set merge.method rebase" {
		t.Fatalf("picking a method writes it once: %q", got)
	}
	if got := pressSetting(t, s, &ran, "popup"); got != "set board.open popup" {
		t.Fatalf("board.open: %q", got)
	}
	if got := pressSetting(t, s, &ran, "ask"); got != "set worker.access ask" {
		t.Fatalf("access: %q", got)
	}
}

func TestSettingsAmountsPickTheirUnit(t *testing.T) {
	var ran []string
	s := settingsShell(t, &ran, nil)
	// check.timeout 15m: switching the unit keeps the number; limit.tokens none: k/m/none.
	tt := ui.NewTester(s.settingsWorkboard, 900, 2600)
	tt = ui.NewTester(s.settingsWorkboard, 900, 2600)
	if !tt.HasText("k") {
		t.Fatalf("tokens offer k and m: %q", tt.Texts())
	}
	if got := pressSetting(t, s, &ran, "h"); got != "set check.timeout 15h" {
		t.Fatalf("time unit: %q", got)
	}
}

func TestSettingsAgentAndEffortComeFromModels(t *testing.T) {
	var ran []string
	s := settingsShell(t, &ran, nil)
	ui.NewTester(s.settingsWorkboard, 900, 2600)
	tt := ui.NewTester(s.settingsWorkboard, 900, 2600)
	// Programs and models are drop-downs; a program the plugin lists no models for, or
	// "none", has no model control; efforts are drop-downs of the sibling agent's levels.
	for _, want := range []string{"worker.agent program", "worker.agent model", "reviewer.agent program", "reviewer.agent model", "coordinator.agent program", "claude", "codex", "default"} {
		if !tt.HasText(want) {
			t.Fatalf("agent controls missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("coordinator.agent model") {
		t.Fatalf("a none agent has no model: %q", tt.Texts())
	}
}

// groupTexts returns what the page shows from a group's title up to the next title.
func groupTexts(texts []string, title string) []string {
	for i, text := range texts {
		if text == title {
			return texts[i+1:]
		}
	}
	return nil
}

func TestSettingsUnknownOrDriftedValuesStayText(t *testing.T) {
	var ran []string
	s := settingsShell(t, &ran, nil)
	ui.NewTester(s.settingsWorkboard, 900, 2600)
	tt := ui.NewTester(s.settingsWorkboard, 900, 2600)
	// attempts.by is a count-or-list the table leaves alone; plan.approve's "sideways" is a word
	// the table does not know: each row keeps its label, a text field's label and the note, no
	// choice labels in between.
	rest := groupTexts(tt.Texts(), "Attempts")
	want := []string{"attempts.by", "attempts.by", "built in", "Plan", "plan.approve", "plan.approve", "built in"}
	if len(rest) != len(want) {
		t.Fatalf("drifted rows: %q", rest)
	}
	for i := range want {
		if rest[i] != want[i] {
			t.Fatalf("drifted rows: %q", rest)
		}
	}
}

func TestSettingsWithoutModelsFallBackToText(t *testing.T) {
	var ran []string
	s := settingsShell(t, &ran, nil)
	s.workboard.settings.modelsTried = true // the models read never happened
	ui.NewTester(s.settingsWorkboard, 900, 2600)
	tt := ui.NewTester(s.settingsWorkboard, 900, 2600)
	if tt.HasText("worker.agent program") || tt.HasText("reviewer.agent model") || tt.HasText("default") {
		t.Fatalf("without the plugin's lists agent and effort stay text fields: %q", tt.Texts())
	}
}

func TestDelegateWithSendsOnlyTheChoicesMade(t *testing.T) {
	var ran []string
	s := settingsShell(t, &ran, nil)
	task := workboard.Task{ID: "r/4", Title: "Later"}
	s.openWorkboardDelegateWith(task)
	d := s.workboard.delegate
	if !d.open || d.loading || d.failed || len(d.view.Settings) == 0 {
		t.Fatalf("the dialog must have read the task's settings: %+v", d)
	}
	if got := strings.Join(ran, "|"); !strings.Contains(got, "show r/4 --json") {
		t.Fatalf("settings come from `show <id>`, ran %q", got)
	}
	ran = nil
	tt := ui.NewTester(s.delegateDialog, 900, 900)
	for _, want := range []string{"Delegate “Later”", "worker.agent", "worker.access", "full", "ask", "auto", "Delegate", "Cancel"} {
		if !tt.HasText(want) {
			t.Fatalf("dialog missing %q: %q", want, tt.Texts())
		}
	}
	for _, not := range []string{"merge.method", "board.open", "limit.tokens"} {
		if tt.HasText(not) {
			t.Fatalf("dialog must offer only its own settings, got %q", not)
		}
	}
	if err := tt.Click("auto"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Delegate"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ran, "|"); got != "delegate r/4 --worker.access auto" {
		t.Fatalf("ran = %q: only the choice made goes to the plugin, no setting is written", got)
	}
	if s.workboard.delegate.open {
		t.Fatal("delegating closes the dialog")
	}
}

func TestDelegateWithNoChoiceIsPlainDelegate(t *testing.T) {
	var ran []string
	s := settingsShell(t, &ran, nil)
	s.openWorkboardDelegateWith(workboard.Task{ID: "r/4"})
	ran = nil
	s.submitDelegateWith()
	if got := strings.Join(ran, "|"); got != "delegate r/4" {
		t.Fatalf("ran = %q", got)
	}
}
