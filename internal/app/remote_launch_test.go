package app

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/remotehost"
)

func newTestLauncher(rt *fakeAppRuntime, kinds []string, kindsErr error) *RemoteLauncher {
	service := &LaunchService{registry: agent.NewLaunchRegistry()}
	starter := &SessionStarter{
		service:   service,
		runtime:   func(string) agent.LaunchRuntime { return rt },
		listKinds: func(context.Context) ([]string, error) { return kinds, kindsErr },
		now:       time.Now,
	}
	l := &RemoteLauncher{sessions: starter}
	starter.run = func(ctx context.Context, _ agent.LaunchRuntime, intent agent.LaunchIntent) (agent.LaunchOutcome, *agent.LaunchFailure) {
		return agent.RunLaunchTransaction(ctx, rt, Preparer{}, intent, agent.TransactionTimings{})
	}
	return l
}

func TestRemoteLauncherKinds(t *testing.T) {
	ids := func(ks []remotehost.LaunchKind) []string {
		var out []string
		for _, k := range ks {
			out = append(out, k.ID)
		}
		return out
	}
	got := ids(newTestLauncher(&fakeAppRuntime{}, []string{"claude", "pi", "nonsense"}, nil).Kinds())
	if want := []string{"terminal", "claude-code", "pi"}; !equalStrings(got, want) {
		t.Fatalf("live list: got %v want %v", got, want)
	}
	got = ids(newTestLauncher(&fakeAppRuntime{}, nil, errors.New("no herdr")).Kinds())
	if want := []string{"terminal", "claude-code", "codex", "pi", "omp"}; !equalStrings(got, want) {
		t.Fatalf("fallback: got %v want %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRemoteLauncherAgentEmptyPromptStartsOnly(t *testing.T) {
	rt := &fakeAppRuntime{}
	l := newTestLauncher(rt, nil, nil)
	dir := t.TempDir()
	pane, err := l.Launch(context.Background(), "default", remotehost.LaunchSpec{RequestID: "req_aaaaaaaa", CWD: dir, Kind: "codex"})
	if err != nil || pane.PaneID != "pane-9" || pane.PromptPending {
		t.Fatalf("pane %+v err %v", pane, err)
	}
	if len(rt.started) != 1 || len(rt.prompts) != 0 {
		t.Fatalf("started %d prompts %d", len(rt.started), len(rt.prompts))
	}
	if !rt.createdWS {
		t.Fatal("a new path should create a workspace")
	}
}

func TestRemoteLauncherAgentSendsPrompt(t *testing.T) {
	rt := &fakeAppRuntime{}
	l := newTestLauncher(rt, nil, nil)
	if _, err := l.Launch(context.Background(), "default", remotehost.LaunchSpec{RequestID: "req_bbbbbbbb", WorkspaceID: "w1", CWD: t.TempDir(), Kind: "codex", Prompt: "hello"}); err != nil {
		t.Fatal(err)
	}
	if len(rt.prompts) != 1 || rt.prompts[0] != "hello" {
		t.Fatalf("prompts %v", rt.prompts)
	}
}

func TestRemoteLauncherIdempotentAndConflict(t *testing.T) {
	rt := &fakeAppRuntime{}
	l := newTestLauncher(rt, nil, nil)
	spec := remotehost.LaunchSpec{RequestID: "req_cccccccc", WorkspaceID: "w1", CWD: t.TempDir(), Kind: "codex"}
	first, err := l.Launch(context.Background(), "default", spec)
	if err != nil {
		t.Fatal(err)
	}
	again, err := l.Launch(context.Background(), "default", spec)
	if err != nil || again != first || len(rt.started) != 1 {
		t.Fatalf("replay: %+v %v started=%d", again, err, len(rt.started))
	}
	spec.Prompt = "different"
	_, err = l.Launch(context.Background(), "default", spec)
	var le *remotehost.LaunchError
	if !errors.As(err, &le) || le.Code != "launch_conflict" {
		t.Fatalf("want launch_conflict, got %v", err)
	}
}

func TestRemoteLauncherMissingPathIsPathUnavailable(t *testing.T) {
	l := newTestLauncher(&fakeAppRuntime{}, nil, nil)
	missing := t.TempDir() + "/nope"
	for _, kind := range []string{"codex", remotehost.LaunchTerminal} {
		_, err := l.Launch(context.Background(), "default", remotehost.LaunchSpec{RequestID: "req_" + kind + "_x", CWD: missing, Kind: kind})
		var le *remotehost.LaunchError
		if !errors.As(err, &le) || le.Code != "path_unavailable" || le.Status != 400 {
			t.Fatalf("%s: got %v", kind, err)
		}
	}
	if _, err := os.Stat(missing); err == nil {
		t.Fatal("the launcher must not create directories")
	}
}

func TestRemoteLauncherTerminalOpensTab(t *testing.T) {
	rt := &fakeAppRuntime{}
	l := newTestLauncher(rt, nil, nil)
	pane, err := l.Launch(context.Background(), "default", remotehost.LaunchSpec{RequestID: "req_dddddddd", WorkspaceID: "w1", CWD: t.TempDir(), Kind: remotehost.LaunchTerminal})
	if err != nil || pane.PaneID != "pane-1" || len(rt.started) != 0 {
		t.Fatalf("pane %+v err %v started %d", pane, err, len(rt.started))
	}
}

func TestRemoteLauncherTerminalRetryOpensNoSecondTab(t *testing.T) {
	rt := &countingRuntime{}
	l := newTestLauncher(&rt.fakeAppRuntime, nil, nil)
	l.sessions.runtime = func(string) agent.LaunchRuntime { return rt }
	spec := remotehost.LaunchSpec{RequestID: "req_eeeeeeee", WorkspaceID: "w1", CWD: t.TempDir(), Kind: remotehost.LaunchTerminal}
	first, err := l.Launch(context.Background(), "default", spec)
	if err != nil {
		t.Fatal(err)
	}
	again, err := l.Launch(context.Background(), "default", spec)
	if err != nil || again != first || rt.tabs != 1 {
		t.Fatalf("retry: pane %+v err %v tabs %d", again, err, rt.tabs)
	}
	spec.Kind = "codex"
	if _, err := l.Launch(context.Background(), "default", spec); err == nil {
		t.Fatal("the same id for a different kind must conflict")
	}
}

type countingRuntime struct {
	fakeAppRuntime
	tabs int
}

func (c *countingRuntime) CreateTabWithoutFocus(ctx context.Context, workspaceID, cwd string) (agent.TabCreated, error) {
	c.tabs++
	return agent.TabCreated{TabID: "tab-1", PaneID: "pane-1"}, nil
}

func TestLaunchServiceSessionsIsOneSharedService(t *testing.T) {
	service := NewLaunchService(nil)
	if service.Sessions() != service.Sessions() {
		t.Fatal("desktop and phone must share one SessionStarter (and so one idempotency registry)")
	}
	if NewRemoteLauncher(service).sessions != service.Sessions() {
		t.Fatal("the phone launcher is an adapter over the shared service")
	}
}

// blockedRuntime is an Agent that Herdr reports blocked and never interactive_ready (Claude Code's
// first-run trust prompt): the transaction cannot verify it, but it is a started session.
type blockedRuntime struct{ fakeAppRuntime }

func (b *blockedRuntime) AgentByPane(_ context.Context, paneID string) (*agent.StartedAgent, error) {
	return &agent.StartedAgent{PaneID: paneID, Agent: "claude", Status: "blocked"}, nil
}

func TestRemoteLauncherBlockedAgentIsStartedAndKeepsThePromptForTheUser(t *testing.T) {
	rt := &blockedRuntime{}
	l := newTestLauncher(&rt.fakeAppRuntime, nil, nil)
	l.sessions.runtime = func(string) agent.LaunchRuntime { return rt }
	spec := remotehost.LaunchSpec{RequestID: "req_ffffffff", WorkspaceID: "w1", CWD: t.TempDir(), Kind: "claude-code", Prompt: "fix it"}
	first, err := l.Launch(context.Background(), "default", spec)
	if err != nil || first.PaneID == "" || !first.PromptPending {
		t.Fatalf("blocked agent with a prompt: %+v %v", first, err)
	}
	if len(rt.prompts) != 0 {
		t.Fatal("the prompt must not be typed into a trust prompt")
	}
	again, err := l.Launch(context.Background(), "default", spec)
	if err != nil || again != first || len(rt.started) != 1 {
		t.Fatalf("replay must answer the same without a second start: %+v %v started=%d", again, err, len(rt.started))
	}
	spec.RequestID, spec.Prompt = "req_gggggggg", ""
	bare, err := l.Launch(context.Background(), "default", spec)
	if err != nil || bare.PaneID == "" || bare.PromptPending {
		t.Fatalf("blocked agent without a prompt has nothing pending: %+v %v", bare, err)
	}
}

func TestRemoteLauncherTerminalTypesItsFirstCommand(t *testing.T) {
	rt := &fakeAppRuntime{}
	l := newTestLauncher(rt, nil, nil)
	var typed []string
	l.sessions.sendInput = func(_ context.Context, _, paneID, text string) error {
		typed = append(typed, paneID+":"+text)
		return nil
	}
	spec := remotehost.LaunchSpec{RequestID: "req_eeeeeeee", WorkspaceID: "w1", CWD: t.TempDir(), Kind: remotehost.LaunchTerminal, Prompt: "ls -la"}
	pane, err := l.Launch(context.Background(), "default", spec)
	if err != nil || pane.PromptPending || len(typed) != 1 || typed[0] != "pane-1:ls -la" {
		t.Fatalf("pane %+v err %v typed %v", pane, err, typed)
	}
	if _, err := l.Launch(context.Background(), "default", spec); err != nil || len(typed) != 1 {
		t.Fatalf("a replay typed the command again: %v %v", err, typed)
	}
}

func TestRemoteLauncherTerminalCommandFailureKeepsThePane(t *testing.T) {
	l := newTestLauncher(&fakeAppRuntime{}, nil, nil)
	l.sessions.sendInput = func(context.Context, string, string, string) error { return errors.New("refused") }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pane, err := l.Launch(ctx, "default", remotehost.LaunchSpec{RequestID: "req_ffffffff", WorkspaceID: "w1", CWD: t.TempDir(), Kind: remotehost.LaunchTerminal, Prompt: "ls"})
	if err != nil || pane.PaneID != "pane-1" || !pane.PromptPending {
		t.Fatalf("pane %+v err %v", pane, err)
	}
}
