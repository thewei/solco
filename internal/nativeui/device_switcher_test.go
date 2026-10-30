package nativeui

/**
 * [INPUT]: device_switcher.go 的页脚芯片/统一面板/设备行菜单，device_add.go 的 Add Device 面板，headless ui.Tester
 * [OUTPUT]: 芯片读数（本机+会话/远程设备+状态/未选；最窄侧栏上本机名不被截、会话先让位）、宽侧栏上面板行撑满、面板分区与行状态、行菜单随状态给出恢复动作、Add 面板校验与流程的回归测试
 * [POS]: 设备切换器呈现层的测试；只渲染与点击，动作落到纯状态上，不起 SSH、不调 Herdr
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
)

func deviceTester(t *testing.T, s *Shell) *ui.Tester {
	t.Helper()
	s.router.Replace(routeWorkspace)
	tester := ui.NewTester(s.View, 1200, 800)
	settleFrames(tester, 3)
	return tester
}

func TestDeviceChipReadsTheDeviceInUse(t *testing.T) {
	// This Mac: its name, with the session behind it.
	s := deviceTestShell(t)
	tester := deviceTester(t, s)
	for _, want := range []string{"This Mac", "· Default"} {
		if !tester.HasText(want) {
			t.Fatalf("local chip missing %q; texts=%q", want, tester.Texts())
		}
	}

	// A device: its own name; this Mac is no longer named.
	s = deviceTestShell(t)
	s.activeInstance = testDeviceKey(testDeviceA)
	tester = deviceTester(t, s)
	if !tester.HasText("Build box") || tester.HasText("This Mac") {
		t.Fatalf("device chip wrong; texts=%q", tester.Texts())
	}

	// Nothing in use: the old chooser wording.
	s = deviceTestShell(t)
	s.activeInstance = ""
	tester = deviceTester(t, s)
	if !tester.HasText("Choose Session") {
		t.Fatalf("empty chip must say Choose Session; texts=%q", tester.Texts())
	}
}

// On the narrowest sidebar the chip cannot show "This Mac · Default" whole:
// the session behind the name gives way, the name stays as wide as it is on the
// widest one.
func TestDeviceChipKeepsThisMacWholeOnTheNarrowestSidebar(t *testing.T) {
	widths := func(sidebar float32) (title, session float32) {
		s := deviceTestShell(t)
		s.sidebarW = sidebar
		tester := deviceTester(t, s)
		name, ok := tester.Find("This Mac")
		behind, behindOK := tester.Find("· Default")
		if !ok || !behindOK {
			t.Fatalf("sidebar %v: chip missing its texts; texts=%q", sidebar, tester.Texts())
		}
		return name.W, behind.W
	}
	narrowTitle, narrowSession := widths(sidebarMinWidth)
	wideTitle, wideSession := widths(sidebarMaxWidth)
	if narrowTitle != wideTitle {
		t.Fatalf("This Mac is %v wide on the narrowest sidebar and %v on the widest: the name was cut", narrowTitle, wideTitle)
	}
	if narrowSession >= wideSession {
		t.Fatalf("the session reads %v wide on the narrowest sidebar and %v on the widest: nothing gave way", narrowSession, wideSession)
	}
}

// MyGo's Popover is at least as wide as its anchor. A chip wider than the
// panel's own width (a wide sidebar) widens the panel, and the rows follow it
// rather than stop short of its edge.
func TestDevicePanelRowsFillAChipWiderThanThePanel(t *testing.T) {
	right := func(sidebar float32) (rowEdge, chipWidth float32) {
		s := deviceTestShell(t)
		s.sidebarW = sidebar
		s.devices.open = true
		tester := deviceTester(t, s)
		chip, chipOK := tester.Find("Switch device or session")
		row, rowOK := tester.Find("Add Device…")
		if !chipOK || !rowOK {
			t.Fatalf("sidebar %v: chip or panel row missing; texts=%q", sidebar, tester.Texts())
		}
		return row.X + row.W, chip.W
	}
	narrowEdge, _ := right(sidebarMinWidth)
	wideEdge, wideChip := right(sidebarMaxWidth)
	const popoverChrome = 10 // the panel's own padding and 1px border, both sides
	if want := wideChip - devicePanelWidth - popoverChrome; wideEdge-narrowEdge < want-2 {
		t.Fatalf("rows reach %v on the widest sidebar and %v on the narrowest: they should have grown by about %v with the panel", wideEdge, narrowEdge, want)
	}
}

func TestDevicePanelListsSessionsAndDevices(t *testing.T) {
	s := deviceTestShell(t)
	s.machineStates = map[string]herdr.MachineState{testDeviceA: {Reachable: true, Status: "reachable"}}
	s.devices.open = true
	tester := deviceTester(t, s)
	for _, want := range []string{"This Mac", "Default", "Aux", "New Session…", "Devices", "Build box", "Online", "Old laptop", "Disabled", "Add Device…"} {
		if !tester.HasText(want) {
			t.Fatalf("panel missing %q; texts=%q", want, tester.Texts())
		}
	}
	if tester.HasText("Add a device to work on another machine.") {
		t.Fatal("the empty-devices hint shows with devices saved")
	}
	if _, ok := tester.Find("Search devices and sessions"); ok {
		t.Fatal("a four-row panel opens with a search field")
	}

	// No devices: the hint stands in for the rows, and no check button.
	s = deviceTestShell(t)
	s.instances = s.instances[:2]
	s.devices.open = true
	tester = deviceTester(t, s)
	if !tester.HasText("Add a device to work on another machine.") || !tester.HasText("Add Device…") {
		t.Fatalf("empty panel wrong; texts=%q", tester.Texts())
	}
	if tester.HasText("Check Devices") {
		t.Fatal("a check button with nothing to check")
	}
}

func TestDevicePanelSearchAppearsWithManyRows(t *testing.T) {
	s := deviceTestShell(t)
	for i := 0; i < deviceSearchFrom; i++ {
		s.instances = append(s.instances, herdr.Instance{Name: "extra-" + string(rune('a'+i)), DisplayName: "Extra " + string(rune('A'+i))})
	}
	s.devices.open = true
	tester := deviceTester(t, s)
	if !tester.HasText("Search devices and sessions") {
		t.Fatalf("a long list opens without a search field; texts=%q", tester.Texts())
	}
}

func TestDeviceRowStatesAndTooltipText(t *testing.T) {
	s := deviceTestShell(t)
	a := *s.machineInstanceByID(testDeviceA)
	if got := s.deviceTooltip(*a.Machine, s.deviceFactsFor(a)); got != "deploy@build · session default" {
		t.Fatalf("tooltip = %q", got)
	}
	s.devices.fail(testDeviceA, herdr.FailureAuth, "Permission denied (publickey).")
	if got := s.deviceTooltip(*a.Machine, s.deviceFactsFor(a)); got != "deploy@build · session default — Permission denied (publickey)." {
		t.Fatalf("tooltip with a failure = %q", got)
	}

	s.devices.open = true
	tester := deviceTester(t, s)
	if !tester.HasText("Sign-in needed") {
		t.Fatalf("a device with an auth failure must read Sign-in needed; texts=%q", tester.Texts())
	}
	s.devices.waiting = map[string]*deviceTask{testDeviceA: {kind: "sign-in", cancel: func() {}}}
	tester = deviceTester(t, s)
	if !tester.HasText("Waiting for Terminal…") {
		t.Fatalf("a device with a Terminal handoff must say so; texts=%q", tester.Texts())
	}
}

func TestDeviceRowMenuOffersTheRecoveryTheStateAsksFor(t *testing.T) {
	menuFor := func(s *Shell, label string) []string {
		t.Helper()
		s.devices.open = true
		tester := deviceTester(t, s)
		if err := tester.RightClick(label); err != nil {
			t.Fatal(err)
		}
		menu := tester.Menu()
		tester.CloseMenu()
		return menu
	}
	has := func(menu []string, want ...string) bool {
		for _, item := range want {
			if !containsText(menu, item) {
				return false
			}
		}
		return true
	}

	s := deviceTestShell(t)
	menu := menuFor(s, "Build box")
	if !has(menu, "Switch to Build box", "Check Again", "Rename…", "Disable", "Forget…") || has(menu, "Sign In…", "Set Up…", "Enable", "Stop Waiting") {
		t.Fatalf("idle device menu = %q", menu)
	}

	s = deviceTestShell(t)
	s.devices.fail(testDeviceA, herdr.FailureAuth, "Permission denied")
	if menu := menuFor(s, "Build box"); !has(menu, "Sign In…") || has(menu, "Set Up…") {
		t.Fatalf("auth-failed device menu = %q, want Sign In… only", menu)
	}

	s = deviceTestShell(t)
	s.devices.fail(testDeviceA, herdr.FailureServer, "open failed")
	if menu := menuFor(s, "Build box"); !has(menu, "Set Up…") || has(menu, "Sign In…") {
		t.Fatalf("no-server device menu = %q, want Set Up… only", menu)
	}

	s = deviceTestShell(t)
	if menu := menuFor(s, "Old laptop"); !has(menu, "Enable", "Rename…", "Forget…") || has(menu, "Switch to Old laptop", "Disable", "Check Again") {
		t.Fatalf("disabled device menu = %q", menu)
	}

	s = deviceTestShell(t)
	s.devices.waiting = map[string]*deviceTask{testDeviceA: {kind: "sign-in", cancel: func() {}}}
	s.devices.fail(testDeviceA, herdr.FailureAuth, "Permission denied")
	if menu := menuFor(s, "Build box"); !has(menu, "Stop Waiting") || has(menu, "Sign In…") {
		t.Fatalf("waiting device menu = %q, want Stop Waiting and no second Sign In…", menu)
	}

	// The device in use needs no "Switch to"; lost, it asks for its cause's recovery.
	s = deviceTestShell(t)
	s.activeInstance = testDeviceKey(testDeviceA)
	s.devices.lost = &deviceOutage{instance: s.activeInstance, kind: herdr.FailureAuth}
	s.devices.open = true
	tester := deviceTester(t, s)
	if err := tester.RightClick("Connection lost"); err != nil {
		t.Fatal(err)
	}
	menu = tester.Menu()
	tester.CloseMenu()
	if !has(menu, "Sign In…") || has(menu, "Switch to Build box") {
		t.Fatalf("lost device-in-use menu = %q", menu)
	}
}

// Picking a disabled device explains instead of dialing.
func TestPickingADisabledDeviceExplainsInsteadOfDialing(t *testing.T) {
	s := deviceTestShell(t)
	s.devices.open = true
	tester := deviceTester(t, s)
	if err := tester.Click("Old laptop"); err != nil {
		t.Fatal(err)
	}
	if s.devices.switching != nil || s.activeInstance != "default" {
		t.Fatalf("a disabled device was dialed: switching=%v active=%q", s.devices.switching, s.activeInstance)
	}
	settleFrames(tester, 2)
	if !tester.HasText("Old laptop is disabled. Enable it from the device switcher.") {
		t.Fatalf("no explanation toast; texts=%q", tester.Texts())
	}
}

func TestAddDeviceRowOpensTheSheet(t *testing.T) {
	s := deviceTestShell(t)
	s.devices.open = true
	tester := deviceTester(t, s)
	if err := tester.Click("Add Device…"); err != nil {
		t.Fatal(err)
	}
	if !s.devices.add.open || s.devices.open {
		t.Fatalf("sheet open=%v, panel open=%v; want the sheet open and the panel closed", s.devices.add.open, s.devices.open)
	}
	settleFrames(tester, 3)
	for _, want := range []string{"Add Device", "SSH target", "Remote session", "Name (optional)", "Cancel"} {
		if !tester.HasText(want) {
			t.Fatalf("sheet missing %q; texts=%q", want, tester.Texts())
		}
	}
}
