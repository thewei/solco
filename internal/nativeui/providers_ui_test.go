package nativeui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/agenthooks"
	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

// toggleKindsStarter serves a fixed launch list so the pages' kinds reads
// never touch the real herdr CLI.
type toggleKindsStarter struct{ kinds []app.SessionKind }

func (f toggleKindsStarter) Kinds() []app.SessionKind { return f.kinds }
func (f toggleKindsStarter) StartSession(context.Context, string, app.SessionSpec) (app.SessionResult, error) {
	return app.SessionResult{}, nil
}

// providersTestKinds is the launch list the toggle-page tests use: two
// hook-covered agents, one integration-only agent, Terminal last.
var providersTestKinds = []app.SessionKind{
	{ID: app.TerminalKind, Label: "Terminal"},
	{ID: "claude-code", Label: "Claude Code"},
	{ID: "antigravity", Label: "Antigravity CLI"},
	{ID: "pi", Label: "Pi"},
}

// TestProvidersPageTogglesLead pins the toggle-first Agents page: every
// launchable agent renders as a toggle row whose leading mechanism (the
// Solco status hook) carries the status and the repair, while agents Herdr
// cannot start here keep their diagnosis cards (hook lead, integration
// fallback) without a toggle.
func TestProvidersPageTogglesLead(t *testing.T) {
	shell := NewShell()
	shell.newTabStarter = toggleKindsStarter{providersTestKinds}
	installed := map[string]bool{}
	var commands []string
	shell.providers.integrations = agent.NewIntegrationHealthServiceWithRunner(func(ctx context.Context, args []string) (string, error) {
		commands = append(commands, args[0]+" "+args[1])
		if args[1] == "install" {
			installed[args[2]] = true
			return "", nil
		}
		state := func(target string) string {
			if installed[target] {
				return "current (v10)"
			}
			return "not installed"
		}
		return "claude: " + state("claude") + " (/Users/demo/.claude/hooks/herdr-agent-state.sh)\n" +
			"codex: " + state("codex") + " (/Users/demo/.codex/herdr-agent-state.sh)\n" +
			"antigravity-cli: " + state("antigravity-cli") + " (/Users/demo/.gemini/config/hooks/herdr-agent-state.sh)\n", nil
	})
	home := t.TempDir()
	shell.providers.hooks = agenthooks.NewManagerWithHome(home)

	shell.router.Replace("/settings/providers")
	tester := ui.NewTester(shell.View, 1200, 800)

	// Route entry audited the integration facet exactly once.
	audits := 0
	for _, command := range commands {
		if command == "integration status" {
			audits++
		}
	}
	if audits != 1 {
		t.Fatalf("route-entry integration audits = %d, want 1", audits)
	}

	// Every launchable agent is a toggle row (the switch repeats its label
	// as its accessible text, so the name may count twice).
	for _, label := range []string{"Claude Code", "Antigravity CLI", "Pi"} {
		if got := countTextExact(tester.Texts(), label); got < 1 {
			t.Fatalf("%s toggle row missing; texts=%q", label, tester.Texts())
		}
	}

	// Hook repairs: Claude Code and Antigravity CLI on their toggle rows,
	// Codex and Cursor on their diagnosis cards (kinds absent from the
	// launch list).
	if got := countTextExact(tester.Texts(), "Install status hook"); got != 4 {
		t.Fatalf("hook actions = %d, want 4; texts=%q", got, tester.Texts())
	}
	// The Herdr integration fallback lane survives only on diagnosis cards
	// of agents that are not launchable here: Codex and Cursor (the audit
	// reports an unsupported integration for every provider missing from
	// its output). The toggle rows carry the hook as their single leading
	// mechanism.
	if got := countTextExact(tester.Texts(), "Herdr integration"); got != 2 {
		t.Fatalf("integration fallback rows = %d, want 2 (Codex, Cursor); texts=%q", got, tester.Texts())
	}
	if got := countTextExact(tester.Texts(), "Install integration"); got != 1 {
		t.Fatalf("integration repair actions = %d, want 1 (Codex); texts=%q", got, tester.Texts())
	}

	// The Codex diagnosis card still repairs end to end: herdr integration
	// install runs off render and the facet reconciles from a fresh audit.
	if err := tester.Click("Install integration"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !installed["codex"] {
		t.Fatalf("herdr integration install did not run; installed=%v", installed)
	}
	if !tester.HasText("Codex integration is current") {
		t.Fatalf("integration toast missing; texts=%q", tester.Texts())
	}

	// Installing Claude Code's status hook flips its toggle row to Current:
	// the repair disappears and the reconciled detail shows.
	if err := tester.Click("Install status hook"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if shell.providers.actionRunning {
		t.Fatal("hook action still marked in flight after reconciliation")
	}
	script := filepath.Join(home, ".solco", "hooks", "solco-claude-hook.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("hook script not installed: %v", err)
	}
	wiring, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil || !strings.Contains(string(wiring), "solco-claude-hook.sh") {
		t.Fatalf("claude settings not wired (err=%v)", err)
	}
	if !tester.HasText("Claude Code status hook is current") {
		t.Fatalf("hook toast missing; texts=%q", tester.Texts())
	}
	if !tester.HasText("Live status is on") {
		t.Fatalf("reconciled hook detail missing; texts=%q", tester.Texts())
	}
	if got := countTextExact(tester.Texts(), "Install status hook"); got != 3 {
		t.Fatalf("hook actions after install = %d, want 3; texts=%q", got, tester.Texts())
	}
}

// TestAgentsTogglePersistsAndInstalls pins the toggle-on promise: turning
// an agent off only persists the opt-out; turning it back on installs
// Solco's own status hook at once, and agents without a Solco hook install
// nothing.
func TestAgentsTogglePersistsAndInstalls(t *testing.T) {
	shell := NewShell()
	shell.newTabStarter = toggleKindsStarter{providersTestKinds}
	shell.providers.integrations = agent.NewIntegrationHealthServiceWithRunner(func(ctx context.Context, args []string) (string, error) {
		return "claude: current (v10) (/Users/demo/.claude/hooks/herdr-agent-state.sh)\n" +
			"antigravity-cli: current (v4) (/Users/demo/.gemini/config/hooks/herdr-agent-state.sh)\n", nil
	})
	home := t.TempDir()
	shell.providers.hooks = agenthooks.NewManagerWithHome(home)

	shell.router.Replace("/settings/providers")
	tester := ui.NewTester(shell.View, 1200, 800)

	// Absent key = enabled: Claude Code starts on, turns off on one click.
	if !shell.agentEnabled(history.AgentClaudeCode) {
		t.Fatal("an agent without an opt-out must start enabled")
	}
	if err := tester.Click("Claude Code"); err != nil {
		t.Fatal(err)
	}
	if shell.agentEnabled(history.AgentClaudeCode) {
		t.Fatal("the off click must persist the opt-out")
	}
	persisted := shell.settingsService.Current().Agents
	if persisted.IsEnabled("claude-code") {
		t.Fatalf("persisted agents map = %+v, want claude-code off", persisted.Enabled)
	}

	// Turning it back on installs the Solco status hook immediately: only
	// our own wiring, never the Herdr integration.
	if err := tester.Click("Claude Code"); err != nil {
		t.Fatal(err)
	}
	if !shell.agentEnabled(history.AgentClaudeCode) {
		t.Fatal("the on click must clear the opt-out")
	}
	script := filepath.Join(home, ".solco", "hooks", "solco-claude-hook.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("status hook not installed on enable: %v", err)
	}
	wiring, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil || !strings.Contains(string(wiring), "solco-claude-hook.sh") {
		t.Fatalf("claude settings not wired on enable (err=%v)", err)
	}
	if value := shell.settingsService.Current().Agents; len(value.Enabled) != 0 {
		t.Fatalf("enabling stores nothing; enabled map = %+v", value.Enabled)
	}

	// Pi has no Solco hook: toggling it installs nothing and never fails.
	if err := tester.Click("Pi"); err != nil {
		t.Fatal(err)
	}
	if shell.agentEnabled(history.AgentPi) {
		t.Fatal("the off click for Pi must persist")
	}
	if err := tester.Click("Pi"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi")); err == nil {
		t.Fatal("Pi must have no Solco wiring to install")
	}
}

// TestProvidersPageHookOnlyAndDeferred pins the honest edges of the merged
// surface: hook-only extras (Aider) get one informational card with no
// action, and deferred providers never expose one.
func TestProvidersPageHookOnlyAndDeferred(t *testing.T) {
	shell := NewShell()
	shell.newTabStarter = toggleKindsStarter{[]app.SessionKind{
		{ID: app.TerminalKind, Label: "Terminal"},
		{ID: "claude-code", Label: "Claude Code"},
	}}
	shell.providers.integrations = agent.NewIntegrationHealthServiceWithRunner(func(ctx context.Context, args []string) (string, error) {
		return "", nil
	})
	shell.providers.hooks = agenthooks.NewManagerWithHome(t.TempDir())

	shell.router.Replace("/settings/providers")
	tester := ui.NewTester(shell.View, 1200, 800)

	if !tester.HasText("Aider") {
		t.Fatalf("aider informational card missing; texts=%q", tester.Texts())
	}
	if !tester.HasText("Status comes from Herdr's screen detection") {
		t.Fatalf("aider honest detail missing; texts=%q", tester.Texts())
	}
	if tester.HasText("Update integration") || tester.HasText("Update status hook") {
		t.Fatalf("unexpected update actions; texts=%q", tester.Texts())
	}
	if !tester.HasText("Not available yet") {
		t.Fatalf("deferred provider state missing; texts=%q", tester.Texts())
	}
	// The empty integration audit leaves every registry provider
	// unsupported or deferred: no integration action may appear. Hook
	// actions may legitimately appear for provisionable providers.
	if tester.HasText("Install integration") {
		t.Fatalf("integration action offered on a non-actionable audit; texts=%q", tester.Texts())
	}
}

// TestProvidersAuditFailureStaysPlain pins the failed-audit surface: the
// notice speaks plainly instead of printing the exec error, offers Retry,
// and agents without a reading show no column of Unknown pills.
func TestProvidersAuditFailureStaysPlain(t *testing.T) {
	shell := NewShell()
	shell.newTabStarter = toggleKindsStarter{[]app.SessionKind{
		{ID: app.TerminalKind, Label: "Terminal"},
		{ID: "codex", Label: "Codex"},
	}}
	shell.providers.integrations = agent.NewIntegrationHealthServiceWithRunner(func(ctx context.Context, args []string) (string, error) {
		return "", fmt.Errorf("herdr integration status: %w", herdr.ErrCLINotFound)
	})
	shell.providers.hooks = agenthooks.NewManagerWithHome(t.TempDir())

	shell.router.Replace("/settings/providers")
	tester := ui.NewTester(shell.View, 1200, 800)

	if !tester.HasText("Couldn't find Herdr. Install it, then try again.") {
		t.Fatalf("plain failure notice missing; texts=%q", tester.Texts())
	}
	if !tester.HasText("Retry") {
		t.Fatalf("failure notice has no Retry; texts=%q", tester.Texts())
	}
	for _, text := range tester.Texts() {
		for _, leak := range []string{"exec:", "$PATH", "executable file", "integration status", "CLI not found"} {
			if strings.Contains(text, leak) {
				t.Fatalf("internal detail %q on screen: %q", leak, text)
			}
		}
	}
	if tester.HasText("Unknown") {
		t.Fatalf("agents without a reading must not show Unknown pills; texts=%q", tester.Texts())
	}
	if !tester.HasText("Codex") {
		t.Fatalf("agent rows must stay listed; texts=%q", tester.Texts())
	}
}

// TestContinueTargetsRespectsToggle pins the Continue gate: the "Continue
// in" candidates drop agents the user turned off.
func TestContinueTargetsRespectsToggle(t *testing.T) {
	shell := NewShell()
	shell.hist.providers = []history.AgentID{history.AgentClaudeCode, history.AgentCodex, history.AgentPi}
	shell.settings.Agents.Enabled = map[string]bool{"codex": false}
	meta := history.SessionMeta{Agent: history.AgentClaudeCode}
	var got []string
	for _, target := range shell.continueTargets(meta) {
		got = append(got, string(target))
	}
	if want := "pi"; len(got) != 1 || got[0] != want {
		t.Fatalf("continue targets = %v, want [%s]", got, want)
	}
}

func TestIntegrationErrorTextIsPlain(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("herdr integration status: %w", herdr.ErrCLINotFound), "Couldn't find Herdr. Install it, then try again."},
		{context.DeadlineExceeded, "Checking took too long. Try again."},
		{errors.New(`exec: "herdr": executable file not found in $PATH`), "Couldn't read agent status. Try again."},
	} {
		if got := integrationErrorText(tc.err); got != tc.want {
			t.Errorf("integrationErrorText(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func countTextExact(texts []string, want string) int {
	count := 0
	for _, text := range texts {
		if text == want {
			count++
		}
	}
	return count
}
