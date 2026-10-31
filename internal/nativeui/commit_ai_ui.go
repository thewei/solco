package nativeui

/**
 * [INPUT]: selected reviewed Git snapshot, the configured API channel's suggestion state and privacy preference
 * [OUTPUT]: compact disclosure-based AI drafting UI within the Commit editor
 * [POS]: one optional Commit subsection; no duplicate modal, Git task or provider runtime; disabled until endpoint, model and key are configured
 */

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
)

// commitAISection is collapsed until explicitly opened. Its optional
// source preview and paste editor also stay collapsed until requested,
// keeping the primary Commit form short and predictable.
func (s *Shell) commitAISection(c *ui.Context, snap *gitworkbench.ChangesSnapshot) {
	sp := Spacing()
	t := c.Theme()
	if !s.commitPromptPreviewOpen {
		open := tipped(c, compactButton(c, "Draft Commit Message…"), "Generate a commit message draft with the configured AI")
		open.Disabled(len(s.selectedCommitPaths()) == 0 || !s.commitGeneratorUsable())
		if open.Clicked() {
			s.commitPromptPreviewOpen = true
			s.commitPromptIncludeExcerpt = false
			s.commitPromptTextVisible = false
			s.commitAIManualEntryOpen = false
		}
		if !s.commitGeneratorUsable() && len(s.selectedCommitPaths()) > 0 {
			ui.Text(c, "AI drafting is unavailable. Configure endpoint, model and API key in Settings → Git.").
				FontSize(Typography().Caption).TextColor(t.TextMuted)
		}
		return
	}
	preview := gitworkbench.PrepareCommitPrompt(snap, s.selectedCommitPaths(), s.commitPromptIncludeExcerpt)
	ui.Column(c).FillWidth().MinWidth(0).Gap(sp.S).Padding(sp.M).
		Border(1, designTokens(t.Dark).BorderSubtle).Radius(Radius().Control).Children(func() {
		ui.Row(c).FillWidth().Gap(sp.S).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Commit message draft").FontSize(Typography().Section).FontWeight(WeightSemibold).Grow(1)
			if compactButton(c, "Hide Draft").Clicked() {
				s.cancelCommitAISuggestion()
				s.commitPromptPreviewOpen = false
			}
		})
		ui.Textf(c, "%d included · %d sensitive excluded", preview.IncludedFiles, preview.ExcludedFiles).
			FontSize(Typography().Caption).TextColor(t.TextMuted)
		ui.Checkbox(c, &s.commitPromptIncludeExcerpt, "Include code excerpts")
		ui.Text(c, "Follows the commit rules in Settings → Git.").
			FontSize(Typography().Caption).TextColor(t.TextMuted)
		if preview.Text == "" {
			ui.Text(c, "Nothing to summarize. Sensitive files are left out.").
				FontSize(Typography().Caption).TextColor(t.Warning)
		}
		if preview.Truncated {
			ui.Text(c, "The changes are long, so only part is included.").FontSize(Typography().Caption).
				TextColor(t.Warning)
		}
		ui.Row(c).FillWidth().Gap(sp.S).Wrap().AlignItems(ui.Center).Children(func() {
			if s.commitGenRunning {
				ui.Spinner(c).Size(14, 14)
				ui.Text(c, "Generating…").FontSize(Typography().Caption).TextColor(t.TextMuted)
				if regularButton(c, "Cancel Generation").Clicked() {
					s.cancelCommitAISuggestion()
					s.commitGenError = "Generation canceled."
				}
			} else {
				generate := regularPrimaryButton(c, "Generate with AI")
				generate.Disabled(preview.Text == "" || !s.surface.commit.Captured || s.surface.commit.InFlight || !s.commitGeneratorUsable())
				if generate.Clicked() {
					s.startCommitAISuggestion()
				}
			}
			review := regularButton(c, "Review Prompt")
			review.Disabled(preview.Text == "")
			if review.Clicked() {
				s.commitPromptTextVisible = !s.commitPromptTextVisible
			}
			if !s.commitAIManualEntryOpen && s.commitAIResponse == "" {
				if regularButton(c, "Paste Suggestion…").Clicked() {
					s.commitAIManualEntryOpen = true
				}
			}
		})
		if s.commitGenError != "" {
			ui.Text(c, s.commitGenError).FontSize(Typography().Caption).TextColor(t.TextMuted)
		}
		if s.commitPromptTextVisible && preview.Text != "" {
			ui.Column(c).FillWidth().Height(118).Clip().
				Background(designTokens(t.Dark).Panel).Children(func() {
				ui.Scroll(c).Grow(1).Children(func() {
					ui.Text(c, preview.Text).FontSize(Typography().Micro).TextColor(t.TextMuted)
				})
			})
			if regularButton(c, "Copy Prompt").Clicked() {
				s.copyToClipboard(preview.Text)
				s.pendingToast = "Prompt copied to clipboard."
			}
		}
		if s.commitAIManualEntryOpen || s.commitAIResponse != "" {
			ui.Text(c, "Suggested commit message").FontSize(Typography().Caption).FontWeight(WeightSemibold)
			textArea(c, &s.commitAIResponse).Height(76).Grow(0).
				Placeholder("Subject followed by optional body").Label("Message suggestion")
			if s.commitAIError != "" {
				ui.Text(c, s.commitAIError).FontSize(Typography().Caption).TextColor(t.Danger)
			}
			if s.commitAIReplaceConfirm {
				ui.Text(c, "Replace your existing commit message?").FontSize(Typography().Caption).TextColor(t.Warning)
				ui.Row(c).Gap(sp.S).Children(func() {
					if regularButton(c, "Keep Current Message").Clicked() {
						s.commitAIReplaceConfirm = false
					}
					replace := regularButton(c, "Replace Message")
					replace.Disabled(s.surface.commit.InFlight || s.commitGenRunning)
					if replace.Clicked() {
						s.replaceCommitMessageWithSuggestion()
					}
				})
			} else {
				use := regularButton(c, "Use Suggested Message")
				use.Disabled(strings.TrimSpace(s.commitAIResponse) == "" || s.surface.commit.InFlight || s.commitGenRunning)
				if use.Clicked() {
					s.applyManualAISuggestion()
				}
			}
		}
	})
}
