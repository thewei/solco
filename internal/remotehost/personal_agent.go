package remotehost

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/thewei/solco/internal/personalagent"
)

/**
 * [INPUT]: 依赖 Options.PersonalAgent（桌面 Assistants 核心的 CLI 客户端缝，nativeui 提供；门关返回 ok=false）、internal/personalagent 的类型与稳定错误码、server.go 的 guard/writeJSON/writeError
 * [OUTPUT]: 对外提供 PersonalAgentFunc、PersonalAssistantDetail/PersonalTask，
 *           GET /api/v3/personal-agent/overview|assistants|assistants/{id}|goals|tasks/{id}|approvals|inbox，
 *           POST …/assistants（建）、…/assistants/{id}/rename|pause|resume、…/goals/draft、…/goals/{id}/approve|pause|resume|cancel、
 *           …/approvals/{id}/decision、…/tasks/{id}/ack|retry|publish，bootstrap 的 capabilities.personalAgent/personalAgentManage/personalAgentApprove，
 *           Server.NotifyPersonalAgent（events 帧 `personal-agent.changed`，只带修订号与计数）
 * [POS]: 手机 Assistants 的 Host 面（2026-11-03 决策，B4）：权威是插件 daemon 的 SQLite 核心，Host 只校验请求形状并经桌面同一个 CLI 客户端转发，不缓存、不重算状态；
 *        每个写请求必带 requestId（重放得原收据），带 expectedRevision（过期 409 stale_revision），审批必带 actionHash；
 *        插件的稳定拒绝码原样映射成 HTTP 状态，message 只用插件自己的句子（有界）；日志只记设备、操作名与结果码——
 *        名字、目标、简报、记忆、审批动作文本永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	maxPABody        = 32 << 10
	maxPAName        = 80
	maxPAShort       = 2 << 10
	maxPAText        = 8 << 10
	maxPAMemoryText  = 16 << 10 // one memory file written from the phone (the core's own cap is 64 KiB)
	maxPARequestID   = 128
	maxPACommitments = 4
	maxPATasksDetail = 30 // task details (with runs) one Assistant page reads
	// Command receipts preserve a retry across a phone disconnect. A hung
	// plugin CLI must nevertheless release Host goroutines and subprocesses.
	maxPAWriteDuration = 2 * time.Minute
)

var paIDRe = regexp.MustCompile(`^[a-z]_[A-Za-z0-9]{1,40}$`)

// PersonalAgentFunc hands the handler the desktop's Assistants client. ok is
// false while the gate is closed (no plugin, an older plugin, a remote Herdr
// instance on the Mac): the capability is then absent and every route is 404.
type PersonalAgentFunc func(ctx context.Context) (*personalagent.Client, bool)

// PersonalTask is a Task with the Runs the phone shows beneath it. Only open
// tasks (and the ones that need you) carry runs on the Assistant page; the
// Task route carries them always.
type PersonalTask struct {
	personalagent.Task
	Runs []personalagent.Run `json:"runs"`
}

// PersonalAgentKind is one Agent a Coordinator may run on: the Herdr kind id the
// plugin takes and the label the desktop shows.
type PersonalAgentKind struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// PersonalAssistantDetail is one Assistant's page: its Coordinator (the
// conversation), goals, tasks and approvals, isolated per Assistant by the
// core. Memory has its own read (GET …/memory). Agents is what Start conversation may choose from.
type PersonalAssistantDetail struct {
	Assistant   personalagent.Assistant   `json:"assistant"`
	Coordinator personalagent.Coordinator `json:"coordinator"`
	Agents      []PersonalAgentKind       `json:"agents"`
	Goals       []personalagent.Goal      `json:"goals"`
	Tasks       []PersonalTask            `json:"tasks"`
	Approvals   []personalagent.Approval  `json:"approvals"`
	// Results are the tasks that settled in the last day (the core's own bounded
	// `assistant results`): the phone's result cards, one projection for both clients.
	Results []personalagent.Result `json:"results"`
}

func (s *Server) paSeam(w http.ResponseWriter, r *http.Request) *personalagent.Client {
	if s.opts.PersonalAgent != nil {
		if c, ok := s.opts.PersonalAgent(r.Context()); ok && c != nil {
			return c
		}
	}
	writeError(w, http.StatusNotFound, "personal_agent_unavailable", "")
	return nil
}

// paRefuse maps the core's stable codes to HTTP; anything else is a 502 with
// no detail.
func (s *Server) paRefuse(w http.ResponseWriter, dev Device, op string, err error) {
	var pe *personalagent.Error
	if !errors.As(err, &pe) {
		// One vocabulary with the desktop bridge (Gap L06): the phone learns
		// whether the core was slow, down, or broken — never the CLI's own text.
		switch personalagent.Classify(err) {
		case personalagent.FailTimeout:
			slog.Warn("remote host personal agent timed out", "device", dev.ID, "op", op)
			writeError(w, http.StatusGatewayTimeout, "personal_agent_timeout", "The Assistants service did not answer in time")
		case personalagent.FailUnavailable:
			slog.Warn("remote host personal agent offline", "device", dev.ID, "op", op)
			writeError(w, http.StatusServiceUnavailable, "personal_agent_offline", "The Assistants service is not running")
		default:
			slog.Warn("remote host personal agent failed", "device", dev.ID, "op", op)
			writeError(w, http.StatusBadGateway, "personal_agent_failed", "")
		}
		return
	}
	status := http.StatusConflict
	switch pe.Code {
	case "not_found":
		status = http.StatusNotFound
	case "invalid":
		status = http.StatusBadRequest
	case "internal":
		status = http.StatusBadGateway
	}
	slog.Warn("remote host personal agent refused", "device", dev.ID, "op", op, "code", pe.Code)
	writeError(w, status, pe.Code, truncateUTF8(pe.Message, 240))
}

func paID(w http.ResponseWriter, id string) (string, bool) {
	if !paIDRe.MatchString(id) {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return "", false
	}
	return id, true
}

func paText(v string, limit int) bool {
	return len(v) <= limit && utf8.ValidString(v) && !strings.ContainsRune(v, 0)
}

// paDecode decodes one bounded JSON body.
func paDecode(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPABody)).Decode(into); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return false
	}
	return true
}

// paBody decodes one bounded JSON body and checks its requestId.
func paBody(w http.ResponseWriter, r *http.Request, into any, requestID func() string) bool {
	if !paDecode(w, r, into) {
		return false
	}
	if id := requestID(); id == "" || len(id) > maxPARequestID || !paText(id, maxPARequestID) || strings.ContainsAny(id, "\r\n") {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return false
	}
	return true
}

// --- reads ----------------------------------------------------------------

func (s *Server) handlePAOverview(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	out, err := c.Overview(r.Context())
	if err != nil {
		s.paRefuse(w, dev, "overview", err)
		return
	}
	if out.Assistants == nil {
		out.Assistants = []personalagent.OverviewRow{}
	}
	if out.Groups == nil {
		out.Groups = []personalagent.OverviewGroup{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePAAssistants(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	list, err := c.Assistants(r.Context())
	if err != nil {
		s.paRefuse(w, dev, "assistants", err)
		return
	}
	if list == nil {
		list = []personalagent.Assistant{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": 1, "assistants": list})
}

func (s *Server) handlePAAssistant(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	ctx := r.Context()
	list, err := c.Assistants(ctx)
	if err != nil {
		s.paRefuse(w, dev, "assistant", err)
		return
	}
	d := PersonalAssistantDetail{Agents: s.paAgents(), Goals: []personalagent.Goal{}, Tasks: []PersonalTask{}, Approvals: []personalagent.Approval{}, Results: []personalagent.Result{}}
	found := false
	for _, a := range list {
		if a.ID == id {
			d.Assistant, found = a, true
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	// A Coordinator that cannot be read is "none": the page then offers Start.
	d.Coordinator = personalagent.Coordinator{AssistantID: id, State: personalagent.CoordinatorNone}
	if co, err := c.Coordinator(ctx, id); err == nil {
		d.Coordinator = co
	}
	if goals, err := c.Goals(ctx, id); err != nil {
		s.paRefuse(w, dev, "assistant", err)
		return
	} else if goals != nil {
		d.Goals = goals
	}
	if approvals, err := c.Approvals(ctx, id); err != nil {
		s.paRefuse(w, dev, "assistant", err)
		return
	} else if approvals != nil {
		d.Approvals = approvals
	}
	// A plugin that predates the command only leaves the cards out.
	if results, err := c.Results(ctx, id, 4); err == nil && results != nil {
		d.Results = results
	}
	tasks, err := c.Tasks(ctx, id)
	if err != nil {
		s.paRefuse(w, dev, "assistant", err)
		return
	}
	reads := 0
	for _, t := range tasks {
		item := PersonalTask{Task: t, Runs: []personalagent.Run{}}
		if (t.NeedsYou || paOpenTask(t.State)) && reads < maxPATasksDetail {
			reads++
			if _, runs, err := c.TaskDetail(ctx, t.ID); err == nil && runs != nil {
				item.Runs = runs
			}
		}
		d.Tasks = append(d.Tasks, item)
	}
	slog.Info("remote host personal agent assistant", "device", dev.ID, "goals", len(d.Goals), "tasks", len(d.Tasks))
	writeJSON(w, http.StatusOK, d)
}

func paOpenTask(state string) bool {
	switch state {
	case "draft", "queued", "claimed", "starting", "running", "blocked", "review_required", "uncertain":
		return true
	}
	return false
}

func (s *Server) handlePAGoals(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	id, ok := paID(w, r.URL.Query().Get("assistant"))
	if !ok {
		return
	}
	goals, err := c.Goals(r.Context(), id)
	if err != nil {
		s.paRefuse(w, dev, "goals", err)
		return
	}
	if goals == nil {
		goals = []personalagent.Goal{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": 1, "goals": goals})
}

func (s *Server) handlePATask(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	task, runs, err := c.TaskDetail(r.Context(), id)
	if err != nil {
		s.paRefuse(w, dev, "task", err)
		return
	}
	if runs == nil {
		runs = []personalagent.Run{}
	}
	writeJSON(w, http.StatusOK, PersonalTask{Task: task, Runs: runs})
}

func (s *Server) handlePAApprovals(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	ctx := r.Context()
	var ids []string
	if q := r.URL.Query().Get("assistant"); q != "" {
		id, ok := paID(w, q)
		if !ok {
			return
		}
		ids = []string{id}
	} else {
		list, err := c.Assistants(ctx)
		if err != nil {
			s.paRefuse(w, dev, "approvals", err)
			return
		}
		for _, a := range list {
			ids = append(ids, a.ID)
		}
	}
	out := []personalagent.Approval{}
	for _, id := range ids {
		list, err := c.Approvals(ctx, id)
		if err != nil {
			s.paRefuse(w, dev, "approvals", err)
			return
		}
		for _, a := range list {
			if a.State == "pending" {
				out = append(out, a)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": 1, "approvals": out})
}

// handlePAInbox serves the plugin's one inbox snapshot (approvals, reviews,
// failed starts, stalls); the phone draws it and decides nothing from it.
func (s *Server) handlePAInbox(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	who := ""
	if q := r.URL.Query().Get("assistant"); q != "" {
		id, ok := paID(w, q)
		if !ok {
			return
		}
		who = id
	}
	in, err := c.Inbox(r.Context(), who)
	if err != nil {
		s.paRefuse(w, dev, "inbox", err)
		return
	}
	if in.Items == nil {
		in.Items = []personalagent.InboxItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": 1, "items": in.Items, "needsYou": in.NeedsYou, "seq": in.Seq})
}

// --- writes ---------------------------------------------------------------

// durablePAContext keeps an in-flight idempotent write alive after a phone
// disconnect, but gives the plugin CLI an independent hard deadline. The
// incoming HTTP deadline/cancellation must not silently remove this bound.
func durablePAContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

// paWrite runs one bounded command that outlives the request: a phone may
// disconnect before the CLI answers and retry with the same requestId.
func (s *Server) paWrite(w http.ResponseWriter, r *http.Request, dev Device, op string, run func(ctx context.Context, c *personalagent.Client) (any, error)) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	ctx, cancel := durablePAContext(r.Context(), maxPAWriteDuration)
	defer cancel()
	out, err := run(ctx, c)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || personalagent.Classify(err).WriteOutcomeUnknown() {
			// The CLI may have committed before its reply was lost. Do not
			// promise a failure or encourage a new requestId here.
			slog.Warn("remote host personal agent write uncertain", "device", dev.ID, "op", op)
			writeError(w, http.StatusGatewayTimeout, "personal_agent_uncertain", "Execution timed out; retry with the same requestId")
			return
		}
		s.paRefuse(w, dev, op, err)
		return
	}
	slog.Info("remote host personal agent op", "device", dev.ID, "op", op)
	writeJSON(w, http.StatusOK, out)
}

// paAgents is the Agent choice offered for a Coordinator; never nil on the wire.
func (s *Server) paAgents() []PersonalAgentKind {
	if s.opts.AssistantAgents != nil {
		if list := s.opts.AssistantAgents(); list != nil {
			return list
		}
	}
	return []PersonalAgentKind{}
}

// handlePAStartCoordinator brings the Assistant's Coordinator up as a real
// Herdr Agent (the plugin owns the lifecycle; the requestId makes a double tap
// one start). The Agent must be one the desktop offers; empty = the default.
func (s *Server) handlePAStartCoordinator(w http.ResponseWriter, r *http.Request, dev Device) {
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		RequestID string `json:"requestId"`
		Agent     string `json:"agent"`
	}
	if !paBody(w, r, &req, func() string { return req.RequestID }) {
		return
	}
	if req.Agent != "" {
		allowed := false
		for _, kind := range s.paAgents() {
			if kind.ID == req.Agent && kind.ID == "omp" {
				allowed = true
			}
		}
		if !allowed {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
	}
	s.paWrite(w, r, dev, "coordinator.start", func(ctx context.Context, c *personalagent.Client) (any, error) {
		co, err := c.StartCoordinator(ctx, req.RequestID, id, req.Agent, "")
		return map[string]any{"schema": 1, "coordinator": co}, err
	})
}

// handlePAStopCoordinator stops the Assistant's Coordinator: the plugin marks it
// stopped and closes its own recorded Herdr pane (the conversation stays and the
// next start resumes it). Starting again is the start route — the phone's
// Restart is the two in a row.
func (s *Server) handlePAStopCoordinator(w http.ResponseWriter, r *http.Request, dev Device) {
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		RequestID string `json:"requestId"`
	}
	if !paBody(w, r, &req, func() string { return req.RequestID }) {
		return
	}
	s.paWrite(w, r, dev, "coordinator.stop", func(ctx context.Context, c *personalagent.Client) (any, error) {
		co, err := c.StopCoordinator(ctx, req.RequestID, id)
		return map[string]any{"schema": 1, "coordinator": co}, err
	})
}

func (s *Server) handlePACreateAssistant(w http.ResponseWriter, r *http.Request, dev Device) {
	var req struct {
		RequestID   string `json:"requestId"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !paBody(w, r, &req, func() string { return req.RequestID }) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > maxPAName || !paText(name, maxPAName) || strings.ContainsAny(name, "\r\n") || !paText(req.Description, maxPAShort) {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	s.paWrite(w, r, dev, "assistant.create", func(ctx context.Context, c *personalagent.Client) (any, error) {
		a, err := c.CreateAssistant(ctx, req.RequestID, name, strings.TrimSpace(req.Description))
		return map[string]any{"schema": 1, "assistant": a}, err
	})
}

func (s *Server) handlePARenameAssistant(w http.ResponseWriter, r *http.Request, dev Device) {
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		RequestID        string `json:"requestId"`
		Name             string `json:"name"`
		ExpectedRevision int    `json:"expectedRevision"`
	}
	if !paBody(w, r, &req, func() string { return req.RequestID }) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > maxPAName || !paText(name, maxPAName) || strings.ContainsAny(name, "\r\n") || req.ExpectedRevision < 1 {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	s.paWrite(w, r, dev, "assistant.rename", func(ctx context.Context, c *personalagent.Client) (any, error) {
		a, err := c.RenameAssistant(ctx, req.RequestID, id, name, req.ExpectedRevision)
		return map[string]any{"schema": 1, "assistant": a}, err
	})
}

// handlePAAssistantEnabled serves pause (enabled=false) and resume.
func (s *Server) handlePAAssistantEnabled(enabled bool) deviceHandler {
	op := "assistant.pause"
	if enabled {
		op = "assistant.resume"
	}
	return func(w http.ResponseWriter, r *http.Request, dev Device) {
		id, ok := paID(w, r.PathValue("id"))
		if !ok {
			return
		}
		var req struct {
			RequestID        string `json:"requestId"`
			ExpectedRevision int    `json:"expectedRevision"`
		}
		if !paBody(w, r, &req, func() string { return req.RequestID }) {
			return
		}
		if req.ExpectedRevision < 1 {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
		s.paWrite(w, r, dev, op, func(ctx context.Context, c *personalagent.Client) (any, error) {
			a, err := c.SetAssistantEnabled(ctx, req.RequestID, id, enabled, req.ExpectedRevision)
			return map[string]any{"schema": 1, "assistant": a}, err
		})
	}
}

func (s *Server) handlePADraftGoal(w http.ResponseWriter, r *http.Request, dev Device) {
	var req struct {
		RequestID   string                          `json:"requestId"`
		AssistantID string                          `json:"assistantId"`
		Title       string                          `json:"title"`
		Objective   string                          `json:"objective"`
		Commitments []personalagent.CommitmentInput `json:"commitments"`
	}
	if !paBody(w, r, &req, func() string { return req.RequestID }) {
		return
	}
	title := strings.TrimSpace(req.Title)
	valid := paIDRe.MatchString(req.AssistantID) && title != "" && len(title) <= maxPAShort && paText(title, maxPAShort) &&
		paText(req.Objective, maxPAText) && len(req.Commitments) <= maxPACommitments
	for _, c := range req.Commitments {
		valid = valid && c.Kind != "" && len(c.Kind) <= 40 && paText(c.Kind, 40) && paText(c.SuccessCriteria, maxPAShort) &&
			paText(c.ExitCondition, maxPAShort) && paText(c.Spec, maxPAShort)
	}
	if !valid {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	s.paWrite(w, r, dev, "goal.draft", func(ctx context.Context, c *personalagent.Client) (any, error) {
		g, err := c.CreateGoal(ctx, req.RequestID, personalagent.CreateGoalInput{
			AssistantID: req.AssistantID, Title: title, Objective: strings.TrimSpace(req.Objective), Commitments: req.Commitments,
		})
		return map[string]any{"schema": 1, "goal": g}, err
	})
}

// handlePAGoalAction serves approve|pause|resume|cancel on one Goal. A Goal is
// never completed from the phone: completion needs evidence a person reads.
func (s *Server) handlePAGoalAction(action string) deviceHandler {
	return func(w http.ResponseWriter, r *http.Request, dev Device) {
		id, ok := paID(w, r.PathValue("id"))
		if !ok {
			return
		}
		var req struct {
			RequestID        string `json:"requestId"`
			ExpectedRevision int    `json:"expectedRevision"`
		}
		if !paBody(w, r, &req, func() string { return req.RequestID }) {
			return
		}
		if req.ExpectedRevision < 1 {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
		s.paWrite(w, r, dev, "goal."+action, func(ctx context.Context, c *personalagent.Client) (any, error) {
			g, err := c.GoalAction(ctx, req.RequestID, action, id, req.ExpectedRevision, "")
			return map[string]any{"schema": 1, "goal": g}, err
		})
	}
}

func (s *Server) handlePADecideApproval(w http.ResponseWriter, r *http.Request, dev Device) {
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		RequestID  string `json:"requestId"`
		ActionHash string `json:"actionHash"`
		Approve    bool   `json:"approve"`
	}
	if !paBody(w, r, &req, func() string { return req.RequestID }) {
		return
	}
	if len(req.ActionHash) != 64 || strings.Trim(req.ActionHash, "0123456789abcdef") != "" {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	op := "approval.deny"
	if req.Approve {
		op = "approval.approve"
	}
	s.paWrite(w, r, dev, op, func(ctx context.Context, c *personalagent.Client) (any, error) {
		a, err := c.DecideApproval(ctx, req.RequestID, id, req.ActionHash, req.Approve)
		return map[string]any{"schema": 1, "approval": a}, err
	})
}

// handlePATaskAction serves ack|retry|publish on one Task (publish releases a draft the Coordinator filed).
func (s *Server) handlePATaskAction(action string) deviceHandler {
	return func(w http.ResponseWriter, r *http.Request, dev Device) {
		id, ok := paID(w, r.PathValue("id"))
		if !ok {
			return
		}
		var req struct {
			RequestID        string `json:"requestId"`
			ExpectedRevision int    `json:"expectedRevision"`
		}
		if !paBody(w, r, &req, func() string { return req.RequestID }) {
			return
		}
		if req.ExpectedRevision < 1 {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
		s.paWrite(w, r, dev, "task."+action, func(ctx context.Context, c *personalagent.Client) (any, error) {
			var t personalagent.Task
			var err error
			switch action {
			case "ack":
				t, err = c.AckTask(ctx, req.RequestID, id, req.ExpectedRevision)
			case "publish":
				t, err = c.PublishTask(ctx, req.RequestID, id, req.ExpectedRevision)
			default:
				t, err = c.RetryTask(ctx, req.RequestID, id, req.ExpectedRevision)
			}
			return map[string]any{"schema": 1, "task": t}, err
		})
	}
}

// --- events ---------------------------------------------------------------

// NotifyPersonalAgent tells every open events socket that the Assistants
// overview changed. It carries a monotonic revision and the two counters the
// phone badges with — never a name, a prompt or a memory — and the phone
// answers by re-reading the authoritative overview. A socket whose queue is
// full drops the frame rather than stall the Mac.
func (s *Server) NotifyPersonalAgent(assistants, needsYou int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paRevision++
	frame := map[string]any{"t": "personal-agent.changed", "revision": s.paRevision, "assistants": assistants, "needsYou": needsYou}
	for q := range s.notices {
		select {
		case q <- frame:
		default:
		}
	}
}
