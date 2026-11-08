package nativeui

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 chatsession.DetectTerminalDialog（Cursor Agent / Antigravity CLI 只能在终端回答的对话框）、herdr 面板可见文本读取
 * [OUTPUT]: 对外提供 Shell.checkTerminalDialog，使 chatDispositionNow 在该对话框存在时返回 NeedsTerminal（页面显示 Open Terminal 卡）
 * [POS]: 桌面 Chat 的"终端保持"：这些对话框期间 Herdr 状态是 idle/done（不是 blocked），状态边沿也不会再来，所以只在绑定、投影落地、
 *        以及对话框存在期间的 2 秒复查链上读一次屏；只对 Cursor/agy 且 agent 非 working 时读；只留一句固定提示语，屏幕文本不入日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	terminalDialogMinGap  = 1500 * time.Millisecond
	terminalDialogRecheck = 2 * time.Second
)

// checkTerminalDialog reads the bound chat pane once (debounced) when its
// agent is a Cursor/Antigravity one that is not working, and records whether a
// terminal-only dialog is on screen. UI lane.
func (s *Shell) checkTerminalDialog() {
	paneID := s.chatPaneID
	if paneID == "" || s.chatReadOnly || s.runtime == nil || s.activeInstance == "" || s.chatTerminalBusy {
		s.dropTerminalDialog()
		return
	}
	provider, _ := s.chatProvider()
	if provider != history.AgentCursor && provider != history.AgentAntigravity {
		s.dropTerminalDialog()
		return
	}
	if agent := s.paneAgent(paneID); agent != nil && agent.Status == "working" {
		s.dropTerminalDialog()
		return
	}
	if time.Since(s.chatTerminalReadAt) < terminalDialogMinGap {
		return
	}
	s.chatTerminalReadAt = time.Now()
	s.chatTerminalBusy = true
	instance := s.activeInstance
	s.dispatch(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		screen, err := s.runtime.ReadPane(ctx, instance, paneID, "visible")
		s.applyOnWindow(func() {
			s.chatTerminalBusy = false
			if err != nil {
				slog.Warn("terminal dialog read failed", "pane", paneID)
				return
			}
			if paneID != s.chatPaneID || instance != s.activeInstance {
				return
			}
			reason, hit := chatsession.DetectTerminalDialog(screen)
			if !hit {
				s.dropTerminalDialog()
				return
			}
			changed := s.chatTerminalPane != paneID || s.chatTerminalReason != reason
			s.chatTerminalPane, s.chatTerminalReason = paneID, reason
			if changed {
				slog.Info("terminal dialog holds chat", "pane", paneID)
				s.publishChatBridge()
			}
			time.AfterFunc(terminalDialogRecheck, func() { s.applyOnWindow(s.checkTerminalDialog) })
		})
	})
}

// dropTerminalDialog clears the hold and tells the page when it was showing.
func (s *Shell) dropTerminalDialog() {
	if s.chatTerminalReason == "" {
		return
	}
	s.chatTerminalPane, s.chatTerminalReason = "", ""
	s.publishChatBridge()
}

// terminalDialogHeld reports whether the bound pane is parked on a
// terminal-only dialog.
func (s *Shell) terminalDialogHeld() bool {
	return s.chatTerminalReason != "" && s.chatTerminalPane == s.chatPaneID && strings.TrimSpace(s.chatPaneID) != ""
}

// terminalReason is the Open Terminal card's sentence while the hold is on.
func (s *Shell) terminalReason() string {
	if s.terminalDialogHeld() {
		return s.chatTerminalReason
	}
	return ""
}
