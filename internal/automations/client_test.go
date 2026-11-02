package automations

import (
	"context"
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
const listFixture = `{"result":{"paused":false,
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
		  "started_at":"","finished_at":"","duration_seconds":0,"worktree":"","workspace_id":"w-2","pane_id":"","agent":"claude"}]}}`, nil
	})
	got, err := c.History(context.Background(), "nightly-ci-triage")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, "|") != "history nightly-ci-triage --json" {
		t.Fatalf("argv = %q", calls)
	}
	if len(got.Runs) != 1 || got.Runs[0].Status != "queued" || got.Runs[0].Trigger != "cron" {
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
		Cron: "0 9 * * 1-5", Repo: "~/solco", Agent: "claude", Prompt: "triage",
		Rationale: "nightly", OnOverlap: "queue",
		TimeoutMinutes: 60, CatchUpMinutes: 120, CleanupTTLHours: 48,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "add nightly-ci-triage --draft --cron 0 9 * * 1-5 --repo ~/solco --agent claude " +
		"--prompt triage --rationale nightly --on-overlap queue --timeout-minutes 60 " +
		"--catch-up-minutes 120 --cleanup-ttl-hours 48"
	if strings.Join(calls, "|") != want {
		t.Fatalf("argv = %q, want %q", calls, want)
	}
}

func TestAddRequiresAPromptAndOneTrigger(t *testing.T) {
	var calls []string
	c := captureExec(&calls, func([]string) (string, error) { return "", nil })
	if err := c.Add(context.Background(), "x", Fields{Cron: "0 9 * * *"}); err == nil {
		t.Fatal("a draft without a prompt must be refused client-side")
	}
	// An explicit --once-at moment is the other trigger; both is the
	// plugin's refusal, not ours — only the empty-prompt case is local.
	if err := c.Add(context.Background(), "x", Fields{Prompt: "-p"}); err == nil {
		t.Fatal("option-like text must be refused client-side")
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
	want := "run a|pause a|resume a|rm a"
	if strings.Join(calls, "|") != want {
		t.Fatalf("argv = %q, want %q", calls, want)
	}
}

func TestPutTargetsTheOriginalByName(t *testing.T) {
	var calls []string
	c := captureExec(&calls, func([]string) (string, error) { return "", nil })
	err := c.Put(context.Background(), "nightly-ci-triage", Fields{
		Cron: "30 9 * * 1-5", Prompt: "triage harder", OnOverlap: "skip", CleanupTTLHours: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "put --original nightly-ci-triage --cron 30 9 * * 1-5 --prompt triage harder " +
		"--on-overlap skip --cleanup-ttl-hours 24"
	if strings.Join(calls, "|") != want {
		t.Fatalf("argv = %q, want %q", calls, want)
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
