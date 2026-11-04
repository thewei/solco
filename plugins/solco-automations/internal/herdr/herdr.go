// Package herdr is a thin client over the herdr CLI (which itself fronts the
// socket API). Where that binary lives is hostpath's problem.
//
// The calls are methods on Client rather than package functions so a caller
// that wants a narrower interface can embed it and get the whole set, instead
// of hand-forwarding a dozen one-line wrappers.
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/hostpath"
)

// Client talks to the herdr CLI. The zero value is ready, and talks to
// whichever session the environment points at — what every caller wanted
// before a run could name its own (ADR 0010).
type Client struct {
	// Session, when set, is the Herdr session every call targets.
	Session string
}

// argv puts --session ahead of the subcommand, where herdr's CLI reads it. It
// outranks HERDR_SOCKET_PATH, so a daemon started by one session can still
// reach another.
func (c Client) argv(args []string) []string {
	if c.Session == "" {
		return args
	}
	return append([]string{"--session", c.Session}, args...)
}

// run executes a herdr subcommand against the client's session.
func (c Client) run(out any, args ...string) error {
	return runCtx(context.Background(), out, c.argv(args)...)
}

func (c Client) runCtx(ctx context.Context, out any, args ...string) error {
	return runCtx(ctx, out, c.argv(args)...)
}

// runCtx executes a herdr subcommand and decodes the socket-API JSON envelope
// ({"id": ..., "result": {...}}) into out when out is non-nil. It takes a
// context so a caller that cannot afford to block forever — a toast, not a run
// step — can bound it.
func runCtx(ctx context.Context, out any, args ...string) error {
	cmd := exec.CommandContext(ctx, hostpath.Bin(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return newAPIError(args, stdout.Bytes(), stderr.String(), err)
	}
	if out == nil {
		return nil
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		return fmt.Errorf("herdr %s: unexpected output %q: %w", commandName(args), stdout.String(), err)
	}
	raw := envelope.Result
	if raw == nil {
		raw = stdout.Bytes() // some commands print the result object bare
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("herdr %s: decode result: %w", commandName(args), err)
	}
	return nil
}

// APIError carries herdr's error code so callers can recover from the ones
// that are recoverable, and so logs get one readable line instead of a JSON
// payload.
type APIError struct {
	Command string
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Code != "" && e.Message != "" {
		return e.Command + ": " + e.Code + ": " + e.Message
	}
	if e.Code != "" {
		return e.Command + ": " + e.Code
	}
	return e.Command + ": " + e.Message
}

// HasCode reports whether err is a herdr API error with the given code.
func HasCode(err error, code string) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Code == code
}

// commandName is the command an error may name: its first two words (`agent
// prompt`), never the arguments — those carry prompts and paths.
func commandName(args []string) string {
	if len(args) >= 2 && args[0] == "--session" {
		args = args[2:]
	}
	return strings.Join(args[:min(2, len(args))], " ")
}

// newAPIError turns a failed herdr invocation into one readable line. runErr
// is the error from cmd.Run and is the last resort: a herdr that exits non-zero
// while printing nothing at all used to produce a bare "worktree create: ",
// which says only that the run failed — not that it exited 1, was killed, or
// was never on PATH.
func newAPIError(args []string, stdout []byte, stderr string, runErr error) error {
	if len(args) >= 2 && args[0] == "--session" {
		args = args[2:] // name the command, not where it was sent
	}
	cmd := commandName(args)
	// herdr ≥ 0.8.0 prints the error envelope on stderr; older builds used stdout.
	for _, out := range [][]byte{stdout, []byte(stderr)} {
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(out, &envelope) == nil && envelope.Error.Code != "" {
			return &APIError{Command: cmd, Code: envelope.Error.Code, Message: envelope.Error.Message}
		}
	}
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = strings.TrimSpace(string(stdout))
	}
	if msg == "" && runErr != nil {
		msg = runErr.Error()
	}
	return &APIError{Command: cmd, Message: msg}
}

// createResult matches both worktree_created and workspace_created payloads:
// the new workspace plus its initial pane, ready for `agent start`.
type createResult struct {
	Workspace struct {
		WorkspaceID string `json:"workspace_id"`
	} `json:"workspace"`
	RootPane struct {
		PaneID string `json:"pane_id"`
	} `json:"root_pane"`
}

func (r createResult) ids(what string) (string, string, error) {
	if r.Workspace.WorkspaceID == "" || r.RootPane.PaneID == "" {
		return "", "", fmt.Errorf("%s returned no workspace/pane id", what)
	}
	return r.Workspace.WorkspaceID, r.RootPane.PaneID, nil
}

// WorktreeCreate provisions a fresh git worktree workspace off repo and
// returns its workspace and root pane IDs.
func (c Client) WorktreeCreate(repo, branch, label string) (workspaceID, paneID string, err error) {
	var res createResult
	err = c.run(&res, "worktree", "create",
		"--cwd", repo, "--branch", branch, "--label", label, "--no-focus")
	if err != nil {
		return "", "", err
	}
	return res.ids("worktree create")
}

// WorkspaceCreate opens a workspace directly on a directory (root mode).
func (c Client) WorkspaceCreate(cwd, label string) (workspaceID, paneID string, err error) {
	var res createResult
	err = c.run(&res, "workspace", "create", "--cwd", cwd, "--label", label, "--no-focus")
	if err != nil {
		return "", "", err
	}
	return res.ids("workspace create")
}

// TabCreate gives a run its own shell without creating a workspace.
func (c Client) TabCreate(workspaceID, cwd, label string) (string, error) {
	var res createResult
	if err := c.run(&res, "tab", "create", "--workspace", workspaceID,
		"--cwd", cwd, "--label", label, "--no-focus"); err != nil {
		return "", err
	}
	if res.RootPane.PaneID == "" {
		return "", fmt.Errorf("tab create returned no pane id")
	}
	return res.RootPane.PaneID, nil
}

// Worktree is one checkout backing a workspace. OpenWorkspaceID is empty once
// the workspace has been closed, which is the only durable signal Herdr keeps
// about whether anyone came back to look at a run.
type Worktree struct {
	Branch           string `json:"branch"`
	Path             string `json:"path"`
	OpenWorkspaceID  string `json:"open_workspace_id"`
	IsLinkedWorktree bool   `json:"is_linked_worktree"`
}

// WorktreeList returns every worktree Herdr knows about for repo, including
// the source checkout itself.
func (c Client) WorktreeList(repo string) ([]Worktree, error) {
	var res struct {
		Worktrees []Worktree `json:"worktrees"`
	}
	if err := c.run(&res, "worktree", "list", "--cwd", repo); err != nil {
		return nil, err
	}
	return res.Worktrees, nil
}

// AgentStart launches an interactive agent in a pane sitting at a shell
// prompt. extraArgs are forwarded to the agent executable (e.g. --mcp-config).
func (c Client) AgentStart(name, kind, paneID string, extraArgs []string) error {
	args := []string{"agent", "start", name, "--kind", kind, "--pane", paneID}
	if len(extraArgs) > 0 {
		args = append(args, "--")
		args = append(args, extraArgs...)
	}
	return c.run(nil, args...)
}

// CodePaneBusy is herdr's refusal to start an agent in a pane that is not
// sitting at a shell prompt. It means the workspace exists but its shell has
// not spawned yet, so it is a wait-and-retry rather than a dead run: a
// sleep-delayed worktree create can return minutes before the pane is usable.
const CodePaneBusy = "agent_pane_busy"

// CodeAgentGone means there is no agent in the target pane any more — the
// workspace was closed, or the agent exited on its own. herdr only reports it
// when the call it was given returns, so a wait handed the run's whole timeout
// sits on a dead pane for that long before saying so.
const CodeAgentGone = "agent_not_running"

// CodeWorkspaceGone is herdr's answer when the workspace ID no longer names
// anything — the expected result of asking about a run somebody has reviewed
// and closed.
const CodeWorkspaceGone = "workspace_not_found"

// CodeStalled is herdr's verdict when a submitted prompt produces no visible
// state change within 5 seconds. It does not mean the prompt was lost — an
// agent still loading its MCP servers takes longer than that to react.
const CodeStalled = "agent_prompt_stalled"

// AgentSubmit types a prompt into the agent and asks herdr to confirm the
// agent reacted. A CodeStalled error is inconclusive; callers should check the
// status before giving up.
func (c Client) AgentSubmit(target, text string) error {
	return c.run(nil, "agent", "prompt", target, text, "--wait", "--until", "working",
		"--timeout", "30000")
}

// AgentPromptAccepted confirms ONLY that Herdr accepted a prompt into this
// exact Pane; it deliberately does not await an observed transition to working.
// On real OMP TUI a fast model can answer before Herdr polls working, causing
// the --wait form to report agent_prompt_stalled even though OMP replied.
// An accepted prompt is NOT proof of an OMP completion; the real transcript is.
func (c Client) AgentPromptAccepted(paneID, text string) error {
	// A hung Herdr Socket must not stall the one report lane indefinitely.
	// Timeout after a prompt attempt remains an uncertain delivery, not an
	// automatic retry, because Herdr might already have accepted the text.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var response struct {
		Type  string `json:"type"`
		Agent struct {
			PaneID string `json:"pane_id"`
		} `json:"agent"`
	}
	if err := c.runCtx(ctx, &response, "agent", "prompt", paneID, text); err != nil {
		return err
	}
	if response.Type != "agent_prompted" || response.Agent.PaneID != paneID {
		return fmt.Errorf("herdr agent prompt: cannot confirm target pane")
	}
	return nil
}

// AgentLiveState is Herdr's real-time read-only agent.get projection. It is
// independent of Solco's persistent CoordinatorReady launch history.
type AgentLiveState struct {
	PaneID           string
	Kind             string
	Status           string
	InteractiveReady bool
}

// AgentState reads one exact Pane, without starting or interrupting its Agent.
func (c Client) AgentState(target string) (AgentLiveState, error) {
	// This is a pre-send read, not an operation that may hang a report worker
	// indefinitely. An unavailable Herdr answer means "defer".
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var res struct {
		Type  string `json:"type"`
		Agent struct {
			PaneID           string `json:"pane_id"`
			Kind             string `json:"agent"`
			Status           string `json:"agent_status"`
			InteractiveReady bool   `json:"interactive_ready"`
		} `json:"agent"`
	}
	if err := c.runCtx(ctx, &res, "agent", "get", target); err != nil {
		return AgentLiveState{}, err
	}
	if res.Type != "agent_info" || res.Agent.PaneID != target || res.Agent.Kind == "" || res.Agent.Status == "" {
		return AgentLiveState{}, fmt.Errorf("herdr agent get: incomplete or mismatched agent identity")
	}
	return AgentLiveState{
		PaneID: res.Agent.PaneID, Kind: res.Agent.Kind, Status: res.Agent.Status,
		InteractiveReady: res.Agent.InteractiveReady,
	}, nil
}

// CodeKindMismatch is herdr's answer when `agent start` looked at the pane
// and saw a different agent than the one it was asked to start. Right after
// launch that is often not a verdict: an agent that is still unpacking a
// toolchain, or sitting on its first-run dialog, is read as whatever it looks
// like at that instant (measured 2026-11-03: Claude Code read as omp for
// seconds, then registered as claude). The pane gets the chance to settle.
const CodeKindMismatch = "agent_kind_mismatch"

// CodeNotReady is herdr's answer when `agent start` finds the agent already
// "blocked during startup" — the other face of a first-run dialog (measured
// 2026-11-03: the same Claude trust question, answered agent_kind_mismatch on
// one run and agent_not_ready on the next). Like a mismatch it is not a verdict
// until the screen has been read.
const CodeNotReady = "agent_not_ready"

// AgentStatus reports the agent's current state: idle, working, blocked…
func (c Client) AgentStatus(target string) (string, error) {
	_, status, err := c.AgentIdentity(target)
	return status, err
}

// AgentIdentity reports which agent herdr has registered in the pane, and its
// state. An agent not yet detected is an error (CodeAgentGone), not a blank.
func (c Client) AgentIdentity(target string) (kind, status string, err error) {
	var res struct {
		Agent struct {
			Agent       string `json:"agent"`
			AgentStatus string `json:"agent_status"`
		} `json:"agent"`
	}
	if err := c.run(&res, "agent", "get", target); err != nil {
		return "", "", err
	}
	return res.Agent.Agent, res.Agent.AgentStatus, nil
}

// AgentSubmitPending presses Enter on the pane, submitting anything already
// sitting in the composer. Harmless when the composer is empty.
func (c Client) AgentSubmitPending(paneID string) error {
	return c.run(nil, "pane", "send-keys", paneID, "enter")
}

// PaneSendKeys presses named keys on the pane (herdr's own key names: enter,
// down …). Unlike AgentSubmit it does not care which agent is in the pane.
func (c Client) PaneSendKeys(paneID string, keys ...string) error {
	return c.run(nil, append([]string{"pane", "send-keys", paneID}, keys...)...)
}

// AgentWait blocks until the agent settles (idle, done or blocked).
func (c Client) AgentWait(target string, timeout time.Duration) error {
	return c.run(nil, "agent", "wait", target,
		"--timeout", fmt.Sprintf("%d", timeout.Milliseconds()))
}

// ErrGone means the run's workspace no longer exists — the expected outcome
// once you've reviewed and closed it, not a failure worth a stack trace.
var ErrGone = errors.New("workspace already closed")

// Focus brings a run's workspace to the front, then its agent pane when one
// is known — the "jump to what this automation did" move.
func (c Client) Focus(workspaceID, paneID string) error {
	if workspaceID != "" {
		if err := c.run(nil, "workspace", "focus", workspaceID); err != nil {
			if HasCode(err, CodeWorkspaceGone) {
				return ErrGone
			}
			return err
		}
	}
	if paneID != "" {
		// Best-effort: the pane may be gone while the workspace lives on.
		_ = c.run(nil, "agent", "focus", paneID)
	}
	return nil
}

// Sound is which of herdr's notification sounds a toast plays.
type Sound string

const (
	SoundNone    Sound = "none"
	SoundDone    Sound = "done"
	SoundRequest Sound = "request"
)

// notificationTimeout bounds a toast. A local socket call needs milliseconds;
// three seconds is the point past which a wedged herdr is costing a finished
// run something.
const notificationTimeout = 3 * time.Second

// notificationPosition: the board is an overlay pane and list prints at the
// bottom, so a toast goes top-right and covers neither.
const notificationPosition = "top-right"

// notificationArgs assembles the toast's argv. Split out because the one thing
// worth pinning — an empty body producing no --body flag rather than an empty
// one — is testable with no herdr to talk to.
func notificationArgs(title, body string, sound Sound) []string {
	args := []string{"notification", "show", title}
	if body != "" {
		args = append(args, "--body", body)
	}
	args = append(args, "--position", notificationPosition, "--sound", string(sound))
	return args
}

// NotificationShow raises an in-session Herdr toast. In-session is the whole
// caveat: a panel in the Herdr window, not an OS notification.
func (c Client) NotificationShow(title, body string, sound Sound) error {
	ctx, cancel := context.WithTimeout(context.Background(), notificationTimeout)
	defer cancel()
	return c.runCtx(ctx, nil, notificationArgs(title, body, sound)...)
}

// PaneRun executes a shell command in a pane (used to delegate to hwf).
func (c Client) PaneRun(paneID string, command ...string) error {
	return c.run(nil, append([]string{"pane", "run", paneID}, command...)...)
}

// PaneRead returns the pane's recent terminal output. A pane is the only
// channel a delegated command has, so this is how its result gets read back.
// Unlike the rest of the API this one prints the screen, not a JSON envelope.
func (c Client) PaneRead(paneID string, lines int) (string, error) {
	return c.PaneReadContext(context.Background(), paneID, lines)
}

// PaneReadContext allows an automated reporting probe to limit how long it
// waits for the real Herdr Pane. Ordinary Workers still use PaneRead.
func (c Client) PaneReadContext(ctx context.Context, paneID string, lines int) (string, error) {
	cmd := exec.CommandContext(ctx, hostpath.Bin(), c.argv([]string{"pane", "read", paneID,
		"--source", "recent", "--lines", fmt.Sprintf("%d", lines), "--format", "text"})...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", newAPIError([]string{"pane", "read"}, stdout.Bytes(), stderr.String(), err)
	}
	return stdout.String(), nil
}

// sessionInfo is one entry of `herdr session list --json`.
type sessionInfo struct {
	Name       string `json:"name"`
	Default    bool   `json:"default"`
	Running    bool   `json:"running"`
	SocketPath string `json:"socket_path"`
}

// sessions lists every Herdr session on the machine. It never carries
// --session: the list is the machine's, not one session's.
func sessions() ([]sessionInfo, error) {
	var res struct {
		Sessions []sessionInfo `json:"sessions"`
	}
	if err := runCtx(context.Background(), &res, "session", "list", "--json"); err != nil {
		return nil, err
	}
	return res.Sessions, nil
}

// RunningSessions names the sessions whose server is up.
func (Client) RunningSessions() ([]string, error) {
	all, err := sessions()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range all {
		if s.Running {
			out = append(out, s.Name)
		}
	}
	return out, nil
}

// CurrentSession names the session this client's calls land in: its own when
// bound, otherwise the one whose socket the environment points at, otherwise
// the default — the same resolution herdr's CLI makes. Empty when Herdr cannot
// say, which callers read as "unknown", never as a session.
func (c Client) CurrentSession() string {
	if c.Session != "" {
		return c.Session
	}
	if s := os.Getenv("HERDR_SESSION"); s != "" {
		return s
	}
	all, err := sessions()
	if err != nil {
		return ""
	}
	socket := os.Getenv("HERDR_SOCKET_PATH")
	for _, s := range all {
		if (socket != "" && s.SocketPath == socket) || (socket == "" && s.Default) {
			return s.Name
		}
	}
	return ""
}

// WorkspaceInfo is one entry of `herdr workspace list`.
type WorkspaceInfo struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

// WorkspaceList returns every workspace in the client's Herdr session.
func (c Client) WorkspaceList() ([]WorkspaceInfo, error) {
	var res struct {
		Workspaces []WorkspaceInfo `json:"workspaces"`
	}
	if err := c.run(&res, "workspace", "list"); err != nil {
		return nil, err
	}
	return res.Workspaces, nil
}

// PaneIDs returns the pane ids of one workspace, in Herdr's order.
func (c Client) PaneIDs(workspaceID string) ([]string, error) {
	var res struct {
		Panes []struct {
			PaneID string `json:"pane_id"`
		} `json:"panes"`
	}
	if err := c.run(&res, "pane", "list", "--workspace", workspaceID); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(res.Panes))
	for _, p := range res.Panes {
		ids = append(ids, p.PaneID)
	}
	return ids, nil
}
