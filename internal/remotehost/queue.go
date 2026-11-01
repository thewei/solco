package remotehost

import (
	"errors"
	"log/slog"
	"net/http"
)

/**
 * [INPUT]: Options.Queue（app 层把桌面同一条 conversation.FollowUpQueue 暴露给 Host）、chatTarget 的会话身份指纹与 ConversationID（"live:<provider>:<terminal>"，与桌面同队列）
 * [OUTPUT]: chat/send 的 `queue` 标志（agent 工作中把下一条追加到回合边界后的列表）、POST /api/v3/panes/{paneID}/chat/queue/cancel（全清）、POST /api/v3/panes/{paneID}/chat/queue/item/{requestID}/cancel（单条）、聊天帧里的 `queued{items[]}`、bootstrap capability `queue`
 * [POS]: 手机 Chat 的跟进队列（持久、跨设备）：队列与投递 worker 仍是桌面那一套（一会话一有序列表、边界等待、身份指纹复核、不确定即墓碑、绝不自动重发）；Host 只入队/查询/取消；
 *        指纹只认 Herdr 自己的 agent_session（与桌面一致：Claude 无 hook 身份则不能排队，回 409 queue_unavailable）；提示正文永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// FollowUpItem is one queued follow-up of a conversation as the phone sees it.
type FollowUpItem struct {
	ID string `json:"id"`
	// Text is the prompt; it never reaches a log.
	Text string `json:"text"`
	// State is the queue lifecycle verbatim (queued, waiting-for-turn-boundary,
	// delivering, failed-recoverable, delivery-uncertain).
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// FollowUps is the Host's window onto the desktop's follow-up queue.
type FollowUps interface {
	// Enqueue appends the follow-up to the conversation's list; ErrFollowUpFrozen
	// while the head is a delivery-uncertain tombstone.
	Enqueue(instance, paneID, conversationID, fingerprint, text string) error
	// Items returns the conversation's live (not yet settled) follow-ups in
	// delivery order.
	Items(conversationID string) ([]FollowUpItem, bool)
	// CancelItem withdraws one follow-up before its delivery commit:
	// ErrFollowUpNone / ErrFollowUpCommitted otherwise.
	CancelItem(conversationID, requestID string) error
	// Cancel withdraws every cancellable follow-up of the conversation.
	Cancel(conversationID string) error
}

var (
	ErrFollowUpFrozen    = errors.New("delivery uncertain — cancel the failed entry before queueing more")
	ErrFollowUpNone      = errors.New("no queued follow-up")
	ErrFollowUpCommitted = errors.New("the follow-up is already being delivered")
)

// queuedFrame is the `queued` field of a chat frame: the conversation's
// ordered follow-ups.
type queuedFrame struct {
	Items []FollowUpItem `json:"items"`
}

func (s *Server) queuedFor(target chatTarget) *queuedFrame {
	if s.opts.Queue == nil || target.ConversationID == "" {
		return nil
	}
	items, ok := s.opts.Queue.Items(target.ConversationID)
	if !ok {
		return nil
	}
	return &queuedFrame{Items: items}
}

// enqueueFollowUp queues text behind the running turn (the caller validated text).
func (s *Server) enqueueFollowUp(w http.ResponseWriter, dev Device, instance, paneID string, target chatTarget, text string) {
	if s.opts.Queue == nil || target.ConversationID == "" || target.Fingerprint == "" {
		writeError(w, http.StatusConflict, "queue_unavailable", "")
		return
	}
	if err := s.opts.Queue.Enqueue(instance, paneID, target.ConversationID, target.Fingerprint, text); err != nil {
		if errors.Is(err, ErrFollowUpFrozen) {
			writeError(w, http.StatusConflict, "queue_blocked", "")
			return
		}
		slog.Warn("remote host chat: enqueue failed", "device", dev.ID, "pane", paneID)
		writeError(w, http.StatusBadGateway, "queue_failed", truncate(err.Error(), 200))
		return
	}
	slog.Info("remote host chat follow-up queued", "device", dev.ID, "pane", paneID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "queued": true})
}

func (s *Server) handleChatQueueCancel(w http.ResponseWriter, r *http.Request, dev Device) {
	_, paneID, target, ok := s.sendTarget(w, r)
	if !ok {
		return
	}
	if s.opts.Queue == nil || target.ConversationID == "" {
		writeError(w, http.StatusConflict, "queue_unavailable", "")
		return
	}
	if !writeWithdrawn(w, s.opts.Queue.Cancel(target.ConversationID)) {
		return
	}
	slog.Info("remote host chat follow-up cancelled", "device", dev.ID, "pane", paneID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleChatQueueItemCancel(w http.ResponseWriter, r *http.Request, dev Device) {
	_, paneID, target, ok := s.sendTarget(w, r)
	if !ok {
		return
	}
	if s.opts.Queue == nil || target.ConversationID == "" {
		writeError(w, http.StatusConflict, "queue_unavailable", "")
		return
	}
	if !writeWithdrawn(w, s.opts.Queue.CancelItem(target.ConversationID, r.PathValue("requestID"))) {
		return
	}
	slog.Info("remote host chat follow-up cancelled", "device", dev.ID, "pane", paneID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// writeWithdrawn maps a withdraw result onto the wire (nil = carried out,
// ErrFollowUpNone → 404 no_queued, ErrFollowUpCommitted → 409
// already_delivered, anything else 502 queue_failed) and reports whether the
// caller may answer ok.
func writeWithdrawn(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, ErrFollowUpNone):
		writeError(w, http.StatusNotFound, "no_queued", "")
	case errors.Is(err, ErrFollowUpCommitted):
		writeError(w, http.StatusConflict, "already_delivered", "")
	default:
		writeError(w, http.StatusBadGateway, "queue_failed", truncate(err.Error(), 200))
	}
	return false
}
