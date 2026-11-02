package automations

/**
 * [INPUT]: 依赖 Detect 给出的 plugin_root、`<root>/bin/herdr-automations <cmd> --json` 插件 CLI（无 run.sh，直接二进制）
 * [OUTPUT]: 对外提供 Client（List / History 读；Add（永远 --draft）/ Approve / Run / Pause / Resume / Remove / Put（`put --original <name>`）写）、可注入的 Exec 与契约 §3 的类型（Automation / Run / ListResult / HistoryResult）
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

	"github.com/thewei/solco/internal/herdr"
)

// Exec runs one plugin CLI invocation and returns its stdout.
type Exec func(ctx context.Context, args ...string) (string, error)

// Client reads and writes automations through the plugin's own CLI.
type Client struct{ exec Exec }

// NewClient binds a client to one plugin folder and the Herdr session the
// window is looking at (empty = default), so a manual run lands where the
// user is and its pane can be jumped to. An empty root yields a client whose
// every call fails: callers gate on Detect.
func NewClient(root, session string) *Client {
	return &Client{exec: cliExec(root, session)}
}

// NewClientWithExec is the test seam.
func NewClientWithExec(exec Exec) *Client { return &Client{exec: exec} }

// LastRun is the one run a list row carries (contract §3 `last_run`).
type LastRun struct {
	RunID           string `json:"run_id"`
	Status          string `json:"status"`
	StartedAt       string `json:"started_at"`
	DurationSeconds int64  `json:"duration_seconds"`
	Worktree        string `json:"worktree"`
	WorkspaceID     string `json:"workspace_id"`
	PaneID          string `json:"pane_id"`
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
	Paused      bool           `json:"paused"`
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
	out, err := c.exec(ctx, "list", "--json")
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
	out, err := c.exec(ctx, args...)
	if err != nil {
		return HistoryResult{}, err
	}
	var env historyEnvelope
	if err := jsonUnmarshal(out, &env); err != nil {
		return HistoryResult{}, fmt.Errorf("automations: history: %w", err)
	}
	return env.Result, nil
}

// Fields is what `add` and `put` take beyond the name (contract §3): one
// trigger — Cron (a cron expression) or OnceAt (one RFC3339 moment) — plus
// the run's body and the Phase 4 increments. The plugin parses and refuses
// what does not apply; nothing is created or changed then.
type Fields struct {
	Cron            string
	OnceAt          string
	Repo            string
	Agent           string
	Prompt          string
	Rationale       string
	OnOverlap       string // skip | queue; empty = the plugin's default
	TimeoutMinutes  int
	CatchUpMinutes  int
	CleanupTTLHours int
}

func (f Fields) validate(op string) error {
	for _, v := range []string{f.Cron, f.OnceAt, f.Repo, f.Agent, f.Prompt, f.Rationale, f.OnOverlap} {
		if strings.HasPrefix(v, "-") {
			return fmt.Errorf("automations: %s: option-like text is refused", op)
		}
	}
	return nil
}

func (f Fields) args() []string {
	var args []string
	switch {
	case f.Cron != "":
		args = append(args, "--cron", f.Cron)
	case f.OnceAt != "":
		args = append(args, "--once-at", f.OnceAt)
	}
	if f.Repo != "" {
		args = append(args, "--repo", f.Repo)
	}
	if f.Agent != "" {
		args = append(args, "--agent", f.Agent)
	}
	if f.Prompt != "" {
		args = append(args, "--prompt", f.Prompt)
	}
	if f.Rationale != "" {
		args = append(args, "--rationale", f.Rationale)
	}
	if f.OnOverlap != "" {
		args = append(args, "--on-overlap", f.OnOverlap)
	}
	if f.TimeoutMinutes > 0 {
		args = append(args, "--timeout-minutes", itoa(f.TimeoutMinutes))
	}
	if f.CatchUpMinutes > 0 {
		args = append(args, "--catch-up-minutes", itoa(f.CatchUpMinutes))
	}
	if f.CleanupTTLHours > 0 {
		args = append(args, "--cleanup-ttl-hours", itoa(f.CleanupTTLHours))
	}
	return args
}

// Add creates a draft. Creation always goes through --draft (contract §4:
// an agent proposes, a human approves); a prompt is required because an
// automation without one has nothing to run.
func (c *Client) Add(ctx context.Context, name string, f Fields) error {
	name = strings.TrimSpace(name)
	if err := checkName("add", name); err != nil {
		return err
	}
	if err := f.validate("add"); err != nil {
		return err
	}
	if strings.TrimSpace(f.Prompt) == "" {
		return errors.New("automations: add: a prompt is required")
	}
	args := append([]string{"add", name, "--draft"}, f.args()...)
	_, err := c.exec(ctx, args...)
	return err
}

// Approve turns one draft active (`approve <name>`); the plugin refuses a
// name that is not a draft and its refusal comes back as the error.
func (c *Client) Approve(ctx context.Context, name string) error {
	return c.nameVerb(ctx, "approve", name)
}

// Run fires one automation now (`run <name>`); the plugin refuses a draft.
func (c *Client) Run(ctx context.Context, name string) error {
	return c.nameVerb(ctx, "run", name)
}

// Pause stops a daemon from scheduling the automation (`pause <name>`).
func (c *Client) Pause(ctx context.Context, name string) error {
	return c.nameVerb(ctx, "pause", name)
}

// Resume hands the automation back to the daemon (`resume <name>`).
func (c *Client) Resume(ctx context.Context, name string) error {
	return c.nameVerb(ctx, "resume", name)
}

// Remove deletes one automation (`rm <name>`); its saved runs' record is the
// plugin's to keep or drop.
func (c *Client) Remove(ctx context.Context, name string) error {
	return c.nameVerb(ctx, "rm", name)
}

// Put saves an edit over the automation `original` names
// (`put --original <original> …`). The plugin owns what may change when;
// its refusal comes back as the error.
func (c *Client) Put(ctx context.Context, original string, f Fields) error {
	original = strings.TrimSpace(original)
	if err := checkName("put", original); err != nil {
		return err
	}
	if err := f.validate("put"); err != nil {
		return err
	}
	args := append([]string{"put", "--original", original}, f.args()...)
	_, err := c.exec(ctx, args...)
	return err
}

func (c *Client) nameVerb(ctx context.Context, verb, name string) error {
	if err := checkName(verb, name); err != nil {
		return err
	}
	_, err := c.exec(ctx, verb, name)
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

func cliExec(root, session string) Exec {
	return func(ctx context.Context, args ...string) (string, error) {
		if root == "" {
			return "", errors.New("automations: plugin root unknown")
		}
		cmd := exec.CommandContext(ctx, filepath.Join(root, "bin", "herdr-automations"), args...)
		cmd.Env = cliEnv(session)
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
