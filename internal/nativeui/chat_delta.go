/**
 * [INPUT]: Shell 的 chatBridgeState/OMP chatStream、现有 chat:updated 序号与 MyGo typed events
 * [OUTPUT]: chat:delta 有序尾部 patch；50ms 批合并、缺帧回退 Snapshot，完整 Chat 状态只由 chat:updated 承担
 * [POS]: WebView 增量展示优化（SC-04）；仅当已发布同会话 baseline 时传输小片段
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package nativeui

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/history"
)

// ChatDelta is a suffix replacement of the visible turn window, NOT a
// source transcript mutation. The receiver must have exactly BaseSeq or
// obtain a canonical chat.snapshot() before applying any later patch.
type ChatDelta struct {
	BaseSeq        uint64                 `json:"baseSeq"`
	Seq            uint64                 `json:"seq"`
	ConversationID string                 `json:"conversationId"`
	Start          int                    `json:"start"`
	Turns          []chatsession.ChatTurn `json:"turns"`
}

var chatDelta = mygo.NewEvent[ChatDelta]("chat:delta")

// chatDeltaPatch carries no headers/queue/settings and transmits at most the
// last three turns. A suffix of three covers transcript-reconciled last turns,
// the in-flight token row and pending echo without copying the whole log.
func chatDeltaPatch(state ChatBridgeState, base uint64) ChatDelta {
	const tailLimit = 3
	start := max(0, len(state.Turns)-tailLimit)
	return ChatDelta{
		BaseSeq: base, Seq: state.Seq, ConversationID: state.ConversationID,
		Start: start, Turns: state.Turns[start:],
	}
}

// publishChatStreamDelta is only called on the UI lane after an accepted OMP
// extension event. It does not suppress full chat:updated notifications.
func (s *Shell) publishChatStreamDelta() {
	if s.win == nil || s.chatLiveProvider != history.AgentOMP || s.chatStream == nil {
		return
	}
	if s.chatDeltaTimer != nil {
		return
	}
	s.chatDeltaTimer = time.AfterFunc(50*time.Millisecond, func() {
		s.applyOnWindow(func() {
			s.chatDeltaTimer = nil
			s.emitChatDelta()
		})
	})
}

// emitChatDelta will deliver a full snapshot if there is no published
// baseline for this conversation (e.g. first chat frame after binding).
func (s *Shell) emitChatDelta() {
	if s.win == nil || s.chatLiveProvider != history.AgentOMP || s.chatStream == nil {
		return
	}
	if s.chatLastPublishedSeq == 0 || s.chatLastPublishedConversation != s.chatConversationID {
		s.emitChatBridge()
		return
	}
	state := s.chatBridgeState()
	patch := chatDeltaPatch(state, s.chatLastPublishedSeq)
	s.chatLastPublishedSeq = patch.Seq
	_ = chatDelta.Emit(s.win, patch)
}
