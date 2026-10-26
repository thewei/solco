package workboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wh-studio/solco/internal/herdr"
)

/**
 * [INPUT]: 依赖 Detect 给出的 plugin_root、herdr.SocketPath（会话 socket）、`sh <root>/bin/run.sh <cmd> --json` 插件 CLI
 * [OUTPUT]: 对外提供 Client（List / Details 读；Approve / SendBack / Retry / Fix / Delegate / DelegateWith（`delegate <id> --<setting> V`，只作用于这个任务）/ Pick（`pick <id> <letter>`）/ Archive / Add 写）、ErrNotReady 与可注入的 Exec
 * [POS]: 插件 CLI 的唯一调用点；读写命令都经此处。写入不自造语义：动词含义归插件，拒绝原文透传；Approve 在执行瞬间重读复核。以人类身份运行——剥掉 HERDR_PANE_ID 等 Agent 身份变量，避免从 Herdr pane 启动的 Solco 被插件当成 Agent 调用方
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Exec runs one plugin CLI invocation and returns its stdout.
type Exec func(ctx context.Context, args ...string) (string, error)

// Client reads the board through the plugin's own CLI.
type Client struct{ exec Exec }

// NewClient binds a client to one plugin folder and one Herdr session. An
// empty root yields a client whose every call fails: callers gate on Detect.
func NewClient(root, session string) *Client {
	return &Client{exec: cliExec(root, session)}
}

// NewClientWithExec is the test seam.
func NewClientWithExec(exec Exec) *Client { return &Client{exec: exec} }

// List returns the board's live (non-archived) tasks.
func (c *Client) List(ctx context.Context) ([]Task, error) {
	out, err := c.exec(ctx, "list", "--json")
	if err != nil {
		return nil, err
	}
	return ParseTasks(out)
}

// Details returns one task's details.
func (c *Client) Details(ctx context.Context, id string) (Detail, error) {
	if strings.TrimSpace(id) == "" || strings.HasPrefix(id, "-") {
		return Detail{}, errors.New("workboard: details: invalid task id")
	}
	out, err := c.exec(ctx, "details", id, "--json")
	if err != nil {
		return Detail{}, err
	}
	return ParseDetail(out)
}

// ErrNotReady refuses an Approve whose task, re-read at the moment of the
// click, is no longer safely "ready to approve".
var ErrNotReady = errors.New("workboard: the task is no longer ready to approve")

func checkID(op, id string) error {
	if strings.TrimSpace(id) == "" || strings.HasPrefix(id, "-") {
		return fmt.Errorf("workboard: %s: invalid task id", op)
	}
	return nil
}

// act runs one id-only plugin verb. The plugin owns the verb's meaning and
// refuses what does not apply (its message comes back as the error).
func (c *Client) act(ctx context.Context, verb, id string) error {
	if err := checkID(verb, id); err != nil {
		return err
	}
	_, err := c.exec(ctx, verb, id)
	return err
}

// SendBack hands the reviewer's findings back to the task's agent.
func (c *Client) SendBack(ctx context.Context, id string) error { return c.act(ctx, "review", id) }

// Retry sends the saved error back to the agent.
func (c *Client) Retry(ctx context.Context, id string) error { return c.act(ctx, "retry", id) }

// Fix sends failure details to the agent; it never approves a merge.
func (c *Client) Fix(ctx context.Context, id string) error { return c.act(ctx, "fix", id) }

// Delegate puts an agent on a task that waits.
func (c *Client) Delegate(ctx context.Context, id string) error { return c.act(ctx, "delegate", id) }

// Stop stops a task's agent; the plugin refuses Archive while one is active.
func (c *Client) Stop(ctx context.Context, id string) error { return c.act(ctx, "stop", id) }

// Archive moves a task off the board.
func (c *Client) Archive(ctx context.Context, id string) error { return c.act(ctx, "archive", id) }

// Approve merges finished work. It re-reads the task first and only runs when
// GroupOf still says Ready; it never passes --anyway, so a failed review or
// pull-request check can only be approved from the plugin's own board.
func (c *Client) Approve(ctx context.Context, id string) error {
	if err := checkID("approve", id); err != nil {
		return err
	}
	tasks, err := c.List(ctx)
	if err != nil {
		return err
	}
	var task *Task
	for i := range tasks {
		if tasks[i].ID == id {
			task = &tasks[i]
		}
	}
	if task == nil || !task.ReadyCandidate() {
		return ErrNotReady
	}
	detail, err := c.Details(ctx, id)
	if err != nil || GroupOf(*task, &detail) != GroupReady {
		return ErrNotReady
	}
	_, err = c.exec(ctx, "approve", id)
	return err
}

// ApprovePlan says yes to a written plan so the build starts. `approve` also
// merges finished work, so the task is re-read first and must still be at its
// plan: anything else is refused rather than approved as something it is not.
func (c *Client) ApprovePlan(ctx context.Context, id string) error {
	if err := checkID("approve", id); err != nil {
		return err
	}
	tasks, err := c.List(ctx)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t.ID == id && t.PlanReady() {
			_, err = c.exec(ctx, "approve", id)
			return err
		}
	}
	return ErrNotReady
}

// NewTask is everything the plugin's `add` takes that the client offers.
// Description is the task's longer text; Research and Chat mark the two kinds
// that change nothing in the repo; After names the tasks it waits for;
// Delegate also starts an agent on it.
type NewTask struct {
	Title, Repo, Description string
	Research, Chat           bool
	After                    []string
	Delegate                 bool
}

// Add creates a task in repo (a name or a path); delegate also starts an
// agent on it. Option-like text is refused rather than parsed as a flag.
func (c *Client) Add(ctx context.Context, title, repo string, delegate bool) error {
	return c.AddTask(ctx, NewTask{Title: title, Repo: repo, Delegate: delegate})
}

// AddTask creates a task with the options the plugin's `add` takes. The
// plugin checks what --after names before adding anything, and its refusal
// comes back as the error.
func (c *Client) AddTask(ctx context.Context, t NewTask) error {
	title, repo, desc := strings.TrimSpace(t.Title), strings.TrimSpace(t.Repo), strings.TrimSpace(t.Description)
	if title == "" || strings.HasPrefix(title, "-") || strings.HasPrefix(repo, "-") || strings.HasPrefix(desc, "-") {
		return errors.New("workboard: add: invalid title, repo or description")
	}
	var after []string
	for _, ref := range t.After {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, ", ") {
			return errors.New("workboard: add: invalid task to wait for")
		}
		after = append(after, ref)
	}
	args := []string{"add", title}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	if desc != "" {
		args = append(args, "--desc", desc)
	}
	if t.Research {
		args = append(args, "--research")
	}
	if t.Chat {
		args = append(args, "--chat")
	}
	if len(after) > 0 {
		args = append(args, "--after", strings.Join(after, ","))
	}
	if t.Delegate {
		args = append(args, "--delegate")
	}
	_, err := c.exec(ctx, args...)
	return err
}

// SettingValue is one setting as `delegate --<name> VALUE` takes it.
type SettingValue struct{ Name, Value string }

// DelegateWith puts an agent on a task that waits, with work settings for this
// task only (`delegate <id> --worker.effort high …`). The plugin refuses an
// option it does not accept and then changes nothing on the task.
func (c *Client) DelegateWith(ctx context.Context, id string, with []SettingValue) error {
	if err := checkID("delegate", id); err != nil {
		return err
	}
	args := []string{"delegate", id}
	for _, w := range with {
		value := strings.TrimSpace(w.Value)
		if err := checkSettingName(w.Name); err != nil || value == "" || strings.HasPrefix(value, "-") {
			return errors.New("workboard: delegate: invalid setting")
		}
		args = append(args, "--"+w.Name, value)
	}
	_, err := c.exec(ctx, args...)
	return err
}

// Pick keeps one of a task's several attempts (`pick <id> <letter>`); the
// plugin closes the others' tabs and refuses a letter that did not pass.
func (c *Client) Pick(ctx context.Context, id, letter string) error {
	if err := checkID("pick", id); err != nil {
		return err
	}
	letter = strings.TrimSpace(letter)
	if len(letter) != 1 || !strings.ContainsAny(letter, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		return errors.New("workboard: pick: invalid attempt")
	}
	_, err := c.exec(ctx, "pick", id, letter)
	return err
}

// DelegateNow starts a task that is queued after others without waiting for
// them (`delegate <id> --now`).
func (c *Client) DelegateNow(ctx context.Context, id string) error {
	if err := checkID("delegate", id); err != nil {
		return err
	}
	_, err := c.exec(ctx, "delegate", id, "--now")
	return err
}

// Tell sends a message to a task's agent.
func (c *Client) Tell(ctx context.Context, id, text string) error {
	text = strings.TrimSpace(text)
	if err := checkID("tell", id); err != nil {
		return err
	}
	if text == "" || strings.HasPrefix(text, "-") {
		return errors.New("workboard: tell: invalid message")
	}
	_, err := c.exec(ctx, "tell", id, text)
	return err
}

// CoordinatorTell says something to a project's coordinator (the plugin starts
// it when it is not running); delegate puts an agent on whatever the
// coordinator adds from the message.
func (c *Client) CoordinatorTell(ctx context.Context, project, text string, delegate bool) error {
	project, text = strings.TrimSpace(project), strings.TrimSpace(text)
	if project == "" || strings.HasPrefix(project, "-") || text == "" || strings.HasPrefix(text, "-") {
		return errors.New("workboard: coordinator: invalid project or message")
	}
	args := []string{"coordinator", "--project", project, "tell", text}
	if delegate {
		args = append(args, "--delegate")
	}
	_, err := c.exec(ctx, args...)
	return err
}

// agentIdentityEnv is the per-pane / per-invocation environment Herdr injects
// into plugin processes; the plugin itself drops exactly this set when it
// spawns its helpers (runner.rs DROP_ENV). Solco is the human, so none of
// it may leak in from the launching environment.
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
			return "", errors.New("workboard: plugin root unknown")
		}
		cmd := exec.CommandContext(ctx, "sh", append([]string{filepath.Join(root, "bin", "run.sh")}, args...)...)
		cmd.Env = cliEnv(session)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			// stderr is the plugin's own message about a task id, never
			// terminal or conversation content.
			return "", fmt.Errorf("workboard: %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
		}
		return stdout.String(), nil
	}
}
