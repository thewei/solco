package workboard

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Captured from herdr-projects 1.2.26 on 2026-10-26 (`add`, then `list --json`
// and `details repo/1 --json` in a scratch HERDR_PROJECTS_HOME).
const realList = `[
  {"id":"repo/1","state":"idle","title":"Sample task one","msg":"","stage":"todo"}
]`

const realDetailsTodo = `{
  "task": {"id":"repo/1","title":"Sample task one","stage":"todo","msg":"","description":""},
  "failure": null, "review": null, "reviews": [], "agent": null, "handovers": [],
  "research": false, "chat": false, "report": null, "plan": null,
  "pr": null, "worktree": null, "branch": null, "kept": null,
  "stillRunning": null, "inUse": null,
  "limits": {"time":{"limit":"none"},"tokens":{"limit":"none","used":0,"total":0}},
  "addedBy": "the human"
}`

func TestParseRealSamples(t *testing.T) {
	tasks, err := ParseTasks(realList)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ParseTasks = %v, %v", tasks, err)
	}
	if got := tasks[0]; got.ID != "repo/1" || got.Title != "Sample task one" || got.Stage != "todo" || got.State != "idle" {
		t.Fatalf("task = %+v", got)
	}
	d, err := ParseDetail(realDetailsTodo)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d, Detail{}) {
		t.Fatalf("a fresh task has empty details, got %+v", d)
	}
	if g := GroupOf(tasks[0], &d); g != GroupTodo {
		t.Fatalf("group = %v, want To do", g)
	}
}

func TestParseTasksRejectsJunkAndSkipsIDless(t *testing.T) {
	if _, err := ParseTasks(`{"not":"a list"}`); err == nil {
		t.Fatal("an object is not a list")
	}
	if _, err := ParseTasks(``); err == nil {
		t.Fatal("empty output is not a list")
	}
	tasks, err := ParseTasks(`[{"title":"no id"},{"id":"a/1"}]`)
	if err != nil || len(tasks) != 1 || tasks[0].ID != "a/1" {
		t.Fatalf("tasks = %v, %v", tasks, err)
	}
}

func TestParseDetailShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want Detail
	}{
		{"delegated with pull request",
			`{"failure":null,"review":{"pass":true},"pr":"https://github.com/o/r/pull/18","worktree":"/w/r-1","branch":"task/1"}`,
			Detail{Worktree: "/w/r-1", Branch: "task/1", PR: "https://github.com/o/r/pull/18"}},
		{"failure object", `{"failure":{"stage":"agent","title":"x"}}`, Detail{Failure: true, FailureTitle: "x"}},
		{"tagged review failed", `{"review":{"pass":false}}`, Detail{ReviewFailed: true}},
		{"older review recommends", `{"review":{"recommend":true}}`, Detail{}},
		{"older review rejects", `{"review":{"recommend":false}}`, Detail{ReviewFailed: true}},
		{"unknown review shape fails safe", `{"review":{"verdict":"meh"}}`, Detail{ReviewFailed: true}},
		{"non-object review fails safe", `{"review":"lgtm"}`, Detail{ReviewFailed: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDetail(tc.raw)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseDetail = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
	if _, err := ParseDetail(`nope`); err == nil {
		t.Fatal("non-JSON must error")
	}
}

func TestPRNumber(t *testing.T) {
	if got := (Detail{PR: "https://github.com/o/r/pull/18"}).PRNumber(); got != "18" {
		t.Fatalf("PRNumber = %q", got)
	}
	if got := (Detail{}).PRNumber(); got != "" {
		t.Fatalf("no PR -> %q", got)
	}
}

func sample(t *testing.T, name string) (Task, Detail) {
	t.Helper()
	read := func(suffix string) string {
		raw, err := os.ReadFile(filepath.Join("testdata", name+"."+suffix+".json"))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	tasks, err := ParseTasks(read("list"))
	if err != nil || len(tasks) != 1 {
		t.Fatalf("%s list: %v, %v", name, tasks, err)
	}
	detail, err := ParseDetail(read("details"))
	if err != nil {
		t.Fatalf("%s details: %v", name, err)
	}
	return tasks[0], detail
}

// Contract: the four approval-stage shapes captured from herdr-projects 1.2.26
// on 2026-10-26 by driving its own test world (fake herdr and fake gh, scripted
// agent) to the approval stage and printing `list --json` / `details --json`
// (sources: tests/pull_requests_checks.rs scenarios). Only the genuinely ready
// ones may read "Ready to approve".
func TestApprovalSamplesFromThePluginsOwnWorld(t *testing.T) {
	cases := []struct {
		name string
		want Group
		pr   bool
	}{
		{"approval_real_claude", GroupReady, false}, // a real claude agent run (herdr-projects 1.2.26, Herdr 0.9.5)
		{"approval_local", GroupReady, false},
		{"approval_pr", GroupReady, true},
		{"approval_pr_failed_at_approve", GroupNeedsYou, true},
		{"approval_pr_failed_polled", GroupNeedsYou, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task, detail := sample(t, tc.name)
			if task.Stage != "approval" || task.State != "blocked" || task.Kind != "permission" {
				t.Fatalf("shape drifted: %+v", task)
			}
			if got := GroupOf(task, &detail); got != tc.want {
				t.Fatalf("GroupOf = %s, want %s (msg %q)", got.Label(), tc.want.Label(), task.Msg)
			}
			if (task.PR != "") != tc.pr || (detail.PR != "") != tc.pr {
				t.Fatalf("pr: list %q details %q, want present=%v", task.PR, detail.PR, tc.pr)
			}
			if detail.Worktree == "" || detail.Branch == "" {
				t.Fatalf("a delegated task has a worktree and a branch: %+v", detail)
			}
		})
	}
	if _, d := sample(t, "approval_pr_failed_at_approve"); !d.Failure {
		t.Fatal("a checks failure at approve records details.failure")
	}
	if tk, _ := sample(t, "approval_local"); !strings.HasPrefix(tk.Msg, "Approve to merge?") {
		t.Fatalf("ready note drifted: %q", tk.Msg)
	}
}

// The derivation's one rule: ready to approve only when everything is proven;
// every doubt reads as "needs you".
func TestGroupOf(t *testing.T) {
	note := "Approve to merge? Review: Pass. Did v1."
	ready := Task{ID: "r/1", Stage: "approval", State: "blocked", Kind: "permission", Msg: note}
	clean := &Detail{}
	cases := []struct {
		name string
		task Task
		det  *Detail
		want Group
	}{
		{"all conditions hold", ready, clean, GroupReady},
		{"with a pull request", Task{ID: "r/1", Stage: "approval", State: "blocked", Kind: "permission", Msg: "Approve to merge? Did v1. Pull request: https://x/pull/1", PR: "https://x/pull/1"}, &Detail{PR: "https://x/pull/1"}, GroupReady},
		{"details not read yet", ready, nil, GroupNeedsYou},
		{"recorded failure", ready, &Detail{Failure: true}, GroupNeedsYou},
		{"failed review", ready, &Detail{ReviewFailed: true}, GroupNeedsYou},
		// The hidden-flag case: the plugin's poll sets prFailure and replaces the
		// note while details.failure stays null (housekeeping/prs.rs).
		{"note overwritten by a failing pull request, failure still null",
			Task{ID: "r/1", Stage: "approval", State: "blocked", Kind: "permission", Msg: "Pull request checks failed: build. Send details to the agent, or wait for remote confirmation", PR: "https://x/pull/1"},
			&Detail{PR: "https://x/pull/1"}, GroupNeedsYou},
		{"still blocked note", Task{ID: "r/1", Stage: "approval", State: "blocked", Kind: "permission", Msg: "Local checks passed; pull request still blocked: x"}, clean, GroupNeedsYou},
		{"no note at all", Task{ID: "r/1", Stage: "approval", State: "blocked", Kind: "permission"}, clean, GroupNeedsYou},
		{"asking a question at approval", Task{ID: "r/1", Stage: "approval", State: "blocked", Kind: "question", Msg: note}, clean, GroupNeedsYou},
		{"permission outside approval (plan stage)", Task{ID: "r/1", Stage: "plan", State: "blocked", Kind: "permission", Msg: note}, clean, GroupNeedsYou},
		{"approval stage but not blocked", Task{ID: "r/1", Stage: "approval", State: "idle", Msg: note}, clean, GroupTodo},
		{"blocked on auth", Task{ID: "r/1", Stage: "working", State: "blocked", Kind: "auth"}, nil, GroupNeedsYou},
		{"error", Task{ID: "r/1", Stage: "working", State: "error"}, nil, GroupNeedsYou},
		{"exited mid-flight", Task{ID: "r/1", Stage: "working", State: "exited"}, nil, GroupNeedsYou},
		{"never started and exited", Task{ID: "r/1", Stage: "todo", State: "exited"}, nil, GroupTodo},
		{"working", Task{ID: "r/1", Stage: "working", State: "working"}, nil, GroupWorking},
		{"to do", Task{ID: "r/1", Stage: "todo", State: "idle"}, nil, GroupTodo},
		{"done by stage", Task{ID: "r/1", Stage: "done", State: "idle"}, nil, GroupDone},
		{"done by state", Task{ID: "r/1", Stage: "working", State: "done"}, nil, GroupDone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := GroupOf(tc.task, tc.det); got != tc.want {
				t.Fatalf("GroupOf = %v (%s), want %v (%s)", got, got.Label(), tc.want, tc.want.Label())
			}
		})
	}
}

// ReadyCandidate must be the only path to GroupReady.
func TestOnlyReadyCandidatesCanBeReady(t *testing.T) {
	for _, stage := range []string{"", "todo", "working", "plan", "approval", "done", "stopped"} {
		for _, state := range []string{"", "idle", "working", "blocked", "error", "exited", "done", "none"} {
			for _, kind := range []string{"", "question", "permission", "auth"} {
				for _, msg := range []string{"", "Approve to merge? Did v1.", "  Approve to merge? x", "Approve to merge", "Checks failed"} {
					task := Task{ID: "r/1", Stage: stage, State: state, Kind: kind, Msg: msg}
					if GroupOf(task, &Detail{}) == GroupReady && !task.ReadyCandidate() {
						t.Fatalf("%+v became Ready without being a candidate", task)
					}
				}
			}
		}
	}
}

// The display facts come from the same real samples; a shape we do not know
// leaves them empty and never fails the parse.
func TestParseDetailDisplayFacts(t *testing.T) {
	_, local := sample(t, "approval_local")
	if local.ReviewBy != "codex" || local.ReviewText != "Looks right. Nothing found." || local.ReviewFindings != 0 || local.Agent != "claude" {
		t.Fatalf("review facts = %+v", local)
	}
	_, failed := sample(t, "approval_pr_failed_polled")
	if failed.FailureTitle != "Pull request checks failed" ||
		!strings.Contains(failed.FailureDetail, "test parses_flags ... FAILED") ||
		!strings.HasPrefix(failed.FailureNext, "Address this on your own branch") {
		t.Fatalf("failure facts = %+v", failed)
	}
	_, claude := sample(t, "approval_real_claude")
	if claude.Description != "Create a file named hello.txt containing the single word hi, and commit it. Do nothing else." {
		t.Fatalf("description = %q", claude.Description)
	}
	odd, err := ParseDetail(`{"task":"x","agent":3,"failure":"boom","review":[1]}`)
	if err != nil || !odd.Failure || !odd.ReviewFailed || odd.Description != "" || odd.FailureTitle != "" || odd.ReviewText != "" {
		t.Fatalf("odd shapes: %+v err=%v", odd, err)
	}
}

func TestTaskProjectIsTheRepoPartOfTheID(t *testing.T) {
	for id, want := range map[string]string{"app/12": "app", "my-repo/3": "my-repo", "~/4": "~", "x": "", "/5": "", "": ""} {
		if got := (Task{ID: id}).Project(); got != want {
			t.Errorf("Project(%q) = %q, want %q", id, got, want)
		}
	}
}

// Row shape per the plugin's state.rs (stage "plan" -> blocked + permission,
// msg = the plan's first words; stage "pick" -> blocked + permission) and its
// list brief (app, queue, reviewRound, research, chat). Hand-built from the
// source, not captured: the plugin needs a live agent to reach these stages.
const stageRows = `[
 {"id":"r/1","state":"blocked","kind":"permission","app":"claude","title":"Plan me","msg":"Add a cache layer","stage":"plan"},
 {"id":"r/2","state":"blocked","kind":"permission","title":"Pick me","msg":"3 attempts passed","stage":"pick"},
 {"id":"r/3","state":"working","app":"codex","title":"Waits","msg":"Waiting for a free slot","stage":"queued","queue":2},
 {"id":"r/4","state":"working","app":"claude","title":"Reviewed","stage":"reviewing","reviewRound":3,"research":true,"chat":true}
]`

func TestTaskReadsPlanPickQueueAndRoundFields(t *testing.T) {
	tasks, err := ParseTasks(stageRows)
	if err != nil || len(tasks) != 4 {
		t.Fatalf("tasks=%v err=%v", tasks, err)
	}
	if !tasks[0].PlanReady() || tasks[0].NeedsPick() || tasks[0].App != "claude" {
		t.Fatalf("plan row = %+v", tasks[0])
	}
	if !tasks[1].NeedsPick() || tasks[1].PlanReady() {
		t.Fatalf("pick row = %+v", tasks[1])
	}
	if tasks[2].Queue != 2 || tasks[2].App != "codex" || GroupOf(tasks[2], nil) != GroupWorking {
		t.Fatalf("queued row = %+v group=%v", tasks[2], GroupOf(tasks[2], nil))
	}
	if tasks[3].ReviewRound != 3 || !tasks[3].Research || !tasks[3].Chat {
		t.Fatalf("flags row = %+v", tasks[3])
	}
	// A plan or pick is the human's turn: never Ready, never To do.
	for _, i := range []int{0, 1} {
		if g := GroupOf(tasks[i], nil); g != GroupNeedsYou {
			t.Fatalf("row %d group = %v, want NeedsYou", i, g)
		}
	}
}

// Hand-built from finish_line::details (plan.text, reviews[], report.link,
// limits.{time,tokens,stopped}, handovers[].words, attempts[], queue.words):
// the plugin needs live agents to produce the real thing.
const richDetails = `{
 "task":{"id":"r/1","title":"T","description":"d"},
 "review":{"by":"codex","text":"Looks risky","pass":false,"meets":true,"findings":[
   {"severity":"major","subject":"Misses the cache","where":"plan step 2","proof":"read store.go","preexisting":false},
   {"severity":"minor","subject":"Vague","where":"","proof":""}]},
 "reviews":[{"meets":true,"pass":false,"text":"first","findings":[{"severity":"major","subject":"a"}]},
            {"meets":true,"pass":true,"text":"second","findings":[]}],
 "plan":{"text":"1. do it\n2. test it","approvedAt":null},
 "report":{"file":"/Users/x/.herdr-projects/reports/r/1/index.html","link":"http://127.0.0.1:8123/r/1"},
 "limits":{"time":{"limit":"2h","used":"1h12m","usedMs":4320000},"tokens":{"limit":"200k","used":40123.0,"total":9},"stopped":{"line":"Stopped at its time limit"}},
 "handovers":[{"role":"worker","from":"claude","to":"codex","why":"out","words":"codex took over from claude: out"}],
 "attempts":[{"letter":"a","agent":"claude","standing":"passed","stat":"+10 -2"},{"letter":"b","agent":"codex","standing":"out","stat":""}],
 "queue":{"place":2,"words":"waiting for a free slot, Q#2"}
}`

func TestParseDetailReadsTheTaskPageSections(t *testing.T) {
	d, err := ParseDetail(richDetails)
	if err != nil {
		t.Fatal(err)
	}
	if d.Plan != "1. do it\n2. test it" || len(d.Findings) != 2 || d.Findings[0].Severity != "major" || d.Findings[0].Where != "plan step 2" {
		t.Fatalf("plan=%q findings=%+v", d.Plan, d.Findings)
	}
	if len(d.Reviews) != 2 || !d.Reviews[0].Failed || d.Reviews[1].Failed || d.Reviews[1].Text != "second" {
		t.Fatalf("reviews = %+v", d.Reviews)
	}
	if d.ReportLink != "http://127.0.0.1:8123/r/1" || d.LimitTime != "1h12m of 2h" || d.LimitTokens != "40.1k of 200k" || d.LimitStopped != "Stopped at its time limit" {
		t.Fatalf("report=%q time=%q tokens=%q stop=%q", d.ReportLink, d.LimitTime, d.LimitTokens, d.LimitStopped)
	}
	if len(d.Handovers) != 1 || d.Handovers[0] != "codex took over from claude: out" || len(d.Attempts) != 2 || d.Attempts[0].Standing != "passed" || d.QueueWords != "waiting for a free slot, Q#2" {
		t.Fatalf("handovers=%v attempts=%+v queue=%q", d.Handovers, d.Attempts, d.QueueWords)
	}
	// Grouping inputs are untouched by the new fields.
	if !d.ReviewFailed || d.ReviewFindings != 2 {
		t.Fatalf("review verdict = %v/%d", d.ReviewFailed, d.ReviewFindings)
	}
}

func TestParseDetailShrugsOffOtherShapes(t *testing.T) {
	d, err := ParseDetail(`{"plan":"x","reviews":[7,null],"report":[1],"limits":{"time":"no","tokens":3},"handovers":[{"words":5}],"attempts":"?","queue":4,"review":{"findings":"?"}}`)
	if err == nil && (d.Plan != "" || d.ReportLink != "" || d.LimitTime != "") {
		t.Fatalf("unexpected: %+v", d)
	}
	// Nothing in it may panic or leak a half-read section; absent sections stay empty.
	d, err = ParseDetail(`{"task":{"id":"r/1"}}`)
	if err != nil || d.Plan != "" || len(d.Reviews) != 0 || d.LimitTime != "" || d.LimitTokens != "" {
		t.Fatalf("empty = %+v err=%v", d, err)
	}
	if got := tokensSay(1234567); got != "1.23m" {
		t.Fatalf("tokensSay = %q", got)
	}
	if got := tokensSay(950); got != "950" {
		t.Fatalf("tokensSay = %q", got)
	}
}

func TestTaskTagsAreThePluginsOwnMarks(t *testing.T) {
	tk := Task{App: "claude", ReviewRound: 2, PR: "https://github.com/o/r/pull/18", Queue: 1}
	if got := strings.Join(tk.Tags(), " "); got != "claude R#2 PR#18 Q#1" {
		t.Fatalf("tags = %q", got)
	}
	if len((Task{}).Tags()) != 0 {
		t.Fatal("a bare row wears no marks")
	}
}
