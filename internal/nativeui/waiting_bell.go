package nativeui

/**
 * [INPUT]: 依赖 Shell.BuildStatusCenterSnapshot / waitingExtras（含 Feedback 待答数）、quick_panel.go 的 toggleQuickPanelAt、gorexIconButtonCount（角标图标按钮）、iconBell
 * [OUTPUT]: 对外提供 Shell.waitingTotal（托盘标题、Dock 角标与侧栏铃铛共用的那一个「等你」数字）、Shell.sidebarBell（搜索行里的铃铛：角标 = 该数字，点击打开与菜单栏图标同一个 Agent 浮层）
 * [POS]: 侧栏搜索行右端的入口（2026-11-09 决策）：不另造浮层与计数，只是同一个等待数字与同一个 quick panel 的第二个锚点
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"strconv"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// waitingTotal is the one number of things that wait for the user: what the
// menu-bar title and the Dock badge show — attention + review of the status
// center, the Assistants' inbox, unopened Automation runs and Agent questions
// in the Feedback inbox. Window lane only.
func (s *Shell) waitingTotal() int {
	counts := TrayCountsFromSnapshot(s.BuildStatusCenterSnapshot())
	attention, review := s.waitingExtras()
	return counts.Attention + counts.Review + attention + review
}

// bellTip words the bell's tooltip from the number it carries.
func bellTip(n int) string {
	if n <= 0 {
		return "Nothing waiting for you"
	}
	return strconv.Itoa(n) + " waiting for you"
}

// sidebarBell is the bell at the right end of the sidebar's search row: its
// badge is the waiting number, a click opens the Agent panel beneath it — the
// same panel and the same open/close rule as the menu-bar item.
func (s *Shell) sidebarBell(c *ui.Context, k *gorexColors) {
	n := s.waitingTotal()
	bell := gorexIconButtonCount(c, k, iconBell, "Notifications", bellTip(n), s.quickPanel != nil, gorexIconBtn, gorexIconGlyph, n)
	if !bell.Clicked() || s.win == nil {
		return
	}
	r := bell.Bounds()
	s.toggleQuickPanelAt(mygo.FlyoutOptions{
		Parent:    s.win,
		Anchor:    mygo.Rectangle{X: int(r.X), Y: int(r.Y), Width: int(r.W), Height: int(r.H)},
		Placement: mygo.PlacementBottom,
		Gap:       quickPanelGap,
	})
}
