/**
 * [INPUT]: Shell 的 chatBridgeState/OMP chatStream、现有 chat:updated 序号与 MyGo typed events
 * [OUTPUT]: chat:delta 有序尾部 patch；50ms 批合并、缺帧回退 Snapshot，完整 Chat 状态只由 chat:updated 承担
 * [POS]: WebView 增量展示优化（SC-04）；仅当已发布同会话 baseline 时传输小片段
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package nativeui

import (
	"reflect"
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

// chatDeltaPatch is allowed only when the untouched prefix and *all* other
// visible metadata match the last projection actually delivered. A rolling
// transcript can keep the same length while replacing its head; blindly
// replacing the last three turns would otherwise leave stale older messages.
func chatDeltaPatch(base, next ChatBridgeState) (ChatDelta, bool) {
	const tailLimit = 3
	start := max(0, len(next.Turns)-tailLimit)
	if base.ConversationID != next.ConversationID || start > len(base.Turns) {
		return ChatDelta{}, false
	}
	for i := 0; i < start; i++ {
		if !reflect.DeepEqual(base.Turns[i], next.Turns[i]) {
			return ChatDelta{}, false
		}
	}
	// If disposition, queue, interaction cards or presentation settings
	// changed, a delta alone would leave the page with stale metadata.
	oldMeta, newMeta := base, next
	oldMeta.Seq, newMeta.Seq = 0, 0
	oldMeta.Turns, newMeta.Turns = nil, nil
	if !reflect.DeepEqual(oldMeta, newMeta) {
		return ChatDelta{}, false
	}
	return ChatDelta{
		BaseSeq: base.Seq, Seq: next.Seq, ConversationID: next.ConversationID,
		Start: start, Turns: next.Turns[start:],
	}, true
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

// emitChatDelta falls back to a full snapshot when the prior delivered
// projection cannot be patched safely (first bind, rolling window, metadata).
func (s *Shell) emitChatDelta() {
	if s.win == nil || s.chatLiveProvider != history.AgentOMP || s.chatStream == nil {
		return
	}
	state := s.chatBridgeState()
	if s.chatLastPublishedState == nil {
		s.emitChatBridgeState(state)
		return
	}
	patch, ok := chatDeltaPatch(*s.chatLastPublishedState, state)
	if !ok {
		s.emitChatBridgeState(state)
		return
	}
	s.chatLastPublishedState = &state
	_ = chatDelta.Emit(s.win, patch)
}
