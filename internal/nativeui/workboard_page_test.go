package nativeui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// actionKinds lists the verbs the page offers, in order.
func actionKinds(t *TaskPane) []string {
	var out []string
	for _, a := range t.Actions {
		out = append(out, a.Kind)
	}
	return out
}

func hasFact(t *TaskPane, label, value string) bool {
	for _, f := range t.Facts {
		if f.Label == label && strings.Contains(f.Value, value) {
			return true
		}
	}
	return false
}

func TestTaskPageForAReadyTaskLeadsWithApprove(t *testing.T) {
	var ran []string
	s := pageShell(t, "approval_real_claude", &ran)
	if s.router.Path() != routeTask || s.workboard.page.detail == nil {
		t.Fatalf("path=%q detail=%v: opening reads the details", s.router.Path(), s.workboard.page.detail)
	}
	v := s.projectsView()
	task := v.Task
	if v.Mode != "task" || task == nil {
		t.Fatalf("mode=%q task=%v", v.Mode, task)
	}
	if task.GroupLabel != "Ready to approve" || task.Tone != "ready" {
		t.Fatalf("group = %q/%q", task.GroupLabel, task.Tone)
	}
	if !hasFact(task, "Note", "Approve to merge? 完成。") || !hasFact(task, "Agent", "claude") || !hasFact(task, "Branch", "work/2-add-hello-file") {
		t.Fatalf("facts = %+v", task.Facts)
	}
	if !strings.Contains(task.Narrative.Description, "Create a file named hello.txt") {
		t.Fatalf("description = %q", task.Narrative.Description)
	}
	if task.Narrative.FailureTitle != "" {
		t.Fatalf("a ready task has no Failure card: %+v", task.Narrative)
	}
	kinds := actionKinds(task)
	if kinds[0] != "approve" || !task.Actions[0].Primary || !task.Actions[0].Dock || task.Actions[0].Label != "Approve…" {
		t.Fatalf("actions = %+v: Approve leads, primary, in the dock", task.Actions)
	}
	for _, k := range kinds {
		if k == "retry" {
			t.Fatalf("a ready task offers no Retry: %v", kinds)
		}
	}
	NewProjectsBridge(s).Act("approve", "repo/2")
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
	task := s.projectsView().Task
	if task.GroupLabel != "Needs you" || task.Narrative.FailureTitle != "Pull request checks failed" {
		t.Fatalf("group=%q failure=%q", task.GroupLabel, task.Narrative.FailureTitle)
	}
	kinds := strings.Join(actionKinds(task), " ")
	for _, want := range []string{"retry", "fix", "archive"} {
		if !strings.Contains(kinds, want) {
			t.Fatalf("actions %q missing %q", kinds, want)
		}
	}
	if strings.Contains(kinds, "approve") {
		t.Fatalf("a task with a failing pull request must not offer Approve: %q", kinds)
	}
}

// The plugin keeps a failure record after a Stop (real 1.2.26: details still
// carries `failure` at stage "stopped"); a stopped task is not failing now.
func TestStoppedTaskPageDropsTheOldFailureCard(t *testing.T) {
	var ran []string
	s := pageShell(t, "approval_pr_failed_polled", &ran)
	s.workboard.board.tasks[0].Stage = "stopped"
	s.workboard.board.tasks[0].State = "idle"
	task := s.projectsView().Task
	if task.Narrative.FailureTitle != "" || task.Narrative.FailureDetail != "" {
		t.Fatalf("a stopped task shows no Failure card: %+v", task.Narrative)
	}
	if !strings.Contains(strings.Join(actionKinds(task), " "), "delegate") {
		t.Fatalf("a stopped task is To do and can be delegated: %v", actionKinds(task))
	}
}

func TestTaskPageForAVanishedTaskIsGone(t *testing.T) {
	var ran []string
	s := pageShell(t, "approval_real_claude", &ran)
	s.workboard.board.tasks = nil
	if v := s.projectsView(); v.Mode != "gone" || v.Task != nil {
		t.Fatalf("a vanished task: mode=%q task=%v", v.Mode, v.Task)
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
