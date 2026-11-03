package host

import (
	"fmt"
	"regexp"
)

// startupDialogs are the full-screen questions an agent asks before it will
// take a prompt: trust this folder, enable these project MCP servers. A run
// lands in a brand-new directory every time, so the first run in one meets
// them unattended.
//
// They are an answer for the person, never for the scheduler: typing the
// prompt (or Enter, or space) into one of these chooses an option on someone's
// behalf — enabling MCP servers is code execution. So a run that finds one
// stops, types nothing, and says what is on screen and where. The workspace
// stays open for exactly that.
//
// Measured: the Claude Code pair on 2026-11-03 (a fresh worktree path, and a
// repo with unapproved project MCP servers); Cursor and Antigravity are the
// strings Solco's own terminal-dialog guard already matches; Codex is its
// documented wording and has not been measured here.
var startupDialogs = []struct {
	marker *regexp.Regexp
	what   string
}{
	{regexp.MustCompile(`Quick safety check: Is this a project you created`), "Claude Code is asking whether to trust this folder"},
	{regexp.MustCompile(`new MCP servers? found in this project`), "Claude Code is asking which project MCP servers to enable"},
	{regexp.MustCompile(`Do you trust the contents of this directory\?`), "Codex is asking whether to trust this folder"},
	{regexp.MustCompile(`Workspace Trust Required`), "Cursor Agent is asking whether to trust this folder"},
	{regexp.MustCompile(`Do you trust the contents of this project\?`), "Antigravity CLI is asking whether to trust this folder"},
}

// startupDialog reports which dialog a screen is showing, if any.
func startupDialog(screen string) (string, bool) {
	for _, d := range startupDialogs {
		if d.marker.MatchString(screen) {
			return d.what, true
		}
	}
	return "", false
}

// blockedBy reads the pane once and says whether the agent is sitting on a
// startup dialog. A pane that cannot be read is not a dialog: the guard only
// ever adds a refusal, it never invents a failure.
func (w agentWork) blockedBy(s Session) error {
	screen, err := w.ops.PaneRead(s.PaneID, 60)
	if err != nil {
		return nil
	}
	what, hit := startupDialog(screen)
	if !hit {
		return nil
	}
	return &BlockedError{Pane: s.PaneID, What: what}
}

// BlockedError is a run that stopped because the agent is waiting for a
// person. It is a failed run — nothing worked — but the text names the way
// out, which an agent_kind_mismatch never did.
type BlockedError struct {
	Pane string
	What string
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("%s in pane %s — answer it once there (the prompt was not typed); runs in this folder go through after that, or run the automation headless, which has no such dialog", e.What, e.Pane)
}
