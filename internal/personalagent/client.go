// Package personalagent is Solco's seam to the Assistants core that lives in
// the com.whstudio.solco.automations plugin's daemon (product decision
// 2026-11-03). The plugin owns the database (SQLite, one writer), the
// dispatcher and every state transition; this package only calls its CLI —
// JSON on stdin, one JSON object on stdout — and presents the answers. The
// root module never imports the plugin's internals.
package personalagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Exec runs one CLI invocation. It returns stdout even when the process exits
// non-zero (the CLI reports refusals as JSON on stdout), and an error for the
// exit status.
type Exec func(ctx context.Context, stdin string, args ...string) ([]byte, error)

// Client calls the plugin's `assistant …` commands.
type Client struct{ exec Exec }

// NewClient binds a client to one plugin folder (automations.Availability.Root).
// env carries the Herdr session socket the CLI should use (HERDR_SOCKET_PATH).
func NewClient(root string, env []string) *Client {
	if root == "" {
		return nil
	}
	bin := filepath.Join(root, "bin", "solco-automations")
	return &Client{exec: func(ctx context.Context, stdin string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = append(os.Environ(), env...)
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		var out bytes.Buffer
		cmd.Stdout = &out
		err := cmd.Run()
		return out.Bytes(), err
	}}
}

// NewClientWithExec is the test seam.
func NewClientWithExec(e Exec) *Client { return &Client{exec: e} }

// Available is the capability gate: the plugin answers `assistant overview`
// with this schema. An older plugin, a missing binary or a broken database all
// close it, and the Assistants surfaces leave no trace.
func (c *Client) Available(ctx context.Context) bool {
	if c == nil {
		return false
	}
	_, err := c.Overview(ctx)
	return err == nil
}

// IsCode reports whether err is a plugin refusal with this code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// do runs a command and decodes its answer into out (which may be nil).
func (c *Client) do(ctx context.Context, body any, out any, args ...string) error {
	if c == nil || c.exec == nil {
		return errors.New("personalagent: unavailable")
	}
	stdin := ""
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		stdin = string(raw)
	}
	raw, runErr := c.exec(ctx, stdin, append([]string{"assistant"}, args...)...)
	var env struct {
		Schema int    `json:"schema"`
		Error  *Error `json:"error"`
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &env); err != nil {
			if runErr != nil {
				return fmt.Errorf("personalagent: %s: %w", args[0], runErr)
			}
			return fmt.Errorf("personalagent: %s: unreadable answer", args[0])
		}
	}
	if env.Error != nil {
		return env.Error
	}
	if runErr != nil {
		return fmt.Errorf("personalagent: %s: %w", args[0], runErr)
	}
	if env.Schema != 1 {
		return fmt.Errorf("personalagent: %s: unsupported schema %d", args[0], env.Schema)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type req struct {
	RequestID        string `json:"requestId"`
	ExpectedRevision int    `json:"expectedRevision,omitempty"`
}

// Overview reads the per-Assistant counters.
func (c *Client) Overview(ctx context.Context) (Overview, error) {
	var out Overview
	return out, c.do(ctx, nil, &out, "overview")
}

// Assistants lists every identity.
func (c *Client) Assistants(ctx context.Context) ([]Assistant, error) {
	var out struct {
		Assistants []Assistant `json:"assistants"`
	}
	return out.Assistants, c.do(ctx, nil, &out, "list")
}

// ArchivedAssistants lists identities held in the Settings archive.
func (c *Client) ArchivedAssistants(ctx context.Context) ([]Assistant, error) {
	var out struct {
		Assistants []Assistant `json:"assistants"`
	}
	return out.Assistants, c.do(ctx, nil, &out, "archived")
}

// ArchiveAssistant removes an Assistant from active surfaces, retaining data.
func (c *Client) ArchiveAssistant(ctx context.Context, requestID, id string, rev int) (Assistant, error) {
	var out struct {
		Assistant Assistant `json:"assistant"`
	}
	return out.Assistant, c.do(ctx, req{RequestID: requestID, ExpectedRevision: rev}, &out, "archive", id)
}

// RestoreAssistant reactivates an archived identity and all of its records.
func (c *Client) RestoreAssistant(ctx context.Context, requestID, id string, rev int) (Assistant, error) {
	var out struct {
		Assistant Assistant `json:"assistant"`
	}
	return out.Assistant, c.do(ctx, req{RequestID: requestID, ExpectedRevision: rev}, &out, "restore", id)
}

// DeleteAssistantForever refuses if shared or external Agent state is unresolved.
func (c *Client) DeleteAssistantForever(ctx context.Context, requestID, id string, rev int) error {
	return c.do(ctx, req{RequestID: requestID, ExpectedRevision: rev}, nil, "delete-forever", id)
}

// CreateAssistant records a new identity (idempotent on requestID).
func (c *Client) CreateAssistant(ctx context.Context, requestID, name, description string) (Assistant, error) {
	var out struct {
		Assistant Assistant `json:"assistant"`
	}
	body := struct {
		req
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	}{req{RequestID: requestID}, name, description}
	return out.Assistant, c.do(ctx, body, &out, "create")
}

// RenameAssistant changes the display name.
func (c *Client) RenameAssistant(ctx context.Context, requestID, id, name string, rev int) (Assistant, error) {
	var out struct {
		Assistant Assistant `json:"assistant"`
	}
	body := struct {
		req
		Name string `json:"name"`
	}{req{requestID, rev}, name}
	return out.Assistant, c.do(ctx, body, &out, "rename", id)
}

// SetAssistantEnabled disables or re-enables an Assistant.
func (c *Client) SetAssistantEnabled(ctx context.Context, requestID, id string, enabled bool, rev int) (Assistant, error) {
	var out struct {
		Assistant Assistant `json:"assistant"`
	}
	verb := "disable"
	if enabled {
		verb = "enable"
	}
	return out.Assistant, c.do(ctx, req{requestID, rev}, &out, verb, id)
}

// Goals lists an Assistant's goals.
func (c *Client) Goals(ctx context.Context, assistantID string) ([]Goal, error) {
	var out struct {
		Goals []Goal `json:"goals"`
	}
	return out.Goals, c.do(ctx, nil, &out, "goal", "list", "--assistant", assistantID)
}

// CreateGoal drafts a goal.
func (c *Client) CreateGoal(ctx context.Context, requestID string, in CreateGoalInput) (Goal, error) {
	var out struct {
		Goal Goal `json:"goal"`
	}
	body := struct {
		req
		CreateGoalInput
	}{req{RequestID: requestID}, in}
	return out.Goal, c.do(ctx, body, &out, "goal", "create")
}

// GoalAction runs approve | pause | resume | cancel | complete (evidence only
// for complete).
func (c *Client) GoalAction(ctx context.Context, requestID, action, id string, rev int, evidence string) (Goal, error) {
	var out struct {
		Goal Goal `json:"goal"`
	}
	body := struct {
		req
		Evidence string `json:"evidence,omitempty"`
	}{req{requestID, rev}, evidence}
	return out.Goal, c.do(ctx, body, &out, "goal", action, id)
}

// Tasks lists an Assistant's tasks.
func (c *Client) Tasks(ctx context.Context, assistantID string) ([]Task, error) {
	var out struct {
		Tasks []Task `json:"tasks"`
	}
	return out.Tasks, c.do(ctx, nil, &out, "task", "list", "--assistant", assistantID)
}

// TaskDetail reads one task with its runs.
func (c *Client) TaskDetail(ctx context.Context, id string) (Task, []Run, error) {
	var out struct {
		Task Task  `json:"task"`
		Runs []Run `json:"runs"`
	}
	err := c.do(ctx, nil, &out, "task", "show", id)
	return out.Task, out.Runs, err
}

// CreateTask delegates one unit of work.
func (c *Client) CreateTask(ctx context.Context, requestID string, in CreateTaskInput) (Task, error) {
	var out struct {
		Task Task `json:"task"`
	}
	body := struct {
		req
		CreateTaskInput
	}{req{RequestID: requestID}, in}
	return out.Task, c.do(ctx, body, &out, "task", "create")
}

// AckTask clears Needs You on a finished task.
func (c *Client) AckTask(ctx context.Context, requestID, id string, rev int) (Task, error) {
	var out struct {
		Task Task `json:"task"`
	}
	return out.Task, c.do(ctx, req{requestID, rev}, &out, "task", "ack", id)
}

// RetryTask queues a failed or cancelled task again, on purpose.
func (c *Client) RetryTask(ctx context.Context, requestID, id string, rev int) (Task, error) {
	var out struct {
		Task Task `json:"task"`
	}
	return out.Task, c.do(ctx, req{requestID, rev}, &out, "task", "retry", id)
}

// Inbox reads one Assistant's inbox snapshot ("" = every Assistant's).
func (c *Client) Inbox(ctx context.Context, assistantID string) (Inbox, error) {
	var out struct {
		Inbox Inbox `json:"inbox"`
	}
	args := []string{"inbox"}
	if assistantID != "" {
		args = append(args, "--assistant", assistantID)
	}
	return out.Inbox, c.do(ctx, nil, &out, args...)
}

// PublishTask moves a draft into the queue (or into waiting behind its dependencies).
func (c *Client) PublishTask(ctx context.Context, requestID, id string, rev int) (Task, error) {
	var out struct {
		Task Task `json:"task"`
	}
	return out.Task, c.do(ctx, req{requestID, rev}, &out, "task", "publish", id)
}

// Coordinator reads an Assistant's Coordinator (state "none" before its first start).
func (c *Client) Coordinator(ctx context.Context, assistantID string) (Coordinator, error) {
	var out struct {
		Coordinator Coordinator `json:"coordinator"`
	}
	return out.Coordinator, c.do(ctx, nil, &out, "coordinator", "show", assistantID)
}

// StartCoordinator queues the Coordinator; the daemon launches it as a real
// Herdr Agent of the given kind (empty = the plugin's default) and optional model.
func (c *Client) StartCoordinator(ctx context.Context, requestID, assistantID, agentKind, model string) (Coordinator, error) {
	var out struct {
		Coordinator Coordinator `json:"coordinator"`
	}
	body := struct {
		req
		Agent string `json:"agent,omitempty"`
		Model string `json:"model,omitempty"`
	}{req{RequestID: requestID}, agentKind, model}
	return out.Coordinator, c.do(ctx, body, &out, "coordinator", "start", assistantID)
}

// StopCoordinator marks the Coordinator stopped.
func (c *Client) StopCoordinator(ctx context.Context, requestID, assistantID string) (Coordinator, error) {
	var out struct {
		Coordinator Coordinator `json:"coordinator"`
	}
	return out.Coordinator, c.do(ctx, req{RequestID: requestID}, &out, "coordinator", "stop", assistantID)
}

// SupportsCoordinatorReports verifies a plugin explicitly implements the
// report protocol. An older Overview-capable plugin must not advertise this
// feature to a newer phone.
func (c *Client) SupportsCoordinatorReports(ctx context.Context) bool {
	var out struct {
		Supported bool `json:"supported"`
	}
	return c.do(ctx, nil, &out, "report", "probe") == nil && out.Supported
}

// CoordinatorReport is the persisted delivery status for periodic progress
// narration. A submitted prompt may still need time for OMP to reply.
func (c *Client) CoordinatorReport(ctx context.Context, assistantID string) (CoordinatorReport, error) {
	var out struct {
		Report CoordinatorReport `json:"report"`
	}
	return out.Report, c.do(ctx, nil, &out, "report", "show", assistantID)
}

// ResolveCoordinatorReport is an explicit human review of one uncertain
// delivery, not an automatic retry. The throughSeq fences a stale click.
func (c *Client) ResolveCoordinatorReport(ctx context.Context, requestID, assistantID string, throughSeq int64, action string, confirmed bool) (CoordinatorReport, error) {
	var out struct {
		Report CoordinatorReport `json:"report"`
	}
	in := struct {
		RequestID  string `json:"requestId"`
		ThroughSeq int64  `json:"throughSeq"`
		Action     string `json:"action"`
		Confirmed  bool   `json:"confirmed"`
	}{requestID, throughSeq, action, confirmed}
	return out.Report, c.do(ctx, in, &out, "report", "resolve", assistantID)
}

// Approvals lists an Assistant's approvals.
func (c *Client) Approvals(ctx context.Context, assistantID string) ([]Approval, error) {
	var out struct {
		Approvals []Approval `json:"approvals"`
	}
	return out.Approvals, c.do(ctx, nil, &out, "approval", "list", "--assistant", assistantID)
}

// DecideApproval answers one approval card.
func (c *Client) DecideApproval(ctx context.Context, requestID, id, actionHash string, approve bool) (Approval, error) {
	var out struct {
		Approval Approval `json:"approval"`
	}
	body := struct {
		req
		ActionHash string `json:"actionHash"`
		Approve    bool   `json:"approve"`
	}{req{RequestID: requestID}, actionHash, approve}
	return out.Approval, c.do(ctx, body, &out, "approval", "decide", id)
}

// VisibleMemory is the approved memory an Assistant may use.
func (c *Client) VisibleMemory(ctx context.Context, assistantID string) ([]MemoryItem, error) {
	var out struct {
		Memory []MemoryItem `json:"memory"`
	}
	return out.Memory, c.do(ctx, nil, &out, "memory", "visible", assistantID)
}

// Groups lists every Group, most recently active first.
func (c *Client) Groups(ctx context.Context) ([]Group, error) {
	var out struct {
		Groups []Group `json:"groups"`
	}
	return out.Groups, c.do(ctx, nil, &out, "group", "list")
}

// CreateGroup records a Group of at least two Assistants (idempotent on requestID).
func (c *Client) CreateGroup(ctx context.Context, requestID, title string, members []string) (Group, error) {
	var out struct {
		Group Group `json:"group"`
	}
	body := struct {
		req
		Title   string   `json:"title,omitempty"`
		Members []string `json:"members"`
	}{req{RequestID: requestID}, title, members}
	return out.Group, c.do(ctx, body, &out, "group", "create")
}

// GroupFeed reads a Group's log: the newest page when since is 0, else what
// came after the message seq.
func (c *Client) GroupFeed(ctx context.Context, id string, since int64, limit int) (GroupFeed, error) {
	var out struct {
		Feed GroupFeed `json:"feed"`
	}
	args := []string{"group", "show", id}
	if since > 0 {
		args = append(args, "--since", fmt.Sprint(since))
	}
	if limit > 0 {
		args = append(args, "--limit", fmt.Sprint(limit))
	}
	return out.Feed, c.do(ctx, nil, &out, args...)
}

// RenameGroup changes a Group's title.
func (c *Client) RenameGroup(ctx context.Context, requestID, id, title string, rev int) (Group, error) {
	var out struct {
		Group Group `json:"group"`
	}
	body := struct {
		req
		Title string `json:"title"`
	}{req{requestID, rev}, title}
	return out.Group, c.do(ctx, body, &out, "group", "rename", id)
}

// GroupMember adds or removes (add=false) one Assistant.
func (c *Client) GroupMember(ctx context.Context, requestID, id, assistantID string, add bool) (Group, error) {
	var out struct {
		Group Group `json:"group"`
	}
	body := struct {
		req
		AssistantID string `json:"assistantId"`
	}{req{RequestID: requestID}, assistantID}
	verb := "remove"
	if add {
		verb = "add"
	}
	return out.Group, c.do(ctx, body, &out, "group", verb, id)
}

// PostGroupMessage is the person's own message; mentions are member ids whose
// `@Name` must appear in the text. Only those Assistants are ever woken.
func (c *Client) PostGroupMessage(ctx context.Context, requestID, groupID, text string, mentions []string, replyTo string) (PostResult, error) {
	var out struct {
		Result PostResult `json:"result"`
	}
	body := struct {
		req
		GroupID  string   `json:"groupId"`
		Body     string   `json:"body"`
		Mentions []string `json:"mentions,omitempty"`
		ReplyTo  string   `json:"replyTo,omitempty"`
	}{req{RequestID: requestID}, groupID, text, mentions, replyTo}
	return out.Result, c.do(ctx, body, &out, "group", "post")
}

// Instructions reads an Assistant's working style (text, history, applied revisions).
func (c *Client) Instructions(ctx context.Context, id string) (Instructions, error) {
	var out struct {
		Instructions Instructions `json:"instructions"`
	}
	return out.Instructions, c.do(ctx, nil, &out, "persona", "instructions", id)
}

// SetInstructions saves a new revision of the working style.
func (c *Client) SetInstructions(ctx context.Context, requestID, id, body string, rev int) (Instructions, error) {
	var out struct {
		Instructions Instructions `json:"instructions"`
	}
	in := struct {
		req
		Body string `json:"body"`
	}{req{requestID, rev}, body}
	return out.Instructions, c.do(ctx, in, &out, "persona", "set-instructions", id)
}

// RestoreInstructions makes an earlier revision's text current as a new revision.
func (c *Client) RestoreInstructions(ctx context.Context, requestID, id string, revision, rev int) (Instructions, error) {
	var out struct {
		Instructions Instructions `json:"instructions"`
	}
	in := struct {
		req
		Revision int `json:"revision"`
	}{req{requestID, rev}, revision}
	return out.Instructions, c.do(ctx, in, &out, "persona", "restore-instructions", id)
}

// Runtime reads the Assistant's Agent/Model/Effort profile and its running Agents.
func (c *Client) Runtime(ctx context.Context, id string) (Runtime, error) {
	var out struct {
		Runtime Runtime `json:"runtime"`
	}
	return out.Runtime, c.do(ctx, nil, &out, "persona", "runtime", id)
}

// SetRuntime saves the profile (applies to the next start).
func (c *Client) SetRuntime(ctx context.Context, requestID, id, agent, model, effort string, rev int) (Runtime, error) {
	var out struct {
		Runtime Runtime `json:"runtime"`
	}
	in := struct {
		requestIDOnly
		Agent  string `json:"agent"`
		Model  string `json:"model"`
		Effort string `json:"effort"`
		Rev    int    `json:"expectedRevision"`
	}{requestIDOnly{requestID}, agent, model, effort, rev}
	return out.Runtime, c.do(ctx, in, &out, "persona", "set-runtime", id)
}

type requestIDOnly struct {
	RequestID string `json:"requestId"`
}

// MCP reads the Assistant's MCP servers with their four states.
func (c *Client) MCP(ctx context.Context, id string) (MCPView, error) {
	var out struct {
		MCP MCPView `json:"mcp"`
	}
	return out.MCP, c.do(ctx, nil, &out, "persona", "mcp", id)
}

// GrantMCP allows or withdraws one server for the Assistant.
func (c *Client) GrantMCP(ctx context.Context, requestID, id, serverID string, granted bool) (MCPView, error) {
	var out struct {
		MCP MCPView `json:"mcp"`
	}
	in := struct {
		requestIDOnly
		ServerID string `json:"serverId"`
		Granted  bool   `json:"granted"`
	}{requestIDOnly{requestID}, serverID, granted}
	return out.MCP, c.do(ctx, in, &out, "persona", "mcp-grant", id)
}

// VerifyMCP probes what an Agent would load and records its own per-server status.
func (c *Client) VerifyMCP(ctx context.Context, id string) (MCPView, error) {
	var out struct {
		MCP MCPView `json:"mcp"`
	}
	return out.MCP, c.do(ctx, nil, &out, "persona", "mcp-verify", id)
}

// GoalPlan reads a Goal's plan; a Goal with none answers ErrNotFound-coded `not_found`.
func (c *Client) GoalPlan(ctx context.Context, goalID string) (GoalPlan, error) {
	var out struct {
		Plan GoalPlan `json:"plan"`
	}
	return out.Plan, c.do(ctx, nil, &out, "goal", "plan", goalID)
}

// SetGoalPlan stores (rev 0) or revises (current planRevision) a Goal's plan.
func (c *Client) SetGoalPlan(ctx context.Context, requestID, goalID string, in GoalPlanInput, reason string, rev int) (GoalPlan, error) {
	var out struct {
		Plan GoalPlan `json:"plan"`
	}
	body := struct {
		req
		GoalPlanInput
		Reason string `json:"reason,omitempty"`
	}{req{requestID, rev}, in, reason}
	return out.Plan, c.do(ctx, body, &out, "goal", "set-plan", goalID)
}

// GoalFacts lists a Goal's ledger, newest first.
func (c *Client) GoalFacts(ctx context.Context, goalID string) ([]Fact, error) {
	var out struct {
		Facts []Fact `json:"facts"`
	}
	return out.Facts, c.do(ctx, nil, &out, "goal", "facts", goalID)
}

// Facts lists an Assistant's ledger across its Goals, newest first.
func (c *Client) Facts(ctx context.Context, assistantID string, limit int) ([]Fact, error) {
	var out struct {
		Facts []Fact `json:"facts"`
	}
	args := []string{"facts", assistantID}
	if limit > 0 {
		args = append(args, "--limit", strconv.Itoa(limit))
	}
	return out.Facts, c.do(ctx, nil, &out, args...)
}

// Activity returns a bounded, oldest-first page of Solco progress facts.
func (c *Client) Activity(ctx context.Context, assistantID, consumer string, after int64, limit int) (ActivityPage, error) {
	var out struct {
		Activity ActivityPage `json:"activity"`
	}
	args := []string{"activity", "list", assistantID, "--consumer", consumer, "--after", strconv.FormatInt(after, 10)}
	if limit > 0 {
		args = append(args, "--limit", strconv.Itoa(limit))
	}
	return out.Activity, c.do(ctx, nil, &out, args...)
}

// UnreadActivity returns the page after this consumer's durable ACK.
func (c *Client) UnreadActivity(ctx context.Context, assistantID, consumer string, limit int) (ActivityPage, error) {
	var out struct {
		Activity ActivityPage `json:"activity"`
	}
	args := []string{"activity", "list", assistantID, "--consumer", consumer, "--unread"}
	if limit > 0 {
		args = append(args, "--limit", strconv.Itoa(limit))
	}
	return out.Activity, c.do(ctx, nil, &out, args...)
}

// AckActivity stores one consumer's processed progress watermark. A caller
// must ACK only after it has handled the returned Activity facts.
func (c *Client) AckActivity(ctx context.Context, requestID, assistantID, consumer string, seq int64) (int64, error) {
	var out struct {
		AckSeq int64 `json:"ackSeq"`
	}
	in := struct {
		RequestID string `json:"requestId"`
		Consumer  string `json:"consumer"`
		Seq       int64  `json:"seq"`
	}{requestID, consumer, seq}
	return out.AckSeq, c.do(ctx, in, &out, "activity", "ack", assistantID)
}

// Routines lists an Assistant's routines (everyone's when empty), newest first.
func (c *Client) Routines(ctx context.Context, assistantID string) ([]Routine, error) {
	var out struct {
		Routines []Routine `json:"routines"`
	}
	args := []string{"routine", "list"}
	if assistantID != "" {
		args = append(args, "--assistant", assistantID)
	}
	return out.Routines, c.do(ctx, nil, &out, args...)
}

// RoutineAction runs cancel | pause | resume on a routine (an Assistant never
// enables one: that is the approval's answer).
func (c *Client) RoutineAction(ctx context.Context, requestID, action, name string) (Routine, error) {
	var out struct {
		Routine Routine `json:"routine"`
	}
	return out.Routine, c.do(ctx, req{RequestID: requestID}, &out, "routine", action, name)
}

// AddMemory is a person writing a fact down: approved at once.
func (c *Client) AddMemory(ctx context.Context, requestID, assistantID, content, kind string) (MemoryItem, error) {
	var out struct {
		Memory MemoryItem `json:"memory"`
	}
	body := struct {
		req
		AssistantID string `json:"assistantId"`
		Content     string `json:"content"`
		Kind        string `json:"kind,omitempty"`
	}{req{RequestID: requestID}, assistantID, content, kind}
	return out.Memory, c.do(ctx, body, &out, "memory", "add")
}

// EditMemory approves a candidate, rewrites an item (content) or forgets it.
func (c *Client) EditMemory(ctx context.Context, requestID, action, id string, rev int, content string) (MemoryItem, error) {
	var out struct {
		Memory MemoryItem `json:"memory"`
	}
	body := struct {
		req
		Content string `json:"content,omitempty"`
	}{req{requestID, rev}, content}
	return out.Memory, c.do(ctx, body, &out, "memory", action, id)
}

// MemoryHistory lists one item's versions, newest first.
func (c *Client) MemoryHistory(ctx context.Context, id string) ([]MemoryVersion, error) {
	var out struct {
		Versions []MemoryVersion `json:"versions"`
	}
	return out.Versions, c.do(ctx, nil, &out, "memory", "history", id)
}

// ReviewMemory is the live memory plus the tidy-up findings.
func (c *Client) ReviewMemory(ctx context.Context, assistantID string) (MemoryReview, error) {
	var out struct {
		Review MemoryReview `json:"review"`
	}
	return out.Review, c.do(ctx, nil, &out, "memory", "review", assistantID)
}

// MemoryFolder reconciles the Assistant's memory folder and returns its path.
func (c *Client) MemoryFolder(ctx context.Context, assistantID string) (string, error) {
	var out struct {
		Path string `json:"path"`
	}
	return out.Path, c.do(ctx, nil, &out, "memory", "folder", assistantID)
}

// Proactivity reads one Assistant's proactivity rules.
func (c *Client) Proactivity(ctx context.Context, assistantID string) (Proactivity, error) {
	var out struct {
		Proactivity Proactivity `json:"proactivity"`
	}
	return out.Proactivity, c.do(ctx, nil, &out, "proactivity", "show", assistantID)
}

// SetProactivity writes them (rev 0 creates).
func (c *Client) SetProactivity(ctx context.Context, requestID, assistantID string, in ProactivityInput, rev int) (Proactivity, error) {
	var out struct {
		Proactivity Proactivity `json:"proactivity"`
	}
	body := struct {
		req
		ProactivityInput
	}{req{requestID, rev}, in}
	return out.Proactivity, c.do(ctx, body, &out, "proactivity", "set", assistantID)
}

// HookDelivery is one webhook request as the Host saw it; the core verifies the
// signature itself, so the Host forwards, never trusts.
type HookDelivery struct {
	KeyID     string `json:"keyId"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Timestamp string `json:"timestamp"`
	Delivery  string `json:"delivery"`
	Signature string `json:"signature"`
	Body      string `json:"body"`
}

// HookReceipt is the core's durable answer: accepted = recorded, never "looked at".
type HookReceipt struct {
	EventID     string `json:"eventId"`
	AssistantID string `json:"assistantId"`
	Receipt     string `json:"receipt"`
	Routing     string `json:"routing"`
	Replayed    bool   `json:"replayed"`
}

// HookIngest hands one delivery to the core (`assistant hook ingest`).
func (c *Client) HookIngest(ctx context.Context, d HookDelivery) (HookReceipt, error) {
	var out struct {
		Receipt HookReceipt `json:"receipt"`
	}
	return out.Receipt, c.do(ctx, d, &out, "hook", "ingest")
}

// HookKeys lists an Assistant's webhook keys (no secrets), newest first.
func (c *Client) HookKeys(ctx context.Context, assistantID string) ([]HookKey, error) {
	var out struct {
		Keys []HookKey `json:"keys"`
	}
	return out.Keys, c.do(ctx, nil, &out, "hook", "key", "list", "--assistant", assistantID)
}

// HookRecords lists an Assistant's newest accepted webhook deliveries.
func (c *Client) HookRecords(ctx context.Context, assistantID string, limit int) ([]HookRecord, error) {
	var out struct {
		Events []HookRecord `json:"events"`
	}
	args := []string{"hook", "events", assistantID}
	if limit > 0 {
		args = append(args, "--limit", strconv.Itoa(limit))
	}
	return out.Events, c.do(ctx, nil, &out, args...)
}

// CreateHookKey makes a key. The secret is returned once (empty on a replay)
// and is never listed again; path is where the sender must post.
func (c *Client) CreateHookKey(ctx context.Context, requestID, assistantID string, sources []string) (key HookKey, secret, path string, err error) {
	var out struct {
		Key    HookKey `json:"key"`
		Secret string  `json:"secret"`
		Path   string  `json:"path"`
	}
	in := struct {
		RequestID   string   `json:"requestId"`
		AssistantID string   `json:"assistantId"`
		Sources     []string `json:"sources"`
	}{requestID, assistantID, sources}
	err = c.do(ctx, in, &out, "hook", "key", "create")
	return out.Key, out.Secret, out.Path, err
}

// RevokeHookKey stops a key at once.
func (c *Client) RevokeHookKey(ctx context.Context, requestID, keyID string) (HookKey, error) {
	var out struct {
		Key HookKey `json:"key"`
	}
	return out.Key, c.do(ctx, req{RequestID: requestID}, &out, "hook", "key", "revoke", keyID)
}

// Triggers lists an Assistant's event triggers, newest first.
func (c *Client) Triggers(ctx context.Context, assistantID string) ([]Trigger, error) {
	var out struct {
		Triggers []Trigger `json:"triggers"`
	}
	return out.Triggers, c.do(ctx, nil, &out, "trigger", "list", "--assistant", assistantID)
}

// TriggerAction runs cancel | pause | resume on a trigger (approving is the
// approval card's answer, never this).
func (c *Client) TriggerAction(ctx context.Context, requestID, action, id string, rev int) (Trigger, error) {
	var out struct {
		Trigger Trigger `json:"trigger"`
	}
	return out.Trigger, c.do(ctx, req{requestID, rev}, &out, "trigger", action, id)
}

// OwnerStatus says whether the core has an owner public key (A04).
func (c *Client) OwnerStatus(ctx context.Context) (OwnerStatus, error) {
	var out struct {
		Owner OwnerStatus `json:"owner"`
	}
	return out.Owner, c.do(ctx, nil, &out, "owner", "status")
}

// RegisterOwner stores the first owner public key; another one is refused
// (`owner_key_exists`) until a sealed rotation.
func (c *Client) RegisterOwner(ctx context.Context, requestID, publicKeyB64 string) (OwnerStatus, error) {
	var out struct {
		Owner OwnerStatus `json:"owner"`
	}
	in := struct {
		RequestID string `json:"requestId"`
		PublicKey string `json:"publicKey"`
	}{requestID, publicKeyB64}
	return out.Owner, c.do(ctx, in, &out, "owner", "register")
}

// RotateOwner hands the owner key to a new public key with the current key's seal.
func (c *Client) RotateOwner(ctx context.Context, requestID, publicKeyB64, seal string) (OwnerStatus, error) {
	var out struct {
		Owner OwnerStatus `json:"owner"`
	}
	in := struct {
		RequestID string `json:"requestId"`
		PublicKey string `json:"publicKey"`
		Seal      string `json:"seal"`
	}{requestID, publicKeyB64, seal}
	return out.Owner, c.do(ctx, in, &out, "owner", "rotate")
}

// Grants lists an Assistant's grants (no seals) and the grantable capabilities.
func (c *Client) Grants(ctx context.Context, assistantID string) ([]Grant, []string, error) {
	var out struct {
		Grants       []Grant  `json:"grants"`
		Capabilities []string `json:"capabilities"`
	}
	err := c.do(ctx, nil, &out, "grant", "list", "--assistant", assistantID)
	return out.Grants, out.Capabilities, err
}

// CreateGrant sends a grant the owner's client already sealed.
func (c *Client) CreateGrant(ctx context.Context, requestID string, in GrantInput) (Grant, error) {
	var out struct {
		Grant Grant `json:"grant"`
	}
	body := struct {
		RequestID string `json:"requestId"`
		GrantInput
	}{requestID, in}
	return out.Grant, c.do(ctx, body, &out, "grant", "create")
}

// RevokeGrant ends a grant at once.
func (c *Client) RevokeGrant(ctx context.Context, requestID, id string) (Grant, error) {
	var out struct {
		Grant Grant `json:"grant"`
	}
	return out.Grant, c.do(ctx, req{RequestID: requestID}, &out, "grant", "revoke", id)
}
