package nativeui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/wh-studio/herdr-client/internal/workboard"
)

// settingsShow mirrors 1.2.26's `show --json` rows (kinds and shapes from the
// sandbox); the voice/web-UI/keys/machines rows exist there and must stay hidden.
const settingsShow = `{"settings":[
 {"name":"worker.agent","kind":"work","value":"claude","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"worker.fast","kind":"work","value":true,"from":"you","place":"you","own":true,"note":"does nothing: claude has no fast mode"},
 {"name":"merge.method","kind":"work","value":"squash","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"board.theme","kind":"board","value":"auto","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"board.webUi","kind":"board","value":false,"from":"built in","place":"builtin","own":false,"note":""},
 {"name":"board.keys","kind":"board","value":"x","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"board.machines","kind":"board","value":"none","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"voice.calls","kind":"board","value":false,"from":"built in","place":"builtin","own":false,"note":""}
],"problems":[]}`

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
	if got := strings.Join(ran, "|"); got != "show --json" {
		t.Fatalf("reads = %q: one show for the open scope", got)
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
