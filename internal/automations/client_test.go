package automations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/thewei/solco/internal/herdr"
)

// captureExec returns a client whose exec records every argv and answers from
// one function — the same fake shape the workboard client tests use.
func captureExec(calls *[]string, answer func(args []string) (string, error)) *Client {
	return NewClientWithExec(func(_ context.Context, args ...string) (string, error) {
		*calls = append(*calls, strings.Join(args, " "))
		return answer(args)
	})
}

// listFixture is contract §3's `list --json` shape, one active automation
// with a last run and history30, one draft.
const listFixture = `{"result":{"paused":false,"scheduler":4242,"state":"/tmp/state",
 "invalid":[{"name":"broken","line":12,"message":"unknown field held"}],
 "automations":[{
   "name":"nightly-ci-triage","draft":false,"disabled":false,
   "cron":"0 9 * * 1-5","schedule_text":"Weekdays 09:00","once_at":null,
   "repo":"~/Workspaces/wh-studio/solco","workspace_mode":"worktree",
   "agent":"claude","model":"","prompt":"triage","rationale":"nightly",
   "on_overlap":"skip","timeout_minutes":60,"catch_up_minutes":120,
   "cleanup_ttl_hours":0,
   "next_run":"2026-11-03T09:00:00+08:00",
   "last_run":{"run_id":"r-128","status":"done","started_at":"2026-11-01T09:00:00+08:00","duration_seconds":2280,
               "worktree":"auto/nightly-ci-1101","workspace_id":"w-1","pane_id":"p-1"},
   "history30":{"done":18,"failed":2,"skipped":1}},
  {"name":"draft-sweep","draft":true,"disabled":false,
   "cron":"","schedule_text":"","once_at":null,
   "repo":"~/Workspaces/wh-studio/solco","workspace_mode":"worktree",
   "agent":"codex","model":"","prompt":"sweep","rationale":"",
   "on_overlap":"queue","timeout_minutes":0,"catch_up_minutes":0,
   "cleanup_ttl_hours":48,"next_run":"","last_run":null,
   "history30":{"done":0,"failed":0,"skipped":0}}]}}`

func TestListDecodesTheContractShape(t *testing.T) {
	var calls []string
	c := captureExec(&calls, func([]string) (string, error) { return listFixture, nil })
	got, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, "|") != "list --json" {
		t.Fatalf("argv = %q", calls)
	}
	if got.Scheduler != 4242 || got.State != "/tmp/state" {
		t.Fatalf("scheduler/state = %d %q", got.Scheduler, got.State)
	}
	if got.Paused || len(got.Invalid) != 1 || got.Invalid[0].Line != 12 || len(got.Automations) != 2 {
		t.Fatalf("List = %+v", got)
	}
	a := got.Automations[0]
	if a.Name != "nightly-ci-triage" || a.Draft || a.Cron != "0 9 * * 1-5" || a.ScheduleText != "Weekdays 09:00" ||
		a.OnOverlap != "skip" || a.History30.Done != 18 || a.History30.Failed != 2 || a.LastRun == nil ||
		a.LastRun.RunID != "r-128" || a.LastRun.DurationSeconds != 2280 {
		t.Fatalf("automation = %+v", a)
	}
	draft := got.Automations[1]
	if !draft.Draft || draft.OnOverlap != "queue" || draft.CleanupTTLHours != 48 || draft.LastRun != nil {
		t.Fatalf("draft = %+v", draft)
	}
}

func TestHistoryDecodesRunsAndTargetsOneName(t *testing.T) {
	var calls []string
	c := captureExec(&calls, func([]string) (string, error) {
		return `{"result":{"runs":[{"run_id":"r-129","name":"nightly-ci-triage","trigger":"cron","status":"queued",
		  "started_at":"","finished_at":"","duration_seconds":0,"worktree":"","workspace_id":"w-2","pane_id":"","agent":"claude","error":"Claude Code is asking whether to trust this folder","seen":true}]}}`, nil
	})
	got, err := c.History(context.Background(), "nightly-ci-triage")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, "|") != "history nightly-ci-triage --json" {
		t.Fatalf("argv = %q", calls)
	}
	if len(got.Runs) != 1 || got.Runs[0].Status != "queued" || got.Runs[0].Trigger != "cron" ||
		!strings.Contains(got.Runs[0].Error, "trust this folder") || !got.Runs[0].Seen {
		t.Fatalf("runs = %+v", got.Runs)
	}
}

// A plugin refusal passes through verbatim: the error carries the CLI's own
// words, only wrapped with the operation that failed.
func TestRefusalsPassThroughVerbatim(t *testing.T) {
	var calls []string
	c := captureExec(&calls, func(args []string) (string, error) {
		return "", errors.New("exit status 1: refuse: " + args[0])
	})
	err := c.Approve(context.Background(), "not-a-draft")
	if err == nil || !strings.Contains(err.Error(), "refuse: approve") {
		t.Fatalf("err = %v, want the plugin's own refusal", err)
	}
}

func TestNameGuardsRefuseBeforeAnyExec(t *testing.T) {
	var calls []string
	c := captureExec(&calls, func([]string) (string, error) { return "", nil })
	for name, run := range map[string]func() error{
		"approve": func() error { return c.Approve(context.Background(), "") },
		"run":     func() error { return c.Run(context.Background(), "-x") },
		"pause":   func() error { return c.Pause(context.Background(), " ") },
		"resume":  func() error { return c.Resume(context.Background(), "") },
		"rm":      func() error { return c.Remove(context.Background(), "-draft") },
		"put":     func() error { return c.Put(context.Background(), "", Fields{}) },
	} {
		if err := run(); err == nil {
			t.Errorf("%s accepted an empty or option-like name", name)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("a refused name must reach no CLI call: %q", calls)
	}
}

func TestAddAlwaysCreatesADraft(t *testing.T) {
	var calls []string
	c := captureExec(&calls, func([]string) (string, error) { return "", nil })
	err := c.Add(context.Background(), "nightly-ci-triage", Fields{
		Cron: "0 9 * * 1-5", Repo: "~/solco", Workspace: "root", Agent: "claude", Model: "opus",
		Prompt: "- triage", Rationale: "nightly", OnOverlap: "queue",
		TimeoutMinutes: 60, CatchUpMinutes: 120, CleanupTTLHours: 48,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The real CLI takes the name as --name (a positional name is an
	// "unknown flag"), and a prose prompt that starts with "-" is fine.
	want := "add --name nightly-ci-triage --draft --cron 0 9 * * 1-5 --repo ~/solco --workspace root " +
		"--agent claude --model opus --prompt - triage --rationale nightly --on-overlap queue " +
		"--timeout-minutes 60 --catch-up-minutes 120 --cleanup-ttl-hours 48"
	if strings.Join(calls, "|") != want {
		t.Fatalf("argv = %q, want %q", calls, want)
	}
}

// The CLI wizard would ask for what a flag left out, and nobody is there to
// answer: the client refuses first.
func TestAddRequiresPromptScheduleAndRepo(t *testing.T) {
	var calls []string
	c := captureExec(&calls, func([]string) (string, error) { return "", nil })
	full := Fields{Cron: "0 9 * * *", Repo: "/r", Prompt: "p"}
	for name, f := range map[string]Fields{
		"prompt":   {Cron: full.Cron, Repo: full.Repo},
		"schedule": {Repo: full.Repo, Prompt: full.Prompt},
		"repo":     {Cron: full.Cron, Prompt: full.Prompt},
	} {
		if err := c.Add(context.Background(), "x", f); err == nil {
			t.Errorf("an add without %s must be refused client-side", name)
		}
	}
	if err := c.Add(context.Background(), "x", Fields{Cron: "0 9 * * *", Repo: "-r", Prompt: "p"}); err == nil {
		t.Error("option-like repository must be refused client-side")
	}
	if len(calls) != 0 {
		t.Fatalf("a refused add must reach no CLI call: %q", calls)
	}
}

func TestSimpleVerbsKeepTheirArgv(t *testing.T) {
	var calls []string
	c := captureExec(&calls, func([]string) (string, error) { return "", nil })
	ctx := context.Background()
	if err := c.Run(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := c.Pause(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := c.Resume(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	want := "run a --bg|pause a|resume a|rm a"
	if strings.Join(calls, "|") != want {
		t.Fatalf("argv = %q, want %q", calls, want)
	}
}

// putExec answers `list --json` with one entry that carries keys the form
// does not know (agent_args, mcp_config, a draft flag) and records what `put`
// receives on stdin.
func putExec(stdin *string, verdict string) *Client {
	return NewClientWithExecIn(func(_ context.Context, in string, args ...string) (string, error) {
		switch args[0] {
		case "list":
			return `{"result":{"automations":[{"name":"nightly","draft":true,"cron":"0 9 * * *","repo":"/r",
			  "agent":"claude","model":"opus","prompt":"old","rationale":"why","on_overlap":"skip",
			  "timeout_minutes":60,"catch_up_minutes":0,"cleanup_ttl_hours":0,
			  "agent_args":["--verbose"],"mcp_config":"/m.json","disabled":false}]}}`, nil
		case "put":
			*stdin = in
			return verdict, nil
		}
		return "", errors.New("unexpected " + args[0])
	})
}

func TestPutReadsModifiesWritesTheWholeEntryThroughStdin(t *testing.T) {
	var stdin string
	c := putExec(&stdin, `{"next":["2026-11-04T09:30:00Z"]}`)
	err := c.Put(context.Background(), "nightly", Fields{
		Cron: "30 9 * * 1-5", Prompt: "triage harder", OnOverlap: "queue", Workspace: "root",
		Model: "", Rationale: "", TimeoutMinutes: 30, CleanupTTLHours: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdin), &got); err != nil {
		t.Fatalf("stdin is not JSON: %v\n%s", err, stdin)
	}
	if got["cron"] != "30 9 * * 1-5" || got["prompt"] != "triage harder" || got["on_overlap"] != "queue" ||
		got["workspace"] != "root" || got["timeout_minutes"] != float64(30) || got["cleanup_ttl_hours"] != float64(24) {
		t.Errorf("form keys not applied: %v", got)
	}
	// Cleared by the form.
	if got["model"] != "" || got["rationale"] != "" {
		t.Errorf("model/rationale should be cleared: %v", got)
	}
	// Untouched: what the form does not know must survive the round trip,
	// or an edit would silently drop a hand-tuned entry's other settings.
	if got["repo"] != "/r" || got["agent"] != "claude" || got["draft"] != true || got["mcp_config"] != "/m.json" {
		t.Errorf("unrelated keys lost: %v", got)
	}
	if args, _ := got["agent_args"].([]any); len(args) != 1 || args[0] != "--verbose" {
		t.Errorf("agent_args lost: %v", got["agent_args"])
	}
}

func TestPutSurfacesThePluginsProblemAsTheError(t *testing.T) {
	var stdin string
	c := putExec(&stdin, `{"problem":"nightly: invalid cron \"nope\""}`)
	err := c.Put(context.Background(), "nightly", Fields{Cron: "nope"})
	if err == nil || !strings.Contains(err.Error(), `invalid cron "nope"`) {
		t.Fatalf("err = %v, want the plugin's problem text", err)
	}
	// The same problem inside an envelope is no quieter.
	c = putExec(&stdin, `{"result":{"problem":"bad repo"}}`)
	if err := c.Put(context.Background(), "nightly", Fields{}); err == nil || !strings.Contains(err.Error(), "bad repo") {
		t.Fatalf("enveloped problem lost: %v", err)
	}
}

func TestPutOfAMissingNameFailsBeforeWriting(t *testing.T) {
	var stdin string
	c := putExec(&stdin, `{}`)
	if err := c.Put(context.Background(), "ghost", Fields{Prompt: "x"}); err == nil {
		t.Fatal("editing an automation the plugin does not list must fail")
	}
	if stdin != "" {
		t.Fatalf("put was called with %q", stdin)
	}
}

func TestStartSchedulerUsesTheInjectedStarter(t *testing.T) {
	var got string
	c := NewClientWithExec(func(context.Context, ...string) (string, error) { return "", nil }).
		WithDaemonStarter(func(log string) error { got = log; return nil })
	if err := c.StartScheduler("/tmp/daemon.log"); err != nil || got != "/tmp/daemon.log" {
		t.Fatalf("starter got %q, err %v", got, err)
	}
	bare := NewClientWithExec(func(context.Context, ...string) (string, error) { return "", nil })
	if err := bare.StartScheduler(""); err == nil {
		t.Fatal("a client without a starter must say so, not pretend")
	}
}

// An empty root yields a client whose every call fails: callers gate on
// Detect.
func TestEmptyRootNeverExecutes(t *testing.T) {
	c := NewClient("", "")
	if _, err := c.List(context.Background()); err == nil {
		t.Fatal("an empty root must fail every call")
	}
}

// The CLI runs as the human, in the session the window is looking at: agent
// identity variables are gone and the socket is that session's own.
func TestCLIEnvBindsTheSessionAndDropsAgentIdentity(t *testing.T) {
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/other.sock")
	env := cliEnv("work")
	var sockets, panes []string
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "HERDR_SOCKET_PATH="):
			sockets = append(sockets, strings.TrimPrefix(kv, "HERDR_SOCKET_PATH="))
		case strings.HasPrefix(kv, "HERDR_PANE_ID="):
			panes = append(panes, kv)
		}
	}
	if len(panes) != 0 {
		t.Fatalf("agent identity leaked: %v", panes)
	}
	if len(sockets) != 1 || sockets[0] != herdr.SocketPath("work") {
		t.Fatalf("socket = %v, want exactly %q", sockets, herdr.SocketPath("work"))
	}
}

func TestHeadlessTravelsOnAddAndPut(t *testing.T) {
	var argv [][]string
	var stdin []string
	c := NewClientWithExecIn(func(_ context.Context, in string, args ...string) (string, error) {
		argv, stdin = append(argv, args), append(stdin, in)
		if args[0] == "list" {
			return `{"result":{"automations":[{"name":"a","cron":"0 9 * * *","repo":"/r","agent":"claude","prompt":"p","headless":false}]}}`, nil
		}
		return "", nil
	})
	f := Fields{Cron: "0 9 * * *", Repo: "/r", Agent: "claude", Prompt: "p", Headless: true}
	if err := c.Add(context.Background(), "a2", f); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(argv[0], " "); !strings.Contains(got, "--headless") {
		t.Errorf("add argv = %q, want --headless", got)
	}
	if err := c.Put(context.Background(), "a", f); err != nil {
		t.Fatal(err)
	}
	last := stdin[len(stdin)-1]
	if !strings.Contains(last, `"headless":true`) {
		t.Errorf("put body = %s, want headless true", last)
	}
	f.Headless = false
	if err := c.Put(context.Background(), "a", f); err != nil {
		t.Fatal(err)
	}
	if last := stdin[len(stdin)-1]; !strings.Contains(last, `"headless":false`) {
		t.Errorf("put body = %s, want headless cleared", last)
	}
}
