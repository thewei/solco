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

	"github.com/thewei/solco/internal/herdr"
)

/**
 * [INPUT]: 依赖 Options.Launcher（启动一个会话的唯一外部缝）、Backend.Projection（解析 Workspace 与新 pane）、server.go 的 guard/resolveInstance/writeError
 * [OUTPUT]: 对外提供 Launcher/LaunchKind/LaunchSpec/LaunchError 与 POST /api/v3/launch
 * [POS]: 手机「启动新会话」（Home Composer）：在已有 Workspace 里新开 Tab，或按绝对路径复用/新建 Workspace，再启动 Agent（可带首条 prompt）或开终端（可带首条命令，需 terminal-input scope）；事务、幂等（requestId）与 Herdr 调用全在 Launcher 背后（Host 不 import app/nativeui）；Mac 焦点不动；日志只记设备、kind、pane，路径与 prompt 永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	// LaunchTerminal is the kind id of a plain shell tab (no Agent).
	LaunchTerminal = "terminal"

	maxLaunchPrompt = 64 << 10
	maxLaunchPath   = 4096
	launchTimeout   = 150 * time.Second // shell-ready + agent start + readiness + first prompt/command
)

// launchVisible is how long the new pane may take to show in the projection.
var launchVisible = 4 * time.Second

var launchRequestID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// LaunchKind is one thing the phone may start: a plain terminal or an Agent.
type LaunchKind struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// LaunchSpec is one validated launch. CWD is an absolute path; WorkspaceID is
// set when the phone picked an existing Workspace (CWD is then that
// Workspace's own directory) and empty when it typed a path.
type LaunchSpec struct {
	RequestID   string
	WorkspaceID string
	CWD         string
	Kind        string
	Prompt      string
}

// Launcher runs the launch transaction on a local Herdr session. It owns
// idempotency (the same RequestID never launches twice) and returns the new
// pane's id.
type Launcher interface {
	// Kinds lists what may be launched, LaunchTerminal first.
	Kinds() []LaunchKind
	Launch(ctx context.Context, instance string, spec LaunchSpec) (LaunchResult, error)
}

// LaunchResult is the new pane. PromptPending: the Agent started but is blocked on its user (a
// first-run trust prompt), so the initial prompt was not delivered.
type LaunchResult struct {
	PaneID        string
	PromptPending bool
}

// LaunchError is a launch refusal the Host relays with a stable code.
type LaunchError struct {
	Code   string // path_unavailable | launch_conflict | launch_uncertain | launch_failed
	Status int
	Detail string
}

func (e *LaunchError) Error() string { return e.Code + ": " + e.Detail }

type launchRequest struct {
	RequestID   string `json:"requestId"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	Path        string `json:"path,omitempty"`
	Kind        string `json:"kind"`
	Prompt      string `json:"prompt,omitempty"`
}

type launchResponse struct {
	PaneID      string `json:"paneId"`
	TerminalID  string `json:"terminalId"`
	WorkspaceID string `json:"workspaceId"`
	Agent       bool   `json:"agent"`
	// PromptPending is set only when the prompt could not be delivered (see LaunchResult).
	PromptPending bool `json:"promptPending,omitempty"`
}

func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request, dev Device) {
	launcher := s.opts.Launcher
	if launcher == nil {
		writeError(w, http.StatusNotFound, "launch_unavailable", "")
		return
	}
	var req launchRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLaunchPrompt+maxLaunchPath+4<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	if !validLaunchRequest(req, launcher.Kinds()) {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	// Typing a command into a new shell is terminal input: it needs that scope on top of Manage.
	if req.Kind == LaunchTerminal && strings.TrimSpace(req.Prompt) != "" {
		if _, status := s.authorize(r, ScopeTerminalInput); status != 0 {
			writeError(w, status, map[int]string{401: "unauthorized", 403: "forbidden"}[status], "")
			return
		}
	}
	instance, ok := s.resolveAnyInstance(w, r)
	if !ok {
		return
	}
	// A device's agents run on another computer: only a Terminal in one of its existing
	// Workspaces can start, from the directory that computer's Herdr reports (nothing typed
	// on the phone is ever checked against, or created on, this Mac's disk).
	if isDeviceInstance(instance) && (req.Kind != LaunchTerminal || req.WorkspaceID == "") {
		writeError(w, http.StatusConflict, "instance_remote", "")
		return
	}
	spec := LaunchSpec{RequestID: req.RequestID, WorkspaceID: req.WorkspaceID, CWD: req.Path, Kind: req.Kind, Prompt: strings.TrimSpace(req.Prompt)}
	if req.WorkspaceID != "" {
		proj, err := s.opts.Backend.Projection(instance)
		if err != nil {
			slog.Warn("remote host launch: projection failed", "error", err)
			writeError(w, http.StatusBadGateway, "backend_unavailable", "")
			return
		}
		cwd, found := workspaceCWD(proj.Projects, proj.Panes, req.WorkspaceID)
		if !found {
			writeError(w, http.StatusNotFound, "unknown_workspace", "")
			return
		}
		if cwd == "" {
			writeError(w, http.StatusConflict, "workspace_no_directory", "")
			return
		}
		spec.CWD = cwd
	}

	// A phone that drops mid-launch must not abort a half-built session: the
	// transaction finishes and a retry with the same requestId replays it.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), launchTimeout)
	defer cancel()
	result, err := launcher.Launch(ctx, instance, spec)
	if err != nil {
		var le *LaunchError
		if errors.As(err, &le) {
			slog.Warn("remote host launch refused", "device", dev.ID, "kind", req.Kind, "code", le.Code)
			writeError(w, le.Status, le.Code, truncate(le.Detail, 200))
			return
		}
		slog.Warn("remote host launch failed", "device", dev.ID, "kind", req.Kind)
		writeError(w, http.StatusBadGateway, "launch_failed", truncate(err.Error(), 200))
		return
	}
	paneID := result.PaneID
	resp, found := s.awaitLaunchedPane(ctx, instance, paneID)
	resp.PromptPending = result.PromptPending
	if !found {
		slog.Warn("remote host launch: pane not visible", "device", dev.ID, "kind", req.Kind, "pane", paneID)
		writeError(w, http.StatusGatewayTimeout, "launch_uncertain", "")
		return
	}
	slog.Info("remote host launched", "device", dev.ID, "kind", req.Kind, "pane", paneID)
	writeJSON(w, http.StatusOK, resp)
}

// validLaunchRequest is the boundary check: exactly one target, a known kind,
// an absolute clean path, and a Terminal prompt that is a single line.
func validLaunchRequest(req launchRequest, kinds []LaunchKind) bool {
	if !launchRequestID.MatchString(req.RequestID) {
		return false
	}
	known := false
	for _, k := range kinds {
		if k.ID == req.Kind {
			known = true
		}
	}
	if !known {
		return false
	}
	if (req.WorkspaceID == "") == (req.Path == "") {
		return false
	}
	if req.Path != "" && !validLaunchPath(req.Path) {
		return false
	}
	if len(req.Prompt) > maxLaunchPrompt || !utf8.ValidString(req.Prompt) {
		return false
	}
	// A Terminal's prompt is its first command line: one line, no control characters.
	return req.Kind != LaunchTerminal || !strings.ContainsFunc(req.Prompt, func(c rune) bool { return c < 0x20 || c == 0x7f })
}

func validLaunchPath(p string) bool {
	if !strings.HasPrefix(p, "/") || len(p) > maxLaunchPath || !utf8.ValidString(p) {
		return false
	}
	for _, c := range p {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// awaitLaunchedPane polls the projection until the new pane shows up (Herdr's
// snapshot can lag the call that created it) and maps it to the wire answer.
func (s *Server) awaitLaunchedPane(ctx context.Context, instance, paneID string) (launchResponse, bool) {
	deadline := time.Now().Add(launchVisible)
	for {
		if proj, err := s.opts.Backend.Projection(instance); err == nil {
			for _, p := range proj.Panes {
				if p.ID == paneID {
					return launchResponse{PaneID: p.ID, TerminalID: p.TerminalID, WorkspaceID: p.ProjectID, Agent: p.Agent != ""}, p.TerminalID != ""
				}
			}
		}
		if time.Now().After(deadline) || !sleepCtx(ctx, 100*time.Millisecond) {
			return launchResponse{}, false
		}
	}
}

// workspaceCWD is the directory a new tab in the Workspace starts in: the
// Workspace's own, else its first pane's. found reports whether the id exists.
func workspaceCWD(projects []herdr.Project, panes []herdr.Pane, id string) (cwd string, found bool) {
	for _, p := range projects {
		if p.ID != id {
			continue
		}
		if p.CWD != "" {
			return p.CWD, true
		}
		for _, pane := range panes {
			if pane.ProjectID == id && pane.CWD != "" {
				return pane.CWD, true
			}
		}
		return "", true
	}
	return "", false
}
