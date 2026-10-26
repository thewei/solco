package workboard

import (
	"context"
	"strings"
	"testing"
)

// Shapes captured from herdr-projects 1.2.26 in the sandbox (autopilot list,
// archive --done) plus the project-row form from autopilot/command.rs.
const (
	realArchiveDone = `{"archived": [], "left": {"to do": 1}, "failed": []}`
	realAutopilots  = `[{"on": false, "coordinator": "main", "name": "Coordinator"},
	 {"on": true, "coordinator": "project:repoA", "name": "repoA coordinator"},
	 {"on": false, "coordinator": "project:repoB", "name": "repoB coordinator"}]`
)

func recording(out string) (*Client, *[]string) {
	var calls []string
	return NewClientWithExec(func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return out, nil
	}), &calls
}

func TestManageArgv(t *testing.T) {
	ctx := context.Background()
	c, calls := recording(realArchiveDone)
	if _, err := c.Archived(ctx); err == nil {
		t.Fatal("an object is not a list of archived tasks")
	}
	s, err := c.ArchiveDone(ctx, "repoA")
	if err != nil || len(s.Archived) != 0 {
		t.Fatalf("swept = %+v err=%v", s, err)
	}
	c.Restore(ctx, "r/1")
	c.Clear(ctx, "r/1")
	c.ArchiveProject(ctx, "repoA")
	c.RestoreProject(ctx, "repoA")
	for _, verb := range []string{"restart", "new", "stop"} {
		c.CoordinatorControl(ctx, "repoA", verb)
	}
	c.SetAutopilot(ctx, "repoA", true)
	c.SetAutopilot(ctx, "repoA", false)
	want := []string{
		"list --archived --json", "archive --done --repo repoA --json", "restore r/1", "clear r/1",
		"archive --repo repoA", "restore --repo repoA",
		"coordinator --project repoA restart", "coordinator --project repoA new", "coordinator --project repoA stop",
		"autopilot on --project repoA", "autopilot off --project repoA",
	}
	if got := strings.Join(*calls, "|"); got != strings.Join(want, "|") {
		t.Fatalf("argv:\n got %q\nwant %q", got, strings.Join(want, "|"))
	}
}

func TestManageRefusesOptionShapedTargetsAndUnknownVerbs(t *testing.T) {
	ctx := context.Background()
	c, calls := recording("[]")
	for _, bad := range []string{"", " ", "-x"} {
		if _, err := c.ArchiveDone(ctx, bad); err == nil {
			t.Fatalf("ArchiveDone(%q)", bad)
		}
		if c.ArchiveProject(ctx, bad) == nil || c.RestoreProject(ctx, bad) == nil || c.SetAutopilot(ctx, bad, true) == nil {
			t.Fatalf("project %q accepted", bad)
		}
		if c.CoordinatorControl(ctx, bad, "stop") == nil || c.Restore(ctx, bad) == nil || c.Clear(ctx, bad) == nil {
			t.Fatalf("target %q accepted", bad)
		}
	}
	if c.CoordinatorControl(ctx, "r", "where") == nil || c.CoordinatorControl(ctx, "r", "tell") == nil {
		t.Fatal("only restart, new and stop go through CoordinatorControl")
	}
	if len(*calls) != 0 {
		t.Fatalf("ran %q", *calls)
	}
}

func TestAutopilotsKeyProjectCoordinatorsByName(t *testing.T) {
	c, _ := recording(realAutopilots)
	on, err := c.Autopilots(context.Background())
	if err != nil || len(on) != 2 || !on["repoA"] || on["repoB"] {
		t.Fatalf("on = %v err=%v", on, err)
	}
	if _, ok := on["main"]; ok {
		t.Fatal("the main coordinator is not a Project")
	}
	c, _ = recording("nope")
	if _, err := c.Autopilots(context.Background()); err == nil {
		t.Fatal("junk must error")
	}
}

// Captured from 1.2.26 (`automations --json`, one automation, off).
const realAutomations = `[
  {"id":"acc00d66","name":"nightly check","on":false,"trigger":"daily 09:00","repo":"repoA",
   "folder":"/tmp/hp-probe/repoA","research":false,"chat":false,"prompt":"Look at things","settings":{},"last":null,"next":null},
  {"id":"b1","name":"weekly","on":true,"trigger":"mon 09:00","repo":"repoB","next":1793062800000,"last":{"at":1,"task":"repoB/3"}},
  {"name":"no id"}
]`

func TestAutomationsReadAndWrite(t *testing.T) {
	ctx := context.Background()
	c, calls := recording(realAutomations)
	all, err := c.Automations(ctx)
	if err != nil || len(all) != 2 || all[0].Name != "nightly check" || all[0].On || all[0].Repo != "repoA" || all[1].Next != 1793062800000 || all[1].Trigger != "mon 09:00" {
		t.Fatalf("automations = %+v err=%v", all, err)
	}
	for _, id := range []string{"", "-x"} {
		if c.SetAutomation(ctx, id, true) == nil || c.RunAutomation(ctx, id) == nil || c.RemoveAutomation(ctx, id) == nil {
			t.Fatalf("id %q accepted", id)
		}
	}
	c.SetAutomation(ctx, "b1", true)
	c.SetAutomation(ctx, "b1", false)
	c.RunAutomation(ctx, "b1")
	c.RemoveAutomation(ctx, "b1")
	want := "automations --json|automations on b1|automations off b1|automations run b1|automations remove b1"
	if got := strings.Join(*calls, "|"); got != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	c, _ = recording(`{"nope":1}`)
	if _, err := c.Automations(ctx); err == nil {
		t.Fatal("an object is not a list")
	}
}

func TestAddTaskArgvAndGuards(t *testing.T) {
	ctx := context.Background()
	c, calls := recording("")
	err := c.AddTask(ctx, NewTask{Title: " Ship it ", Repo: "r", Description: "More words", Research: true, Chat: true, After: []string{"r/1", " ", "r/2"}, Delegate: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddTask(ctx, NewTask{Title: "Plain"}); err != nil {
		t.Fatal(err)
	}
	c.Add(ctx, "Old way", "r", true)
	c.DelegateNow(ctx, "r/4")
	want := "add Ship it --repo r --desc More words --research --chat --after r/1,r/2 --delegate|add Plain|add Old way --repo r --delegate|delegate r/4 --now"
	if got := strings.Join(*calls, "|"); got != want {
		t.Fatalf("argv:\n got %q\nwant %q", got, want)
	}
	*calls = nil
	for _, bad := range []NewTask{
		{Title: ""}, {Title: "-x"}, {Title: "t", Repo: "-r"}, {Title: "t", Description: "--desc"},
		{Title: "t", After: []string{"-x"}}, {Title: "t", After: []string{"a, b"}},
	} {
		if c.AddTask(ctx, bad) == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if c.DelegateNow(ctx, "-x") == nil || len(*calls) != 0 {
		t.Fatalf("ran %q", *calls)
	}
}

// A slice of 1.2.26's `show --json` (sandbox): the shape of each row, a bool,
// an own value, and a note.
const realShow = `{"settings":[
 {"name":"worker.agent","kind":"work","value":"claude","from":"built in","place":"builtin","own":false,"note":""},
 {"name":"worker.fast","kind":"work","value":true,"from":"you","place":"you","own":true,"note":"does nothing: claude has no fast mode"},
 {"name":"review.rounds","kind":"work","value":2,"from":"built in","place":"builtin","own":false,"note":""},
 {"name":"board.theme","kind":"board","value":null,"from":"built in","place":"builtin","own":false,"note":""}
],"problems":["settings.json: line 3","  "]}`

func TestSettingsReadsRowsAndValues(t *testing.T) {
	c, calls := recording(realShow)
	view, err := c.Settings(context.Background(), Scope{})
	if err != nil || len(view.Settings) != 4 {
		t.Fatalf("view = %+v err=%v", view, err)
	}
	s := view.Settings
	if s[0].ValueText() != "claude" || s[0].IsBool() || s[0].Own {
		t.Fatalf("string row = %+v", s[0])
	}
	if !s[1].IsBool() || s[1].ValueText() != "true" || !s[1].Own || s[1].Note != "does nothing: claude has no fast mode" {
		t.Fatalf("bool row = %+v", s[1])
	}
	if s[2].ValueText() != "2" || s[3].ValueText() != "" {
		t.Fatalf("number/null = %q %q", s[2].ValueText(), s[3].ValueText())
	}
	if len(view.Problems) != 1 || view.Problems[0] != "settings.json: line 3" {
		t.Fatalf("problems = %q", view.Problems)
	}
	c.Settings(context.Background(), Scope{Repo: "r"})
	c.Settings(context.Background(), Scope{Repo: "r", Coordinator: true})
	want := "show --json|show --json --repo r|show --json --project-coordinator r"
	if got := strings.Join(*calls, "|"); got != want {
		t.Fatalf("argv = %q", got)
	}
}

func TestSetAndUnsetArgvPerScope(t *testing.T) {
	ctx := context.Background()
	c, calls := recording("merge.method: squash  (you)")
	said, err := c.SetSetting(ctx, Scope{}, "merge.method", " squash ")
	if err != nil || said != "merge.method: squash  (you)" {
		t.Fatalf("said=%q err=%v", said, err)
	}
	c.SetSetting(ctx, Scope{Repo: "r"}, "merge.method", "rebase")
	c.SetSetting(ctx, Scope{Repo: "r", Coordinator: true}, "coordinator.agent", "codex")
	c.UnsetSetting(ctx, Scope{}, "worker.fast")
	c.UnsetSetting(ctx, Scope{Repo: "r"}, "merge.method")
	c.UnsetSetting(ctx, Scope{Repo: "r", Coordinator: true}, "coordinator.agent")
	want := []string{
		"set merge.method squash", "set merge.method rebase --repo r", "set coordinator.agent codex --project-coordinator r",
		"unset worker.fast", "unset merge.method --repo r", "unset coordinator.agent --project-coordinator r",
	}
	if got := strings.Join(*calls, "|"); got != strings.Join(want, "|") {
		t.Fatalf("argv:\n got %q\nwant %q", got, strings.Join(want, "|"))
	}
}

func TestSettingsWritesRefuseShapesThatCouldBecomeOptions(t *testing.T) {
	ctx := context.Background()
	c, calls := recording("")
	for _, v := range []string{"", " ", "--repo", "-1"} {
		if _, err := c.SetSetting(ctx, Scope{}, "merge.method", v); err == nil {
			t.Fatalf("value %q accepted", v)
		}
	}
	for _, n := range []string{"", "-x", "a b", "a=b"} {
		if _, err := c.SetSetting(ctx, Scope{}, n, "x"); err == nil {
			t.Fatalf("name %q accepted", n)
		}
		if _, err := c.UnsetSetting(ctx, Scope{}, n); err == nil {
			t.Fatalf("unset name %q accepted", n)
		}
	}
	for _, sc := range []Scope{{Repo: "-r"}, {Coordinator: true}} {
		if _, err := c.SetSetting(ctx, sc, "merge.method", "x"); err == nil {
			t.Fatalf("scope %+v accepted", sc)
		}
		if _, err := c.Settings(ctx, sc); err == nil {
			t.Fatalf("read scope %+v accepted", sc)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("ran %q", *calls)
	}
}

func TestAddAutomationArgv(t *testing.T) {
	ctx := context.Background()
	c, calls := recording("")
	if err := c.AddAutomation(ctx, NewAutomation{Name: " Nightly ", Prompt: "Run the audit", Repo: "r", Schedule: "daily 03:00", Research: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddAutomation(ctx, NewAutomation{Name: "On merge", Prompt: "Tidy", When: "merged", EveryRepo: true, If: "branch = feature/*", Chat: true, Research: true}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"automations add Nightly --prompt Run the audit --schedule daily 03:00 --repo r --research",
		"automations add On merge --prompt Tidy --when merged --every-repo --if branch = feature/* --chat",
	}
	if len(*calls) != 2 || (*calls)[0] != want[0] || (*calls)[1] != want[1] {
		t.Fatalf("calls = %q, want %q", *calls, want)
	}
	for _, bad := range []NewAutomation{
		{Prompt: "p", Schedule: "every 2h"},
		{Name: "n", Schedule: "every 2h"},
		{Name: "n", Prompt: "p"},
		{Name: "n", Prompt: "p", Schedule: "every 2h", When: "merged"},
		{Name: "n", Prompt: "p", Schedule: "every 2h", If: "title = x"},
		{Name: "-n", Prompt: "p", Schedule: "every 2h"},
		{Name: "n", Prompt: "-p", Schedule: "every 2h"},
		{Name: "n", Prompt: "p", When: "merged", Repo: "--x"},
	} {
		if err := c.AddAutomation(ctx, bad); err == nil {
			t.Errorf("%+v must be refused", bad)
		}
	}
	if len(*calls) != 2 {
		t.Fatalf("a refused automation must reach no CLI call: %q", *calls)
	}
}

func TestDelegateWithAndTaskSettingsArgv(t *testing.T) {
	ctx := context.Background()
	c, calls := recording(`{"settings":[{"name":"worker.agent","value":"claude"}]}`)
	if err := c.DelegateWith(ctx, "r/4", []SettingValue{{"worker.agent", "codex/gpt-6"}, {"plan.approve", " you "}}); err != nil {
		t.Fatal(err)
	}
	view, err := c.TaskSettings(ctx, "r/4")
	if err != nil || len(view.Settings) != 1 {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	want := []string{"delegate r/4 --worker.agent codex/gpt-6 --plan.approve you", "show r/4 --json"}
	if len(*calls) != 2 || (*calls)[0] != want[0] || (*calls)[1] != want[1] {
		t.Fatalf("calls = %q, want %q", *calls, want)
	}
	for _, bad := range [][]SettingValue{{{"", "x"}}, {{"-worker.agent", "x"}}, {{"a b", "x"}}, {{"worker.agent", ""}}, {{"worker.agent", "--now"}}} {
		if c.DelegateWith(ctx, "r/4", bad) == nil {
			t.Errorf("%v must be refused", bad)
		}
	}
	if c.DelegateWith(ctx, "-x", nil) == nil {
		t.Error("an option-like id must be refused")
	}
	if _, err := c.TaskSettings(ctx, "--all"); err == nil {
		t.Error("an option-like id must be refused")
	}
	if len(*calls) != 2 {
		t.Fatalf("refusals must reach no CLI call: %q", *calls)
	}
}
