package herdr

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNewAPIErrorPrefersHerdrsOwnCode(t *testing.T) {
	body := []byte(`{"error":{"code":"agent_pane_busy","message":"pane w1T:p1 is not an available shell"},"id":"cli:agent:start"}`)
	err := newAPIError([]string{"agent", "start", "triage"}, body, "", errors.New("exit status 1"))

	if !HasCode(err, CodePaneBusy) {
		t.Fatalf("code not recovered from %v", err)
	}
	want := "agent start: agent_pane_busy: pane w1T:p1 is not an available shell"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err.Error(), want)
	}
}

func TestNewAPIErrorReadsTheCodeFromStderr(t *testing.T) {
	// herdr 0.8.0 writes the error envelope to stderr, not stdout.
	stderr := `{"error":{"code":"agent_pane_busy","message":"agent target pane w3V:p1 is not an available shell"},"id":"cli:agent:start"}` + "\n"
	err := newAPIError([]string{"agent", "start", "triage"}, nil, stderr, errors.New("exit status 1"))

	if !HasCode(err, CodePaneBusy) {
		t.Fatalf("code not recovered from %v", err)
	}
	want := "agent start: agent_pane_busy: agent target pane w3V:p1 is not an available shell"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err.Error(), want)
	}
}

func TestNewAPIErrorFallsBackToTheExecError(t *testing.T) {
	// The failure that made a run undiagnosable: herdr exits non-zero and
	// prints nothing, so the only thing left to report is why the process died.
	err := newAPIError([]string{"worktree", "create"}, nil, "", errors.New("exit status 1"))

	want := "worktree create: exit status 1"
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
}

func TestNewAPIErrorKeepsStderrOverTheExecError(t *testing.T) {
	// "exit status 1" says less than whatever herdr wrote, so it loses.
	err := newAPIError([]string{"worktree", "create"}, nil,
		"  fatal: not a git repository\n", errors.New("exit status 128"))

	want := "worktree create: fatal: not a git repository"
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
}

func TestNewAPIErrorSurvivesANilExecError(t *testing.T) {
	if err := newAPIError([]string{"pane", "read"}, nil, "", nil); err == nil {
		t.Fatal("want an error even with nothing to say")
	}
}

func TestNotificationArgsOmitsAnEmptyBody(t *testing.T) {
	got := notificationArgs("triage failed", "", SoundRequest)
	for _, a := range got {
		if a == "--body" {
			t.Fatalf("args = %v, want no --body flag for an empty body", got)
		}
	}
}

func TestNotificationArgsPassesTheTitleAsAPositionalArgument(t *testing.T) {
	got := notificationArgs("triage failed", "boom", SoundRequest)
	want := []string{"notification", "show", "triage failed",
		"--body", "boom", "--position", notificationPosition, "--sound", "request"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

func TestTabCreate(t *testing.T) {
	for _, tc := range []struct {
		name, response, pane string
		wantError            bool
	}{
		{"created", `{"result":{"type":"tab_created","root_pane":{"pane_id":"w7:p2"}}}`, "w7:p2", false},
		{"missing pane", `{"result":{"type":"tab_created"}}`, "", true},
		{"invalid response", `not json`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "herdr")
			argsFile := filepath.Join(dir, "args")
			script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$HERDR_TEST_ARGS\"\nprintf '%s\\n' \"$HERDR_TEST_RESPONSE\"\n"
			if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HERDR_BIN_PATH", bin)
			t.Setenv("HERDR_TEST_ARGS", argsFile)
			t.Setenv("HERDR_TEST_RESPONSE", tc.response)
			pane, err := (Client{}).TabCreate("w7", "/a repo", "auto: daily summary")
			if (err != nil) != tc.wantError || pane != tc.pane {
				t.Fatalf("pane=%q err=%v", pane, err)
			}
			raw, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
			want := []string{"tab", "create", "--workspace", "w7", "--cwd", "/a repo", "--label", "auto: daily summary", "--no-focus"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("args=%q, want %q", got, want)
			}
		})
	}
}

// fakeHerdr installs a herdr that records its argv and prints response.
func fakeHerdr(t *testing.T, response string) (argsFile string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	argsFile = filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$HERDR_TEST_ARGS\"\nprintf '%s\\n' \"$HERDR_TEST_RESPONSE\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", bin)
	t.Setenv("HERDR_TEST_ARGS", argsFile)
	t.Setenv("HERDR_TEST_RESPONSE", response)
	return argsFile
}

func readArgs(t *testing.T, argsFile string) []string {
	t.Helper()
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

func TestAClientBoundToASessionTargetsItOnEveryCall(t *testing.T) {
	args := fakeHerdr(t, `{"result":{"root_pane":{"pane_id":"w7:p2"}}}`)
	if _, err := (Client{Session: "automations"}).TabCreate("w7", "/r", "l"); err != nil {
		t.Fatal(err)
	}
	got := readArgs(t, args)
	if len(got) < 3 || got[0] != "--session" || got[1] != "automations" || got[2] != "tab" {
		t.Fatalf("args = %q, want --session automations ahead of the subcommand", got)
	}
}

const twoSessions = `{"sessions":[
 {"default":true,"name":"default","running":true,"socket_path":"/h/.config/herdr/herdr.sock"},
 {"default":false,"name":"automations","running":true,"socket_path":"/h/.config/herdr/sessions/automations/herdr.sock"},
 {"default":false,"name":"old","running":false,"socket_path":"/h/.config/herdr/sessions/old/herdr.sock"}]}`

func TestRunningSessionsLeavesOutTheStoppedOnes(t *testing.T) {
	args := fakeHerdr(t, twoSessions)
	got, err := (Client{Session: "ignored"}).RunningSessions()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"default", "automations"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sessions = %q, want %q", got, want)
	}
	// Listing sessions is about the machine, not about one of them.
	if a := readArgs(t, args); a[0] != "session" {
		t.Fatalf("args = %q, want no --session on session list", a)
	}
}

func TestCurrentSessionIsTheOneWhoseSocketThisProcessUses(t *testing.T) {
	fakeHerdr(t, twoSessions)
	t.Setenv("HERDR_SESSION", "")
	t.Setenv("HERDR_SOCKET_PATH", "/h/.config/herdr/sessions/automations/herdr.sock")
	if got := (Client{}).CurrentSession(); got != "automations" {
		t.Fatalf("current = %q, want automations", got)
	}
}

func TestCurrentSessionOutsideHerdrIsTheDefaultOne(t *testing.T) {
	// herdr's CLI talks to the default session when nothing says otherwise.
	fakeHerdr(t, twoSessions)
	t.Setenv("HERDR_SESSION", "")
	t.Setenv("HERDR_SOCKET_PATH", "")
	if got := (Client{}).CurrentSession(); got != "default" {
		t.Fatalf("current = %q, want default", got)
	}
}

func TestCurrentSessionOfABoundClientIsItsOwn(t *testing.T) {
	if got := (Client{Session: "automations"}).CurrentSession(); got != "automations" {
		t.Fatalf("current = %q, want automations", got)
	}
}

func TestAnErrorFromABoundClientIsLabelledByTheCommandNotTheSession(t *testing.T) {
	err := newAPIError([]string{"--session", "b", "agent", "start", "x"}, nil,
		`{"error":{"code":"agent_not_ready","message":"m"}}`, errors.New("exit status 1"))
	if want := "agent start: agent_not_ready: m"; err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
}

func TestAgentLiveStateIsBoundToPaneAndRecognizesRealOMPIdle(t *testing.T) {
	cases := []struct {
		name, body   string
		wantErr      bool
		kind, status string
		ready        bool
	}{
		{name: "interactive omp idle", body: `{"result":{"type":"agent_info","agent":{"pane_id":"w1:p1","agent":"omp","agent_status":"idle","interactive_ready":true}}}`, kind: "omp", status: "idle", ready: true},
		{name: "human turn working", body: `{"result":{"type":"agent_info","agent":{"pane_id":"w1:p1","agent":"omp","agent_status":"working","interactive_ready":true}}}`, kind: "omp", status: "working", ready: true},
		{name: "trust dialog", body: `{"result":{"type":"agent_info","agent":{"pane_id":"w1:p1","agent":"omp","agent_status":"blocked","interactive_ready":false}}}`, kind: "omp", status: "blocked"},
		{name: "different pane", body: `{"result":{"type":"agent_info","agent":{"pane_id":"w8:p9","agent":"omp","agent_status":"idle","interactive_ready":true}}}`, wantErr: true},
		{name: "incomplete identity", body: `{"result":{"type":"agent_info","agent":{"pane_id":"w1:p1","agent_status":"idle","interactive_ready":true}}}`, wantErr: true},
		{name: "unknown response", body: `{"result":{"type":"pane_info","agent":{"pane_id":"w1:p1","agent":"omp","agent_status":"idle","interactive_ready":true}}}`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeHerdr(t, tc.body)
			state, err := (Client{Session: "work"}).AgentState("w1:p1")
			if (err != nil) != tc.wantErr {
				t.Fatalf("state=%+v error=%v", state, err)
			}
			if !tc.wantErr && (state.Kind != tc.kind || state.Status != tc.status || state.InteractiveReady != tc.ready) {
				t.Fatalf("status: %+v", state)
			}
		})
	}
}

func TestAgentPromptAcceptedUsesNonWaitingHerdrPromptAndVerifiesReceipt(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"right pane", `{"result":{"type":"agent_prompted","agent":{"pane_id":"w1:p1","agent_status":"working"}}}`, true},
		{"wrong pane", `{"result":{"type":"agent_prompted","agent":{"pane_id":"w2:p2"}}}`, false},
		{"wrong response", `{"result":{"type":"agent_info","agent":{"pane_id":"w1:p1"}}}`, false},
		{"missing receipt", `{"result":{}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := fakeHerdr(t, tc.body)
			err := (Client{Session: "work"}).AgentPromptAccepted("w1:p1", "automated status text")
			if (err == nil) != tc.ok {
				t.Fatalf("receipt: %v", err)
			}
			got := readArgs(t, args)
			expected := []string{"--session", "work", "agent", "prompt", "w1:p1", "automated status text"}
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("unsafe CLI options: %q", got)
			}
		})
	}
}

func TestPaneReadContextHonorsCancelledReportProbe(t *testing.T) {
	fakeHerdr(t, `{"result":{"text":"should not execute"}}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Client{}).PaneReadContext(ctx, "w1:p1", 60); err == nil {
		t.Fatal("cancelled Herdr Pane read did not fail closed")
	}
}

// A herdr that is not on PATH is told apart from one that died mid-call: the
// first provably created nothing.
func TestNewAPIErrorKeepsAMissingBinaryRecognisable(t *testing.T) {
	missing := newAPIError([]string{"workspace", "create"}, nil, "", &exec.Error{Name: "herdr", Err: exec.ErrNotFound})
	if !errors.Is(missing, exec.ErrNotFound) {
		t.Fatalf("a missing binary is not recognisable: %v", missing)
	}
	if errors.Is(newAPIError([]string{"workspace", "create"}, nil, "", errors.New("signal: killed")), exec.ErrNotFound) {
		t.Fatal("a killed herdr was taken for a missing one")
	}
}

func TestIdentityRefusedMatchesOnlyHerdrsTwoIdentityRefusals(t *testing.T) {
	for msg, want := range map[string]bool{
		"agent w1:p1 is no longer the pane foreground process": true,
		"pane w1:p1 is not an active named agent":              true,
		"agent w1:p1 is busy":                                  false,
		"agent w1:p1 is still starting":                        false,
	} {
		err := newAPIError([]string{"agent", "prompt", "w1:p1"},
			[]byte(`{"error":{"code":"agent_not_ready","message":"`+msg+`"}}`), "", errors.New("exit status 1"))
		if got := IdentityRefused(err); got != want {
			t.Errorf("IdentityRefused(%q) = %v, want %v", msg, got, want)
		}
	}
	if IdentityRefused(errors.New("no longer the pane foreground process")) {
		t.Error("a non-Herdr error must never open the fallback")
	}
}

// A herdr that refuses `agent prompt` on the foreground identity and answers
// `agent get` / `pane run` as scripted; every call is appended to a log.
func identityHerdr(t *testing.T, agentGet string, getFails bool) (logFile string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	logFile = filepath.Join(dir, "calls")
	fail := "0"
	if getFails {
		fail = "1"
	}
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$HERDR_TEST_LOG"
case "$*" in
*"agent prompt"*)
  printf '%s\n' '{"error":{"code":"agent_not_ready","message":"agent w1:p1 is no longer the pane foreground process"}}'; exit 1;;
*"agent get"*)
  if [ "$HERDR_TEST_GET_FAILS" = 1 ]; then printf '%s\n' '{"error":{"code":"agent_not_running","message":"gone"}}'; exit 1; fi
  printf '%s\n' "$HERDR_TEST_GET";;
*"pane run"*) printf '%s\n' '{"result":{"type":"ok"}}';;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", bin)
	t.Setenv("HERDR_TEST_LOG", logFile)
	t.Setenv("HERDR_TEST_GET", agentGet)
	t.Setenv("HERDR_TEST_GET_FAILS", fail)
	return logFile
}

func TestAgentPromptAcceptedFallsBackToThePaneOnlyForALiveAgent(t *testing.T) {
	live := `{"result":{"type":"agent_info","agent":{"pane_id":"w1:p1","agent":"omp","agent_status":"idle","interactive_ready":true}}}`
	notReady := `{"result":{"type":"agent_info","agent":{"pane_id":"w1:p1","agent":"omp","agent_status":"idle","interactive_ready":false}}}`
	for _, tc := range []struct {
		name, get string
		getFails  bool
		typed     bool
	}{
		{"live agent", live, false, true},
		{"agent not interactive-ready", notReady, false, false},
		{"agent gone", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logFile := identityHerdr(t, tc.get, tc.getFails)
			err := (Client{Session: "work"}).AgentPromptAccepted("w1:p1", "report text")
			raw, _ := os.ReadFile(logFile)
			typed := strings.Contains(string(raw), "pane run w1:p1 report text")
			if typed != tc.typed {
				t.Fatalf("typed=%v want %v; calls:\n%s", typed, tc.typed, raw)
			}
			if tc.typed && err != nil {
				t.Fatalf("fallback delivered but returned %v", err)
			}
			if !tc.typed && !IdentityRefused(err) {
				t.Fatalf("a guard failure must return the original refusal, got %v", err)
			}
		})
	}
}

func TestSubmitViaPaneTypesOnlyIntoAnIdleLiveAgent(t *testing.T) {
	info := func(status string, ready bool) string {
		r := "false"
		if ready {
			r = "true"
		}
		return `{"result":{"type":"agent_info","agent":{"pane_id":"w1:p1","agent":"omp","agent_status":"` + status + `","interactive_ready":` + r + `}}}`
	}
	other := `{"result":{"type":"agent_info","agent":{"pane_id":"w1:p9","agent":"omp","agent_status":"idle","interactive_ready":true}}}`
	for _, tc := range []struct {
		name, get string
		getFails  bool
		typed     bool
	}{
		{"idle agent", info("idle", true), false, true},
		{"finished turn nobody has looked at", info("done", true), false, true},
		{"working agent", info("working", true), false, false},
		{"blocked agent", info("blocked", true), false, false},
		{"agent not interactive-ready", info("idle", false), false, false},
		{"another pane's agent", other, false, false},
		{"agent gone", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logFile := identityHerdr(t, tc.get, tc.getFails)
			err := (Client{Session: "work"}).SubmitViaPane("w1:p1", "group message")
			raw, _ := os.ReadFile(logFile)
			typed := strings.Contains(string(raw), "pane run w1:p1 group message")
			if typed != tc.typed {
				t.Fatalf("typed=%v want %v; calls:\n%s", typed, tc.typed, raw)
			}
			if tc.typed && err != nil {
				t.Fatalf("typed once but returned %v", err)
			}
			if !tc.typed && !errors.Is(err, ErrNothingTyped) {
				t.Fatalf("a guard failure must say nothing was typed, got %v", err)
			}
			if n := strings.Count(string(raw), "pane run"); n > 1 {
				t.Fatalf("typed %d times, want at most once", n)
			}
		})
	}
}
