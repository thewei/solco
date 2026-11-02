package nativeui

/**
 * [INPUT]: 依赖 shortcuts.go 的菜单加速键常量/acceleratorKeycaps/有效绑定（shortcutKeycaps、sidebarToggleBinding、chatSwitchBinding）、tooltip.go 的 tipKeycaps 键帽、design_system 令牌、mygo ui.Modal
 * [OUTPUT]: 对外提供 Shell.shortcutsSheet（Help ▸ Keyboard Shortcuts，⌘/ 打开的速查表模态）与 shortcutSections（分组数据，纯数据可测）
 * [POS]: 快捷键的只读速查（2026-10-30 UX 审计第二轮，design: designs/ux-audit-2）：每个键帽都取自真实绑定/菜单加速键，不另写一份；只读，不提供改键
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"github.com/egoist/mygo/ui"
)

// shortcutRow is one line of the sheet: what it does and the keys that do it.
type shortcutRow struct {
	Title string
	Keys  []string
}

// shortcutSection is a titled group of rows.
type shortcutSection struct {
	Title string
	Rows  []shortcutRow
}

// shortcutSections is the sheet's content, resolved from the live bindings:
// the menu bar's accelerators and the route shortcuts as they are bound now.
func (s *Shell) shortcutSections() (left, right []shortcutSection) {
	row := func(title string, keys []string) shortcutRow { return shortcutRow{Title: title, Keys: keys} }
	accel := func(title, accelerator string) shortcutRow { return row(title, acceleratorKeycaps(accelerator)) }
	// Select Tab spells its digit run once: ⌥⌘1–9.
	selectTab := append(acceleratorKeycaps(selectTabAccelerator(1))[:2:2], "1–9")

	left = []shortcutSection{
		{Title: "Go to", Rows: []shortcutRow{
			accel("Workspace", accelWorkspace), accel("Changes", accelChanges), accel("Sessions", accelSessions),
			accel("Files", accelFiles), accel("Hosts", accelHosts), accel("Automations", accelAutomations),
		}},
		{Title: "Tabs", Rows: []shortcutRow{
			accel("New Tab", accelNewTab), accel("Close Tab", accelCloseTab), row("Select Tab", selectTab),
			row("Switch Agent", []string{"⌃", "⇥"}),
		}},
	}
	right = []shortcutSection{
		{Title: "Palette & window", Rows: []shortcutRow{
			row("Search", s.shortcutKeycaps(shortcutSearch)), accel("Command Center", accelCommandCenter),
			row("Toggle Sidebar", sidebarToggleBinding.keycaps()), row("Settings", s.shortcutKeycaps(shortcutSettings)),
			row("Back", s.shortcutKeycaps(shortcutBack)), row("Forward", s.shortcutKeycaps(shortcutForward)),
			row("Refresh", s.shortcutKeycaps(shortcutRefresh)),
		}},
		{Title: "Changes", Rows: []shortcutRow{
			row("Find in Diff", []string{"⌘", "F"}), row("Commit", []string{"⌘", "↩"}),
			row("Next / Previous File", []string{"J", "K"}), row("Wrap Long Lines", []string{"⌥", "Z"}),
		}},
		{Title: "Chat", Rows: []shortcutRow{
			row("Terminal ⇄ Chat", chatSwitchBinding.keycaps()),
			row("Recall Previous Prompt", []string{"↑"}),
			row("Stop the Turn", []string{"⌘", "."}),
			row("Dictate", []string{"⇧", "⌘", "M"}),
			row("Previous / Next Message", []string{"⌘", "↑", "↓"}),
			row("Find in Conversation", []string{"⌘", "F"}),
		}},
	}
	return left, right
}

// shortcutsSheet is the Keyboard Shortcuts modal: read-only, Esc or the
// backdrop closes it.
func (s *Shell) shortcutsSheet(c *ui.Context) {
	if !s.shortcutsOpen {
		return
	}
	left, right := s.shortcutSections()
	t := c.Theme()
	ui.Modal(c, &s.shortcutsOpen, func() {
		ui.Column(c).Width(620).MaxWidth(680).Gap(Spacing().L).Children(func() {
			modalTitle(c, "Keyboard Shortcuts")
			ui.Row(c).FillWidth().Gap(Spacing().XL).AlignItems(ui.Start).Children(func() {
				for _, column := range [][]shortcutSection{left, right} {
					ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(Spacing().M).Children(func() {
						for _, section := range column {
							ui.Column(c).FillWidth().Gap(Spacing().XS).Children(func() {
								ui.Text(c, section.Title).FontSize(Typography().Caption).FontWeight(WeightSemibold).
									TextColor(t.TextMuted).Padding(0, 0, Spacing().XS, 0)
								for _, r := range section.Rows {
									ui.Row(c).FillWidth().Height(Controls().RegularHeight).AlignItems(ui.Center).Gap(Spacing().M).Children(func() {
										ui.Text(c, r.Title).FontSize(Typography().Body).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
										tipKeycaps(c, r.Keys, true, t.Text)
									})
								}
							})
						}
					})
				}
			})
		})
	})
}
