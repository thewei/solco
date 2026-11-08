package nativeui

import (
	"log/slog"

	"github.com/egoist/mygo/ui"
)

/**
 * [INPUT]: 依赖 terminalSurface.takenOver（terminal.go 的 OnExit 在 attachConn 读到 "terminal attach taken over" 后置位）、Shell.takeBack、herdr.Manager.TerminalTakeoverCommand、syncTerminals
 * [OUTPUT]: 对外提供 takenOverBanner（卡片内的「手机已接管 · Take Back」条；Mac 首次 attach 就被已持有的客户端拒绝——“already has an attached client”——时同一个条改说 “Another device is using this terminal”）、takeBackTerminal（用户显式拿回）与 phoneReleasedTerminal（Host 的 OnControlReleased 接缝：手机放手后对仍被标记的卡片做一次普通 attach，绝不 takeover）
 * [POS]: 手机接管（Host `?mode=control`，2026-11-07 用户批准）的桌面一半：手机的 `control --takeover` 会让桌面 attach 退出；这里把死掉的 "[Disconnected]" 换成一个按钮，点击才以 `attach --takeover` 重新 attach（PTY 立刻回到桌面栅格）。任何自动 attach 仍绝不 takeover，也不自动重试，避免两端抢
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// takenOverBanner shows over a pane whose attach a phone replaced. It renders nothing otherwise.
func (s *Shell) takenOverBanner(c *ui.Context, surface *terminalSurface) {
	if !surface.takenOver {
		return
	}
	t := c.Theme()
	text := "Your phone took over this terminal"
	if surface.heldElsewhere {
		text = "Another device is using this terminal"
	}
	ui.Row(c.Key("taken-over")).Absolute().Left(16).Bottom(14).Padding(6, 6, 6, 12).Gap(10).AlignItems(ui.Center).
		Radius(7).Background(t.Surface).Border(1, t.Accent.Alpha(0.5)).Children(func() {
		ui.Text(c, text).FontSize(Typography().BodySmall).FontWeight(WeightSemibold).TextColor(t.Accent)
		if compactPrimaryButton(c, "Take Back").Clicked() {
			s.takeBackTerminal(surface.key)
		}
	})
}

// phoneReleasedTerminal is the Host's OnControlReleased seam: the phone gave its takeover up, so the card it had
// replaced gets an ordinary attach again — the pane is free, nothing is taken from anyone (never --takeover). A card
// that is not marked taken over (the user already took it back, or it never was) is left alone. Any goroutine.
func (s *Shell) phoneReleasedTerminal(instance, key string) {
	s.applyOnWindow(func() {
		surface := s.terminals[key]
		if s.activeInstance != instance || surface == nil || !surface.takenOver {
			return
		}
		_ = surface.term.Close()
		delete(s.terminals, key)
		slog.Info("reattach native terminal after phone release", "instance", instance, "pane_id", surface.paneID, "terminal_id", key)
		if err := s.syncTerminals(s.projection); err != nil {
			slog.Warn("reattach after phone release failed", "pane_id", surface.paneID, "error", err)
		}
	})
}

// takeBackTerminal re-attaches one terminal with --takeover, on the user's explicit request only.
func (s *Shell) takeBackTerminal(key string) {
	surface := s.terminals[key]
	if surface == nil || !surface.takenOver {
		return
	}
	if s.takeBack == nil {
		s.takeBack = make(map[string]bool)
	}
	s.takeBack[key] = true
	_ = surface.term.Close()
	delete(s.terminals, key)
	slog.Info("take back native terminal", "instance", s.activeInstance, "pane_id", surface.paneID, "terminal_id", key)
	if err := s.syncTerminals(s.projection); err != nil {
		slog.Warn("take back attach failed", "pane_id", surface.paneID, "error", err)
	}
}
