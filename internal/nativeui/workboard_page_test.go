package nativeui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/workboard"
)

// pageShell opens the task page for one real contract sample (list row +
// details), with the CLI faked from the same files.
func pageShell(t *testing.T, sampleName string, ran *[]string) *Shell {
	t.Helper()
	read := func(suffix string) string {
		raw, err := os.ReadFile(filepath.Join("..", "workboard", "testdata", sampleName+"."+suffix+".json"))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	newWorkboardClient = func(string, string) *workboard.Client {
		return workboard.NewClientWithExec(func(_ context.Context, args ...string) (string, error) {
			switch args[0] {
			case "list":
				return read("list"), nil
			case "details":
				return read("details"), nil
			case "automations":
				if len(args) == 2 {
					return `[]`, nil
				}
			case "autopilot":
				if len(args) == 2 {
					return `[]`, nil
				}
			case "coordinator": // the read-only status of each Project's coordinator
				return `{"state":"none","pane":""}`, nil
			}
			*ran = append(*ran, strings.Join(args, " "))
			return "", nil
		})
	}
	workboardGo = func(f func()) { f() }
	t.Cleanup(func() { newWorkboardClient, workboardGo = workboard.NewClient, func(f func()) { go f() } })
	s := supportedWorkboardShell()
	tasks, err := workboard.ParseTasks(read("list"))
	if err != nil {
		t.Fatal(err)
	}
	s.workboard.board = workboardBoard{tasks: tasks, loadedAt: timeNow()}
	s.openWorkboardTaskPage(tasks[0].ID)
	return s
}

func TestTaskPageForAReadyTaskLeadsWithApprove(t *testing.T) {
	var ran []string
	s := pageShell(t, "approval_real_claude", &ran)
	if s.router.Path() != routeTask || s.workboard.page.detail == nil {
		t.Fatalf("path=%q detail=%v: opening reads the details", s.router.Path(), s.workboard.page.detail)
	}
	tt := ui.NewTester(s.workboardTaskPage, 900, 700)
	for _, want := range []string{"Approve…", "Archive", "Ready to approve", "Approve to merge? 完成。", "claude", "work/2-add-hello-file",
		"Create a file named hello.txt"} {
		if !tt.HasText(want) {
			t.Fatalf("page missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("Retry") || tt.HasText("Failure") {
		t.Fatalf("a ready task offers neither Retry nor a Failure card: %q", tt.Texts())
	}
	if err := tt.Click("Approve…"); err != nil {
		t.Fatal(err)
	}
	if !s.confirmOpen || s.confirmKind != workboardApproveConfirm || len(ran) != 0 {
		t.Fatalf("confirm=%v kind=%q ran=%q: Approve must ask first", s.confirmOpen, s.confirmKind, ran)
	}
	if h := s.taskRouteHeader(); h.title != "Add hello file" || h.facts != "repo/2 · Ready to approve" {
		t.Fatalf("header = %+v", h)
	}
}

func TestTaskPageShowsTheFailureAndOffersNoApprove(t *testing.T) {
	var ran []string
	s := pageShell(t, "approval_pr_failed_polled", &ran)
	tt := ui.NewTester(s.workboardTaskPage, 900, 700)
	for _, want := range []string{"Needs you", "Failure", "Pull request checks failed", "Retry", "Send failure details", "Archive"} {
		if !tt.HasText(want) {
			t.Fatalf("page missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("Approve…") {
		t.Fatalf("a task with a failing pull request must not offer Approve: %q", tt.Texts())
	}
}

// The plugin keeps a failure record after a Stop (real 1.2.26: details still
// carries `failure` at stage "stopped"); a stopped task is not failing now.
func TestStoppedTaskPageDropsTheOldFailureCard(t *testing.T) {
	var ran []string
	s := pageShell(t, "approval_pr_failed_polled", &ran)
	s.workboard.board.tasks[0].Stage = "stopped"
	s.workboard.board.tasks[0].State = "idle"
	tt := ui.NewTester(s.workboardTaskPage, 900, 700)
	if tt.HasText("Failure") || tt.HasText("Send failure details") {
		t.Fatalf("a stopped task shows no Failure card: %q", tt.Texts())
	}
	if !tt.HasText("Delegate") {
		t.Fatalf("a stopped task is To do and can be delegated: %q", tt.Texts())
	}
}

func TestTaskPageForAVanishedTaskOffersBack(t *testing.T) {
	var ran []string
	s := pageShell(t, "approval_real_claude", &ran)
	s.workboard.board.tasks = nil
	tt := ui.NewTester(s.workboardTaskPage, 900, 700)
	if !tt.HasText("This task is no longer on the board") || !tt.HasText("Back") {
		t.Fatalf("vanished task: %q", tt.Texts())
	}
}

func TestArchivingFromThePageLeavesIt(t *testing.T) {
	var ran []string
	s := pageShell(t, "approval_real_claude", &ran)
	s.runWorkboardAction("archive", "repo/2")
	if len(ran) != 1 || ran[0] != "archive repo/2" {
		t.Fatalf("ran = %q", ran)
	}
	if s.router.Path() == routeTask {
		t.Fatal("an archived task's page must close")
	}
}

func TestTaskPageIsNotARestorableRoute(t *testing.T) {
	if got := persistableRoute(routeTask); got != routeWorkspace {
		t.Fatalf("persistableRoute(%q) = %q, a task page must not survive a relaunch", routeTask, got)
	}
}
