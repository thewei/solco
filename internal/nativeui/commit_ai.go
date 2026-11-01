package nativeui

/**
 * [INPUT]: selected fenced Git review, Git preferences, the configured API channel (endpoint/model/Keychain key)
 * [OUTPUT]: cancelable one-shot AI message candidate in the existing Commit modal; the channel and key helpers every AI-commit surface shares
 * [POS]: UI/application coordinator; Git mutation stays with existing Commit preflight; the channel is API-only and refuses to run unconfigured; the key is read only on click paths, render paths use the cached presence flag, which probeCommitKey fills once off the UI lane the first time a surface needs it (never at launch)
 * [PROTOCOL]: update this header and check CLAUDE.md on changes
 */

import (
	"context"
	"errors"
	"strings"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/gitworkbench"
	"github.com/thewei/solco/internal/settings"
)

// commitAPIKeyName is the Keychain key of the commit generator's API key.
// The key never enters settings files, drafts echoed back, logs or errors.
const commitAPIKeyName = "git.commitapi.token"

// commitStore returns the credential store backing the commit API key.
// Production is the system Keychain; tests inject memory.
func (s *Shell) commitStore() usageSecrets {
	if s.commitSecrets != nil {
		return s.commitSecrets
	}
	return appSecrets
}

// commitAPIKey reads the stored key. Click paths only — never render.
func (s *Shell) commitAPIKey() (string, bool) {
	value, ok := s.commitStore().Get(commitAPIKeyName)
	if !ok {
		return "", false
	}
	trimmed := strings.TrimSpace(value)
	return trimmed, trimmed != ""
}

// commitKeyPresent reports a key is stored (one Keychain read).
func (s *Shell) commitKeyPresent() bool {
	_, ok := s.commitAPIKey()
	return ok
}

// commitEndpointConfigured reports a valid endpoint and a model name.
func commitEndpointConfigured(cfg settings.CommitAPI) bool {
	if strings.TrimSpace(cfg.Model) == "" {
		return false
	}
	_, err := agent.CommitChatURL(cfg.Endpoint)
	return err == nil
}

// commitGeneratorUsable is the render-safe readiness check: it trusts the
// cached key-presence flag because it runs per frame.
func (s *Shell) commitGeneratorUsable() bool {
	if !commitEndpointConfigured(s.settings.Workbench.CommitAPI) {
		return false
	}
	s.probeCommitKey()
	return s.commitAPIKeySet
}

// probeCommitKey learns once, off the UI lane, whether a key is stored. It
// runs only when a surface needs the answer (never at launch, so a Keychain
// prompt cannot hold the window back) and only in a real window: tests set
// commitAPIKeySet themselves. A found key only ever raises the flag, so a
// Save that lands first is not undone by a late read.
func (s *Shell) probeCommitKey() {
	if s.commitKeyProbed || s.win == nil {
		return
	}
	s.commitKeyProbed = true
	go func() {
		if s.commitKeyPresent() {
			s.applyOnUI(func() { s.commitAPIKeySet = true })
		}
	}()
}

// commitGeneratorReady is the click-path check; it touches the Keychain.
func (s *Shell) commitGeneratorReady() bool {
	_, ok := s.commitAPIKey()
	return commitEndpointConfigured(s.settings.Workbench.CommitAPI) && ok
}

// startCommitAISuggestion turns the selected fenced review into one API
// request. The candidate is presented for explicit review; nothing here
// commits, stages or touches Git state.
func (s *Shell) startCommitAISuggestion() {
	if s.commitGenRunning || s.git == nil || s.git.root == "" ||
		s.surface.current() != WorkspaceSurfaceCommit || s.surface.commit.InFlight {
		return
	}
	snap := s.gitSnapshot()
	if snap == nil || !s.surface.commit.Captured {
		s.commitGenError = "Wait for Commit review verification before generating."
		return
	}
	if !s.commitGeneratorReady() {
		s.commitGenError = "AI drafting is unavailable. Configure endpoint, model and API key in Settings → Git."
		return
	}
	selected := s.selectedCommitPaths()
	preview := gitworkbench.PrepareCommitPrompt(snap, selected, s.commitPromptIncludeExcerpt)
	if preview.Text == "" {
		s.commitGenError = "Nothing to summarize. Pick another file; sensitive files are left out."
		return
	}
	rules := commitPromptPreference(s.settings.Workbench.CommitPrompt)
	prompt := agent.BuildCommitPrompt(rules, preview.Text)
	if len(prompt) > agent.MaxCommitPromptBytes {
		s.commitGenError = "The commit rules and review are too long; shorten the rules."
		return
	}
	root, signature := s.git.root, snap.Signature
	cfg := s.settings.Workbench.CommitAPI
	key, _ := s.commitAPIKey()
	includeExcerpt, commitRules := s.commitPromptIncludeExcerpt, rules
	pathsKey := strings.Join(selected, "\x00")
	ctx, cancel := context.WithCancel(context.Background())
	s.commitGenCancel = cancel
	s.commitGenGeneration++
	generation := s.commitGenGeneration
	s.commitGenRunning = true
	s.commitGenError = ""
	s.commitAIReplaceConfirm = false
	executor := s.commitGenExecutor
	if executor == nil {
		executor = agent.GenerateCommitSuggestion
	}
	go func() {
		result, err := executor(ctx, agent.CommitRequest{Endpoint: cfg.Endpoint, Model: cfg.Model, APIKey: key, Prompt: prompt})
		s.applyOnUI(func() {
			if generation != s.commitGenGeneration {
				return
			}
			s.commitGenCancel = nil
			s.commitGenRunning = false
			if s.git == nil || s.git.root != root || s.surface.current() != WorkspaceSurfaceCommit ||
				s.gitSnapshot() == nil || s.gitSnapshot().Signature != signature ||
				strings.Join(s.selectedCommitPaths(), "\x00") != pathsKey ||
				s.commitPromptIncludeExcerpt != includeExcerpt ||
				commitPromptPreference(s.settings.Workbench.CommitPrompt) != commitRules ||
				s.settings.Workbench.CommitAPI != cfg {
				s.commitGenError = "Selected changes or workspace changed. Generate again on the current review."
				return
			}
			if err != nil {
				if errors.Is(err, context.Canceled) {
					s.commitGenError = "Generation canceled."
				} else {
					s.commitGenError = err.Error()
				}
				return
			}
			s.commitAIResponse = result.Message
			s.commitPromptPreviewOpen = true
			if _, _, parseErr := parseCommitCandidate(result.Message); parseErr != nil {
				s.commitGenError = "The suggestion needs editing before it can be applied."
			} else {
				s.commitGenError = "Suggestion ready. Review and apply it to the commit message."
			}
		})
	}()
}

func (s *Shell) cancelCommitAISuggestion() {
	s.commitGenGeneration++
	if s.commitGenCancel != nil {
		s.commitGenCancel()
		s.commitGenCancel = nil
	}
	s.commitGenRunning = false
}

func (s *Shell) resetCommitAISuggestion() {
	s.cancelCommitAISuggestion()
	s.commitGenError = ""
}
