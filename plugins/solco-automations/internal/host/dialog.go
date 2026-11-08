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
// One exception, by the user's explicit decision (2026-11-03): the "trust this
// folder" question of Claude Code and Codex, when the folder it names is this
// run's own directory — the worktree the plugin just created, or the
// repository the person put in the automation. That one is answered with the
// agent's own "yes" row (see acceptTrust; the agent records the trust itself);
// every other dialog, and a trust question about any other folder, still
// stops the run.
//
// Measured on 2026-11-03: the Claude Code pair (a fresh worktree path, and a
// repo with unapproved project MCP servers) and Codex 0.164's trust question
// (names the repository root, even from a worktree; trust is per repository,
// not inherited from a parent directory). Cursor and Antigravity are the
// strings Solco's own terminal-dialog guard already matches, unmeasured here.
var startupDialogs = []struct {
	marker *regexp.Regexp
	what   string
}{
	{regexp.MustCompile(`Quick safety check: Is this a project you created`), "Claude Code is asking whether to trust this folder"},
	{regexp.MustCompile(`new MCP servers? found in this project`), "Claude Code is asking which project MCP servers to enable"},
	{regexp.MustCompile(`Trust this folder\? Codex can read, edit, and run files here`), "Codex is asking whether to trust this folder"},
	{regexp.MustCompile(`hooks? need review before they can run`), "Codex is asking you to review its lifecycle hooks (its \"t trust all\" runs hook commands)"},
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

// StartupDialog reports which agent startup dialog a screen shows, so a caller
// that must type into a live agent (the Group pump) can wait instead of typing
// into a question.
func StartupDialog(screen string) (string, bool) { return startupDialog(screen) }

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
	if w.acceptTrust(s, screen) {
		return nil
	}
	return &BlockedError{Pane: s.PaneID, What: what}
}

// trustDialog describes one agent's "trust this folder" question: how to
// recognise it, where it names the folder, which menu row is the yes, and
// which glyph marks the cursor row.
type trustDialog struct {
	marker *regexp.Regexp
	folder *regexp.Regexp
	yes    string
	cursor string
}

var trustDialogs = []trustDialog{
	{ // Claude Code: the menu opens on "No, exit".
		marker: regexp.MustCompile(`Quick safety check: Is this a project you created`),
		folder: regexp.MustCompile(`(?m)Accessing workspace:\s*\n\s*(\S[^\n]*?)\s*$`),
		yes:    "Yes, I trust this folder",
		cursor: "❯",
	},
	{ // Codex: the menu opens on "Trust and continue"; it names the repository root, also for a worktree.
		marker: regexp.MustCompile(`Trust this folder\? Codex can read, edit, and run files here`),
		folder: regexp.MustCompile(`(?m)Folder access\s*\n\s*(/\S[^\n]*?)\s*$`),
		yes:    "Trust and continue",
		cursor: "›",
	},
}

// acceptTrust answers an agent's folder-trust question with its "yes" row and
// reports whether the dialog is gone. It acts only when the path on screen is
// this run's own directory; the highlight is read from the screen before
// Enter, never assumed, and every key is followed by a fresh read. Anything
// unexpected leaves the dialog untouched and the run stops as it always did.
func (w agentWork) acceptTrust(s Session, screen string) bool {
	var d *trustDialog
	for i := range trustDialogs {
		if trustDialogs[i].marker.MatchString(screen) {
			d = &trustDialogs[i]
		}
	}
	if d == nil || !w.ownsFolder(s, d, screen) {
		return false
	}
	for range 5 {
		key := "down"
		if strings.Contains(selectedLine(screen, d.cursor), d.yes) {
			key = "enter"
		} else if !strings.Contains(screen, d.yes) {
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
		if !d.marker.MatchString(next) {
			return true
		}
		screen = next
	}
	return false
}

// ownsFolder says whether the folder the dialog names is this run's: the
// worktree the plugin created for it (its directory ends in the run branch,
// slashes turned into dashes) or the automation's own repository (compared
// through symlinks: /tmp is /private/tmp).
func (w agentWork) ownsFolder(s Session, d *trustDialog, screen string) bool {
	m := d.folder.FindStringSubmatch(screen)
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
	if repo == "" {
		return false
	}
	return realPath(shown) == realPath(filepath.Clean(repo))
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// selectedLine is the menu row the cursor glyph is on, or "".
func selectedLine(screen, cursor string) string {
	for _, line := range strings.Split(screen, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), cursor) {
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
