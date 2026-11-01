package nativeui

/**
 * [INPUT]: Current fenced Git snapshot, Git preferences, the configured API channel (endpoint/model/stored key)
 * [OUTPUT]: AI candidate applied ONLY to Local Changes quick commit subject, with async stale guards
 * [POS]: Lightweight commit composer coordinator, no Git mutation or automatic commit; refuses to run unconfigured
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"strings"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/gitworkbench"
)

// User chose AI Commit in the explicit split-button menu. The review is
// metadata-only by default, exactly like the Commit modal. A changed root,
// change snapshot, or manually edited subject makes the response stale.
func (s *Shell) startQuickAICommit() {
	if s.git == nil || s.git.root == "" || s.commitQuickRunning || s.git.opBusy || s.git.committing || s.gdReviewingCommit() {
		return
	}
	snap := s.gitSnapshot()
	if snap == nil || len(snap.Files) == 0 {
		s.commitQuickError = "No changes to generate commit message from."
		return
	}
	if !s.commitGeneratorReady() {
		s.commitQuickError = "AI drafting is unavailable. Configure endpoint, model and API key in Settings → Git."
		return
	}
	paths := make([]string, 0, len(snap.Files))
	for _, cf := range snap.Files {
		paths = append(paths, cf.Path)
	}
	preview := gitworkbench.PrepareCommitPrompt(snap, paths, false)
	if preview.Text == "" {
		s.commitQuickError = "No eligible files; sensitive paths were excluded."
		return
	}
	rules := commitPromptPreference(s.settings.Workbench.CommitPrompt)
	prompt := agent.BuildCommitPrompt(rules, preview.Text)
	if len(prompt) > agent.MaxCommitPromptBytes {
		s.commitQuickError = "Changes exceed maximum prompt length."
		return
	}
	root, signature, before := s.git.root, snap.Signature, s.gitQuickSubject
	cfg := s.settings.Workbench.CommitAPI
	key, _ := s.commitAPIKey()
	ctx, cancel := context.WithCancel(context.Background())
	s.commitQuickCancel = cancel
	s.commitQuickGen++
	generation := s.commitQuickGen
	s.commitQuickRunning = true
	s.commitQuickError = ""
	executor := s.commitGenExecutor
	if executor == nil {
		executor = agent.GenerateCommitSuggestion
	}
	go func() {
		result, err := executor(ctx, agent.CommitRequest{Endpoint: cfg.Endpoint, Model: cfg.Model, APIKey: key, Prompt: prompt})
		s.applyOnUI(func() {
			if generation != s.commitQuickGen {
				return
			}
			s.commitQuickCancel = nil
			s.commitQuickRunning = false
			if s.git == nil || s.git.root != root || s.gitSnapshot() == nil || s.gitSnapshot().Signature != signature ||
				s.gitQuickSubject != before || s.surface.current() != WorkspaceSurfaceDiff || s.commitsMode ||
				commitPromptPreference(s.settings.Workbench.CommitPrompt) != rules || s.settings.Workbench.CommitAPI != cfg {
				s.commitQuickError = "Changes or draft changed. Generate again to avoid overwriting your edits."
				return
			}
			if err != nil {
				if ctx.Err() != nil {
					s.commitQuickError = "Generation canceled."
				} else {
					s.commitQuickError = firstErrLine(err)
				}
				return
			}
			subject, _, parseErr := parseCommitCandidate(result.Message)
			if parseErr != nil {
				s.commitQuickError = parseErr.Error()
				return
			}
			s.gitQuickSubject = strings.TrimSpace(subject)
			s.commitQuickError = ""
			s.pendingToast = "Commit subject generated."
		})
	}()
}

func (s *Shell) cancelQuickAICommit() {
	s.commitQuickGen++
	if s.commitQuickCancel != nil {
		s.commitQuickCancel()
		s.commitQuickCancel = nil
	}
	s.commitQuickRunning = false
	s.commitQuickError = ""
}
