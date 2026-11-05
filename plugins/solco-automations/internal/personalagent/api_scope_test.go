package personalagent

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// C02: a Coordinator speaks through `--as <assistantId>`, and the core (not the
// extension) decides what that identity may touch.
func scopedPair(t *testing.T) (*API, string, string, string, string) {
	t.Helper()
	s, _ := openTest(t)
	api := &API{Store: s}
	ada := mustAssistant(t, s, "a1", "Ada")
	bob := mustAssistant(t, s, "a2", "Bob")
	task := mkTask(t, s, "t-bob", bob.ID, "Bob's research")
	g, _, err := s.CreateGoal("g-bob", CreateGoalInput{AssistantID: bob.ID, Title: "Bob's goal"})
	if err != nil {
		t.Fatal(err)
	}
	return api, ada.ID, bob.ID, task.ID, g.ID
}

func TestScopedCallerCannotTouchAnotherAssistantsRecords(t *testing.T) {
	api, ada, bob, bobTask, bobGoal := scopedPair(t)
	for _, args := range [][]string{
		{"assistant", "--as", ada, "task", "show", bobTask},
		{"assistant", "--as", ada, "task", "why", bobTask},
		{"assistant", "--as", ada, "goal", "show", bobGoal},
		{"assistant", "--as", ada, "goal", "pause", bobGoal},
		{"assistant", "--as", ada, "task", "list", "--assistant", bob},
		{"assistant", "--as", ada, "task", "graph", bob},
		{"assistant", "--as", ada, "checkpoint", "get", bob},
		{"assistant", "--as", ada, "memory", "list", bob},
	} {
		_, err := call(t, api, `{"requestId":"r-12345678"}`, args...)
		if codeOf(err) != "forbidden" {
			t.Errorf("%v: code = %q, want forbidden (err %v)", args[2:], codeOf(err), err)
		}
	}
	// A body that names someone else's id is refused, not silently rewritten.
	_, err := call(t, api, fmt.Sprintf(`{"requestId":"r-12345678","assistantId":%q,"title":"t","brief":"b","kind":"research"}`, bob), "assistant", "--as", ada, "task", "create")
	if codeOf(err) != "forbidden" {
		t.Errorf("create for another assistant: %q", codeOf(err))
	}
}

func TestScopedCallerOnlyReachesTheCoordinatorsActions(t *testing.T) {
	api, ada, _, _, _ := scopedPair(t)
	for _, args := range [][]string{
		{"assistant", "--as", ada, "approval", "decide", "x"},
		{"assistant", "--as", ada, "goal", "approve", "g"},   // a person starts goals
		{"assistant", "--as", ada, "goal", "complete", "g"},  // completion needs evidence the core verifies
		{"assistant", "--as", ada, "routine", "resume", "r"}, // a person enables routines
		{"assistant", "--as", ada, "trigger", "pause", "g"},  // so are triggers
		{"assistant", "--as", ada, "trigger", "cancel", "g"},
		{"assistant", "--as", ada, "hook", "events", "g"}, // webhook keys and receipts are the person's
		{"assistant", "--as", ada, "budget", "show", ada}, // the ledger is the person's to read
		{"assistant", "--as", ada, "hook", "key", "create"},
		{"assistant", "--as", ada, "hook", "key", "revoke", "g"},
		{"assistant", "--as", ada, "task", "publish", "t"},     // publishing coding work is the person's
		{"assistant", "--as", ada, "coordinator", "stop", ada}, // not its own lifecycle
		{"assistant", "--as", ada, "delete-forever", ada},
		{"assistant", "--as", ada, "persona", "restore", ada},
	} {
		_, err := call(t, api, `{"requestId":"r-12345678"}`, args...)
		if codeOf(err) != "forbidden" {
			t.Errorf("%v: code = %q, want forbidden", args[2:], codeOf(err))
		}
	}
}

func TestScopedCallerWorksOnItsOwnRecordsAndFillsTheRevision(t *testing.T) {
	api, ada, _, _, _ := scopedPair(t)
	g, err := call(t, api, fmt.Sprintf(`{"requestId":"g-own-0001","assistantId":%q,"title":"Mine"}`, ada), "assistant", "--as", ada, "goal", "create")
	if err != nil {
		t.Fatalf("own create: %v", err)
	}
	gid := field(g, "goal", "id").(string)
	if _, err := call(t, api, `{"requestId":"g-appr-0001","expectedRevision":1}`, "assistant", "goal", "approve", gid); err != nil {
		t.Fatal(err)
	}
	// No expectedRevision from the model: the core reads the current one.
	p, err := call(t, api, `{"requestId":"g-pause-001"}`, "assistant", "--as", ada, "goal", "pause", gid)
	if err != nil || field(p, "goal", "state") != "paused" {
		t.Fatalf("pause: %v %v", p, err)
	}
	r, err := call(t, api, `{"requestId":"g-resume-01"}`, "assistant", "--as", ada, "goal", "resume", gid)
	if err != nil || field(r, "goal", "state") != "active" {
		t.Fatalf("resume: %v %v", r, err)
	}
	// An own-id list is fine, and the flag cannot be dropped to widen it.
	if _, err := call(t, api, "", "assistant", "--as", ada, "task", "list", "--assistant", ada); err != nil {
		t.Fatalf("own list: %v", err)
	}
	if _, err := call(t, api, "", "assistant", "--as", ada, "task", "list"); codeOf(err) != "forbidden" {
		t.Fatalf("a list without its own --assistant must not mean everyone's: %q", codeOf(err))
	}
}

func TestTaskWhyExplainsWhyItIsNotMoving(t *testing.T) {
	s, _ := openTest(t)
	api := &API{Store: s}
	ada := mustAssistant(t, s, "a1", "Ada")
	first := mkTask(t, s, "t-first", ada.ID, "Research A")
	second := mkTask(t, s, "t-second", ada.ID, "Compare", first.ID)
	out, err := call(t, api, "", "assistant", "--as", ada.ID, "task", "why", second.ID)
	if err != nil {
		t.Fatal(err)
	}
	why, _ := out["why"].(map[string]any)
	if why["state"] != "waiting" || !strings.Contains(fmt.Sprint(why["reason"]), "Research A") {
		t.Fatalf("a waiting task names the task it waits for: %v", why)
	}
	// A failed run names its stable code and nothing free-form.
	bad := mkTask(t, s, "t-bad", ada.ID, "Breaks")
	r, _, _ := s.ClaimTask(bad.ID, "d")
	_ = s.MarkStarting(r.ID)
	_ = s.RecordLaunch(r.ID, Session{PaneID: "w1:p1"})
	_ = s.FinishRun(r.ID, RunFailed, "skill_not_granted: ego-browser — secret detail")
	out, err = call(t, api, "", "assistant", "--as", ada.ID, "task", "why", bad.ID)
	if err != nil {
		t.Fatal(err)
	}
	why, _ = out["why"].(map[string]any)
	text := fmt.Sprint(why["reason"])
	if why["state"] != "failed" || why["code"] != "skill_not_granted" || strings.Contains(text, "secret detail") {
		t.Fatalf("failed run: code only, no free text: %v", why)
	}
}

// The extension and the core must list the same actions: a Coordinator that
// can name an action the core refuses (or the reverse) is a silent breakage.
func TestExtensionActionsMatchTheCoreScope(t *testing.T) {
	src, err := os.ReadFile("../../extensions/solco-coordinator.ts")
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)export const actions = \[(.*?)\] as const`).FindSubmatch(src)
	if block == nil {
		t.Fatal("actions list not found in the extension")
	}
	var ext []string
	for _, m := range regexp.MustCompile(`"([a-z-]+)\.([a-z-]+)"`).FindAllSubmatch(block[1], -1) {
		ext = append(ext, string(m[1])+" "+string(m[2]))
	}
	core := CoordinatorActions()
	sort.Strings(ext)
	sort.Strings(core)
	if strings.Join(ext, ",") != strings.Join(core, ",") {
		t.Fatalf("extension actions %v != core scope %v", ext, core)
	}
}

// C06: a Coordinator that forgets it already filed a piece of work must not
// file it again under a new requestId; the core points at the open one.
func TestScopedTaskCreateRefusesAnOpenDuplicate(t *testing.T) {
	s, _ := openTest(t)
	api := &API{Store: s}
	ada := mustAssistant(t, s, "a1", "Ada")
	body := func(req, title string, extra string) string {
		return fmt.Sprintf(`{"requestId":%q,"title":%q,"brief":"look it up","kind":"research"%s}`, req, title, extra)
	}
	first, err := call(t, api, body("dup-req-0001", "Compare  The Two Options", ""), "assistant", "--as", ada.ID, "task", "create")
	if err != nil {
		t.Fatal(err)
	}
	firstID := field(first, "task", "id").(string)
	// Same requestId: a replay, never a duplicate.
	if rep, err := call(t, api, body("dup-req-0001", "Compare  The Two Options", ""), "assistant", "--as", ada.ID, "task", "create"); err != nil || rep["replayed"] != true {
		t.Fatalf("replay: %v %v", rep, err)
	}
	// Same work, new requestId (title compared ignoring case and spacing).
	_, err = call(t, api, body("dup-req-0002", "compare the two options", ""), "assistant", "--as", ada.ID, "task", "create")
	if codeOf(err) != "duplicate_task" || !strings.Contains(err.Error(), firstID) {
		t.Fatalf("want duplicate_task naming %s, got %v", firstID, err)
	}
	// Deliberate repeats are allowed, and so is the same title once it is finished.
	if _, err := call(t, api, body("dup-req-0003", "compare the two options", `,"allowDuplicate":true`), "assistant", "--as", ada.ID, "task", "create"); err != nil {
		t.Fatalf("allowDuplicate: %v", err)
	}
	if _, err := call(t, api, body("dup-req-0004", "Other work", ""), "assistant", "--as", ada.ID, "task", "create"); err != nil {
		t.Fatalf("different work: %v", err)
	}
}

func TestTraceFollowsGoalToTaskToRuns(t *testing.T) {
	s, _ := openTest(t)
	api := &API{Store: s}
	ada := mustAssistant(t, s, "a1", "Ada")
	g := activeGoalWithPlan(t, s, ada, twoStepPlan())
	if _, err := (&Supervisor{Store: s}).Tick(); err != nil {
		t.Fatal(err)
	}
	tasks, _ := s.ListTasks(ada.ID)
	var first Task
	for _, tk := range tasks {
		if tk.GoalID == g.ID && tk.Title == "Collect numbers" {
			first = tk
		}
	}
	if first.ID == "" {
		t.Fatalf("the supervisor should have filed the first milestone's task: %+v", tasks)
	}
	out, err := call(t, api, "", "assistant", "--as", ada.ID, "trace", "show", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	tr := out["trace"].(map[string]any)
	if tr["goalId"] != g.ID || tr["taskId"] != first.ID || tr["milestone"] != "m1" {
		t.Fatalf("trace = %v", tr)
	}
	if g2, _ := call(t, api, "", "assistant", "--as", ada.ID, "trace", "show", g.ID); g2["trace"].(map[string]any)["goalId"] != g.ID {
		t.Fatalf("a goal id traces to itself: %v", g2)
	}
}

func TestScopedCallerDraftsATriggerForItselfOnly(t *testing.T) {
	api, ada, bob, bobTask, _ := scopedPair(t)
	mon := mkTask(t, api.Store, "mon-0001", ada, "Monitor")
	ok, err := call(t, api, fmt.Sprintf(`{"requestId":"trg-own-01","name":"diagnose","on":"task.failed","taskId":%q,"title":"Diagnose","brief":"why"}`, mon.ID), "assistant", "--as", ada, "trigger", "draft")
	if err != nil || field(ok, "trigger", "state") != "draft" || field(ok, "approval", "state") != "pending" {
		t.Fatalf("own draft: %v %v", ok, err)
	}
	_, err = call(t, api, fmt.Sprintf(`{"requestId":"trg-bob-01","assistantId":%q,"name":"x1","on":"task.failed","taskId":%q,"title":"t","brief":"b"}`, bob, bobTask), "assistant", "--as", ada, "trigger", "draft")
	if codeOf(err) != "forbidden" {
		t.Fatalf("a trigger for another Assistant: %q", codeOf(err))
	}
	if _, err := call(t, api, "", "assistant", "--as", ada, "trigger", "list", "--assistant", bob); codeOf(err) != "forbidden" {
		t.Fatalf("listing another Assistant's triggers: %q", codeOf(err))
	}
}
