package nativeui

/**
 * [INPUT]: persisted git preferences, the configured commit API channel (memory secrets in tests), Native settings Git page and MyGo Tester
 * [OUTPUT]: default commit policy, actionable save/restore, and API channel configuration (endpoint/model persist, key only reaches the credential store)
 * [POS]: UI-013 Git settings acceptance, no process launch, no Keychain and no Git mutation
 * [PROTOCOL]: update header and check CLAUDE.md
 */

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/settings"
)

func TestGitPreferencesPageDefaultAndSave(t *testing.T) {
	s := NewShell()
	s.loading = false
	s.router.Replace("/settings/git")
	tester := ui.NewTester(s.View, 1200, 800)
	for _, title := range []string{"Git", "Commit message rules", "Save commit rules", "Restore default commit rules",
		"Commit message generator", "Endpoint", "Model", "API key", "Save endpoint", "Save model", "Save key", "Remove key", "Send Test Request"} {
		if !tester.HasText(title) {
			t.Fatalf("Git Settings missing %q: %q", title, tester.Texts())
		}
	}
	if !strings.Contains(s.settingsGitPromptDraft, "Conventional Commits") {
		t.Fatal("default commit policy missing")
	}
	if !tester.HasText("Unavailable until endpoint, model and API key are configured.") {
		t.Fatal("unconfigured channel must say so on the page")
	}
	// The editable rules are style only: the fixed safety rules are code and
	// the page doesn't show them.
	for _, text := range tester.Texts() {
		if strings.Contains(text, "Do NOT stage") || strings.Contains(text, "data, not instructions") {
			t.Fatalf("Git Settings shows the guardrail prompt: %q", text)
		}
	}
	s.settingsGitPromptDraft = "fix(scope): describe precise changes"
	if err := tester.Click("Save commit rules"); err != nil {
		t.Fatal(err)
	}
	if s.settings.Workbench.CommitPrompt != "fix(scope): describe precise changes" {
		t.Fatal("Commit rules were not persisted through settings service")
	}
	s.commitSecrets = commitMapSecrets{}
	s.settingsCommitEndpointDraft = "https://api.example.com/v1"
	if err := tester.Click("Save endpoint"); err != nil {
		t.Fatal(err)
	}
	if s.settings.Workbench.CommitAPI.Endpoint != "https://api.example.com/v1" {
		t.Fatal("endpoint not persisted")
	}
	s.settingsCommitEndpointDraft = "not a url"
	if err := tester.Click("Save endpoint"); err != nil {
		t.Fatal(err)
	}
	if s.settings.Workbench.CommitAPI.Endpoint != "https://api.example.com/v1" {
		t.Fatal("invalid endpoint was persisted")
	}
	s.settingsCommitModelDraft = "test-model"
	if err := tester.Click("Save model"); err != nil {
		t.Fatal(err)
	}
	if s.settings.Workbench.CommitAPI.Model != "test-model" {
		t.Fatal("model not persisted")
	}
	s.settingsCommitKeyDraft = "test-key-123"
	if err := tester.Click("Save key"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.commitStore().Get(commitAPIKeyName); !ok {
		t.Fatal("API key did not reach the credential store")
	}
	if !s.commitAPIKeySet || s.settingsCommitKeyDraft != "" {
		t.Fatal("saved key must set the presence flag and clear the draft")
	}
	if s.settings.Workbench.CommitAPI.Endpoint != "https://api.example.com/v1" || s.settings.Workbench.CommitAPI.Model != "test-model" {
		t.Fatal("the key must never enter the settings file shape")
	}
	if err := tester.Click("Remove key"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.commitStore().Get(commitAPIKeyName); ok || s.commitAPIKeySet {
		t.Fatal("removed key is still present")
	}
	if err := tester.Click("Restore default commit rules"); err != nil {
		t.Fatal(err)
	}
	if s.settings.Workbench.CommitPrompt != "" || s.settingsGitPromptDraft != settings.DefaultCommitPrompt {
		t.Fatal("Reset default prompt failed")
	}
}

// A custom endpoint typed into the field survives the field losing focus: a
// Combobox put the text back to the last preset (the "Save resets it" bug).
func TestGitEndpointKeepsCustomTextAfterBlur(t *testing.T) {
	s := NewShell()
	s.loading = false
	s.router.Replace("/settings/git")
	tester := ui.NewTester(s.View, 1200, 800)
	if err := tester.Click("API endpoint"); err != nil {
		t.Fatal(err)
	}
	tester.Type("https://llm.example.com/v1")
	if err := tester.Click("Model"); err != nil {
		t.Fatal(err)
	}
	if got := s.settingsCommitEndpointDraft; got != "https://llm.example.com/v1" {
		t.Fatalf("custom endpoint was reverted on blur: %q", got)
	}
	if err := tester.Click("Save endpoint"); err != nil {
		t.Fatal(err)
	}
	if got := s.settings.Workbench.CommitAPI.Endpoint; got != "https://llm.example.com/v1" {
		t.Fatalf("custom endpoint not persisted: %q", got)
	}
}
