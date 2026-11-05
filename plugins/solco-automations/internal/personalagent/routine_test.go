package personalagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
)

// U4: a Routine is an automation owned by an Assistant, drafted from chat,
// enabled only by a person's answer to an approval bound to the exact
// definition, fired by the one rule scheduler, reported to its owner.

type routineEnv struct {
	s    *Store
	api  *API
	a    Assistant
	repo string
	clk  *testClock
}

type testClock struct{ t time.Time }

func newRoutineEnv(t *testing.T) *routineEnv {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", dir)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)
	s, _ := openTest(t)
	clk := &testClock{t: at(10, 0)}
	return &routineEnv{s: s, a: mustAssistant(t, s, "ra", "Ada"), repo: t.TempDir(), clk: clk,
		api: &API{Store: s, Now: func() time.Time { return clk.t }}}
}

func (e *routineEnv) draft(t *testing.T, req string, mut func(m map[string]any)) (map[string]any, error) {
	t.Helper()
	m := map[string]any{"requestId": req, "assistantId": e.a.ID, "name": "morning-check", "cron": "0 9 * * *", "repo": e.repo,
		"prompt": "Check the repository for risky changes and summarise.", "rationale": "The user asked for a daily check."}
	if mut != nil {
		mut(m)
	}
	raw, _ := json.Marshal(m)
	return call(t, e.api, string(raw), "assistant", "routine", "draft")
}

func (e *routineEnv) approve(t *testing.T, approvalID, hash string) {
	t.Helper()
	body := fmt.Sprintf(`{"requestId":"d-%s","actionHash":%q,"approve":true}`, approvalID, hash)
	if _, err := call(t, e.api, body, "assistant", "approval", "decide", approvalID); err != nil {
		t.Fatalf("decide: %v", err)
	}
}

// A personal, non-repository scheduled request must not force the Coordinator
// to invent an OS scheduler or choose a non-OMP agent to get past validation.
func TestRoutineDraftForPersonalResearchUsesOwnedFolderAndOMP(t *testing.T) {
	e := newRoutineEnv(t)
	owned := t.TempDir()
	t.Setenv("SOLCO_ASSISTANTS_WORKDIR", owned)
	got, err := e.draft(t, "personal-x-digest", func(m map[string]any) {
		delete(m, "repo")
		m["prompt"] = "Use the ego-browser skill to read X.com and summarise high-engagement posts with links."
		m["rationale"] = "The person requested a recurring X.com digest."
	})
	if err != nil {
		t.Fatal(err)
	}
	rt := got["routine"].(map[string]any)
	if rt["agent"] != AssistantAgentKind || rt["headless"] != true {
		t.Fatalf("expected unattended OMP, got %v", rt)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	auto := cfg.Find("morning-check")
	if auto == nil || auto.Agent != AssistantAgentKind || auto.Workspace != config.WorkspaceRoot ||
		auto.Repo != filepath.Join(owned, "personal", "routines", e.a.ID) || !auto.Draft {
		t.Fatalf("wrong personal routine definition: %+v", auto)
	}
	if st, err := os.Stat(auto.Repo); err != nil || !st.IsDir() {
		t.Fatalf("personal routine work directory absent: %v", err)
	}
	if tasks, err := e.s.ListTasks(e.a.ID); err != nil || len(tasks) != 0 {
		t.Fatalf("unapproved personal routine emitted tasks: %d, %v", len(tasks), err)
	}
	// A provider named explicitly cannot bypass the rollout's OMP gate.
	if _, err := e.draft(t, "personal-claude", func(m map[string]any) {
		m["name"] = "wrong-provider"
		m["agent"] = "claude"
	}); err == nil {
		t.Fatal("a non-OMP scheduled worker was accepted")
	}
}

func TestRoutineDraftNeverFiresAndWaitsForAPerson(t *testing.T) {
	e := newRoutineEnv(t)
	got, err := e.draft(t, "r1", nil)
	if err != nil {
		t.Fatal(err)
	}
	rt := got["routine"].(map[string]any)
	ap := got["approval"].(map[string]any)
	if rt["state"] != RoutineDraft || rt["assistantId"] != e.a.ID || rt["requestedBy"] != "assistant" || rt["source"] != "chat" {
		t.Fatalf("routine = %v", rt)
	}
	if ap["state"] != ApprovalPending || ap["subjectKind"] != "routine" || !strings.Contains(ap["action"].(string), "unattended") {
		t.Fatalf("approval = %v", ap)
	}
	if rt["headless"] != true || len(got["next"].([]any)) != 3 {
		t.Fatalf("a routine runs unattended by default and shows its next runs: %v", got)
	}
	cfg, _ := config.Load()
	a := cfg.Find("morning-check")
	if a == nil || !a.Draft || a.Rationale == "" {
		t.Fatalf("yaml = %+v", a)
	}
	if owned, _ := e.s.OwnedLegacy(); !owned["morning-check"] {
		t.Fatal("the rule scheduler must own the draft from the start (the old scheduler may never fire it)")
	}
	// The cron time passes: a draft produces no task.
	rs := &RuleScheduler{Store: e.s, Load: config.Load, Now: func() time.Time { return at(10, 0).Add(24*time.Hour - time.Hour + 5*time.Second) }}
	if r, err := rs.Tick(); err != nil || r.Fired != 0 {
		t.Fatalf("draft fired: %+v %v", r, err)
	}
	if ts, _ := e.s.ListTasks(""); len(ts) != 0 {
		t.Fatalf("tasks = %d", len(ts))
	}

	// The same request again changes nothing (one card, one entry).
	again, err := e.draft(t, "r1", nil)
	if err != nil || again["replayed"] != true || again["approval"].(map[string]any)["id"] != ap["id"] {
		t.Fatalf("replay = %v %v", again, err)
	}
	if aps, _ := e.s.ListApprovals(e.a.ID); len(aps) != 1 {
		t.Fatalf("approvals = %d", len(aps))
	}
	if cfg, _ := config.Load(); len(cfg.Automations) != 1 {
		t.Fatalf("automations = %d", len(cfg.Automations))
	}
}

func TestRoutineDraftRefusals(t *testing.T) {
	e := newRoutineEnv(t)
	cases := map[string]func(m map[string]any){
		"bad name":      func(m map[string]any) { m["name"] = "Bad Name" },
		"relative repo": func(m map[string]any) { m["repo"] = "relative/path" },
		"no folder":     func(m map[string]any) { m["repo"] = "/definitely/not/here" },
		"no rationale":  func(m map[string]any) { m["rationale"] = " " },
		"no prompt":     func(m map[string]any) { m["prompt"] = "" },
		"bad cron":      func(m map[string]any) { m["cron"] = "every day" },
		"no assistant":  func(m map[string]any) { m["assistantId"] = "" },
	}
	for name, mut := range cases {
		if _, err := e.draft(t, "", mut); codeOf(err) != "invalid" {
			t.Errorf("%s: code = %q (%v)", name, codeOf(err), err)
		}
	}
	if cfg, _ := config.Load(); len(cfg.Automations) != 0 {
		t.Fatalf("a refused draft left %d automation(s) behind", len(cfg.Automations))
	}
	// A person's own automation with that name is never taken over.
	if err := os.WriteFile(config.Path(), []byte("automations:\n  - name: mine\n    cron: \"0 9 * * *\"\n    repo: "+e.repo+"\n    prompt: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.draft(t, "", func(m map[string]any) { m["name"] = "mine" }); codeOf(err) != "invalid" {
		t.Fatalf("existing name = %q", codeOf(err))
	}
	// Another Assistant cannot redraft someone else's routine.
	if _, err := e.draft(t, "", nil); err != nil {
		t.Fatal(err)
	}
	other := mustAssistant(t, e.s, "rb", "Bo")
	if _, err := e.draft(t, "", func(m map[string]any) { m["assistantId"] = other.ID }); codeOf(err) != "invalid" {
		t.Fatalf("cross-owner = %q", codeOf(err))
	}
}

func TestApprovedRoutineFiresOnceUnderItsOwnerAndReportsBack(t *testing.T) {
	e := newRoutineEnv(t)
	got, err := e.draft(t, "r1", nil)
	if err != nil {
		t.Fatal(err)
	}
	ap := got["approval"].(map[string]any)
	e.approve(t, ap["id"].(string), ap["actionHash"].(string))

	r, err := e.s.GetRoutine("morning-check")
	if err != nil || r.State != RoutineActive {
		t.Fatalf("routine after approval = %+v %v", r, err)
	}
	if cfg, _ := config.Load(); cfg.Find("morning-check").Draft {
		t.Fatal("the automation is still a draft")
	}

	next := at(9, 0).Add(24 * time.Hour) // 2027-03-02 09:00
	rs := &RuleScheduler{Store: e.s, Load: config.Load, Now: func() time.Time { return next.Add(5 * time.Second) }}
	rep, err := rs.Tick()
	if err != nil || rep.Fired != 1 {
		t.Fatalf("tick = %+v %v", rep, err)
	}
	if rep, _ = rs.Tick(); rep.Fired != 0 {
		t.Fatalf("the same occurrence fired twice: %+v", rep)
	}
	tasks, _ := e.s.ListTasks(e.a.ID)
	if len(tasks) != 1 || tasks[0].RuleID != LegacyRuleID("morning-check") || tasks[0].AssistantID != e.a.ID {
		t.Fatalf("owner's tasks = %+v", tasks)
	}
	if sys, err := e.s.AutomationsAssistant(); err == nil {
		if st, _ := e.s.ListTasks(sys.ID); len(st) != 0 {
			t.Fatalf("the system assistant also got %d task(s)", len(st))
		}
	}

	// The run finishes: the owner's ledger says so — a system fact, not an Agent's words.
	finishTaskAs(t, e.s, tasks[0].ID, RunDone)
	sv := &Supervisor{Store: e.s}
	if _, err := sv.Tick(); err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	fs, _ := e.s.Facts(e.a.ID, "", 50)
	for _, f := range fs {
		kinds = append(kinds, f.Kind)
	}
	if strings.Join(kinds, ",") != "routine_done,routine,routine" {
		t.Fatalf("facts = %v", kinds) // newest first: finished, enabled, drafted
	}
	if fs[0].TaskID != tasks[0].ID || !strings.Contains(fs[0].Text, "morning-check") {
		t.Fatalf("fact = %+v", fs[0])
	}
	// Reading it again adds nothing.
	if _, err := sv.Tick(); err != nil {
		t.Fatal(err)
	}
	if again, _ := e.s.Facts(e.a.ID, "", 50); len(again) != len(fs) {
		t.Fatalf("facts grew to %d", len(again))
	}
}

// A scheduled run that stopped on a gate says which one, so the person reading
// the ledger knows an approval card is waiting (real-device AC-02: the fact used
// to read "failed" while the reason sat only in the run row).
func TestFailedRoutineFactNamesTheStableReason(t *testing.T) {
	e := newRoutineEnv(t)
	got, err := e.draft(t, "r-why", nil)
	if err != nil {
		t.Fatal(err)
	}
	ap := got["approval"].(map[string]any)
	e.approve(t, ap["id"].(string), ap["actionHash"].(string))
	rs := &RuleScheduler{Store: e.s, Load: config.Load, Now: func() time.Time { return at(9, 0).Add(24*time.Hour + 5*time.Second) }}
	if rep, err := rs.Tick(); err != nil || rep.Fired != 1 {
		t.Fatalf("tick = %+v %v", rep, err)
	}
	tasks, _ := e.s.ListTasks(e.a.ID)
	r, _, err := e.s.ClaimTask(tasks[0].ID, "d")
	if err != nil {
		t.Fatal(err)
	}
	_ = e.s.MarkStarting(r.ID)
	if err := e.s.FinishRun(r.ID, RunFailed, SkillNotGrantedPrefix+"fixture-browser needs your approval for this Assistant; secret-detail"); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Supervisor{Store: e.s}).Tick(); err != nil {
		t.Fatal(err)
	}
	fs, _ := e.s.Facts(e.a.ID, "", 50)
	if fs[0].Kind != FactRoutineFailed || !strings.Contains(fs[0].Text, "skill_not_granted") {
		t.Fatalf("fact = %+v", fs[0])
	}
	if strings.Contains(fs[0].Text, "secret-detail") {
		t.Fatalf("the fact carries free text: %q", fs[0].Text)
	}
}

func TestAssistantRoutineRunsOnlyAsScopedWorker(t *testing.T) {
	e := newRoutineEnv(t)
	got, err := e.draft(t, "scoped-run", func(m map[string]any) {
		m["prompt"] = "Use ego-browser to read X.com posts and cite sources."
	})
	if err != nil {
		t.Fatal(err)
	}
	ap := got["approval"].(map[string]any)
	e.approve(t, ap["id"].(string), ap["actionHash"].(string))
	rs := &RuleScheduler{Store: e.s, Load: config.Load, Now: func() time.Time { return at(9, 0).Add(24*time.Hour + 5*time.Second) }}
	if rep, err := rs.Tick(); err != nil || rep.Fired != 1 {
		t.Fatalf("scheduled task = %+v %v", rep, err)
	}
	tasks, err := e.s.ListTasks(e.a.ID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("scheduled tasks = %+v %v", tasks, err)
	}
	f := &fakeLauncher{}
	d := &Dispatcher{Store: e.s, Launcher: f, Claimant: "test", Rules: func(name string) (config.Automation, bool) {
		cfg, err := config.Load()
		if err != nil {
			return config.Automation{}, false
		}
		a := cfg.Find(name)
		if a == nil {
			return config.Automation{}, false
		}
		return *a, true
	}}
	if _, err := d.RunTask(t.Context(), tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(f.specs) != 1 || f.specs[0].Auto == nil {
		t.Fatalf("expected one managed Routine worker, got %+v", f.specs)
	}
	prompt := f.specs[0].Auto.Prompt
	for _, needed := range []string{"ego-browser", "Do not create cron/launchd jobs", "do not create sibling tasks", "Solco owns the next occurrence"} {
		if !strings.Contains(prompt, needed) {
			t.Fatalf("scheduled worker missed %q: %s", needed, prompt)
		}
	}
	original, _ := config.Load()
	if strings.Contains(original.Find("morning-check").Prompt, "Do not create cron") {
		t.Fatal("runtime guard polluted the approved YAML definition")
	}
}

func TestRoutineChangedAfterApprovalIsNeverEnabled(t *testing.T) {
	e := newRoutineEnv(t)
	got, _ := e.draft(t, "r1", nil)
	ap := got["approval"].(map[string]any)
	// The answer is recorded, but the definition is edited before the keeper runs.
	if _, _, err := e.s.DecideApproval("", ap["id"].(string), ap["actionHash"].(string), true); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load()
	x := *cfg.Find("morning-check")
	x.Prompt = "Something the person never saw."
	if res, err := config.Put("morning-check", x, false); err != nil || res.Problem != "" {
		t.Fatalf("edit: %+v %v", res, err)
	}
	rep, err := e.api.keeper().Tick()
	if err != nil || rep.Enabled != 0 || rep.Stale != 1 {
		t.Fatalf("keeper = %+v %v", rep, err)
	}
	if r, _ := e.s.GetRoutine("morning-check"); r.State != RoutineStale {
		t.Fatalf("state = %q", r.State)
	}
	if cfg, _ := config.Load(); !cfg.Find("morning-check").Draft {
		t.Fatal("a changed definition was enabled")
	}
	// A new draft is a new revision and a new card; the old card can no longer be answered.
	fresh, err := e.draft(t, "r2", func(m map[string]any) { m["prompt"] = x.Prompt })
	if err != nil {
		t.Fatal(err)
	}
	if fresh["routine"].(map[string]any)["revision"].(float64) != 2 || fresh["approval"].(map[string]any)["id"] == ap["id"] {
		t.Fatalf("redraft = %v", fresh)
	}
	if _, _, err := e.s.DecideApproval("", ap["id"].(string), ap["actionHash"].(string), true); codeOf(codeFor(err)) == "" && err == nil {
		t.Fatal("the stale card was answerable")
	}
}

func TestRoutineCancelPauseAndDenial(t *testing.T) {
	e := newRoutineEnv(t)
	got, _ := e.draft(t, "r1", nil)
	ap := got["approval"].(map[string]any)
	if _, err := call(t, e.api, `{}`, "assistant", "routine", "pause", "morning-check"); codeOf(err) != "bad_state" {
		t.Fatalf("pausing a draft = %q", codeOf(err))
	}
	if _, err := call(t, e.api, fmt.Sprintf(`{"actionHash":%q,"approve":false}`, ap["actionHash"]), "assistant", "approval", "decide", ap["id"].(string)); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.s.GetRoutine("morning-check"); r.State != RoutineDraft || r.Approved {
		t.Fatalf("a denial must leave the draft a draft: %+v", r)
	}
	if _, err := call(t, e.api, `{}`, "assistant", "routine", "cancel", "morning-check"); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := config.Load(); cfg.Find("morning-check") != nil {
		t.Fatal("cancel left the draft in automations.yaml")
	}
	if r, _ := e.s.GetRoutine("morning-check"); r.State != RoutineCancelled {
		t.Fatalf("state = %q", r.State)
	}

	// An enabled routine pauses and resumes through the automation's own flag; it cannot be withdrawn here.
	got, _ = e.draft(t, "r3", func(m map[string]any) { m["name"] = "weekly-sweep" })
	ap = got["approval"].(map[string]any)
	e.approve(t, ap["id"].(string), ap["actionHash"].(string))
	if _, err := call(t, e.api, `{}`, "assistant", "routine", "cancel", "weekly-sweep"); codeOf(err) != "bad_state" {
		t.Fatalf("cancel enabled = %q", codeOf(err))
	}
	paused, err := call(t, e.api, `{}`, "assistant", "routine", "pause", "weekly-sweep")
	if err != nil || paused["routine"].(map[string]any)["paused"] != true {
		t.Fatalf("pause = %v %v", paused, err)
	}
	list, err := call(t, e.api, ``, "assistant", "routine", "list", "--assistant", e.a.ID)
	if err != nil || len(list["routines"].([]any)) != 2 {
		t.Fatalf("list = %v %v", list, err)
	}
}
