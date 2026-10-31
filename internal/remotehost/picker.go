package remotehost

import (
	"context"
	"log/slog"
	"time"

	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/conversation"
)

/**
 * [INPUT]: chatsession.DetectPicker（与桌面共用的编号列表解析）、Backend.ReadPane、chat_send 发出 picker 命令后的 armPicker
 * [OUTPUT]: askCursor 上的 picker 观察窗：derivePicker（出现窗 6 s → 在屏期间持续读 → 离屏即撤卡，最长 10 min）、pickerWatching
 * [POS]: 手机 Chat 的 Claude Code / Codex `/model`、`/permissions` 卡：只在 Host 刚替用户发出 picker 命令后才读屏，不是对任意提示的猜测；
 *        卡的 PendingInteraction 带 Picker/Commit/EscapeOption，答题仍走 handleChatAnswer 的 AnswerKeys（Claude 用 `s`＝仅本会话，Close＝Escape），Herdr 拒绝原样回传；屏幕内容永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	pickerAppear  = 6 * time.Second
	pickerMaxLife = 10 * time.Minute
)

// armPicker opens the watch window for a picker the agent is about to draw.
// The caller holds cur.mu or owns cur exclusively.
func (s *Server) armPicker(cur *askCursor) {
	cur.pickerUntil = s.opts.Now().Add(pickerAppear)
	cur.pickerAt = time.Time{}
	cur.pickerLive = false
}

// pickerWatching reports whether a picker is awaited or on show, so the chat
// stream keeps re-deriving and a non-blocked agent may still carry a card.
func (s *Server) pickerWatching(instance, paneID string) bool {
	s.mu.Lock()
	cur := s.asks[instance+"\x00"+paneID]
	s.mu.Unlock()
	if cur == nil {
		return false
	}
	cur.mu.Lock()
	defer cur.mu.Unlock()
	return cur.pickerLive || s.opts.Now().Before(cur.pickerUntil)
}

// derivePicker returns the picker card, handled=true when the watch owns the
// answer (a card, or "no card yet"); handled=false when no watch is active.
// The caller holds cur.mu.
func (s *Server) derivePicker(ctx context.Context, instance, paneID string, cur *askCursor, now time.Time) (conversation.PendingInteraction, bool) {
	if !cur.pickerLive && !now.Before(cur.pickerUntil) {
		return conversation.PendingInteraction{}, false
	}
	if cur.pickerLive && now.Sub(cur.pickerSince) > pickerMaxLife {
		cur.pickerLive, cur.pickerUntil = false, time.Time{}
		return conversation.PendingInteraction{}, false
	}
	if now.Sub(cur.pickerAt) >= screenReadEvery {
		cur.pickerAt = now
		rctx, cancel := context.WithTimeout(ctx, screenReadBudget)
		screen, err := s.opts.Backend.ReadPane(rctx, instance, paneID)
		cancel()
		switch {
		case err != nil:
			slog.Warn("remote host chat: picker read failed", "instance", instance, "pane", paneID)
		default:
			if inter, hit := chatsession.DetectPicker(screen); hit {
				if !cur.pickerLive {
					cur.pickerSince = now
				}
				inter.ID = interactionID(paneID, "picker", 0, inter)
				cur.pickerInter, cur.pickerLive = inter, true
			} else if cur.pickerLive {
				cur.pickerLive, cur.pickerUntil = false, time.Time{} // the picker left the screen: retire the card
			}
		}
	}
	if !cur.pickerLive {
		return conversation.PendingInteraction{}, true
	}
	if cur.pickerInter.ID == cur.resolvedID && now.Sub(cur.resolvedAt) < resolvedSuppress {
		return conversation.PendingInteraction{}, true
	}
	return cur.pickerInter, true
}
