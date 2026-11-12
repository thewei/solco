// [INPUT]: 依赖 mygo.Tray 的 SetIcon/OnClick、statusTray 平台缝的 SetTitle/SetToolTip、TrayCounts/TrayTitle/TrayToolTip/TrayFingerprint（tray_model.go）、Shell.BuildStatusCenterSnapshot 与 Shell.ToggleQuickPanel
// [OUTPUT]: 导出 AttachTray（Shell）、TrayIconPNG、updateTray（Shell）；内部持有 statusTray 控制器
// [POS]: nativeui 托盘的绑定层。main.go 创建 mygo.Tray 后交 AttachTray；状态投影与图标在此收口（图标是嵌入的 Tabler message-chatbot 模板 PNG，2026-11-07 第三轮），点击进 quick_panel.go 的浮层。
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

package nativeui

import (
	_ "embed"
	"fmt"
	"log/slog"

	"github.com/egoist/mygo"
)

// trayPlatform is the MyGo tray seam (0.7 §17): production binds the real
// mygo.Tray; deterministic tests record applied projections.
type trayPlatform interface {
	SetTitle(title string)
	SetToolTip(tip string)
}

// statusTray owns the macOS menu-bar projection (0.7 §17-18): the shared
// StatusCenterSnapshot projects onto title/tooltip only when the tray
// fingerprint changes, so idle frames and navigation selection never rebuild
// the platform tray.
type statusTray struct {
	platform trayPlatform
	// applied reports whether any projection reached the platform; the
	// first apply always writes.
	applied     bool
	fingerprint string
	// remote names the phone on this Mac's screen ("" = none); a change re-projects like any other.
	remote string
	// extraAttention / extraReview are what the status-center snapshot does not
	// hold but the Header used to count and the title now shares with the Dock:
	// the Assistants' "needs you" inbox and the unopened Automation runs
	// (2026-11-07 round 3, one number). A change re-projects like any other.
	extraAttention, extraReview int
}

func newStatusTray(platform trayPlatform) *statusTray {
	return &statusTray{platform: platform}
}

// apply projects one snapshot onto the platform (0.7 §18): writes happen
// only when the tray fingerprint changed since the last apply.
func (t *statusTray) apply(snapshot StatusCenterSnapshot, connected bool) {
	fingerprint := TrayFingerprint(snapshot, connected) + "|rd=" + t.remote +
		fmt.Sprintf("|extra=%d/%d", t.extraAttention, t.extraReview)
	if t.applied && fingerprint == t.fingerprint {
		return
	}
	t.fingerprint = fingerprint
	t.applied = true
	counts := TrayCountsFromSnapshot(snapshot)
	counts.Attention += t.extraAttention
	counts.Review += t.extraReview
	title, tip := TrayRemoteDesktop(TrayTitle(counts), TrayToolTip(counts, connected), t.remote)
	t.platform.SetTitle(title)
	t.platform.SetToolTip(tip)
}

// AttachTray binds the macOS menu-bar tray (0.7 §17): the normal click
// toggles the §17.1 quick-panel flyout anchored beneath the menu-bar item;
// without a bound tray the in-window Status Center stays the surface. The
// click routes as an action — never a direct runtime mutation.
func (s *Shell) AttachTray(tray *mygo.Tray) {
	s.trayHandle = tray
	s.tray = newStatusTray(tray)
	// The status-first title is empty when nothing is actionable; the
	// template glyph is the always-visible menu-bar mark, so bind it once
	// at attach (0.7 §17).
	if png, err := TrayIconPNG(); err != nil {
		slog.Warn("render tray icon failed", "error", err)
	} else if err := tray.SetIcon(png, true); err != nil {
		slog.Warn("set tray icon failed", "error", err)
	}
	tray.OnClick(func() {
		s.applyOnWindow(func() {
			s.ToggleQuickPanel()
		})
	})
	s.updateTray()
}

// updateTray projects the current status-center snapshot onto the menu bar
// (0.7 §17-18). No-op when no tray is bound: headless sessions, tests, and
// platforms where tray creation failed keep the in-window Status Center as
// the only surface.
func (s *Shell) updateTray() {
	s.islandRefresh() // an open notch roster reads the same snapshot
	if s.tray == nil {
		return
	}
	s.tray.remote = s.remoteDesktopTrayLabel()
	s.tray.extraAttention, s.tray.extraReview = s.waitingExtras()
	s.tray.apply(s.BuildStatusCenterSnapshot(), !s.offline)
}

// trayIconPNG is the menu-bar mark (2026-11-07 round 3): Tabler's
// `message-chatbot` outline, MIT (assets/TABLER-LICENSE.txt), pre-rendered to
// one black-on-transparent 128×128 template PNG that macOS tints against the
// menu bar and scales to 18pt. One image, no per-state variant: the state is
// the title's number. The app icon stays the ram.
//
//go:embed assets/tray-message-chatbot.png
var trayIconPNG []byte

// TrayIconPNG returns the embedded menu-bar template glyph.
func TrayIconPNG() ([]byte, error) { return trayIconPNG, nil }
