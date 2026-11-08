//go:build e2e

// [INPUT]: real Herdr + real OMP in a throw-away HOME, a scripted loopback model, a throw-away git repo
// [OUTPUT]: AC-08 on the real stack: a coding task works in an isolated worktree, its real tests and diff are the evidence, Reject and Accept each do exactly their own thing
// [POS]: M6 gate for W06/W09/W11/Q02/Q03/Q04: `SOLCO_E2E=1 go test -tags e2e -run TestE2EAC08 ./internal/personalagent -v`
// [PROTOCOL]: never touches the user's repositories, Herdr session, OMP home or accounts
package personalagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestE2EAC08CodingTaskIsReviewedRejectedAndAcceptedOnTheRealStack(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not installed")
	}
	goEnv := append(os.Environ(), "GOTOOLCHAIN=auto")
	sb := newSandbox(t, map[string][]fixtureStep{}, e2ePage)
	pluginLayout(t, sb, goBin, goEnv)
	sb.model.delay = 2 * time.Second

	repo := newRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "value.txt"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "test.sh"), []byte("#!/bin/sh\ntest \"$(cat value.txt)\" = 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-m", "value is wrong, test.sh fails")
	if err := exec.Command("sh", filepath.Join(repo, "test.sh")).Run(); err == nil {
		t.Fatal("the fixture's bug must make its test fail before the fix")
	}
	mainBefore := mainHead(t, repo)

	fix := []fixtureStep{
		{tool: "bash", args: map[string]any{"command": "pwd; echo 2 > value.txt && git add -A && git -c user.name=Worker -c user.email=w@example.invalid commit -qm 'Fix value'"}},
		{text: "Fixed value.txt (1 → 2) and committed it on the task branch."},
	}
	sb.model.mu.Lock()
	sb.model.scripts["fixa"] = fix
	sb.model.scripts["fixb"] = fix
	sb.model.mu.Unlock()

	s, _ := openTest(t)
	a := mustAssistant(t, s, "e2e-ac08", "Ada")
	realRoot, err := filepath.EvalSymlinks(sb.root) // the worktree root may not pass through a symlink (/tmp is one on macOS)
	if err != nil {
		t.Fatal(err)
	}
	wt := &Worktrees{Store: s, Root: filepath.Join(realRoot, "worktrees")}
	rvw := &Reviewer{Store: s, Worktrees: wt}
	d := &Dispatcher{Store: s, Launcher: NewHostLauncher("", WorkDirRoot()), Claimant: "e2e", Timeout: 3 * time.Minute, Worktrees: wt}

	// The Owner approves exactly one check; it is the real test script.
	ap, _, err := s.RequestChecks("e2e-ac08-checks", a.ID, []string{"sh test.sh"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.DecideApproval("e2e-ac08-checks-dec", ap.ID, ap.ActionHash, true); err != nil {
		t.Fatal(err)
	}

	runCoding := func(id, scenario string) (Task, WorktreeInfo) {
		task, _, err := s.CreateTask(id, CreateTaskInput{AssistantID: a.ID, Kind: KindCoding, Title: "Fix value " + scenario, Model: "smoke/smoke", RepoPath: repo,
			Brief: "SCENARIO:" + scenario + " value.txt must contain 2; make test.sh pass, commit, do not merge or push."})
		if err != nil {
			t.Fatal(err)
		}
		run, err := d.RunTask(t.Context(), task.ID)
		if err != nil || run.Status != RunDone {
			t.Fatalf("%s: run %+v err=%v", scenario, run, err)
		}
		got, _ := s.GetTask(task.ID)
		if got.State != TaskReviewRequired {
			t.Fatalf("%s: a finished coding run waits for review, got %q", scenario, got.State)
		}
		info, ok, err := wt.Get(task.ID)
		if err != nil || !ok {
			t.Fatalf("%s: no worktree: %v", scenario, err)
		}
		if info.Path == repo || !strings.HasPrefix(info.Path, filepath.Join(realRoot, "worktrees")) {
			t.Fatalf("%s: the work must be isolated, ran in %s", scenario, info.Path)
		}
		if mainHead(t, repo) != mainBefore {
			t.Fatalf("%s: the main checkout moved while the Agent worked", scenario)
		}
		return got, info
	}

	// --- Reject path -------------------------------------------------------
	taskA, infoA := runCoding("e2e-ac08-a", "fixa")
	rev, _, err := rvw.Start("e2e-ac08-rev-a", taskA.ID)
	if err != nil {
		t.Fatalf("review start: %v", err)
	}
	if rev.State != ReviewReady || rev.Files != 1 || len(rev.Paths) != 1 || rev.Paths[0] != "value.txt" {
		t.Fatalf("the evidence is the real diff: %+v", rev)
	}
	if len(rev.Checks) != 1 || !rev.Checks[0].OK || rev.Checks[0].Command != "sh test.sh" {
		t.Fatalf("the real test must have run and passed: %+v", rev.Checks)
	}
	if rev, err = rvw.Reject("e2e-ac08-rej-a", rev.ID, "not what I wanted"); err != nil || rev.State != ReviewRejected {
		t.Fatalf("reject: %+v %v", rev, err)
	}
	if mainHead(t, repo) != mainBefore {
		t.Fatal("Reject moved the main checkout")
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "value.txt")); strings.TrimSpace(string(b)) != "1" {
		t.Fatalf("Reject changed the main checkout's file: %q", b)
	}
	if st, err := os.Stat(infoA.Path); err != nil || !st.IsDir() {
		t.Fatalf("a rejected task's work stays for inspection: %v", err)
	}
	if tk, _ := s.GetTask(taskA.ID); tk.State == TaskDone {
		t.Fatal("a rejected task must never read as done")
	}

	// --- Accept path -------------------------------------------------------
	taskB, infoB := runCoding("e2e-ac08-b", "fixb")
	revB, _, err := rvw.Start("e2e-ac08-rev-b", taskB.ID)
	if err != nil || revB.State != ReviewReady || len(revB.Checks) != 1 || !revB.Checks[0].OK {
		t.Fatalf("review b: %+v %v", revB, err)
	}
	if mainHead(t, repo) != mainBefore {
		t.Fatal("review moved the main checkout before anyone accepted")
	}
	revB, apM, _, err := rvw.RequestMerge("e2e-ac08-merge-req", revB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mainHead(t, repo) != mainBefore {
		t.Fatal("asking for a merge merged it")
	}
	if _, _, err := s.DecideApproval("e2e-ac08-merge-dec", apM.ID, apM.ActionHash, true); err != nil {
		t.Fatal(err)
	}
	if revB, err = rvw.Merge(revB.ID); err != nil || revB.State != ReviewMerged {
		t.Fatalf("merge: %+v %v", revB, err)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "value.txt")); strings.TrimSpace(string(b)) != "2" {
		t.Fatalf("the accepted fix is not on main: %q", b)
	}
	if err := exec.Command("sh", "-c", "cd "+repo+" && sh test.sh").Run(); err != nil {
		t.Fatalf("the real test fails on main after the merge: %v", err)
	}
	if got, _ := s.GetTask(taskB.ID); got.State != TaskDone {
		t.Fatalf("an accepted and merged task is done, got %q", got.State)
	}
	if remotes := gitT(t, repo, "remote"); remotes != "" {
		t.Fatalf("nothing may be pushed anywhere: remotes=%q", remotes)
	}
	if _, err := os.Stat(infoB.Path); err == nil {
		t.Logf("note: the merged task's worktree still exists at %s", infoB.Path)
	}
}

// AC-11 on the real stack: the Coordinator (real OMP + the real extension)
// drafts an event trigger; nothing fires until a person approves; the monitored
// task's real failure (a skill that is not installed stops its run) creates exactly one
// linked diagnostic task, that task runs on a real worker and is read back, and
// no amount of further ticking, nor the diagnosis finishing, starts another.
func TestE2EAC11TriggerFiresOneLinkedDiagnosisAndNeverRecurses(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not installed")
	}
	goEnv := append(os.Environ(), "GOTOOLCHAIN=auto")
	sb := newSandbox(t, map[string][]fixtureStep{}, e2ePage)
	pluginLayout(t, sb, goBin, goEnv)
	sb.model.delay = 2 * time.Second

	s, err := Open(DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a := mustAssistant(t, s, "e2e-ac11", "Ada")
	mon, _, err := s.CreateTask("e2e-ac11-mon", CreateTaskInput{AssistantID: a.ID, Kind: KindResearch, Title: "Monitor prod site", Model: "smoke/smoke",
		Brief: "SCENARIO:idle check the site", RequiredSkills: []string{"fixture-browser"}}) // not installed: its run will fail
	if err != nil {
		t.Fatal(err)
	}
	draft := `{"requestId":"e2e-ac11-trig-0001","name":"diagnose","on":"task.failed","taskId":"` + mon.ID + `","title":"Diagnose the failure","brief":"SCENARIO:diag Find out why the monitor failed and say what you verified."}`
	sb.model.mu.Lock()
	sb.model.scripts["*"] = []fixtureStep{
		{tool: "solco_assistant", args: map[string]any{"action": "trigger.draft", "payload": draft}},
		{text: "Drafted: when the monitor fails I will start one diagnosis. It needs your approval first."},
	}
	sb.model.scripts["diag"] = []fixtureStep{{text: "Diagnosis: the monitor's run was stopped because the fixture-browser skill is not granted. Verified from the task's recorded failure, not from a live site."}}
	sb.model.mu.Unlock()

	c, _, err := s.StartCoordinatorAs("e2e-ac11-start", a.ID, "omp", "smoke/smoke")
	if err != nil {
		t.Fatal(err)
	}
	d := &Dispatcher{Store: s, Launcher: NewHostLauncher("", WorkDirRoot()), Claimant: "e2e", Timeout: 3 * time.Minute}
	if run, err := d.RunTask(t.Context(), c.TaskID); err != nil || run.Status != RunDone {
		t.Fatalf("coordinator: %+v %v", run, err)
	}
	trs, _ := s.ListTriggers(a.ID)
	if len(trs) != 1 || trs[0].State != TriggerDraft || trs[0].TaskID != mon.ID {
		t.Fatalf("the Coordinator's draft: %+v", trs)
	}
	sch := &TriggerScheduler{Store: s}
	children := func() []Task {
		var out []Task
		for _, x := range mustTasks(t, s, a.ID) {
			if strings.HasPrefix(x.RuleID, TriggerRulePrefix) {
				out = append(out, x)
			}
		}
		return out
	}

	// The real failure happens while the trigger is still a draft: it must not fire.
	run, _ := d.RunTask(t.Context(), mon.ID)
	if run.Status != RunFailed || !strings.Contains(run.Error, "fixture-browser") {
		t.Fatalf("the monitor should fail on its missing skill: %+v", run)
	}
	if r, _ := sch.Tick(); r.Fired != 0 || len(children()) != 0 {
		t.Fatalf("a draft trigger fired: %+v", r)
	}

	// A person approves; a later failure of the monitor (a deliberate retry) fires it once.
	cards, _ := s.ListApprovals(a.ID)
	var card Approval
	for _, ap := range cards {
		if ap.SubjectKind == "trigger" {
			card = ap
		}
	}
	if card.ID == "" {
		t.Fatalf("no trigger card: %+v", cards)
	}
	if _, _, err := s.DecideApproval("e2e-ac11-yes", card.ID, card.ActionHash, true); err != nil {
		t.Fatal(err)
	}
	cur, _ := s.GetTask(mon.ID)
	if _, _, err := s.RetryTask("e2e-ac11-retry", mon.ID, cur.Revision); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if run, _ = d.RunTask(t.Context(), mon.ID); run.Status != RunFailed {
		t.Fatalf("the retry should fail the same way: %+v", run)
	}
	for i := 0; i < 3; i++ {
		if _, err := sch.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	kids := children()
	if len(kids) != 1 || kids[0].ParentTaskID != mon.ID || kids[0].Agent != AssistantAgentKind {
		t.Fatalf("one failure → one linked, managed task: %+v", kids)
	}
	if !strings.Contains(kids[0].Brief, mon.ID) {
		t.Fatalf("the diagnosis brief must name its cause: %q", kids[0].Brief)
	}

	// The diagnosis runs on a real worker; its result is read back; nothing recurses.
	run, err = d.RunTask(t.Context(), kids[0].ID)
	if err != nil || run.Status != RunDone {
		t.Fatalf("diagnosis run: %+v %v", run, err)
	}
	if res, ok, err := s.RunResult(run.ID); err != nil || !ok || !strings.Contains(res.Summary, "fixture-browser") {
		t.Fatalf("the diagnosis result must be read back: %+v %v %v", res, ok, err)
	}
	for i := 0; i < 3; i++ {
		if _, err := sch.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	if got := children(); len(got) != 1 {
		t.Fatalf("the diagnosis re-triggered itself: %d children", len(got))
	}
}
