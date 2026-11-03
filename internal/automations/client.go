package automations

/**
 * [INPUT]: 依赖 Detect 给出的 plugin_root、`<root>/bin/herdr-automations <cmd> --json` 插件 CLI（无 run.sh，直接二进制）
 * [OUTPUT]: 对外提供 Client（List / History 读；Add（永远 `add --name … --draft`）/ Approve / Run（`run <name> --bg`，立即返回）/ Pause / Resume / Remove / Put（读出该条目的原始 JSON，改动后经 stdin 交给 `put --original`，插件的 problem 原文当错误）/ StartScheduler 写）、可注入的 Exec/ExecIn 与契约 §3 的类型（Automation / Run / ListResult / HistoryResult）
 * [POS]: 插件 CLI 的唯一调用点；写入不自造语义：触发器/覆盖/清理的语法与校验全归插件，拒绝原文透传为错误；以人类身份运行——剥掉 HERDR_PANE_ID 等 Agent 身份变量，不从启动环境带进任何 Agent 身份
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/thewei/solco/internal/herdr"
)

// Exec runs one plugin CLI invocation and returns its stdout.
type Exec func(ctx context.Context, args ...string) (string, error)

// ExecIn is Exec with the process's stdin: `put` reads the whole automation
// as JSON there.
type ExecIn func(ctx context.Context, stdin string, args ...string) (string, error)

// Client reads and writes automations through the plugin's own CLI.
type Client struct {
	exec ExecIn
	// startDaemon launches the plugin's scheduler detached, logging to the
	// given file. Nil in tests that do not care.
	startDaemon func(logPath string) error
}

// NewClient binds a client to one plugin folder and the Herdr session the
// window is looking at (empty = default), so a manual run lands where the
// user is and its pane can be jumped to. An empty root yields a client whose
// every call fails: callers gate on Detect.
func NewClient(root, session string) *Client {
	return &Client{exec: cliExec(root, session), startDaemon: daemonStarter(root, session)}
}

// NewClientWithExec is the test seam.
func NewClientWithExec(exec Exec) *Client {
	return NewClientWithExecIn(func(ctx context.Context, _ string, args ...string) (string, error) {
		return exec(ctx, args...)
	})
}

// NewClientWithExecIn is the test seam for callers that also read stdin.
func NewClientWithExecIn(exec ExecIn) *Client { return &Client{exec: exec} }

// WithDaemonStarter replaces how StartScheduler launches the daemon (tests).
func (c *Client) WithDaemonStarter(start func(logPath string) error) *Client {
	c.startDaemon = start
	return c
}

// call is the argv-only form most verbs use.
func (c *Client) call(ctx context.Context, args ...string) (string, error) {
	return c.exec(ctx, "", args...)
}

// LastRun is the one run a list row carries (contract §3 `last_run`).
type LastRun struct {
	RunID           string `json:"run_id"`
	Status          string `json:"status"`
	StartedAt       string `json:"started_at"`
	DurationSeconds int64  `json:"duration_seconds"`
	Worktree        string `json:"worktree"`
	WorkspaceID     string `json:"workspace_id"`
	PaneID          string `json:"pane_id"`
	// Seen is whether the run was opened; an unread finished run is what the
	// Header counts.
	Seen bool `json:"seen"`
}

// History30 is the plugin's own 30-day aggregate of one automation.
type History30 struct {
	Done    int `json:"done"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// Automation is one entry of `list --json` (contract §3).
type Automation struct {
	Name            string    `json:"name"`
	Draft           bool      `json:"draft"`
	Disabled        bool      `json:"disabled"`
	Cron            string    `json:"cron"`
	ScheduleText    string    `json:"schedule_text"`
	OnceAt          *string   `json:"once_at"`
	Repo            string    `json:"repo"`
	WorkspaceMode   string    `json:"workspace_mode"`
	Agent           string    `json:"agent"`
	Model           string    `json:"model"`
	Prompt          string    `json:"prompt"`
	Headless        bool      `json:"headless"`
	Rationale       string    `json:"rationale"`
	OnOverlap       string    `json:"on_overlap"`
	TimeoutMinutes  int       `json:"timeout_minutes"`
	CatchUpMinutes  int       `json:"catch_up_minutes"`
	CleanupTTLHours int       `json:"cleanup_ttl_hours"`
	NextRun         string    `json:"next_run"`
	LastRun         *LastRun  `json:"last_run"`
	History30       History30 `json:"history30"`
}

// InvalidEntry is one automations.yaml line the plugin could not load; the
// plugin stays the authority and Solco only shows the row.
type InvalidEntry struct {
	Name    string `json:"name"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// ListResult is the whole `list --json` result (contract §3).
type ListResult struct {
	Paused bool `json:"paused"`
	// Scheduler is the plugin daemon's pid; 0 means nothing is scheduling.
	// Herdr starts the daemon only with its server, so a plugin linked into a
	// running Herdr has none until someone starts it.
	Scheduler int `json:"scheduler"`
	// State is the plugin's state folder (history, daemon log).
	State       string         `json:"state"`
	Invalid     []InvalidEntry `json:"invalid"`
	Automations []Automation   `json:"automations"`
}

// Run is one entry of `history --json` (contract §3). Status is the plugin's
// own vocabulary (scheduled|running|done|failed|skipped|missed|invalid|
// cancelled|queued); Solco never interprets beyond presenting it.
type Run struct {
	RunID           string `json:"run_id"`
	Name            string `json:"name"`
	Trigger         string `json:"trigger"`
	Status          string `json:"status"`
	StartedAt       string `json:"started_at"`
	FinishedAt      string `json:"finished_at"`
	DurationSeconds int64  `json:"duration_seconds"`
	Worktree        string `json:"worktree"`
	WorkspaceID     string `json:"workspace_id"`
	PaneID          string `json:"pane_id"`
	Agent           string `json:"agent"`
	// Error is the plugin's own account of a failed run (a refused folder
	// trust prompt, a missing agent …). Seen is the plugin's unread mark.
	Error string `json:"error"`
	Seen  bool   `json:"seen"`
}

// HistoryResult is the whole `history --json` result.
type HistoryResult struct {
	Runs []Run `json:"runs"`
}

type listEnvelope struct {
	Result ListResult `json:"result"`
}

type historyEnvelope struct {
	Result HistoryResult `json:"result"`
}

// List returns every automation the plugin loaded, its daemon pause state and
// the invalid yaml entries it skipped.
func (c *Client) List(ctx context.Context) (ListResult, error) {
	out, err := c.call(ctx, "list", "--json")
	if err != nil {
		return ListResult{}, err
	}
	var env listEnvelope
	if err := jsonUnmarshal(out, &env); err != nil {
		return ListResult{}, fmt.Errorf("automations: list: %w", err)
	}
	return env.Result, nil
}

// History returns one automation's runs, or the plugin's whole log when name
// is empty.
func (c *Client) History(ctx context.Context, name string) (HistoryResult, error) {
	if name != "" {
		if err := checkName("history", name); err != nil {
			return HistoryResult{}, err
		}
	}
	args := []string{"history"}
	if name != "" {
		args = append(args, name)
	}
	args = append(args, "--json")
	out, err := c.call(ctx, args...)
	if err != nil {
		return HistoryResult{}, err
	}
	var env historyEnvelope
	if err := jsonUnmarshal(out, &env); err != nil {
		return HistoryResult{}, fmt.Errorf("automations: history: %w", err)
	}
	return env.Result, nil
}

// Fields is what the New and Edit forms carry (contract §3): one cron
// trigger plus the run's body. This plugin has no one-shot schedules — a
// moment in time is a cron with a pinned date, which the plugin judges — so
// there is no OnceAt. The plugin parses and refuses what does not apply;
// nothing is created or changed then.
type Fields struct {
	Cron            string
	Repo            string
	Workspace       string // worktree | root; empty = the plugin's default (worktree)
	Agent           string
	Model           string
	Prompt          string
	Rationale       string
	OnOverlap       string // skip | queue; empty = the plugin's default
	Headless        bool   // run the agent's non-interactive command; the plugin judges the agent
	TimeoutMinutes  int
	CatchUpMinutes  int
	CleanupTTLHours int
}

// validate refuses identifier-like values that start with "-": every value
// travels as its flag's argument, so this is belt and braces for the short
// fields. Prompt and Rationale are prose — "- check the logs" is a fine
// prompt — and are never parsed as flags.
func (f Fields) validate(op string) error {
	for _, v := range []string{f.Cron, f.Repo, f.Workspace, f.Agent, f.Model, f.OnOverlap} {
		if strings.HasPrefix(v, "-") {
			return fmt.Errorf("automations: %s: option-like text is refused", op)
		}
	}
	return nil
}

func (f Fields) args() []string {
	var args []string
	add := func(flag, value string) {
		if value != "" {
			args = append(args, flag, value)
		}
	}
	add("--cron", f.Cron)
	add("--repo", f.Repo)
	add("--workspace", f.Workspace)
	add("--agent", f.Agent)
	add("--model", f.Model)
	add("--prompt", f.Prompt)
	add("--rationale", f.Rationale)
	add("--on-overlap", f.OnOverlap)
	if f.Headless {
		args = append(args, "--headless")
	}
	if f.TimeoutMinutes > 0 {
		add("--timeout-minutes", itoa(f.TimeoutMinutes))
	}
	if f.CatchUpMinutes > 0 {
		add("--catch-up-minutes", itoa(f.CatchUpMinutes))
	}
	if f.CleanupTTLHours > 0 {
		add("--cleanup-ttl-hours", itoa(f.CleanupTTLHours))
	}
	return args
}

// Add creates a draft (`add --name <name> --draft …`). Creation always goes
// through --draft (contract §4: an agent proposes, a human approves). The
// CLI's wizard asks about anything a flag did not answer, and there is nobody
// to ask, so everything an automation cannot run without is required here.
func (c *Client) Add(ctx context.Context, name string, f Fields) error {
	name = strings.TrimSpace(name)
	if err := checkName("add", name); err != nil {
		return err
	}
	if err := f.validate("add"); err != nil {
		return err
	}
	for what, v := range map[string]string{"a prompt": f.Prompt, "a schedule": f.Cron, "a repository": f.Repo} {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("automations: add: %s is required", what)
		}
	}
	args := append([]string{"add", "--name", name, "--draft"}, f.args()...)
	_, err := c.call(ctx, args...)
	return err
}

// Approve turns one draft active (`approve <name>`); the plugin refuses a
// name that is not a draft and its refusal comes back as the error.
func (c *Client) Approve(ctx context.Context, name string) error {
	return c.nameVerb(ctx, "approve", name)
}

// Run fires one automation now (`run <name> --bg`). The CLI detaches the run
// into its own session and answers at once — a run takes minutes to an hour —
// so success only means "started"; how it went is in History. The plugin
// refuses a draft.
func (c *Client) Run(ctx context.Context, name string) error {
	if err := checkName("run", name); err != nil {
		return err
	}
	_, err := c.call(ctx, "run", name, "--bg")
	return err
}

// Pause stops a daemon from scheduling the automation (`pause <name>`).
func (c *Client) Pause(ctx context.Context, name string) error {
	return c.nameVerb(ctx, "pause", name)
}

// Resume hands the automation back to the daemon (`resume <name>`).
func (c *Client) Resume(ctx context.Context, name string) error {
	return c.nameVerb(ctx, "resume", name)
}

// MarkSeen records that a run was opened (`seen <run-id>`), which is the
// plugin's own inbox flag; it clears the run from the unread counts.
func (c *Client) MarkSeen(ctx context.Context, runID string) error {
	runID = strings.TrimSpace(runID)
	if runID == "" || strings.HasPrefix(runID, "-") {
		return fmt.Errorf("automations: seen: a run id is required")
	}
	_, err := c.call(ctx, "seen", runID)
	return err
}

// MarkAllSeen records every finished run as opened (`seen --all`).
func (c *Client) MarkAllSeen(ctx context.Context) error {
	_, err := c.call(ctx, "seen", "--all")
	return err
}

// Remove deletes one automation (`rm <name>`); its saved runs' record is the
// plugin's to keep or drop.
func (c *Client) Remove(ctx context.Context, name string) error {
	return c.nameVerb(ctx, "rm", name)
}

// Put saves an edit over the automation `original` names. `put` replaces the
// whole entry from JSON on stdin, so the edit is read-modify-write on the
// entry's own JSON from `list --json`: the form's keys are overwritten and
// everything else (agent_args, MCP config, template, draft/disabled state …)
// rides through untouched. Cron, repository, agent, prompt and on-overlap
// change only when given; model, headless, rationale and the three numbers are the
// form's to clear (0 = the plugin's default). The plugin judges the result
// and its `problem` comes back as the error; nothing is written then.
func (c *Client) Put(ctx context.Context, original string, f Fields) error {
	original = strings.TrimSpace(original)
	if err := checkName("put", original); err != nil {
		return err
	}
	if err := f.validate("put"); err != nil {
		return err
	}
	entry, err := c.rawEntry(ctx, original)
	if err != nil {
		return err
	}
	set := func(key string, v any) {
		raw, _ := json.Marshal(v)
		entry[key] = raw
	}
	for key, v := range map[string]string{
		"cron": f.Cron, "repo": f.Repo, "agent": f.Agent, "prompt": f.Prompt, "on_overlap": f.OnOverlap,
	} {
		if v != "" {
			set(key, v)
		}
	}
	if f.Workspace != "" {
		set("workspace", f.Workspace)
		set("workspace_mode", f.Workspace)
	}
	set("model", f.Model)
	set("headless", f.Headless)
	set("rationale", f.Rationale)
	set("timeout_minutes", f.TimeoutMinutes)
	set("catch_up_minutes", f.CatchUpMinutes)
	set("cleanup_ttl_hours", f.CleanupTTLHours)
	body, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("automations: put: %w", err)
	}
	out, err := c.exec(ctx, string(body), "put", "--original", original)
	if err != nil {
		return err
	}
	// The CLI prints the verdict bare ({"problem":…,"next":[…]}); accept the
	// {"result":…} envelope too so a future wrap does not silence refusals.
	if strings.TrimSpace(out) == "" {
		return nil
	}
	var verdict struct {
		Problem string `json:"problem"`
		Result  struct {
			Problem string `json:"problem"`
		} `json:"result"`
	}
	if err := jsonUnmarshal(out, &verdict); err != nil {
		return fmt.Errorf("automations: put: %w", err)
	}
	if p := firstNonEmpty(verdict.Problem, verdict.Result.Problem); p != "" {
		return fmt.Errorf("automations: put: refused: %s", p)
	}
	return nil
}

// rawEntry is one automation as the plugin printed it, key by key.
func (c *Client) rawEntry(ctx context.Context, name string) (map[string]json.RawMessage, error) {
	out, err := c.call(ctx, "list", "--json")
	if err != nil {
		return nil, err
	}
	var env struct {
		Result struct {
			Automations []map[string]json.RawMessage `json:"automations"`
		} `json:"result"`
	}
	if err := jsonUnmarshal(out, &env); err != nil {
		return nil, fmt.Errorf("automations: put: %w", err)
	}
	for _, entry := range env.Result.Automations {
		var got string
		if json.Unmarshal(entry["name"], &got) == nil && got == name {
			return entry, nil
		}
	}
	return nil, fmt.Errorf("automations: put: no automation named %q", name)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// StartScheduler launches the plugin's daemon, detached, logging beside its
// state. Herdr starts the daemon only with its server, so a plugin linked or
// upgraded into a running Herdr has no scheduler: automations then look
// scheduled and never fire. The daemon refuses a second copy by itself.
func (c *Client) StartScheduler(logPath string) error {
	if c.startDaemon == nil {
		return errors.New("automations: scheduler cannot be started from here")
	}
	return c.startDaemon(logPath)
}

func (c *Client) nameVerb(ctx context.Context, verb, name string) error {
	if err := checkName(verb, name); err != nil {
		return err
	}
	_, err := c.call(ctx, verb, name)
	return err
}

func checkName(op, name string) error {
	if strings.TrimSpace(name) == "" || strings.HasPrefix(name, "-") {
		return fmt.Errorf("automations: %s: invalid automation name", op)
	}
	return nil
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

// jsonUnmarshal is split out only so the two decoders share one wording.
func jsonUnmarshal(data string, v any) error {
	return json.Unmarshal([]byte(data), v)
}

// agentIdentityEnv is the per-pane / per-invocation environment Herdr injects
// into plugin processes (the same set internal/workboard drops). Solco is the
// human, so none of it may leak in from the launching environment. Unlike the
// workboard there is no session seam here: the plugin CLI reaches Herdr
// through its own configuration, exactly as when run from a plain shell.
var agentIdentityEnv = map[string]bool{
	"HERDR_PANE_ID":              true,
	"HERDR_TAB_ID":               true,
	"HERDR_WORKSPACE_ID":         true,
	"HERDR_PLUGIN_ID":            true,
	"HERDR_PLUGIN_CONTEXT_JSON":  true,
	"HERDR_PLUGIN_EVENT":         true,
	"HERDR_PLUGIN_EVENT_JSON":    true,
	"HERDR_PLUGIN_ENTRYPOINT_ID": true,
	"HERDR_PLUGIN_ACTION_ID":     true,
	"HERDR_SOCKET_PATH":          true, // replaced with the session's own
}

// cliEnv is the launching environment minus any agent identity, plus the
// socket of the session the client is bound to (the workboard's rule: the
// plugin CLI lands in "the session of the process that runs it").
func cliEnv(session string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if !agentIdentityEnv[key] {
			env = append(env, kv)
		}
	}
	return append(env, "HERDR_SOCKET_PATH="+herdr.SocketPath(session))
}

func cliExec(root, session string) ExecIn {
	return func(ctx context.Context, stdin string, args ...string) (string, error) {
		if root == "" {
			return "", errors.New("automations: plugin root unknown")
		}
		cmd := exec.CommandContext(ctx, filepath.Join(root, "bin", "herdr-automations"), args...)
		cmd.Env = cliEnv(session)
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			// stderr is the plugin's own refusal (a name, a trigger, a
			// state) — never terminal or conversation content — and passes
			// through verbatim.
			return "", fmt.Errorf("automations: %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
		}
		return stdout.String(), nil
	}
}

// daemonStarter runs `herdr-automations daemon` in its own session so it
// outlives the window, appending its output to logPath (0600). The process is
// reaped in the background; a second daemon exits on its own.
func daemonStarter(root, session string) func(logPath string) error {
	return func(logPath string) error {
		if root == "" {
			return errors.New("automations: plugin root unknown")
		}
		cmd := exec.Command(filepath.Join(root, "bin", "herdr-automations"), "daemon")
		cmd.Env = cliEnv(session)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if logPath != "" {
			if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err == nil {
				if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
					defer f.Close()
					cmd.Stdout, cmd.Stderr = f, f
				}
			}
		}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("automations: daemon: %w", err)
		}
		go func() { _ = cmd.Wait() }()
		return nil
	}
}
