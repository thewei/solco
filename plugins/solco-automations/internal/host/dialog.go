package host

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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
// One exception, by the user's explicit decision (2026-11-03): Claude Code's
// "trust this folder" question, when the folder it names is this run's own
// directory — the worktree the plugin just created, or the repository the
// person put in the automation. That one is answered "Yes, I trust this
// folder" (see acceptClaudeTrust); every other dialog, and a trust question
// about any other folder, still stops the run.
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
	if claudeTrust.MatchString(screen) && w.acceptClaudeTrust(s, screen) {
		return nil
	}
	return &BlockedError{Pane: s.PaneID, What: what}
}

var (
	claudeTrust     = regexp.MustCompile(`Quick safety check: Is this a project you created`)
	claudeWorkspace = regexp.MustCompile(`(?m)Accessing workspace:\s*\n\s*(\S[^\n]*?)\s*$`)
)

// acceptClaudeTrust answers Claude Code's folder-trust question with "Yes, I
// trust this folder" and reports whether the dialog is gone. It acts only when
// the path on screen is this run's own directory; the highlight is read from
// the screen before Enter, never assumed (the menu opens on "No, exit"), and
// every key is followed by a fresh read. Anything unexpected leaves the dialog
// untouched and the run stops as it always did.
func (w agentWork) acceptClaudeTrust(s Session, screen string) bool {
	if !w.ownsFolder(s, screen) {
		return false
	}
	for range 5 {
		sel := selectedLine(screen)
		key := "down"
		if strings.Contains(sel, "Yes, I trust this folder") {
			key = "enter"
		} else if !strings.Contains(screen, "Yes, I trust this folder") {
			return false
		}
		if err := w.ops.PaneSendKeys(s.PaneID, key); err != nil {
			return false
		}
		time.Sleep(w.knobs.statusPoll)
		next, err := w.ops.PaneRead(s.PaneID, 60)
		if err != nil {
			return false
		}
		if !claudeTrust.MatchString(next) {
			return true
		}
		screen = next
	}
	return false
}

// ownsFolder says whether the folder the dialog names is this run's: the
// worktree the plugin created for it (its directory ends in the run branch,
// slashes turned into dashes) or the automation's own repository.
func (w agentWork) ownsFolder(s Session, screen string) bool {
	m := claudeWorkspace.FindStringSubmatch(screen)
	if m == nil {
		return false
	}
	shown := filepath.Clean(m[1])
	if s.Branch != "" && filepath.Base(shown) == strings.ReplaceAll(s.Branch, "/", "-") {
		return true
	}
	repo := w.a.Repo
	if strings.HasPrefix(repo, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			repo = filepath.Join(home, repo[2:])
		}
	}
	return repo != "" && shown == filepath.Clean(repo)
}

// selectedLine is the menu row the cursor (❯) is on, or "".
func selectedLine(screen string) string {
	for _, line := range strings.Split(screen, "\n") {
		if strings.Contains(line, "❯") {
			return line
		}
	}
	return ""
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
