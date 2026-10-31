package remotehost

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/herdr"
)

/**
 * [INPUT]: 依赖 Backend.PromptAgent/SendAgentKeys（Herdr agent.prompt / agent.send_keys，含其自带的身份拒绝回退）、agent.ParseRuntimePhase/ClassifySendability、server.go 的 guard/resolveInstance
 * [OUTPUT]: 对外提供 POST /api/v3/panes/{paneID}/chat/send 与 POST /api/v3/panes/{paneID}/chat/stop
 * [POS]: 手机 Chat 发送（P3 子集）：与桌面同一门禁（blocked/failed → needs_terminal；working 须显式 interrupt 才先 Esc 再发；unknown 不发）；投递只走 Herdr 既有方法，Herdr 的 busy/not-ready 拒绝原样回传、永不绕过；DeliveryUncertain 回 504 且 Host 绝不重发；提示正文与按键内容永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	maxChatText     = 64 << 10
	chatSendTimeout = 15 * time.Second
	interruptSettle = 600 * time.Millisecond // same pauses as the desktop's InterruptAndSend
	interruptClear  = 200 * time.Millisecond
)

type chatSendRequest struct {
	Text string `json:"text"`
	// Interrupt cuts a running turn in: Esc, settle, Ctrl-C (clears the line a
	// TUI restores), settle, then the prompt. Ignored when the agent is ready.
	Interrupt bool `json:"interrupt,omitempty"`
}

func (s *Server) handleChatSend(w http.ResponseWriter, r *http.Request, dev Device) {
	var req chatSendRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxChatText+1<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" || len(text) > maxChatText || !utf8.ValidString(text) {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	instance, paneID, target, ok := s.sendTarget(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), chatSendTimeout)
	defer cancel()
	backend := s.opts.Backend
	switch agent.ClassifySendability(agent.ParseRuntimePhase(target.Status, false)) {
	case agent.Sendable:
	case agent.MidTurn:
		if !req.Interrupt {
			writeError(w, http.StatusConflict, "busy", "")
			return
		}
		if err := backend.SendAgentKeys(ctx, instance, paneID, []string{"esc"}); err != nil {
			s.sendFailed(w, dev, paneID, "interrupt", err)
			return
		}
		if !sleepCtx(ctx, interruptSettle) {
			writeError(w, http.StatusGatewayTimeout, "delivery_uncertain", "")
			return
		}
		if err := backend.SendAgentKeys(ctx, instance, paneID, []string{"ctrl+c"}); err != nil {
			s.sendFailed(w, dev, paneID, "interrupt", err)
			return
		}
		if !sleepCtx(ctx, interruptClear) {
			writeError(w, http.StatusGatewayTimeout, "delivery_uncertain", "")
			return
		}
	case agent.NeedsTerminal:
		writeError(w, http.StatusConflict, "needs_terminal", "")
		return
	default:
		writeError(w, http.StatusConflict, "not_ready", "")
		return
	}
	if err := backend.PromptAgent(ctx, instance, paneID, text); err != nil {
		s.sendFailed(w, dev, paneID, "prompt", err)
		return
	}
	slog.Info("remote host chat prompt sent", "device", dev.ID, "pane", paneID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleChatStop(w http.ResponseWriter, r *http.Request, dev Device) {
	instance, paneID, _, ok := s.sendTarget(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), chatSendTimeout)
	defer cancel()
	if err := s.opts.Backend.SendAgentKeys(ctx, instance, paneID, []string{"esc"}); err != nil {
		s.sendFailed(w, dev, paneID, "stop", err)
		return
	}
	slog.Info("remote host chat stop sent", "device", dev.ID, "pane", paneID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// sendTarget validates the pane and that it runs an agent (writes the refusal).
func (s *Server) sendTarget(w http.ResponseWriter, r *http.Request) (instance, paneID string, target chatTarget, ok bool) {
	instance, ok = s.resolveInstance(w, r)
	if !ok {
		return
	}
	paneID = r.PathValue("paneID")
	target = s.resolveChat(instance, paneID)
	if target.Code == "unknown_pane" || target.Code == "backend_unavailable" {
		writeError(w, chatErrorStatus[target.Code], target.Code, "")
		return "", "", target, false
	}
	if target.Status == "" { // a plain shell: no agent to prompt
		writeError(w, http.StatusConflict, "no_agent", "")
		return "", "", target, false
	}
	return instance, paneID, target, true
}

// sendFailed answers a failed delivery: an uncertain one is never retried; a
// Herdr refusal is relayed as-is (never bypassed).
func (s *Server) sendFailed(w http.ResponseWriter, dev Device, paneID, op string, err error) {
	var uncertain *herdr.DeliveryUncertainError
	if errors.As(err, &uncertain) {
		slog.Warn("remote host chat delivery uncertain", "device", dev.ID, "pane", paneID, "op", op)
		writeError(w, http.StatusGatewayTimeout, "delivery_uncertain", "")
		return
	}
	slog.Warn("remote host chat refused", "device", dev.ID, "pane", paneID, "op", op, "error", err)
	writeError(w, http.StatusConflict, "refused", truncate(err.Error(), 200))
}
