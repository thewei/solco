package nativeui

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/settings"
)

// TestOnboardingOnlyFreshInstalls pins the guide's gate: an upgraded
// settings file (and the memory store tests run on) never sees it; a fresh
// install lands on it instead of Workspace.
func TestOnboardingOnlyFreshInstalls(t *testing.T) {
	existing := NewShell(WithSettings(app.NewSettingsService(settings.NewMemoryStore(settings.Default()))))
	if existing.router.Path() == routeOnboarding {
		t.Fatal("an upgraded settings file must not see the guide")
	}
	fresh := NewShell(WithSettings(app.NewSettingsService(settings.NewFileStore(t.TempDir()))))
	if fresh.router.Path() != routeOnboarding {
		t.Fatalf("a fresh install must land on the guide, got %q", fresh.router.Path())
	}
}

// TestOnboardingSkipPersistsAndLandsWorkspace pins the exit: skipping
// completes the guide once (Onboarded persists) and lands on Workspace.
func TestOnboardingSkipPersistsAndLandsWorkspace(t *testing.T) {
	service := app.NewSettingsService(settings.NewFileStore(t.TempDir()))
	shell := NewShell(WithSettings(service))
	shell.bootstrapPending = false
	shell.newTabStarter = toggleKindsStarter{providersTestKinds}
	tester := ui.NewTester(shell.View, 1200, 800)

	if !tester.HasText("Welcome to Solco") {
		t.Fatalf("guide first step missing; texts=%q", tester.Texts())
	}
	if err := tester.Click("Skip"); err != nil {
		t.Fatal(err)
	}
	if shell.router.Path() != routeWorkspace {
		t.Fatalf("after skip route = %q, want workspace", shell.router.Path())
	}
	if !service.Current().General.Onboarded {
		t.Fatal("skipping must persist Onboarded")
	}
}

// TestOnboardingWalkPersistsChoices walks all three steps: each choice
// persists the moment it is made (skipping would keep it), the agent step
// carries the shared toggle rows, and finishing lands on Workspace.
func TestOnboardingWalkPersistsChoices(t *testing.T) {
	service := app.NewSettingsService(settings.NewFileStore(t.TempDir()))
	shell := NewShell(WithSettings(service))
	shell.bootstrapPending = false
	shell.newTabStarter = toggleKindsStarter{providersTestKinds}
	tester := ui.NewTester(shell.View, 1200, 800)

	if err := tester.Click("Light"); err != nil {
		t.Fatal(err)
	}
	if got := service.Current().General.Appearance; got != "light" {
		t.Fatalf("appearance after step 1 = %q, want light", got)
	}
	if err := tester.Click("Continue"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("Choose your agents") {
		t.Fatalf("agent step missing; texts=%q", tester.Texts())
	}
	if err := tester.Click("Claude Code"); err != nil {
		t.Fatal(err)
	}
	if service.Current().Agents.IsEnabled("claude-code") {
		t.Fatal("the agent step must persist the opt-out at once")
	}
	if err := tester.Click("Continue"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("Solco on your phone") {
		t.Fatalf("phone step missing; texts=%q", tester.Texts())
	}
	if err := tester.Click("Get started"); err != nil {
		t.Fatal(err)
	}
	if shell.router.Path() != routeWorkspace {
		t.Fatalf("after finishing route = %q, want workspace", shell.router.Path())
	}
	if !service.Current().General.Onboarded {
		t.Fatal("finishing must persist Onboarded")
	}
	// The choice made in step 2 survived the walk: skipping never rolls back.
	if service.Current().Agents.IsEnabled("claude-code") {
		t.Fatal("the opt-out made mid-walk must survive")
	}
}
