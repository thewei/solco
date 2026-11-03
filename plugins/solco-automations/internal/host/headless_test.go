package host

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
)

func headlessWith(ops ops, a config.Automation) headlessWork {
	return headlessWork{ops: ops, knobs: fast(), a: a}
}

// pointStateHere keeps the prompt file of a test out of the user's real state.
func pointStateHere(t *testing.T) {
	t.Helper()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
}

func TestHeadlessSucceedsOnAZeroExitAndNeverTypesThePromptInThePane(t *testing.T) {
	pointStateHere(t)
	var command []string
	ops := &fakeOps{paneRun: func(_ string, c ...string) error { command = c; return nil }}
	ops.paneRead = func(string, int) (string, error) { return "OK\n" + hdlMarker(command) + ":0\n", nil }
	a := config.Automation{Agent: "claude", Prompt: "Summarise 'today'\nand stop", Model: "sonnet", MCPConfig: "/m.json"}

	if err := headlessWith(ops, a).do(Session{PaneID: "p"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if len(command) != 1 || !strings.HasPrefix(command[0], "sh -c '") {
		t.Fatalf("command = %v, want one typed line running sh -c", command)
	}
	line := command[0]
	if strings.Contains(line, "Summarise") {
		t.Errorf("the prompt was typed into the pane: %q", line)
	}
	// -p first, the options after it, then -- so a variadic --mcp-config
	// cannot swallow the prompt, then the prompt variable.
	want := `"claude" "-p" "--mcp-config" "/m.json" "--model" "sonnet" -- "$p"`
	if !strings.Contains(line, want) {
		t.Errorf("command = %q, want it to contain %s", line, want)
	}
}

func TestHeadlessPromptFileHoldsThePromptPrivatelyAndTheCommandRemovesIt(t *testing.T) {
	pointStateHere(t)
	var command []string
	ops := &fakeOps{paneRun: func(_ string, c ...string) error { command = c; return nil }}
	ops.paneRead = func(string, int) (string, error) {
		// By the time the pane answers, the prompt must be on disk for the
		// command that was just typed to read.
		return hdlMarker(command) + ":0\n", nil
	}
	a := config.Automation{Agent: "codex", Prompt: "multi\nline 'quoted' $HOME"}
	if err := headlessWith(ops, a).do(Session{PaneID: "p"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command[0], "rm -f ") {
		t.Fatalf("command = %q, want it to delete the prompt file after reading it", command[0])
	}
	// The path is the first quoted argument of cat.
	_, rest, _ := strings.Cut(command[0], `cat "`)
	path, _, _ := strings.Cut(rest, `"`)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != a.Prompt {
		t.Errorf("prompt file = %q, want the prompt verbatim", raw)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("prompt file mode = %v, want 0600", info.Mode().Perm())
	}
	if !strings.Contains(command[0], `"codex" "exec" "$p"`) {
		t.Errorf("command = %q, want codex exec with no -- ", command[0])
	}
}

func TestHeadlessFailsOnANonZeroExitAndSaysWhereToLook(t *testing.T) {
	pointStateHere(t)
	var command []string
	ops := &fakeOps{paneRun: func(_ string, c ...string) error { command = c; return nil }}
	ops.paneRead = func(string, int) (string, error) { return "not logged in\n" + hdlMarker(command) + ":1\n", nil }

	err := headlessWith(ops, config.Automation{Agent: "pi", Prompt: "x"}).do(Session{PaneID: "w1:p1"}, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "pi exited 1") || !strings.Contains(err.Error(), "w1:p1") {
		t.Fatalf("got %v, want the status and the pane to open", err)
	}
}

func TestHeadlessNamesAMissingBinaryAndRunsNothing(t *testing.T) {
	pointStateHere(t)
	ran := false
	ops := &fakeOps{
		lookPath: func(string) error { return errors.New("not found") },
		paneRun:  func(string, ...string) error { ran = true; return nil },
	}
	err := headlessWith(ops, config.Automation{Agent: "claude", Prompt: "x"}).do(Session{PaneID: "p"}, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "needs claude on PATH") {
		t.Fatalf("got %v, want the missing binary named", err)
	}
	if ran {
		t.Error("nothing should have run in the pane")
	}
}

func TestHeadlessTimesOutAndLeavesNoPromptBehind(t *testing.T) {
	pointStateHere(t)
	ops := &fakeOps{paneRead: func(string, int) (string, error) { return "thinking…\n", nil }}
	err := headlessWith(ops, config.Automation{Agent: "claude", Prompt: "x"}).do(Session{PaneID: "p"}, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("got %v, want a timeout", err)
	}
}

func TestHeadlessIsChosenOverTheInteractiveAgent(t *testing.T) {
	h := &live{ops: &fakeOps{}, knobs: fast()}
	if _, ok := h.workFor(config.Automation{Agent: "claude", Prompt: "x", Headless: true}).(headlessWork); !ok {
		t.Error("a headless automation must not use the interactive agent path")
	}
	if _, ok := h.workFor(config.Automation{Agent: "claude", Prompt: "x"}).(agentWork); !ok {
		t.Error("an ordinary automation keeps the interactive agent path")
	}
}

// hdlMarker recovers the marker the typed line prints its status behind.
func hdlMarker(command []string) string {
	if len(command) == 0 {
		return "HDL-none"
	}
	_, rest, _ := strings.Cut(command[0], `printf "\\n`)
	marker, _, _ := strings.Cut(rest, ":")
	return marker
}

// The typed line has to survive the one thing the pane does to it — herdr
// joins arguments with spaces and types them — so run it for real: a prompt
// full of quotes, dollars and newlines must reach the "agent" byte for byte.
func TestHeadlessLineRunsUnderShAndDeliversTheExactPrompt(t *testing.T) {
	pointStateHere(t)
	dir := t.TempDir()
	out := dir + "/got"
	// A stand-in agent that records its arguments.
	script := dir + "/fakeagent"
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+out+"\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	prompt := "a 'b' \"c\" $HOME `x`\nsecond line \\ end"
	a := config.Automation{Agent: script, Prompt: prompt, Model: "m 1"}
	marker := "HDL-test"
	file, err := writePrompt(marker, prompt)
	if err != nil {
		t.Fatal(err)
	}
	line := headlessCommand(a, []string{"exec"}, file, marker)
	cmd := exec.Command("sh", "-c", line)
	screen, _ := cmd.CombinedOutput()
	if code, done := exitCode(string(screen), marker); !done || code != 3 {
		t.Fatalf("exit = (%d, %v) from %q, want the agent's 3 behind the marker", code, done, screen)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := "exec\n--model\nm 1\n" + prompt + "\n"; string(got) != want {
		t.Errorf("agent got %q, want %q", got, want)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("the command must delete the prompt file after reading it")
	}
}
