package mcpshim

/**
 * [INPUT]: internal/feedback 的 Client/Run、官方 MCP Go SDK（stdio）
 * [OUTPUT]: 对外提供 Run（stdio MCP 服务器）、NewServer（可测试的装配）；工具 interactive_feedback / interactive_feedback_start / interactive_feedback_wait / recall_decisions
 * [POS]: 外部 Agent 侧的入口（2026-11-09 决策，取代 mcp-feedback 的 Bun stdio 进程）：`solco-hostd feedback-mcp` 就是它；
 *        工具名与入参向后兼容旧服务（新增可选 thread_id），所以已配置的 Agent 只需改一行命令；
 *        shim 自己也参与 broker 所有权（待命循环），所以没有 Solco/hostd 时 Agent 照样能提问，换主期间轮询按 ErrOffline 重试而不是报错
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thewei/solco/internal/feedback"
)

const (
	pollEvery     = 2 * time.Second
	progressEvery = 10 * time.Second
	offlineGrace  = 90 * time.Second
	maxWaitSec    = 300
)

// instructions is the usage rule sent in initialize; clients put it in the Agent's system prompt, so the blocking
// contract holds even when the Agent never reads a tool description.
const instructions = `Solco Feedback is the user's question inbox. To ask the user anything, call interactive_feedback and keep that ONE call open until it returns: the user may need minutes or hours, so do not poll, do not shorten timeout_seconds, do not give up and never continue on an assumed answer (only status "answered" is an answer). Always pass description (Markdown: what you did, what you ask) and project_root. If your client cuts the call short, ask again with interactive_feedback_start and loop interactive_feedback_wait. Use recall_decisions before asking something the user may already have decided.`

var closedWorld = false

// askNotes: asking the user changes nothing outside Solco's own inbox.
func askNotes(title string) *mcp.ToolAnnotations {
	destructive := false
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: &destructive, OpenWorldHint: &closedWorld}
}

type questionArg struct {
	ID          string   `json:"id" jsonschema:"Unique identifier for this question within the session"`
	Text        string   `json:"text" jsonschema:"The question text shown to the user"`
	Type        string   `json:"type" jsonschema:"One of single (radio buttons), multiple (checkboxes) or text (textarea)"`
	Options     []string `json:"options,omitempty" jsonschema:"Options for single/multiple type. A free-text input is always shown alongside."`
	Required    *bool    `json:"required,omitempty" jsonschema:"Whether the user must answer this question (default true)"`
	Placeholder string   `json:"placeholder,omitempty" jsonschema:"Placeholder text for the free-text input field"`
}

type askArgs struct {
	CallerInfo     string        `json:"caller_info" jsonschema:"Who is asking, e.g. 'Claude Code - main task' or 'Cursor Agent'"`
	Description    string        `json:"description,omitempty" jsonschema:"Markdown context shown above the questions. Summarize what you did and what the question is about."`
	Questions      []questionArg `json:"questions" jsonschema:"1 to 20 questions to present"`
	TimeoutSeconds float64       `json:"timeout_seconds,omitempty" jsonschema:"Wait time in seconds. Default 14400 (4h), max 86400 (24h). Do not override unless the user asked for it."`
	TaskID         string        `json:"task_id,omitempty" jsonschema:"Optional. A stable id for the task; asks with the same task_id and project share one thread in Solco."`
	ThreadID       string        `json:"thread_id,omitempty" jsonschema:"Optional. Asks with the same thread_id share one thread in Solco, across clients and time. Without it Solco groups by project, client and a 60 minute gap."`
	ProjectRoot    string        `json:"project_root,omitempty" jsonschema:"Absolute path of the current workspace/project root. Always pass this so asks attach to the right project."`
}

type waitArgs struct {
	SessionID   string  `json:"session_id" jsonschema:"Session id returned by interactive_feedback_start"`
	WaitSeconds float64 `json:"wait_seconds,omitempty" jsonschema:"How long this call waits before returning pending. Default 300, max 300."`
}

type recallArgs struct {
	ProjectRoot string  `json:"project_root,omitempty" jsonschema:"Absolute path of the project; empty searches every project"`
	Query       string  `json:"query,omitempty" jsonschema:"Case-insensitive text to look for in earlier questions and answers; empty returns the latest"`
	Limit       float64 `json:"limit,omitempty" jsonschema:"How many earlier answers to return (default 10, max 50)"`
}

// Server is the stdio MCP server in front of a broker Client.
type Server struct {
	c       *feedback.Client
	version string
}

// NewServer builds the MCP server around c.
func NewServer(c *feedback.Client, version string) *mcp.Server {
	s := &Server{c: c, version: version}
	srv := mcp.NewServer(&mcp.Implementation{Name: "solco-feedback", Version: version}, &mcp.ServerOptions{Instructions: instructions})
	mcp.AddTool(srv, &mcp.Tool{
		Name: "interactive_feedback",
		Description: `Ask the user questions through Solco and BLOCK until they answer or the timeout passes.

The preferred way to ask the user anything: it creates a question, notifies the user (Solco, phone, Feishu) and waits for the answer, sending keep-alive progress every 10 seconds. The user is a person who may need minutes or hours to notice, read and answer, so keep this ONE call open until it returns: do not poll, do not shorten timeout_seconds (the default is 4 hours on purpose), do not give up, and never carry on with an assumed answer. Only status "answered" is an answer; on "timeout" ask again unless "ended_by_user" is true.

Always pass "description" (what you did, what the question is about) and "project_root". Use caller_info to identify yourself. Questions from the same project and client are filed in one thread automatically; pass thread_id or task_id to group them yourself.

If your client cuts the call short (a tool timeout such as Codex's default 60 s tool_timeout_sec, or a cancellation), that is not an answer: ask again with interactive_feedback_start and loop interactive_feedback_wait. If the result contains "late_answers", the user answered earlier questions after they timed out: take them into account.`,
		Annotations: askNotes("Ask the user (blocking)"),
	}, s.ask(true))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "interactive_feedback_start",
		Description: `Start a question and return IMMEDIATELY with a session_id. Use only when interactive_feedback is cut short by your client or your client has strict per-call time limits. Then loop interactive_feedback_wait with the session_id until the status is no longer "pending": the user may need hours, so never stop waiting and never assume an answer.`,
		Annotations: askNotes("Ask the user (start)"),
	}, s.ask(false))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "interactive_feedback_wait",
		Description: `Poll a question started with interactive_feedback_start. Each call waits up to wait_seconds (default and max 300; set it below your client's per-call limit, e.g. 45 for a 60 s limit) and returns "pending" if the user has not answered yet; call it again immediately. Stop when the status is "answered", "timeout", "ended" or "not_found".`,
		Annotations: askNotes("Wait for the user's answer"),
	}, s.wait)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "recall_decisions",
		Description: `Look up how the user answered earlier questions in a project, so you do not ask the same thing twice. Read-only: returns question and answer text, never attachments.`,
		Annotations: &mcp.ToolAnnotations{Title: "Recall earlier decisions", ReadOnlyHint: true, OpenWorldHint: &closedWorld},
	}, s.recall)
	return srv
}

func textResult(v any, isErr bool, imgs []feedback.Attachment) *mcp.CallToolResult {
	b, _ := json.MarshalIndent(v, "", "  ")
	res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, IsError: isErr}
	for _, a := range imgs {
		if raw, err := base64.StdEncoding.DecodeString(a.DataBase64); err == nil {
			res.Content = append(res.Content, &mcp.ImageContent{Data: raw, MIMEType: a.MimeType})
		}
	}
	return res
}

func clientName(req *mcp.CallToolRequest) (name, version string) {
	if req != nil && req.Session != nil {
		if p := req.Session.InitializeParams(); p != nil && p.ClientInfo != nil {
			return p.ClientInfo.Name, p.ClientInfo.Version
		}
	}
	return "", ""
}

func toQuestions(in []questionArg) []feedback.Question {
	out := make([]feedback.Question, 0, len(in))
	for _, q := range in {
		req := true
		if q.Required != nil {
			req = *q.Required
		}
		out = append(out, feedback.Question{ID: q.ID, Text: q.Text, Type: q.Type, Options: q.Options, Required: req, Placeholder: q.Placeholder})
	}
	return out
}

// awaitBroker waits until a broker answers; the shim's own standby loop may be the one that has to start it.
func (s *Server) awaitBroker(ctx context.Context) error {
	deadline := time.Now().Add(20 * time.Second)
	for {
		if err := s.c.Health(ctx); err == nil {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return feedback.ErrOffline
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (s *Server) create(ctx context.Context, req *mcp.CallToolRequest, a askArgs) (id string, timeout time.Duration, err error) {
	if err := s.awaitBroker(ctx); err != nil {
		return "", 0, errors.New("Solco feedback broker is not reachable")
	}
	timeout = 4 * time.Hour
	if a.TimeoutSeconds > 0 {
		timeout = time.Duration(a.TimeoutSeconds * float64(time.Second))
	}
	if timeout < time.Minute {
		timeout = time.Minute
	}
	if timeout > 24*time.Hour {
		timeout = 24 * time.Hour
	}
	client, cv := clientName(req)
	id, _, err = s.c.Create(ctx, feedback.CreateInput{
		CallerInfo: a.CallerInfo, Client: client, ClientVersion: cv, Description: a.Description,
		ProjectRoot: a.ProjectRoot, TaskID: a.TaskID, ThreadID: a.ThreadID,
		Questions: toQuestions(a.Questions), TimeoutMs: timeout.Milliseconds(),
	})
	if err != nil {
		return "", 0, fmt.Errorf("could not register the question: %w", err)
	}
	return id, timeout, nil
}

func (s *Server) ask(block bool) func(context.Context, *mcp.CallToolRequest, askArgs) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, a askArgs) (*mcp.CallToolResult, any, error) {
		id, timeout, err := s.create(ctx, req, a)
		if err != nil {
			return textResult(map[string]string{"status": "error", "message": err.Error()}, true, nil), nil, nil
		}
		if !block {
			return textResult(map[string]any{"status": "pending", "message": "Session created",
				"data": map[string]any{"session_id": id, "timeout_seconds": int(timeout.Seconds())}}, false, nil), nil, nil
		}
		res := s.poll(ctx, req, id, timeout)
		return res, nil, nil
	}
}

func (s *Server) wait(ctx context.Context, _ *mcp.CallToolRequest, a waitArgs) (*mcp.CallToolResult, any, error) {
	w := time.Duration(maxWaitSec) * time.Second
	if a.WaitSeconds > 0 && a.WaitSeconds < maxWaitSec {
		w = time.Duration(a.WaitSeconds * float64(time.Second))
	}
	deadline := time.Now().Add(w)
	offlineSince := time.Time{}
	for {
		r, err := s.c.Result(ctx, a.SessionID)
		switch {
		case err == nil:
			offlineSince = time.Time{}
			if r.Status != "pending" {
				return resultToTool(r), nil, nil
			}
		case errors.Is(err, feedback.ErrOffline):
			if offlineSince.IsZero() {
				offlineSince = time.Now()
			} else if time.Since(offlineSince) > offlineGrace {
				return textResult(map[string]string{"status": "error", "message": "Solco feedback broker is not reachable"}, true, nil), nil, nil
			}
		default:
			return textResult(map[string]string{"status": "error", "message": "Failed to read session status"}, true, nil), nil, nil
		}
		if time.Now().After(deadline) {
			return textResult(map[string]any{"status": "pending", "message": "Still waiting for user response",
				"data": map[string]any{"session_id": a.SessionID}}, false, nil), nil, nil
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(pollEvery):
		}
	}
}

func (s *Server) poll(ctx context.Context, req *mcp.CallToolRequest, id string, timeout time.Duration) *mcp.CallToolResult {
	started := time.Now()
	deadline := started.Add(timeout + 30*time.Second)
	var offlineSince time.Time
	lastProgress := started
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			// The Agent gave up on this call: the question stops being pending, but the user can still answer it;
			// the answer reaches the Agent as a late answer with its next question.
			ectx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = s.c.Abandon(ectx, id)
			cancel()
			return textResult(map[string]string{"status": "error", "message": "cancelled"}, true, nil)
		case <-time.After(pollEvery):
		}
		r, err := s.c.Result(ctx, id)
		switch {
		case err == nil:
			offlineSince = time.Time{}
			if r.Status != "pending" {
				return resultToTool(r)
			}
		case errors.Is(err, feedback.ErrOffline):
			if offlineSince.IsZero() {
				offlineSince = time.Now()
			} else if time.Since(offlineSince) > offlineGrace {
				return lostSession(id, "Solco feedback broker is not reachable")
			}
		default:
			return lostSession(id, "Failed to read session status")
		}
		if req != nil && req.Session != nil && time.Since(lastProgress) >= progressEvery {
			lastProgress = time.Now()
			if tok := req.Params.GetProgressToken(); tok != nil {
				_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
					ProgressToken: tok, Progress: time.Since(started).Seconds(), Total: timeout.Seconds()})
			}
		}
	}
	return textResult(map[string]string{"status": "timeout", "message": "User did not respond in time"}, false, nil)
}

// lostSession is an error that keeps the Ask's id: the question may still be open, so the Agent resumes it with
// interactive_feedback_wait instead of asking the user the same thing again.
func lostSession(id, message string) *mcp.CallToolResult {
	return textResult(map[string]string{"status": "error", "message": message, "session_id": id,
		"next": "The question may still be open: call interactive_feedback_wait with this session_id instead of asking again."}, true, nil)
}

type lateOut struct {
	AskedAt   string                     `json:"asked_at"`
	Questions []string                   `json:"questions"`
	Answers   map[string]feedback.Answer `json:"answers"`
}

func resultToTool(r feedback.AskResult) *mcp.CallToolResult {
	out := map[string]any{}
	switch r.Status {
	case "answered":
		out["status"] = "answered"
		out["answers"] = r.Answers
	case "not_found":
		out["status"] = "not_found"
		out["message"] = r.Message
	default: // expired, ended
		out["status"] = "timeout"
		if r.Message != "" {
			out["message"] = r.Message
		} else {
			out["message"] = "User did not respond in time"
		}
		if r.Status == "ended" {
			out["ended_by_user"] = true
		}
	}
	if len(r.LateAnswers) > 0 {
		late := make([]lateOut, 0, len(r.LateAnswers))
		for _, l := range r.LateAnswers {
			qs := make([]string, 0, len(l.Questions))
			for _, q := range l.Questions {
				qs = append(qs, q.Text)
			}
			late = append(late, lateOut{AskedAt: time.UnixMilli(l.AskedAt).UTC().Format(time.RFC3339), Questions: qs, Answers: l.Answers})
		}
		out["late_answers"] = late
	}
	return textResult(out, false, r.Attachments)
}

func (s *Server) recall(ctx context.Context, _ *mcp.CallToolRequest, a recallArgs) (*mcp.CallToolResult, any, error) {
	if err := s.awaitBroker(ctx); err != nil {
		return textResult(map[string]string{"status": "error", "message": "Solco feedback broker is not reachable"}, true, nil), nil, nil
	}
	items, err := s.c.Recall(ctx, strings.TrimSpace(a.ProjectRoot), a.Query, int(a.Limit))
	if err != nil {
		return textResult(map[string]string{"status": "error", "message": err.Error()}, true, nil), nil, nil
	}
	type pair struct {
		Question string `json:"question"`
		Answer   string `json:"answer"`
	}
	type item struct {
		AskedAt string `json:"asked_at"`
		Project string `json:"project"`
		Thread  string `json:"thread,omitempty"`
		Pairs   []pair `json:"pairs"`
	}
	out := make([]item, 0, len(items))
	for _, it := range items {
		x := item{AskedAt: time.UnixMilli(it.AskedAt).UTC().Format(time.RFC3339), Project: it.Project, Thread: it.Title}
		for _, p := range it.Pairs {
			x.Pairs = append(x.Pairs, pair{Question: p.Question, Answer: p.Answer})
		}
		out = append(out, x)
	}
	return textResult(map[string]any{"status": "ok", "count": len(out), "items": out}, false, nil), nil, nil
}

// Run serves MCP over stdio while taking part in broker ownership, until the client disconnects or ctx ends.
func Run(ctx context.Context, dir, version string, notifier feedback.Notifier) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_ = feedback.Run(ctx, feedback.Options{Dir: dir, Version: version, Notifier: notifier, ImportLegacy: feedback.LegacyDecisionsPath()})
	}()
	return NewServer(feedback.NewClient(dir), version).Run(ctx, &mcp.StdioTransport{})
}
