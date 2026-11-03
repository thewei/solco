package personalagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
)

// SchemaVersion is the wire version of every CLI answer.
const wireSchema = 1

// CodedError is an error with a stable wire code. Callers branch on the code,
// never on the message (which may be shown to a person).
type CodedError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *CodedError) Error() string { return e.Code + ": " + e.Message }

func coded(code, format string, a ...any) *CodedError {
	return &CodedError{Code: code, Message: fmt.Sprintf(format, a...)}
}

// codeFor maps a store error to its stable code.
func codeFor(err error) *CodedError {
	var ce *CodedError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ce):
		return ce
	case errors.Is(err, ErrRequestConflict):
		return coded("request_conflict", "%v", err)
	case errors.Is(err, ErrStaleRevision):
		return coded("stale_revision", "%v", err)
	case errors.Is(err, ErrNotFound):
		return coded("not_found", "%v", err)
	case errors.Is(err, ErrAssistantDisabled):
		return coded("assistant_disabled", "%v", err)
	case errors.Is(err, ErrBusy):
		return coded("busy", "%v", err)
	case errors.Is(err, ErrNotClaimable):
		return coded("not_claimable", "%v", err)
	case errors.Is(err, ErrBadState):
		return coded("bad_state", "%v", err)
	case errors.Is(err, ErrInvalid):
		return coded("invalid", "%v", err)
	case errors.Is(err, ErrUncertain):
		return coded("uncertain", "%v", err)
	}
	return coded("internal", "%v", err)
}

// API is the CLI's one entry point: argv + a JSON body in, a JSON-ready value
// out. Prompts and briefs travel in the body (stdin), never in argv.
type API struct {
	Store *Store
	// Dispatcher is only set where a Launcher exists (the daemon); commands
	// that need Herdr answer `unavailable` without it.
	Dispatcher *Dispatcher
	// Config reads the legacy definitions (default config.Load); LegacyLast
	// reads the old scheduler's watermarks (default: its schedule.json).
	Config     func() (*config.Config, error)
	LegacyLast func() map[string]time.Time
	// Now is a test seam.
	Now func() time.Time
	// Reviewer serves `assistant review|checks`; nil answers `unavailable`.
	Reviewer *Reviewer
}

type envelope map[string]any

func ok(kv ...any) envelope {
	e := envelope{"schema": wireSchema}
	for i := 0; i+1 < len(kv); i += 2 {
		e[kv[i].(string)] = kv[i+1]
	}
	return e
}

// Handle runs one command. args start at the command group ("assistant").
func (a *API) Handle(args []string, body []byte) (any, error) {
	out, err := a.handle(args, body)
	return out, asCoded(err)
}

func asCoded(err error) error {
	if err == nil {
		return nil
	}
	return codeFor(err)
}

type common struct {
	RequestID        string `json:"requestId"`
	ExpectedRevision int    `json:"expectedRevision"`
}

func decode(body []byte, v any) error {
	if len(strings.TrimSpace(string(body))) == 0 {
		return coded("invalid", "a JSON body is required")
	}
	if err := json.Unmarshal(body, v); err != nil {
		return coded("invalid", "body is not valid JSON: %v", err)
	}
	return nil
}

func flagValue(args []string, name string) (string, []string) {
	for i, x := range args {
		if x == name && i+1 < len(args) {
			rest := append(append([]string{}, args[:i]...), args[i+2:]...)
			return args[i+1], rest
		}
	}
	return "", args
}

func (a *API) handle(args []string, body []byte) (any, error) {
	if len(args) < 2 || args[0] != "assistant" {
		return nil, coded("invalid", "usage: assistant <create|list|show|rename|disable|enable|goal|memory|approval|task|run|heartbeat|event|rule|review|checks|overview>")
	}
	cmd, rest := args[1], args[2:]
	switch cmd {
	case "overview":
		return a.overview()
	case "list":
		list, err := a.Store.ListAssistants()
		return ok("assistants", list), err
	case "show":
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant show <id>")
		}
		x, err := a.Store.GetAssistant(rest[0])
		return ok("assistant", x), err
	case "create":
		var in struct {
			common
			CreateAssistantInput
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		x, replayed, err := a.Store.CreateAssistant(in.RequestID, in.CreateAssistantInput)
		return ok("assistant", x, "replayed", replayed), err
	case "rename":
		var in struct {
			common
			Name string `json:"name"`
		}
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant rename <id>")
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		x, replayed, err := a.Store.RenameAssistant(in.RequestID, rest[0], in.Name, in.ExpectedRevision)
		return ok("assistant", x, "replayed", replayed), err
	case "disable", "enable":
		var in common
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant %s <id>", cmd)
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		status := StatusDisabled
		if cmd == "enable" {
			status = StatusActive
		}
		x, replayed, err := a.Store.SetAssistantStatus(in.RequestID, rest[0], status, in.ExpectedRevision)
		return ok("assistant", x, "replayed", replayed), err
	case "goal":
		return a.goal(rest, body)
	case "memory":
		if len(rest) > 0 && (rest[0] == "context" || rest[0] == "search") {
			return a.memoryContext(rest[0], rest[1:])
		}
		return a.memory(rest, body)
	case "heartbeat":
		return a.heartbeat(rest, body)
	case "event":
		return a.events(rest, body)
	case "rule":
		return a.rule(rest, body)
	case "review":
		return a.review(rest, body)
	case "checks":
		return a.checks(rest, body)
	case "approval":
		return a.approval(rest, body)
	case "task":
		return a.task(rest, body)
	case "run":
		return a.run(rest, body)
	}
	return nil, coded("invalid", "unknown assistant command %q", cmd)
}

func (a *API) task(args []string, body []byte) (any, error) {
	if len(args) == 0 {
		return nil, coded("invalid", "usage: assistant task <create|list|show|retry>")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "create":
		var in struct {
			common
			CreateTaskInput
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		x, replayed, err := a.Store.CreateTask(in.RequestID, in.CreateTaskInput)
		return ok("task", x, "replayed", replayed), err
	case "list":
		who, _ := flagValue(rest, "--assistant")
		list, err := a.Store.ListTasks(who)
		return ok("tasks", list), err
	case "show":
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant task show <id>")
		}
		t, err := a.Store.GetTask(rest[0])
		if err != nil {
			return nil, err
		}
		runs, err := a.Store.ListRuns(t.ID)
		return ok("task", t, "runs", runs), err
	case "ack":
		var in common
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant task ack <id>")
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		x, replayed, err := a.Store.AckTask(in.RequestID, rest[0], in.ExpectedRevision)
		return ok("task", x, "replayed", replayed), err
	case "retry":
		var in common
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant task retry <id>")
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		x, replayed, err := a.Store.RetryTask(in.RequestID, rest[0], in.ExpectedRevision)
		return ok("task", x, "replayed", replayed), err
	}
	return nil, coded("invalid", "unknown task command %q", cmd)
}

func (a *API) run(args []string, _ []byte) (any, error) {
	if len(args) == 0 {
		return nil, coded("invalid", "usage: assistant run <list|show|reconcile>")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "list":
		taskID, _ := flagValue(rest, "--task")
		list, err := a.Store.ListRuns(taskID)
		return ok("runs", list), err
	case "show":
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant run show <id>")
		}
		r, err := a.Store.GetRun(rest[0])
		return ok("run", r), err
	}
	return nil, coded("invalid", "unknown run command %q", cmd)
}

// OverviewRow is one Assistant's counters for the sidebar and the phone.
type OverviewRow struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Revision  int    `json:"revision"`
	Tasks     int    `json:"tasks"`
	Queued    int    `json:"queued"`
	Running   int    `json:"running"`
	NeedsYou  int    `json:"needsYou"`
	Done      int    `json:"done"`
	Goals     int    `json:"goals"`
	Approvals int    `json:"approvals"`
}

func (a *API) overview() (any, error) {
	asst, err := a.Store.ListAssistants()
	if err != nil {
		return nil, err
	}
	rows := make([]OverviewRow, 0, len(asst))
	needs := 0
	for _, x := range asst {
		tasks, err := a.Store.ListTasks(x.ID)
		if err != nil {
			return nil, err
		}
		row := OverviewRow{ID: x.ID, Name: x.Name, Status: x.Status, Revision: x.Revision, Tasks: len(tasks)}
		for _, t := range tasks {
			switch t.State {
			case TaskQueued, TaskClaimed:
				row.Queued++
			case TaskStarting, TaskRunning:
				row.Running++
			case TaskDone:
				row.Done++
			}
			if t.NeedsYou {
				row.NeedsYou++
			}
		}
		goals, err := a.Store.ListGoals(x.ID)
		if err != nil {
			return nil, err
		}
		for _, g := range goals {
			if g.State == GoalActive || g.State == GoalPaused || g.State == GoalDraft {
				row.Goals++
			}
		}
		aps, err := a.Store.ListApprovals(x.ID)
		if err != nil {
			return nil, err
		}
		for _, ap := range aps {
			if ap.State == ApprovalPending {
				row.Approvals++
			}
		}
		needs += row.NeedsYou + row.Approvals
		rows = append(rows, row)
	}
	return ok("assistants", rows, "needsYou", needs), nil
}
