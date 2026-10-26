package nativeui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/wh-studio/herdr-client/internal/workboard"
)

// writeShell is a supported-gate Shell whose board CLI is a recorder: list,
// details and the read-only extras come from canned answers, every other verb
// is appended to ran.
func writeShell(t *testing.T, ran *[]string) *Shell {
	return extrasShell(t, ran, `{"machine":null,"projects":[],"tasks":[]}`, `{"state":"none","pane":""}`)
}

// extrasShell is writeShell with chosen `list --pinned` and `coordinator
// status` answers.
func extrasShell(t *testing.T, ran *[]string, pinned, coordinator string) *Shell {
	t.Helper()
	newWorkboardClient = func(string, string) *workboard.Client {
		return workboard.NewClientWithExec(func(_ context.Context, args ...string) (string, error) {
			switch {
			case args[0] == "list" && len(args) > 1 && args[1] == "--pinned":
				return pinned, nil
			case args[0] == "list":
				return boardList, nil
			case args[0] == "details":
				return `{"task":{"id":"` + args[1] + `"}}`, nil
			case args[0] == "automations" && len(args) == 2:
				return `[]`, nil
			case args[0] == "autopilot" && len(args) == 2:
				return `[]`, nil
			case args[0] == "coordinator" && args[len(args)-2] == "status":
				return coordinator, nil
			}
			*ran = append(*ran, strings.Join(args, " "))
			return "", nil
		})
	}
	workboardGo = func(f func()) { f() }
	t.Cleanup(func() { newWorkboardClient, workboardGo = workboard.NewClient, func(f func()) { go f() } })
	return supportedWorkboardShell()
}

func TestWorkboardActionsByGroup(t *testing.T) {
	labels := func(g workboard.Group) string {
		var out []string
		for _, a := range workboardActionsFor(g, nil) {
			out = append(out, a.Kind)
		}
		return strings.Join(out, ",")
	}
	cases := map[workboard.Group]string{
		workboard.GroupReady:    "approve,stop,archive",
		workboard.GroupNeedsYou: "retry,review,fix,stop,archive",
		workboard.GroupTodo:     "delegate,delegate-with,archive",
		workboard.GroupWorking:  "stop",
		workboard.GroupDone:     "archive",
	}
	for g, want := range cases {
		if got := labels(g); got != want {
			t.Errorf("%v: actions = %q, want %q", g, got, want)
		}
	}
}

func TestApproveAsksFirstAndOnlyConfirmRuns(t *testing.T) {
	var ran []string
	s := writeShell(t, &ran)
	s.startWorkboardAction(boardWrite{"Approve…", "approve"}, workboard.Task{ID: "r/1", Title: "Finished work"})
	if !s.confirmOpen || s.confirmKind != workboardApproveConfirm || s.confirmTarget != "r/1" {
		t.Fatalf("confirm = open:%v kind:%q target:%q", s.confirmOpen, s.confirmKind, s.confirmTarget)
	}
	if !strings.Contains(s.confirmMessage, "Finished work") || len(ran) != 0 {
		t.Fatalf("message=%q ran=%q: asking must not run anything", s.confirmMessage, ran)
	}
	s.submitConfirm(s.confirmKind, s.confirmTarget)
	if len(ran) != 1 || ran[0] != "approve r/1" {
		t.Fatalf("ran = %q, want one plain approve", ran)
	}
}

func TestStopAsksFirstAndOnlyConfirmRuns(t *testing.T) {
	var ran []string
	s := writeShell(t, &ran)
	s.startWorkboardAction(boardWrite{"Stop", "stop"}, workboard.Task{ID: "r/3", Title: "Busy work"})
	if !s.confirmOpen || s.confirmKind != workboardStopConfirm || s.confirmTarget != "r/3" || len(ran) != 0 {
		t.Fatalf("confirm = open:%v kind:%q target:%q ran=%q", s.confirmOpen, s.confirmKind, s.confirmTarget, ran)
	}
	if !strings.Contains(s.confirmMessage, "Busy work") {
		t.Fatalf("message = %q", s.confirmMessage)
	}
	s.submitConfirm(s.confirmKind, s.confirmTarget)
	if len(ran) != 1 || ran[0] != "stop r/3" {
		t.Fatalf("ran = %q, want one stop", ran)
	}
}

func TestApproveOfATaskThatChangedIsRefusedWithAToast(t *testing.T) {
	var ran []string
	s := writeShell(t, &ran)
	s.submitConfirm(workboardApproveConfirm, "r/3") // r/3 is Busy, not ready
	if len(ran) != 0 || s.pendingToast != workboardNotReadyToast {
		t.Fatalf("ran=%q toast=%q", ran, s.pendingToast)
	}
}

func TestRowActionsRunAtOnce(t *testing.T) {
	var ran []string
	s := writeShell(t, &ran)
	for _, k := range []string{"retry", "review", "fix", "delegate", "archive"} {
		s.startWorkboardAction(boardWrite{Kind: k}, workboard.Task{ID: "r/2"})
	}
	want := "retry r/2|review r/2|fix r/2|delegate r/2|archive r/2"
	if got := strings.Join(ran, "|"); got != want || s.confirmOpen {
		t.Fatalf("ran = %q confirm=%v", got, s.confirmOpen)
	}
}

func TestWritesDoNothingWhileGateClosed(t *testing.T) {
	var ran []string
	s := writeShell(t, &ran)
	s.workboard.avail.Supported = false
	s.runWorkboardAction("archive", "r/2")
	s.workboard.newTask = newTaskDraft{open: true, repo: "r", title: "x"}
	s.submitNewTask()
	if len(ran) != 0 {
		t.Fatalf("ran = %q with the plugin gate closed", ran)
	}
}

func TestFailureToastShowsThePluginsOwnWordsBounded(t *testing.T) {
	err := errors.New("workboard: retry: exit status 1: repo/2 has no saved error\nto send")
	if got := workboardFailureToast(err); got != "repo/2 has no saved error to send" {
		t.Fatalf("toast = %q", got)
	}
	colon := errors.New("workboard: approve: exit status 1: checks failed: build")
	if got := workboardFailureToast(colon); got != "checks failed: build" {
		t.Fatalf("a colon inside the plugin's message must survive: %q", got)
	}
	long := errors.New("workboard: fix: exit status 1: " + strings.Repeat("é", 500))
	if got := []rune(workboardFailureToast(long)); len(got) != workboardToastMax+1 {
		t.Fatalf("toast length = %d runes, want %d", len(got), workboardToastMax+1)
	}
	if got := workboardFailureToast(errors.New("workboard: fix: exit status 1: ")); got == "" {
		t.Fatal("an empty refusal still needs words")
	}
}

func TestAddNeedsAGitRepoAndTargetsIt(t *testing.T) {
	var ran []string
	s := writeShell(t, &ran)
	s.openWorkboardAdd(false)
	if s.workboard.newTask.open || s.pendingToast != workboardNoRepoToast {
		t.Fatalf("open=%v toast=%q: no repo, no dialog", s.workboard.newTask.open, s.pendingToast)
	}
	s.git = &gitService{root: "/work/repo"}
	s.openWorkboardAdd(true)
	d := s.workboard.newTask
	if !d.open || d.repo != "/work/repo" || !d.byRoot || !d.delegate {
		t.Fatalf("draft = %+v", d)
	}
	s.workboard.newTask.title = "Ship it"
	s.submitNewTask()
	s.openWorkboardAdd(false)
	s.workboard.newTask.title = "Plain"
	s.submitNewTask()
	want := "add Ship it --repo /work/repo --delegate|add Plain --repo /work/repo"
	if got := strings.Join(ran, "|"); got != want || s.workboard.newTask.open {
		t.Fatalf("ran = %q, want %q", got, want)
	}
}

func TestAddRefusedWhenTheRepositoryMovedOn(t *testing.T) {
	var ran []string
	s := writeShell(t, &ran)
	s.git = &gitService{root: "/other"}
	s.workboard.newTask = newTaskDraft{open: true, repo: "/work/repo", byRoot: true, title: "x"}
	s.submitNewTask()
	if len(ran) != 0 || !strings.Contains(s.pendingToast, "Repository changed") {
		t.Fatalf("ran=%q toast=%q", ran, s.pendingToast)
	}
}

func TestTasksTabOffersNewTask(t *testing.T) {
	var ran []string
	s := writeShell(t, &ran)
	s.quickPanelTab = quickPanelTasksTab
	tt := ui.NewTester(s.QuickPanelView, QuickPanelWidth, QuickPanelHeight)
	if !tt.HasText("New Task") || !tt.HasText("Add and Delegate") {
		t.Fatalf("footer missing: %q", tt.Texts())
	}
	if err := tt.Click("New Task"); err != nil {
		t.Fatal(err)
	}
	if s.pendingToast != workboardNoRepoToast {
		t.Fatalf("toast = %q", s.pendingToast)
	}
}

func TestSendBackVerbsAppearOnlyWithSomethingToSend(t *testing.T) {
	kinds := func(d *workboard.Detail) string {
		var out []string
		for _, a := range workboardActionsFor(workboard.GroupNeedsYou, d) {
			out = append(out, a.Kind)
		}
		return strings.Join(out, ",")
	}
	cases := []struct {
		name string
		d    *workboard.Detail
		want string
	}{
		{"details unknown: everything, the plugin judges", nil, "retry,review,fix,stop,archive"},
		{"nothing to send back", &workboard.Detail{}, "retry,stop,archive"},
		{"a failure", &workboard.Detail{Failure: true}, "retry,fix,stop,archive"},
		{"a review that did not pass", &workboard.Detail{ReviewFailed: true}, "retry,review,stop,archive"},
		{"review text on record", &workboard.Detail{ReviewText: "x"}, "retry,review,stop,archive"},
	}
	for _, tc := range cases {
		if got := kinds(tc.d); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
