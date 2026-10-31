package nativeui

import (
	"context"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/thewei/solco/internal/workboard"
)

// planRows: a written plan, several passed attempts, a to-do (shape per the
// plugin's state.rs; see workboard.stageRows).
const planRows = `[
 {"id":"r/1","state":"blocked","kind":"permission","app":"claude","title":"Plan me","msg":"Add a cache layer","stage":"plan"},
 {"id":"r/2","state":"blocked","kind":"permission","title":"Pick me","msg":"3 attempts passed","stage":"pick"},
 {"id":"r/3","state":"idle","title":"Later","stage":"todo"}
]`

// Details of the plan task, shaped by the plugin's finish_line::details.
const planDetails = `{"task":{"id":"r/1","description":"Add caching"},
 "plan":{"text":"1. Add the cache\n2. Test it"},
 "review":{"pass":false,"meets":true,"by":"codex","text":"Risky","findings":[{"severity":"major","subject":"Misses invalidation","where":"step 2","proof":"read cache.go"}]},
 "reviews":[{"pass":false,"meets":true,"findings":[{"severity":"major","subject":"x"}]},{"pass":false,"meets":true,"findings":[{"severity":"major","subject":"Misses invalidation"}]}],
 "report":{"link":"http://127.0.0.1:8123/r/1"},
 "limits":{"time":{"limit":"2h","used":"1h"},"tokens":{"limit":"200k","used":50000}},
 "handovers":[{"words":"codex took over from claude: out"}],
 "attempts":[{"letter":"a","agent":"claude","standing":"passed","stat":"+3 -1"}],
 "queue":{"words":"waiting for a free slot, Q#2"}}`

var planAutopilot = `[]`

var planCoordinator = `{"name":"r","state":"idle","agent":"claude","pane":"p1","said":"Two tasks need you."}`

func planShell(t *testing.T, ran *[]string) *Shell {
	t.Helper()
	list := planRows
	newWorkboardClient = func(string, string) *workboard.Client {
		return workboard.NewClientWithExec(func(_ context.Context, args ...string) (string, error) {
			switch {
			case args[0] == "list" && len(args) > 1 && args[1] == "--pinned":
				return `{"machine":null,"projects":[],"tasks":[]}`, nil
			case args[0] == "list":
				return list, nil
			case args[0] == "details" && args[1] == "r/1":
				return planDetails, nil
			case args[0] == "details":
				return `{"task":{"id":"` + args[1] + `"}}`, nil
			case args[0] == "automations" && len(args) == 2:
				return `[]`, nil
			case args[0] == "autopilot" && len(args) == 2:
				return planAutopilot, nil
			case args[0] == "coordinator":
				return planCoordinator, nil
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

func kinds(as []boardWrite) string {
	var out []string
	for _, a := range as {
		out = append(out, a.Kind)
	}
	return strings.Join(out, ",")
}

func TestPlanReadyOffersApprovePlanNotRetry(t *testing.T) {
	var ran []string
	s := planShell(t, &ran)
	byID := map[string]workboard.Task{}
	for _, task := range s.workboard.board.tasks {
		byID[task.ID] = task
	}
	plan, pick := byID["r/1"], byID["r/2"]
	if g := s.workboardTaskGroup(plan); g != workboard.GroupNeedsYou {
		t.Fatalf("plan group = %v", g)
	}
	if got := kinds(workboardActionsForTask(plan, workboard.GroupNeedsYou, nil)); got != "approve-plan,plan-back,stop,archive" {
		t.Fatalf("plan actions = %q", got)
	}
	if got := kinds(workboardActionsForTask(pick, workboard.GroupNeedsYou, nil)); got != "pick,stop,archive" {
		t.Fatalf("pick actions = %q (no recovery verb fits an attempt pick)", got)
	}
	if got := kinds(workboardActionsForTask(byID["r/3"], workboard.GroupTodo, nil)); got != "delegate,delegate-with,archive" {
		t.Fatalf("a plain to-do keeps its group's actions: %q", got)
	}
}

func TestApprovePlanAsksFirstAndOnlyConfirmRuns(t *testing.T) {
	var ran []string
	s := planShell(t, &ran)
	s.startWorkboardAction(boardWrite{"Approve Plan…", "approve-plan"}, workboard.Task{ID: "r/1", Title: "Plan me"})
	if !s.confirmOpen || s.confirmKind != workboardApprovePlanConfirm || s.confirmTarget != "r/1" || len(ran) != 0 {
		t.Fatalf("confirm=%v kind=%q target=%q ran=%q", s.confirmOpen, s.confirmKind, s.confirmTarget, ran)
	}
	s.submitConfirm(s.confirmKind, s.confirmTarget)
	if got := strings.Join(ran, "|"); got != "approve r/1" {
		t.Fatalf("ran = %q", got)
	}
	// A task that is no longer at its plan is refused and says so.
	ran = nil
	s.pendingToast = ""
	s.submitConfirm(workboardApprovePlanConfirm, "r/2")
	if len(ran) != 0 || s.pendingToast != workboardNotReadyToast {
		t.Fatalf("ran=%q toast=%q", ran, s.pendingToast)
	}
}

func TestSendPlanBackAsksForWordsThenTells(t *testing.T) {
	var ran []string
	s := planShell(t, &ran)
	s.startWorkboardAction(boardWrite{"Send Plan Back…", "plan-back"}, workboard.Task{ID: "r/1"})
	if !s.dialogOpen || s.dialogKind != workboardPlanBackDialog || s.dialogTarget != "r/1" || s.dialogAction != "Send" {
		t.Fatalf("dialog = %v %q %q %q", s.dialogOpen, s.dialogKind, s.dialogTarget, s.dialogAction)
	}
	s.submitTextDialog(s.dialogKind, s.dialogTarget, "Use a smaller scope")
	if got := strings.Join(ran, "|"); got != "tell r/1 Use a smaller scope" {
		t.Fatalf("ran = %q", got)
	}
	if s.pendingToast != "Sent the plan of r/1 back" {
		t.Fatalf("toast = %q", s.pendingToast)
	}
}

func TestTaskPageShowsPlanVerbsAndPickFallback(t *testing.T) {
	var ran []string
	s := planShell(t, &ran)
	s.openWorkboardTaskPage("r/1")
	task := s.projectsView().Task
	kinds := strings.Join(actionKinds(task), " ")
	for _, want := range []string{"approve-plan", "plan-back"} {
		if !strings.Contains(kinds, want) {
			t.Fatalf("plan page missing %q: %q", want, kinds)
		}
	}
	for _, bad := range []string{"retry", "review", "fix"} {
		if strings.Contains(kinds, bad) {
			t.Fatalf("plan page offers %q: %q", bad, kinds)
		}
	}
	if task.DockTitle != "Plan ready" {
		t.Fatalf("dock = %q", task.DockTitle)
	}
	s.openWorkboardTaskPage("r/2")
	task = s.projectsView().Task
	kinds = strings.Join(actionKinds(task), " ")
	if !strings.Contains(kinds, "pick") || strings.Contains(kinds, "retry") || task.DockTitle != "Attempts to compare" {
		t.Fatalf("pick page: %q / %q", kinds, task.DockTitle)
	}
}

func TestTaskPageReadsPlanReviewReportLimitsHandoversAttempts(t *testing.T) {
	var ran []string
	s := planShell(t, &ran)
	s.openWorkboardTaskPage("r/1")
	task := s.projectsView().Task
	if !strings.Contains(task.Narrative.Plan, "1. Add the cache") || task.ReportLink != "http://127.0.0.1:8123/r/1" {
		t.Fatalf("plan=%q report=%q", task.Narrative.Plan, task.ReportLink)
	}
	for _, f := range [][2]string{{"Time limit", "1h of 2h"}, {"Token limit", "50k of 200k"}, {"Queue", "waiting for a free slot, Q#2"}} {
		if !hasFact(task, f[0], f[1]) {
			t.Fatalf("fact %v missing: %+v", f, task.Facts)
		}
	}
	if got := strings.Join(task.Narrative.Handovers, "|"); !strings.Contains(got, "codex took over from claude: out") {
		t.Fatalf("handovers = %q", got)
	}
	if len(task.Narrative.Attempts) == 0 || task.Narrative.Attempts[0] != (AttemptPane{Letter: "A", Agent: "claude", Standing: "passed", Stat: "+3 -1"}) {
		t.Fatalf("attempts = %+v", task.Narrative.Attempts)
	}
	review := task.Review
	if review == nil || len(review.Findings) == 0 {
		t.Fatalf("review = %+v", review)
	}
	f := review.Findings[0]
	if f.Severity != "MAJOR" || f.Subject != "Misses invalidation" || f.Where != "step 2" || f.Proof != "read cache.go" {
		t.Fatalf("finding = %+v", f)
	}
	if len(review.Rounds) != 1 || review.Rounds[0] != "Round 1 · Did not pass · 1 finding" {
		t.Fatalf("rounds = %v", review.Rounds)
	}
	// OpenReport goes to the browser for a served link and nothing else is run.
	var opened string
	openBoardURL = func(u string) error { opened = u; return nil }
	t.Cleanup(func() { openBoardURL = func(u string) error { return mygo.Shell.OpenExternal(u) } })
	NewProjectsBridge(s).OpenReport(task.ReportLink)
	if opened != "http://127.0.0.1:8123/r/1" || len(ran) != 0 {
		t.Fatalf("opened=%q ran=%q", opened, ran)
	}
	NewProjectsBridge(s).OpenReport("javascript:alert(1)")
	if opened != "http://127.0.0.1:8123/r/1" || s.pendingToast != "Could not open the report" {
		t.Fatalf("a non-http, non-path link must be refused: toast=%q", s.pendingToast)
	}
}

func TestTaskPageDockHoldsTheDecisionOnce(t *testing.T) {
	var ran []string
	s := pageShell(t, "approval_real_claude", &ran)
	task := s.projectsView().Task
	n, docked := 0, 0
	for _, a := range task.Actions {
		if a.Kind == "approve" {
			n++
			if a.Dock {
				docked++
			}
		}
	}
	if n != 1 || docked != 1 || task.DockTitle != "Ready to approve" {
		t.Fatalf("approve offered %d times (%d docked), dock=%q", n, docked, task.DockTitle)
	}
}

func TestRowsWearTheMarksAndTheProjectViewCarriesWhatTheCoordinatorSaid(t *testing.T) {
	var ran []string
	s := planShell(t, &ran)
	s.workboard.board.tasks[0].ReviewRound = 2
	s.workboard.board.tasks[0].PR = "https://github.com/o/r/pull/18"
	s.openWorkboardProjectPage("r")
	v := s.projectsView()
	var row *TaskRow
	for gi := range v.Project.Groups {
		for ti := range v.Project.Groups[gi].Tasks {
			if v.Project.Groups[gi].Tasks[ti].ID == "r/1" {
				row = &v.Project.Groups[gi].Tasks[ti]
			}
		}
	}
	if row == nil || row.Round != 2 || row.PR != "18" || row.App != "claude" || row.Msg != "Add a cache layer" {
		t.Fatalf("row = %+v", row)
	}
	if v.Project.Coordinator.Said != "Two tasks need you." {
		t.Fatalf("said = %q", v.Project.Coordinator.Said)
	}
	planCoordinator = `{"name":"r","state":"none"}`
	t.Cleanup(func() {
		planCoordinator = `{"name":"r","state":"idle","agent":"claude","pane":"p1","said":"Two tasks need you."}`
	})
	s.refreshWorkboardTasks(true)
	if got := s.projectsView().Project.Coordinator; got.Said != "" || got.Running {
		t.Fatalf("no said, not running: %+v", got)
	}
}
