package remotehost

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"
)

/**
 * [INPUT]: 依赖 Options.AutomationsAdmin（桌面 Automations 管理动作的唯一外部缝，nativeui 提供，背后是 internal/automations.Client）、automations.go 的 Automations 线格式、server.go 的 guard/writeJSON/writeError
 * [OUTPUT]: 对外提供 AutomationFields/AutomationDetail/AutomationRunInfo/AutomationError/AutomationsAdmin 与
 *           GET /api/v3/automations/{name}、POST /api/v3/automations（建草稿）、POST /api/v3/automations/{name}/save（保存编辑）、
 *           POST /api/v3/automations/{name}/approve|run|pause|resume|delete、POST /api/v3/automations/scheduler/start、
 *           POST /api/v3/automations/seen、bootstrap 的 capabilities.automationsManage
 * [POS]: 手机对桌面 Automations 的远程管理（对标桌面页：批准/立即运行/暂停恢复/删除/新建/编辑/起调度器/标已读/运行历史）：
 *        插件仍是唯一权威——Host 只校验请求形状（长度、类型）并转发给 AutomationsAdmin，语义、校验与拒绝全在插件；写动作的每个应答都是新读出的列表；
 *        详情含提示词与理由（scope view，与 Chat 同级），运行历史不含错误文本；新建永远是草稿（需人批准，agent 起草→人批准的约定不变）；
 *        拒绝用稳定错误码，message 是插件自己的校验原文（有界）；日志只记设备、操作名与结果码，名字/提示词/理由/错误原文永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	maxAutomationBody   = 64 << 10
	maxAutomationName   = 80
	maxAutomationShort  = 512
	maxAutomationPrompt = 20 << 10
	maxAutomationNote   = 8 << 10
	maxAutomationRunID  = 128
)

// AutomationFields is what the New and Edit forms carry — the plugin's own
// vocabulary, none of it interpreted here. Workspace is worktree|root,
// OnOverlap skip|queue; empty means the plugin's default.
type AutomationFields struct {
	Cron            string `json:"cron,omitempty"`
	Repo            string `json:"repo,omitempty"`
	Workspace       string `json:"workspace,omitempty"`
	Agent           string `json:"agent,omitempty"`
	Model           string `json:"model,omitempty"`
	Prompt          string `json:"prompt,omitempty"`
	Rationale       string `json:"rationale,omitempty"`
	OnOverlap       string `json:"onOverlap,omitempty"`
	Headless        bool   `json:"headless,omitempty"`
	TimeoutMinutes  int    `json:"timeoutMinutes,omitempty"`
	CatchUpMinutes  int    `json:"catchUpMinutes,omitempty"`
	CleanupTTLHours int    `json:"cleanupTtlHours,omitempty"`
}

// AutomationRunInfo is one run in a detail's history. No error text: a failed
// run says so by its status and the Mac's pane tells why.
type AutomationRunInfo struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Trigger     string `json:"trigger,omitempty"`
	AtMs        int64  `json:"atMs,omitempty"`
	DurationSec int64  `json:"durationSec,omitempty"`
	Agent       string `json:"agent,omitempty"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	PaneID      string `json:"paneId,omitempty"`
	Unread      bool   `json:"unread,omitempty"`
}

// AutomationDetail is one automation in full plus its newest runs.
type AutomationDetail struct {
	Name      string `json:"name"`
	State     string `json:"state"` // draft | paused | active
	Schedule  string `json:"schedule,omitempty"`
	NextRunMs int64  `json:"nextRunMs,omitempty"`
	AutomationFields
	Runs []AutomationRunInfo `json:"runs"`
}

// AutomationError is a refusal with a stable code: automation_unavailable
// (404, the gate closed), unknown_automation (404), automation_refused (409,
// the plugin said no — Detail is its own words), automation_failed (502).
type AutomationError struct {
	Code   string
	Status int
	Detail string
}

func (e *AutomationError) Error() string { return e.Code + ": " + e.Detail }

// AutomationOp is one verb on an existing automation.
type AutomationOp string

const (
	OpApproveAutomation AutomationOp = "approve"
	OpRunAutomation     AutomationOp = "run"
	OpPauseAutomation   AutomationOp = "pause"
	OpResumeAutomation  AutomationOp = "resume"
	OpDeleteAutomation  AutomationOp = "delete"
)

// AutomationsAdmin is the Options.AutomationsAdmin seam: the desktop's own
// plugin client. Every write answers the freshly read list.
type AutomationsAdmin interface {
	// Open reports whether the plugin gate is open (the capability follows it).
	Open() bool
	Detail(ctx context.Context, name string) (AutomationDetail, error)
	// Create adds a draft; it never schedules until someone approves it.
	Create(ctx context.Context, name string, f AutomationFields) (Automations, error)
	Update(ctx context.Context, name string, f AutomationFields) (Automations, error)
	Do(ctx context.Context, name string, op AutomationOp) (Automations, error)
	StartScheduler(ctx context.Context) (Automations, error)
	// MarkSeen records one run as opened, or every finished run when all.
	MarkSeen(ctx context.Context, runID string, all bool) (Automations, error)
}

type deviceHandler = func(http.ResponseWriter, *http.Request, Device)

func (s *Server) adminSeam(w http.ResponseWriter) AutomationsAdmin {
	a := s.opts.AutomationsAdmin
	if a == nil || !a.Open() {
		writeError(w, http.StatusNotFound, "automation_unavailable", "")
		return nil
	}
	return a
}

func (s *Server) refuseAutomation(w http.ResponseWriter, dev Device, op string, err error) {
	var ae *AutomationError
	if errors.As(err, &ae) {
		slog.Warn("remote host automation refused", "device", dev.ID, "op", op, "code", ae.Code)
		writeError(w, ae.Status, ae.Code, truncate(ae.Detail, 240))
		return
	}
	slog.Warn("remote host automation failed", "device", dev.ID, "op", op)
	writeError(w, http.StatusBadGateway, "automation_failed", "")
}

func automationName(w http.ResponseWriter, name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxAutomationName || strings.ContainsAny(name, "\x00\r\n") || !utf8.ValidString(name) {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return "", false
	}
	return name, true
}

// validAutomationFields bounds every field's size; what a value means is the
// plugin's to judge.
func validAutomationFields(f AutomationFields) bool {
	for _, v := range []string{f.Cron, f.Repo, f.Workspace, f.Agent, f.Model, f.OnOverlap} {
		if len(v) > maxAutomationShort || strings.ContainsAny(v, "\x00\r\n") || !utf8.ValidString(v) {
			return false
		}
	}
	if len(f.Prompt) > maxAutomationPrompt || len(f.Rationale) > maxAutomationNote ||
		!utf8.ValidString(f.Prompt) || !utf8.ValidString(f.Rationale) ||
		strings.ContainsRune(f.Prompt, 0) || strings.ContainsRune(f.Rationale, 0) {
		return false
	}
	return f.TimeoutMinutes >= 0 && f.CatchUpMinutes >= 0 && f.CleanupTTLHours >= 0 &&
		f.TimeoutMinutes <= 1<<20 && f.CatchUpMinutes <= 1<<20 && f.CleanupTTLHours <= 1<<20
}

type automationWrite struct {
	Name string `json:"name"`
	AutomationFields
}

func decodeAutomationWrite(w http.ResponseWriter, r *http.Request) (automationWrite, bool) {
	var req automationWrite
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAutomationBody)).Decode(&req); err != nil || !validAutomationFields(req.AutomationFields) {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return req, false
	}
	return req, true
}

// handleAutomationDetail answers GET /api/v3/automations/{name}.
func (s *Server) handleAutomationDetail(w http.ResponseWriter, r *http.Request, dev Device) {
	admin := s.adminSeam(w)
	if admin == nil {
		return
	}
	name, ok := automationName(w, r.PathValue("name"))
	if !ok {
		return
	}
	d, err := admin.Detail(r.Context(), name)
	if err != nil {
		s.refuseAutomation(w, dev, "detail", err)
		return
	}
	if d.Runs == nil {
		d.Runs = []AutomationRunInfo{}
	}
	slog.Info("remote host automation detail", "device", dev.ID, "runs", len(d.Runs))
	writeJSON(w, http.StatusOK, d)
}

// handleAutomationCreate answers POST /api/v3/automations.
func (s *Server) handleAutomationCreate(w http.ResponseWriter, r *http.Request, dev Device) {
	admin := s.adminSeam(w)
	if admin == nil {
		return
	}
	req, ok := decodeAutomationWrite(w, r)
	if !ok {
		return
	}
	name, ok := automationName(w, req.Name)
	if !ok {
		return
	}
	out, err := admin.Create(context.WithoutCancel(r.Context()), name, req.AutomationFields)
	if err != nil {
		s.refuseAutomation(w, dev, "create", err)
		return
	}
	slog.Info("remote host automation drafted", "device", dev.ID)
	writeJSON(w, http.StatusOK, out)
}

// handleAutomationUpdate answers POST /api/v3/automations/{name}/save (a POST, not a
// PUT: the phone's relay and P2P tunnels carry GET and POST only).
func (s *Server) handleAutomationUpdate(w http.ResponseWriter, r *http.Request, dev Device) {
	admin := s.adminSeam(w)
	if admin == nil {
		return
	}
	name, ok := automationName(w, r.PathValue("name"))
	if !ok {
		return
	}
	req, ok := decodeAutomationWrite(w, r)
	if !ok {
		return
	}
	out, err := admin.Update(context.WithoutCancel(r.Context()), name, req.AutomationFields)
	if err != nil {
		s.refuseAutomation(w, dev, "update", err)
		return
	}
	slog.Info("remote host automation saved", "device", dev.ID)
	writeJSON(w, http.StatusOK, out)
}

// handleAutomationOp answers POST /api/v3/automations/{name}/<op>.
func (s *Server) handleAutomationOp(op AutomationOp) deviceHandler {
	return func(w http.ResponseWriter, r *http.Request, dev Device) {
		admin := s.adminSeam(w)
		if admin == nil {
			return
		}
		name, ok := automationName(w, r.PathValue("name"))
		if !ok {
			return
		}
		out, err := admin.Do(context.WithoutCancel(r.Context()), name, op)
		if err != nil {
			s.refuseAutomation(w, dev, string(op), err)
			return
		}
		slog.Info("remote host automation op", "device", dev.ID, "op", string(op))
		writeJSON(w, http.StatusOK, out)
	}
}

// handleAutomationScheduler answers POST /api/v3/automations/scheduler/start.
func (s *Server) handleAutomationScheduler(w http.ResponseWriter, r *http.Request, dev Device) {
	admin := s.adminSeam(w)
	if admin == nil {
		return
	}
	out, err := admin.StartScheduler(context.WithoutCancel(r.Context()))
	if err != nil {
		s.refuseAutomation(w, dev, "scheduler", err)
		return
	}
	slog.Info("remote host automation scheduler started", "device", dev.ID)
	writeJSON(w, http.StatusOK, out)
}

// handleAutomationSeen answers POST /api/v3/automations/seen.
func (s *Server) handleAutomationSeen(w http.ResponseWriter, r *http.Request, dev Device) {
	admin := s.adminSeam(w)
	if admin == nil {
		return
	}
	var req struct {
		RunID string `json:"runId"`
		All   bool   `json:"all"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil ||
		(req.All == (req.RunID != "")) || len(req.RunID) > maxAutomationRunID || strings.HasPrefix(req.RunID, "-") {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	out, err := admin.MarkSeen(context.WithoutCancel(r.Context()), req.RunID, req.All)
	if err != nil {
		s.refuseAutomation(w, dev, "seen", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
