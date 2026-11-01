package chatsession

import (
	"slices"
	"testing"

	"github.com/thewei/solco/internal/conversation"
)

// Screens are captured from real panes (2026-11-01, designs/cursor-agy-chat).
const cursorApprovalScreen = `
  $ echo hi > /tmp/matrix-probe/b.txt Waiting for approval...

───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
 $  echo hi > /tmp/matrix-probe/b.txt in .

 Run this command?
 Not in allowlist: echo
  → Run (once) (y)
    Add Shell(echo) to allowlist? (tab)
    Run Everything (shift+tab)
    Skip & tell the agent what to do instead (esc or n)
`

const agyApprovalScreen = `
○ Thought for 6.0s (Initial hypothesis: ` + "`/tmp/matrix-probe`" + ` might be part of a larger codebase...)
● Ran (echo hi > /tmp/matrix-probe/b.txt) (ctrl+o to expand)

Command
───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────

Requesting permission for:
   echo hi > /tmp/matrix-probe/b.txt

Run this command?
> 1. Yes, run command
  2. Yes, and always allow in this conversation for commands that start with 'echo hi > /tmp/matrix-probe/b.txt'
  3. Yes, and always allow for commands that start with 'echo hi > /tmp/matrix-probe/b.txt' (Persist to settings.json)
  4. No, cancel

  ↑/↓ Navigate · tab Amend · ctrl+g edit/expand command
esc to cancel                                                                                   Gemini 3.8 Flash · high
`

func TestDetectAgentPromptCursorApproval(t *testing.T) {
	got, ok := DetectAgentPrompt(cursorApprovalScreen)
	if !ok || got.Kind != conversation.InteractionPermission {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
	if got.Prompt != "Run this command? `echo hi > /tmp/matrix-probe/b.txt`" {
		t.Fatalf("prompt %q", got.Prompt)
	}
	if !slices.Equal(got.Options, []string{"Run once", "Skip"}) {
		t.Fatalf("options %v (the allowlist and Run Everything rows persist: never offered)", got.Options)
	}
	run, _ := got.AnswerKeys("Run once", false)
	skip, _ := got.AnswerKeys("Skip", false)
	if !slices.Equal(run, []string{"y"}) || !slices.Equal(skip, []string{"n", "enter"}) {
		t.Fatalf("keys run=%v skip=%v", run, skip)
	}
}

func TestDetectAgentPromptAgyApproval(t *testing.T) {
	got, ok := DetectAgentPrompt(agyApprovalScreen)
	if !ok || got.Kind != conversation.InteractionPermission {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
	if got.Prompt != "Run this command? `echo hi > /tmp/matrix-probe/b.txt`" {
		t.Fatalf("prompt %q", got.Prompt)
	}
	if len(got.Options) != 3 || got.Options[0] != "Yes, run command" || got.Options[2] != "No, cancel" {
		t.Fatalf("options %v (the Persist-to-settings row is never offered)", got.Options)
	}
	for option, want := range map[string][]string{
		"Yes, run command": {"enter"},
		got.Options[1]:     {"down", "enter"},
		"No, cancel":       {"down", "down", "down", "enter"}, // row 4: the dropped row still counts for navigation
	} {
		keys, err := got.AnswerKeys(option, false)
		if err != nil || !slices.Equal(keys, want) {
			t.Errorf("%q -> %v %v, want %v", option, keys, err, want)
		}
	}
}

func TestDetectAgyApprovalNavigatesFromTheHighlightedRow(t *testing.T) {
	screen := `Requesting permission for:
   rm -rf build

Run this command?
  1. Yes, run command
> 2. Yes, and always allow in this conversation for commands that start with 'rm -rf build'
  3. No, cancel
`
	got, ok := DetectAgentPrompt(screen)
	if !ok {
		t.Fatal("no prompt")
	}
	keys, _ := got.AnswerKeys("Yes, run command", false)
	cancel, _ := got.AnswerKeys("No, cancel", false)
	if !slices.Equal(keys, []string{"up", "enter"}) || !slices.Equal(cancel, []string{"down", "enter"}) {
		t.Fatalf("keys %v / %v", keys, cancel)
	}
}

func TestDetectAgentPromptIgnoresOrdinaryScreens(t *testing.T) {
	for _, screen := range []string{
		"",
		"  → Add a follow-up\n\n  Composer 2.5 · 11.4%      Run Everything",
		"Run this command?\n1. Yes\n2. No", // another agent's sheet: DetectScreenPrompt's job
		"  Run (once) (y)\n",               // no skip row: not the verified shape
	} {
		if got, ok := DetectAgentPrompt(screen); ok {
			t.Errorf("screen %q read as %+v", screen, got)
		}
	}
}

func TestDetectTerminalDialog(t *testing.T) {
	for screen, want := range map[string]string{
		"  │  ⚠ Workspace Trust Required   │":                                             "Cursor Agent is asking whether to trust this folder.",
		" │ ↑/↓ option · ←/→ question · Space select · Enter next/submit · Esc to skip │": "Cursor Agent is asking a question in the terminal.",
		"Do you trust the contents of this project?\n> Yes, I trust this folder":          "Antigravity CLI is asking whether to trust this folder.",
		"Terms of Service & Data Use":                                                     "Antigravity CLI is showing its first-run setup.",
		"Welcome\nChoose your color scheme:":                                              "Antigravity CLI is showing its first-run setup.",
	} {
		if got, ok := DetectTerminalDialog(screen); !ok || got != want {
			t.Errorf("%q -> %q %v", screen, got, ok)
		}
	}
	if _, ok := DetectTerminalDialog("  → Add a follow-up\n  Composer 2.5"); ok {
		t.Error("an ordinary Cursor screen is not a dialog")
	}
}
