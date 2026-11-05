package personalagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
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
	case errors.Is(err, ErrReplanLimit):
		return coded("replan_limit", "%v", err)
	case errors.Is(err, ErrBadState):
		return coded("bad_state", "%v", err)
	case errors.Is(err, ErrHookRejected):
		return coded("unauthorized", "%v", err)
	case errors.Is(err, ErrRateLimited):
		return coded("rate_limited", "%v", err)
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
	// PaneGone is a read-only Herdr check for irreversible Assistant deletion.
	// Without it a recorded pane must block deletion, not be assumed gone.
	PaneGone func(session, workspace, pane string) (bool, error)
	// PaneClose ends one Herdr pane Solco launched (a stopped Coordinator's).
	// Nil leaves the pane to the person; Stop then says `not_attempted`.
	PaneClose func(session, workspace, pane string) error
	// Config reads the legacy definitions (default config.Load); LegacyLast
	// reads the old scheduler's watermarks (default: its schedule.json).
	Config     func() (*config.Config, error)
	LegacyLast func() map[string]time.Time
	// Now is a test seam.
	Now func() time.Time
	// Routine commands edit automations.yaml through these (defaults: the config package).
	ConfigPut     func(original string, a config.Automation) (config.PutResult, error)
	ConfigRemove  func(name string) error
	ConfigPause   func(name string, paused bool) error
	ConfigApprove func(name string) error
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
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields() // a typo'd field is refused, never silently dropped
	if err := dec.Decode(v); err != nil {
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
		return nil, coded("invalid", "usage: assistant <create|list|show|rename|disable|enable|goal|memory|approval|task|run|heartbeat|event|rule|review|checks|inbox|overview|activity|report|group|persona|routine|proactivity|hook|grant|legacy-export>")
	}
	for _, x := range args {
		if x == "--as" {
			who, scopedArgs := flagValue(args, "--as")
			return a.scoped(who, scopedArgs, body)
		}
	}
	cmd, rest := args[1], args[2:]
	switch cmd {
	case "trace":
		if len(rest) != 2 || rest[0] != "show" {
			return nil, coded("invalid", "usage: assistant trace show <taskId|goalId>")
		}
		tr, err := a.Store.Trace(rest[1])
		return ok("trace", tr), err
	case "recovery":
		if len(rest) != 2 || rest[0] != "show" {
			return nil, coded("invalid", "usage: assistant recovery show <assistantId>")
		}
		d, err := a.Store.RecoveryDigest(rest[1])
		return ok("recovery", d), err
	case "inbox":
		who, rest := flagValue(rest, "--assistant")
		opt := InboxOptions{}
		if v, _ := flagValue(rest, "--since"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				return nil, coded("invalid", "--since must be an event watermark")
			}
			opt.Since, opt.News = n, true
		}
		in, err := a.Store.Inbox(who, opt)
		return ok("inbox", in), err
	case "overview":
		return a.overview()
	case "activity":
		return a.activity(rest, body)
	case "report":
		return a.report(rest, body)
	case "legacy-export":
		// Explicit, read-only: copies the retired herdr-projects data aside; nothing is ever removed from it.
		dst, rest := flagValue(rest, "--to")
		src, _ := flagValue(rest, "--from")
		if dst == "" {
			dst = filepath.Join(WorkDirRoot(), "legacy-export")
		}
		if src == "" {
			src = LegacyHome()
		}
		res, err := ExportLegacy(src, dst, time.Now())
		return ok("export", res), err
	case "archived":
		list, err := a.Store.ListArchivedAssistants()
		return ok("assistants", list), err
	case "archive", "restore", "delete-forever":
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant %s <id>", cmd)
		}
		var in common
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		switch cmd {
		case "archive":
			x, replayed, err := a.Store.ArchiveAssistant(in.RequestID, rest[0], in.ExpectedRevision, a.PaneGone)
			return ok("assistant", x, "replayed", replayed), err
		case "restore":
			x, replayed, err := a.Store.RestoreAssistant(in.RequestID, rest[0], in.ExpectedRevision)
			return ok("assistant", x, "replayed", replayed), err
		default:
			err := a.Store.DeleteArchivedAssistant(rest[0], in.ExpectedRevision, a.PaneGone)
			return ok("deleted", err == nil), err
		}
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
	case "facts":
		if len(rest) < 1 {
			return nil, coded("invalid", "usage: assistant facts <assistantId> [--limit <n>]")
		}
		limitS, _ := flagValue(rest[1:], "--limit")
		limit, _ := strconv.Atoi(limitS)
		fs, err := a.Store.Facts(rest[0], "", limit)
		return ok("facts", fs), err
	case "memory":
		if len(rest) > 0 && (rest[0] == "context" || rest[0] == "search") {
			return a.memoryContext(rest[0], rest[1:])
		}
		return a.memory(rest, body)
	case "checkpoint":
		return a.checkpoint(rest, body)
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
	case "coordinator":
		return a.coordinator(rest, body)
	case "group":
		return a.group(rest, body)
	case "persona":
		return a.persona(rest, body)
	case "routine":
		return a.routine(rest, body)
	case "proactivity":
		return a.proactivity(rest, body)
	case "trigger":
		return a.trigger(rest, body)
	case "hook":
		return a.hook(rest, body)
	case "grant":
		return a.grant(rest, body)
	case "run":
		return a.run(rest, body)
	}
	return nil, coded("invalid", "unknown assistant command %q", cmd)
}

func (a *API) task(args []string, body []byte) (any, error) {
	if len(args) == 0 {
		return nil, coded("invalid", "usage: assistant task <create|list|show|ack|retry|publish|deps|graph>")
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
		if in.Agent != "" && in.Agent != AssistantAgentKind {
			return nil, fmt.Errorf("%w: Assistants can only dispatch omp tasks", ErrInvalid)
		}
		in.Agent = AssistantAgentKind
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
	case "why":
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant task why <id>")
		}
		w, err := a.Store.TaskWhy(rest[0])
		return ok("why", w), err
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
	case "publish":
		var in common
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant task publish <id>")
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		x, replayed, err := a.Store.PublishTask(in.RequestID, rest[0], in.ExpectedRevision)
		return ok("task", x, "replayed", replayed), err
	case "deps":
		var in struct {
			common
			DependsOn []string `json:"dependsOn"`
		}
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant task deps <id>")
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		x, replayed, err := a.Store.AddDependencies(in.RequestID, rest[0], in.DependsOn, in.ExpectedRevision)
		return ok("task", x, "replayed", replayed), err
	case "graph":
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant task graph <assistantId>")
		}
		nodes, err := a.Store.TaskGraph(rest[0])
		return ok("nodes", nodes), err
	}
	return nil, coded("invalid", "unknown task command %q", cmd)
}

func (a *API) coordinator(args []string, body []byte) (any, error) {
	if len(args) < 2 {
		return nil, coded("invalid", "usage: assistant coordinator <show|start|stop> <assistantId>")
	}
	cmd, id := args[0], args[1]
	var in struct {
		common
		Agent string `json:"agent"`
		Model string `json:"model"`
	}
	switch cmd {
	case "show":
		c, err := a.Store.GetCoordinator(id)
		return ok("coordinator", c), err
	case "start", "stop":
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		var c Coordinator
		var replayed bool
		var err error
		if cmd == "start" {
			c, replayed, err = a.Store.StartCoordinatorAs(in.RequestID, id, in.Agent, in.Model, a.PaneGone)
			return ok("coordinator", c, "replayed", replayed), err
		}
		before, _ := a.Store.GetCoordinator(id)
		c, replayed, err = a.Store.StopCoordinator(in.RequestID, id)
		pane := ""
		if err == nil && !replayed && before.State != CoordinatorNone && before.State != CoordinatorStopped && before.PaneID != "" {
			pane = a.closeCoordinatorPane(before)
		}
		return ok("coordinator", c, "replayed", replayed, "pane", pane), err
	}
	return nil, coded("invalid", "unknown coordinator command %q", cmd)
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
	// UpdatedAt is the Assistant's last activity (itself or any of its tasks), epoch ms: the order of the mixed Chats list.
	UpdatedAt int64 `json:"updatedAt"`
}

// OverviewGroup is one Group's light row for the sidebar and the phone: who is
// in it, how far its log has run and how many @-mentions are still on their way.
type OverviewGroup struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Members   []GroupMember `json:"members"`
	LastSeq   int64         `json:"lastSeq"`
	Open      int           `json:"open"`
	UpdatedAt int64         `json:"updatedAt"`
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
		row := OverviewRow{ID: x.ID, Name: x.Name, Status: x.Status, Revision: x.Revision, UpdatedAt: x.UpdatedAt}
		for _, t := range tasks {
			if t.UpdatedAt > row.UpdatedAt {
				row.UpdatedAt = t.UpdatedAt
			}
			// Coordinator and Group Agent panes are infrastructure,
			// not user-visible worker tasks. Count the work, not the chat.
			if t.Kind == KindCoordinator || t.Kind == KindGroupAgent {
				continue
			}
			row.Tasks++
			switch t.State {
			case TaskQueued, TaskClaimed, TaskWaiting:
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
	groups, err := a.Store.ListGroups()
	if err != nil {
		return nil, err
	}
	grows := make([]OverviewGroup, 0, len(groups))
	for _, g := range groups {
		var open int
		if err := a.Store.db.QueryRow(`SELECT COUNT(*) FROM group_deliveries WHERE group_id=? AND state IN (?,?)`, g.ID, DeliveryQueued, DeliveryDelivering).Scan(&open); err != nil {
			return nil, wrapBusy(err)
		}
		grows = append(grows, OverviewGroup{ID: g.ID, Title: g.Title, Members: g.Members, LastSeq: g.LastSeq, Open: open, UpdatedAt: g.UpdatedAt})
	}
	// A completed step may add a Fact without altering Task/Goal counters.
	// Expose only the global watermark, never the Fact text, so the desktop
	// can nudge phones to re-read the authoritative activity feed.
	var activitySeq int64
	if err := a.Store.db.QueryRow("SELECT COALESCE(MAX(seq),0) FROM goal_facts").Scan(&activitySeq); err != nil {
		return nil, wrapBusy(err)
	}
	return ok("assistants", rows, "needsYou", needs, "groups", grows, "activitySeq", activitySeq), nil
}

// checkpoint: `assistant checkpoint get <assistantId>` and `assistant checkpoint set`.
func (a *API) checkpoint(args []string, body []byte) (any, error) {
	if len(args) == 0 {
		return nil, coded("invalid", "usage: assistant checkpoint <get|set>")
	}
	switch args[0] {
	case "get":
		if len(args) != 2 {
			return nil, coded("invalid", "usage: assistant checkpoint get <assistantId>")
		}
		cp, err := a.Store.GetCheckpoint(args[1])
		return ok("checkpoint", cp), err
	case "set":
		var in struct {
			common
			CheckpointInput
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		cp, replayed, err := a.Store.SetCheckpoint(in.RequestID, in.CheckpointInput, in.ExpectedRevision)
		return ok("checkpoint", cp, "replayed", replayed), err
	}
	return nil, coded("invalid", "unknown checkpoint command %q", args[0])
}

// closeCoordinatorPane ends the pane Herdr reported for a Coordinator that was
// just stopped, and checks it is gone. The answer is a fixed word: closed,
// close_failed (the person closes it; restart stays refused until Herdr shows
// it gone) or not_attempted (no closer wired).
func (a *API) closeCoordinatorPane(c Coordinator) string {
	if a.PaneClose == nil {
		return "not_attempted"
	}
	if err := a.PaneClose(c.HerdrSession, c.WorkspaceID, c.PaneID); err != nil {
		return "close_failed"
	}
	return "closed"
}
