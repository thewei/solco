package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
)

// headlessWork runs the agent's own non-interactive command in the pane.
//
// The interactive path starts a TUI and types the prompt into it, so any
// first-run dialog (folder trust, project MCP approval) stops an unattended
// run. A headless command has none: it takes the prompt as an argument, prints
// its answer in the pane and exits, and its exit status is the verdict —
// recovered the same way hwfWork recovers a workflow's, by printing it behind
// a marker and reading the screen back.
//
// The prompt travels in a private file and not on the command line: a pane is
// a terminal, whose line discipline truncates a long typed line, and quoting
// a multi-line prompt through a shell invites the first stray quote to change
// what the agent is asked. The file is removed by the command itself as soon
// as it is read.
type headlessWork struct {
	ops   ops
	knobs knobs
	a     config.Automation
}

func (w headlessWork) do(s Session, timeout time.Duration) error {
	flags := config.HeadlessFlags(w.a.Agent)
	if len(flags) == 0 {
		return fmt.Errorf("agent %q has no headless mode", w.a.Agent)
	}
	if err := w.ops.LookPath(w.a.Agent); err != nil {
		return fmt.Errorf("headless run needs %s on PATH", w.a.Agent)
	}

	marker := fmt.Sprintf("HDL-%d", time.Now().UnixNano())
	promptFile, err := writePrompt(marker, w.a.Prompt)
	if err != nil {
		return fmt.Errorf("saving the prompt: %w", err)
	}
	command := headlessCommand(w.a, flags, promptFile, marker)
	if err := w.ops.PaneRun(s.PaneID, command); err != nil {
		_ = os.Remove(promptFile)
		return fmt.Errorf("running %s: %w", w.a.Agent, err)
	}

	deadline := time.Now().Add(timeout)
	for {
		screen, err := w.ops.PaneRead(s.PaneID, 200)
		if err == nil {
			if code, done := exitCode(screen, marker); done {
				if code != 0 {
					return fmt.Errorf("%s exited %d — open %s for its output", w.a.Agent, code, s.PaneID)
				}
				return nil
			}
		}
		if time.Now().After(deadline) {
			_ = os.Remove(promptFile)
			return fmt.Errorf("%s did not finish within %s", w.a.Agent, timeout)
		}
		time.Sleep(w.knobs.workflowPoll)
	}
}

// headlessCommand is the line typed into the pane: an `sh -c` over a script
// that reads the prompt, deletes the file, runs the agent with its
// non-interactive flags and prints the status behind the marker.
//
// `herdr pane run` joins its arguments with spaces and types the result, so
// the script is quoted into one word here, and it runs under sh whatever the
// user's own shell is. Inside it everything is double-quoted and the script
// holds no single quote of its own, so one outer pair is enough. The
// marker's literal appears once in the echoed line (with %d unexpanded) and
// once with the real status; only the latter matches a digit.
func headlessCommand(a config.Automation, flags []string, promptFile, marker string) string {
	argv := []string{dq(a.Agent)}
	for _, f := range append(append([]string{}, flags...), agentArgs(a)...) {
		argv = append(argv, dq(f))
	}
	// claude's --mcp-config takes any number of values and would swallow the
	// prompt that follows it; "--" ends the options.
	if a.Agent == "claude" {
		argv = append(argv, "--")
	}
	argv = append(argv, `"$p"`)
	script := fmt.Sprintf(`p=$(cat %s); rm -f %s; %s; printf "\\n%s:%%d\\n" $?`,
		dq(promptFile), dq(promptFile), strings.Join(argv, " "), marker)
	return "sh -c " + shellQuote(script)
}

// dq quotes one word for a double-quoted context of sh.
func dq(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(s) + `"`
}

// writePrompt keeps the prompt where only this user can read it, in the
// plugin's own state directory, and returns its path.
func writePrompt(marker, prompt string) (string, error) {
	dir := filepath.Join(config.StateDir(), "prompts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, marker)
	return path, os.WriteFile(path, []byte(prompt), 0o600)
}
