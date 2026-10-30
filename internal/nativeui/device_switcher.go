package nativeui

/**
 * [INPUT]: 依赖 mygo/ui 的 ButtonBase/Popover/Scroll/ContextMenu/Menu、controls.go 的 searchField（经 switcherSearch）、tooltip.go 的 tipped、breadcrumb_switcher 的 crumbItemRow/switcherListMaxHeight、device_state 的状态与派生、device_switch/device_recover/device_add 的动作
 * [OUTPUT]: 对外提供 deviceSwitcher（侧栏页脚唯一的设备+会话切换芯片及其向上弹出的统一面板：This Mac 的会话（右键 Rename/Delete，New Session…）、Devices 的两行设备行（状态点 + hover ⋯ 系统菜单）、Check Devices 钮（对未在用的设备真连检查）、Add Device…；≥10 项出搜索；最窄侧栏上会话先让位、本机名不被截；面板至少与芯片同宽（MyGo Popover 的规则），宽侧栏上行跟着撑满不留空槽）与 deviceMenu（⋯ 与右键共用的设备菜单：Switch/Sign In/Set Up/Check Again/Stop Waiting/Rename/Enable·Disable/Forget）
 * [POS]: 页脚呈现层；只读 Shell 的实例列表与 device_state 派生，所有动作委托给 device_* 流程文件；设备档案与会话生命周期权威仍在 Herdr，这里不拥有任何运行时
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
)

// deviceRowHeight is a device row: its name over its state.
const deviceRowHeight float32 = 40

// deviceChipFace is what the footer chip shows of the device in use.
type deviceChipFace struct {
	title, sub string
	glyph      *ui.SVG
	status     deviceStatus
	dot        bool // remote devices carry their state dot
}

// deviceChip reads the device in use. Nothing in use keeps the old chooser
// wording; this Mac shows its session behind the name; a remote device shows
// its name and state (its one session is pinned, so it is not repeated).
func (s *Shell) deviceChip() deviceChipFace {
	active := s.activeInstanceInfo()
	switch {
	case active == nil:
		return deviceChipFace{title: "Choose Session", glyph: iconLaptop}
	case active.Machine != nil:
		return deviceChipFace{title: active.DisplayName, glyph: iconServer, status: s.deviceStatusFor(*active), dot: true}
	}
	return deviceChipFace{title: "This Mac", sub: active.DisplayName, glyph: iconLaptop}
}

// deviceSwitcher is the sidebar foot's chip: the device in use, its session
// behind it, and one panel (opening upward) that switches either.
func (s *Shell) deviceSwitcher(c *ui.Context) {
	t := c.Theme()
	d := &s.devices
	face := s.deviceChip()
	switching := d.switching != nil
	var chip ui.Element
	ui.Box(c).Grow(1).Children(func() {
		chip = ui.ButtonBase(c.Key("device-switcher")).
			FillWidth().
			Height(Controls().RegularHeight).
			Radius(Radius().Row).
			Padding(0, 8).
			Gap(6).
			AlignItems(ui.Center).
			Label("Switch device or session")
		tipped(c, chip, "Switch device or session")
		if chip.Hovered() {
			chip.Background(t.SurfaceHover)
		}
		chip.Children(func() {
			ui.Box(c).Size(14, 14).Shrink(0).Center().Children(func() {
				if switching {
					ui.Spinner(c).Size(12, 12)
					return
				}
				markView(c, visualMark{svg: face.glyph}, 14, t.TextMuted)
				if face.dot {
					ui.Box(c).Absolute().Left(9).Top(7).Children(func() { deviceDot(c, face.status) })
				}
			})
			// On the narrowest sidebar the session behind "This Mac" gives way
			// first; the name only gives way when it is all there is.
			titleShrink := float32(1)
			if face.sub != "" {
				titleShrink = 0
			}
			ui.Text(c, face.title).FontSize(Typography().Caption).TextColor(t.Text).
				SingleLine().Ellipsis("…").Shrink(titleShrink).MinWidth(0)
			if face.sub != "" {
				ui.Text(c, "· "+face.sub).FontSize(Typography().Caption).TextColor(t.TextMuted).
					SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			}
			ui.Box(c).Grow(1).PassThrough()
			ui.Icon(c, iconChevronDown).Size(10, 10).TextColor(t.TextMuted).Shrink(0)
		})
	})
	wasOpen := d.open
	toggleMenu(&d.open, chip)
	if d.open && !wasOpen {
		d.filter = ""
		// Availability checks are fresh SSH round trips; opening the panel is
		// the natural moment to refresh them (throttled, background).
		s.refreshMachineStatus(false)
	}
	if d.open {
		ui.Popover(c, chip, &d.open, func() { s.devicePanel(c, chip.Bounds().W) })
	}
}

// devicePanelWidth is the panel's own width; a chip wider than that (a wide
// sidebar) widens the panel to the chip instead.
const devicePanelWidth = float32(300)

// deviceDot is a device state's small round marker, drawn like the Agent
// state dot beside it in every other switcher.
func deviceDot(c *ui.Context, status deviceStatus) {
	t := c.Theme()
	ui.Box(c).Size(7, 7).Radius(3.5).Background(designTokens(t.Dark).StatusColor(status.tone(), t.Dark)).Shrink(0)
}

// devicePanel is the one panel: this Mac's sessions, the saved devices, and
// the way to add one.
func (s *Shell) devicePanel(c *ui.Context, chipWidth float32) {
	t := c.Theme()
	sp := Spacing()
	d := &s.devices
	local, machines := s.localInstances(), s.machineInstances()
	var shownLocal, shownMachines []herdr.Instance
	for _, instance := range local {
		if gitRefMatches(d.filter, instance.DisplayName) {
			shownLocal = append(shownLocal, instance)
		}
	}
	for _, instance := range machines {
		if gitRefMatches(d.filter, instance.DisplayName) {
			shownMachines = append(shownMachines, instance)
		}
	}
	// MyGo's Popover is at least as wide as its anchor (plus its own padding and
	// 1px border): the rows take that width up rather than leave a gutter.
	width := max(devicePanelWidth, chipWidth-2*(t.Space(1)+1))
	ui.Column(c).Width(width).Padding(sp.S).Gap(sp.XS).Children(func() {
		if len(local)+len(machines) >= deviceSearchFrom {
			if switcherSearch(c, &d.filter, "Search devices and sessions") {
				switch {
				case len(shownLocal) > 0:
					d.open = false
					s.openInstance(shownLocal[0].Name)
				case len(shownMachines) > 0:
					d.open = false
					s.switchToDevice(shownMachines[0].Name)
				}
				return
			}
		}
		ui.Scroll(c).FillWidth().MaxHeight(switcherListMaxHeight).Children(func() {
			ui.Column(c).FillWidth().Gap(1).Children(func() {
				sectionLabel(c, "This Mac")
				for _, instance := range shownLocal {
					s.localSessionRow(c, instance)
				}
				if d.filter == "" {
					create := crumbItemRow(c, visualMark{svg: iconPlus}, "New Session…", opUnknown, false)
					if create.Clicked() {
						d.open = false
						s.openTextDialog("create-workspace", "", "New Session", "Name", "")
					}
				}
				s.devicesHeader(c, len(machines))
				if len(machines) == 0 {
					ui.Text(c, "Add a device to work on another machine.").
						FontSize(Typography().Caption).TextColor(t.TextMuted).Padding(sp.XS, sp.M, sp.S)
				}
				if len(machines) > 0 && len(shownMachines) == 0 {
					ui.Text(c, "No matching devices").
						FontSize(Typography().Caption).TextColor(t.TextMuted).Padding(sp.XS, sp.M, sp.S)
				}
				for _, instance := range shownMachines {
					s.deviceRow(c, instance)
				}
			})
		})
		ui.Divider(c)
		add := crumbItemRow(c, visualMark{svg: iconPlus}, "Add Device…", opUnknown, false)
		if add.Clicked() {
			s.openDeviceAdd("")
		}
	})
}

// devicesHeader is the Devices section label with the check button at its
// trailing edge: one real try of every saved device that is not in use.
func (s *Shell) devicesHeader(c *ui.Context, count int) {
	k := gorexColorsOf(c.Theme().Dark)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		sectionLabel(c, "Devices")
		ui.Box(c).Grow(1).PassThrough()
		if count == 0 {
			return
		}
		ui.Box(c).Size(gorexRowBtn+2, gorexRowBtn).Shrink(0).Center().Children(func() {
			if s.machineStatusRunning || s.devices.checkingAny() {
				ui.Spinner(c).Size(12, 12)
				return
			}
			if gorexIconButton(c, k, iconRefresh, "Check Devices", "Check Devices", false, gorexRowBtn, gorexRowGlyph).Clicked() {
				s.checkDevices()
			}
		})
	})
}

// localSessionRow is one of this Mac's Herdr sessions: a click opens it; the
// context menu renames or deletes it.
func (s *Shell) localSessionRow(c *ui.Context, instance herdr.Instance) {
	row := crumbItemRow(c, visualMark{svg: iconWorkspace}, instance.DisplayName, opUnknown, instance.Name == s.activeInstance)
	if row.Clicked() {
		s.devices.open = false
		s.openInstance(instance.Name)
	}
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("Rename Session…").Chosen() {
			s.devices.open = false
			s.openTextDialog("rename-workspace", instance.Name, "Rename Session", "Name", instance.DisplayName)
		}
		if !instance.Default {
			if m.Item("Delete Session…").Chosen() {
				s.devices.open = false
				s.openConfirm("delete-workspace", instance.Name, "Delete Session?", "This stops and deletes the Herdr session. Runtime data owned by that session will no longer be available.")
			}
		}
	})
}

// deviceRow is one saved device: its name over its state, a state dot at the
// glyph's corner, and the actions behind the ⋯ at the trailing edge (the same
// menu is the row's right click). A click switches to it.
func (s *Shell) deviceRow(c *ui.Context, instance herdr.Instance) {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	machine := *instance.Machine
	facts := s.deviceFactsFor(instance)
	status := deviceStatusOf(facts)
	selected := instance.Name == s.activeInstance
	sub := status.label()
	if s.devices.waiting[machine.ID] != nil {
		sub = "Waiting for Terminal…"
	}
	build := s.deviceMenu(instance)
	muted := status == deviceDisabled

	var row ui.Element
	ui.Box(c).Children(func() {
		row = ui.ButtonBase(c.Key("device:"+machine.ID)).
			FillWidth().
			Height(deviceRowHeight).
			Radius(Radius().Row).
			Padding(0, 8).
			Gap(7).
			AlignItems(ui.Center).
			Label(instance.DisplayName + ", " + sub)
		tipped(c, row, s.deviceTooltip(machine, facts))
		if tint, ok := rowTint(tokens, selected, row.Hovered()); ok {
			row.Background(tint)
		}
		row.ContextMenu(build)
		row.Children(func() {
			ui.Box(c).Size(14, 14).Shrink(0).Children(func() {
				ui.Icon(c, iconServer).Size(14, 14).TextColor(t.TextMuted)
				ui.Box(c).Absolute().Left(9).Top(7).Children(func() { deviceDot(c, status) })
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
				name := ui.Text(c, instance.DisplayName).FontSize(Typography().Body).SingleLine().Ellipsis("…")
				if muted {
					name.TextColor(t.TextMuted)
				}
				ui.Text(c, sub).FontSize(Typography().Caption).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
			})
			var more ui.Element
			ui.Box(c).Size(gorexRowBtn+2, gorexRowBtn).Shrink(0).Center().Children(func() {
				more = gorexIconButton(c, gorexColorsOf(t.Dark), iconActions, "Device Actions", "Device Actions", false, gorexRowBtn, gorexRowGlyph)
				// Always built — the menu's choice is read on the frame after
				// it closes, when the pointer is no longer over the row — but
				// drawn only while the row is the one in hand.
				if !row.Hovered() && !selected {
					more.Opacity(0)
				}
			})
			more.Menu(build)
		})
	})
	if row.Clicked() {
		s.devices.open = false
		s.switchToDevice(instance.Name)
	}
}

// deviceTooltip is a device row's hover text: where it points, and why its
// state is what it is when that is known.
func (s *Shell) deviceTooltip(machine herdr.Machine, facts deviceFacts) string {
	text := machine.Target
	if machine.Session != "" {
		text += " · session " + machine.Session
	}
	if detail := facts.detail(); detail != "" {
		text += " — " + detail
	}
	return text
}

// recoveryOf names the recovery a device's state asks for: the device in use
// that was lost asks for what its loss asks for.
func (s *Shell) recoveryOf(instance herdr.Instance, status deviceStatus) (signIn, setUp bool) {
	if status == deviceLost {
		if lost := s.devices.lostFor(instance.Name); lost != nil {
			signIn = lost.kind == herdr.FailureAuth || lost.kind == herdr.FailureHostKey
			setUp = lost.kind == herdr.FailureServer
		}
		return signIn, setUp
	}
	return status.needsSignIn(), status.needsSetUp()
}

// deviceMenu builds a device's menu (the ⋯ button and the row's right click
// share it): the recovery its state asks for first, then the management
// actions. Forget and Disable of the device in use return the window to this
// Mac (device_switch.go).
func (s *Shell) deviceMenu(instance herdr.Instance) func(m *ui.Menu) {
	machine := *instance.Machine
	label := instance.DisplayName
	return func(m *ui.Menu) {
		status := s.deviceStatusFor(instance)
		waiting := s.devices.waiting[machine.ID] != nil
		signIn, setUp := s.recoveryOf(instance, status)
		if waiting {
			if m.Item("Stop Waiting").Chosen() {
				s.stopDeviceTask(machine.ID)
			}
			m.Separator()
		}
		if !machine.Enabled {
			if m.Item("Enable").Chosen() {
				s.setDeviceEnabled(machine.ID, true)
			}
		} else {
			if instance.Name != s.activeInstance && m.Item("Switch to "+label).Chosen() {
				s.devices.open = false
				s.switchToDevice(instance.Name)
			}
			if signIn && !waiting && m.Item("Sign In…").Chosen() {
				s.signInDevice(machine.ID)
			}
			if setUp && !waiting && m.Item("Set Up…").Chosen() {
				s.setUpDevice(machine.ID)
			}
			if m.Item("Check Again").Chosen() {
				s.checkDevice(machine.ID)
			}
		}
		m.Separator()
		if m.Item("Rename…").Chosen() {
			s.devices.open = false
			s.openTextDialog("rename-machine", machine.ID, "Rename Device", "Name", machine.Label)
		}
		if machine.Enabled && m.Item("Disable").Chosen() {
			s.setDeviceEnabled(machine.ID, false)
		}
		if m.Item("Forget…").Chosen() {
			s.devices.open = false
			s.openConfirm("forget-machine", machine.ID, "Forget "+strings.TrimSpace(label)+"?",
				"This removes the saved SSH machine from Herdr. The remote session and its panes keep running on that device; Solco disconnects from it.")
		}
	}
}
