package nativeui

import (
	"slices"
	"testing"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/history"
)

// Screens captured from the real TUIs (Claude Code 2.1.308, Codex 0.164).
const claudeModelScreen = `   Select model
   Switch between Claude models. Your pick becomes the default for new sessions. For other/previous model names,
   specify with --model.

     1.  Default (recommended)                            Use the default model (currently GLM flash · routing
                                                          group)
     2.  group/glm-flash[1m]                              Custom Opus model (1M context)
     3.  Fable                                            Fable 5.1 · Most capable for your hardest and
                                                          longest-running tasks · $10/$50 per Mtok
     4.  group/glm-flash[1m]                              Custom Sonnet model (1M context)
   ❯ 5.  group/glm-flash[1m] ✔                            Custom Haiku model
     6.  GLM flash · routing group                        via magpie
     7.  Gemini 3.8 Flash · routing group                 via magpie
     8.  All Models · routing group                       via magpie
     9.  flash · routing group                            via magpie
   ↓ 10. OurFreeModel · routing group                     via magpie
      … +25 models

   ● High effort (default) ←/→ to adjust

   Enter to set as default · s to use this session only · Esc to cancel
`

const codexModelScreen = `  Select Model and Effort
  Warning: OpenAI base URL is overridden to http://127.0.0.1:3425/backend-api/codex. Selecting models may not be
  supported or work properly.


  1. GPT-6-Sol                                         Previous generation workhorse model.
  2. GPT-6-Luna                                        Fast and affordable model for easier tasks.
› 3. GLM flash · routing group (current)               GLM flash · routing group via magpie
  4. Gemini 3.8 Flash · routing group                  Gemini 3.8 Flash · routing group via magpie
  5. All Models · routing group                        All Models · routing group via magpie
  6. flash · routing group                             flash · routing group via magpie
  7. OurFreeModel · routing group                      OurFreeModel · routing group via magpie
  8. deepseek-flash · routing group                    deepseek-flash · routing group via magpie
↓
  enter select · esc back
`

const codexPermissionsScreen = `  Update Model Permissions
  1. Ask for approval       Read and edit workspace files and run commands, with approval required for internet
                            or edits outside the workspace
  2. Approve for me         Only ask for actions detected as potentially unsafe
› 3. Full Access (current)  Use with caution: Codex can edit files outside this workspace and access the internet
                            without approval
  4. Read Only              Read workspace files, with approval required for edits or internet access

  enter select · esc back
`

func TestDetectPickerClaudeModel(t *testing.T) {
	got, ok := detectPicker(claudeModelScreen)
	if !ok {
		t.Fatal("no picker")
	}
	if got.Prompt != "Select model (rows beyond the screen: use Terminal)" || got.Start != 4 || got.Commit != "s" || !got.Picker {
		t.Fatalf("prompt=%q start=%d commit=%q", got.Prompt, got.Start, got.Commit)
	}
	if len(got.Options) != 11 || got.Options[0] != "Default (recommended)" || got.Options[10] != pickerClose || got.EscapeOption != pickerClose {
		t.Fatalf("options %q", got.Options)
	}
	// Two rows share a label; the description keeps them apart.
	if got.Options[1] == got.Options[3] || got.Options[1] == got.Options[4] {
		t.Fatalf("duplicate labels survive: %q", got.Options)
	}
	if got.Descriptions[2] != "Fable 5.1 · Most capable for your hardest and longest-running tasks · $10/$50 per Mtok" {
		t.Fatalf("wrapped description: %q", got.Descriptions[2])
	}
}

func TestDetectPickerCodex(t *testing.T) {
	model, ok := detectPicker(codexModelScreen)
	if !ok || model.Prompt != "Select Model and Effort" || model.Start != 2 || model.Commit != "" || len(model.Options) != 9 {
		t.Fatalf("model %+v ok=%v", model, ok)
	}
	perm, ok := detectPicker(codexPermissionsScreen)
	if !ok || perm.Prompt != "Update Model Permissions" || perm.Start != 2 {
		t.Fatalf("permissions %+v", perm)
	}
	if !slices.Equal(perm.Options[:4], []string{"Ask for approval", "Approve for me", "Full Access (current)", "Read Only"}) {
		t.Fatalf("options %q", perm.Options)
	}
	if perm.Descriptions[2] != "Use caution: Codex can edit files outside this workspace and access the internet without approval" && perm.Descriptions[2] != "Use with caution: Codex can edit files outside this workspace and access the internet without approval" {
		t.Fatalf("description %q", perm.Descriptions[2])
	}
}

func TestDetectPickerIgnoresPlainScreens(t *testing.T) {
	for _, screen := range []string{
		"",
		"1. first\n2. second\n\nsome reply",
		"Esc to cancel · Enter to confirm\n",
		"  1. only one\n  enter select · esc back\n",
	} {
		if _, ok := detectPicker(screen); ok {
			t.Fatalf("matched %q", screen)
		}
	}
}

func TestDetectPickerCodexEffortAppliesToSessionOnly(t *testing.T) {
	got, ok := detectPicker(`  Select Reasoning Level for Gemini 3.8 Flash · routing group
  1. Low
  2. Medium
› 3. High (default)

  enter default · s session · esc back
`)
	if !ok || got.Commit != "s" || got.Start != 2 || got.Prompt != "Select Reasoning Level for Gemini 3.8 Flash · routing group" {
		t.Fatalf("%+v ok=%v", got, ok)
	}
}

func TestDetectPickerClaudeSwitchConfirmationHasNoFooter(t *testing.T) {
	got, ok := detectPicker(`▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔
   Switch model?
   Your next response will be slower and use more tokens
   This conversation is cached for the current model. Switching to
   Gemini 3.8 Flash · routing group means the full history gets
   re-read on your next message.
   ❯ 1. Yes, switch to Gemini 3.8 Flash · routing group
     2. No, go back
`)
	if !ok || got.Prompt != "Switch model?" || got.Start != 0 || len(got.Options) != 3 || got.Options[1] != "No, go back" {
		t.Fatalf("%+v ok=%v", got, ok)
	}
	// Without a selector mark a footerless list is just text.
	if _, ok := detectPicker("Pick one\n  1. a\n  2. b\n"); ok {
		t.Fatal("matched unmarked footerless rows")
	}
}

// A command the agent answers outside its transcript leaves no row to consume
// the pending echo; it must not stand as a user bubble over a "Working" line.
func TestTerminalCommandLeavesNoEcho(t *testing.T) {
	shell := NewShell()
	shell.chatPaneID = "p-cmd"
	shell.chatLivePump = &history.LivePump{}
	shell.workbench.directory = agent.NewAgentDirectory()
	shell.workbench.directory.Replace([]agent.AgentCardModel{{
		Key:      agent.AgentKey{InstanceID: "inst", TerminalID: "term-cmd"},
		PaneID:   "p-cmd",
		Provider: history.AgentClaudeCode,
	}})

	shell.chatAfterPrompt("req-1", "/status", conversation.DispositionSentNow, nil)
	if shell.chatPendingEcho != nil {
		t.Fatalf("a Terminal command left an echo: %#v", shell.chatPendingEcho)
	}
	shell.chatAfterPrompt("req-2", "explain this repo", conversation.DispositionSentNow, nil)
	if shell.chatPendingEcho == nil {
		t.Fatal("an ordinary prompt must still echo")
	}
}
