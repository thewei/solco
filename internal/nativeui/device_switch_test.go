package nativeui

/**
 * [INPUT]: device_switch.go 的事务式切换（beginDeviceSwitch/finishDeviceSwitch/cancelDeviceSwitch）、失败分类与 toast、watcher 掉线/恢复钩子、横幅与页面遮罩，Command Center 的设备动作
 * [OUTPUT]: “失败/取消/过期结果不污染当前设备”“成功才换入”“掉线可见、可恢复”“命令面板能切设备/本机/新增”的回归测试
 * [POS]: 设备切换流程层的测试；用合成的 deviceDial 结果直接驱动 UI 线程半程，不起 SSH、不调 Herdr
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/commandcenter"
	"github.com/thewei/solco/internal/herdr"
)

func deviceProjection() herdr.Projection {
	return herdr.Projection{
		Protocol: 22,
		Projects: []herdr.Project{{ID: "r1", Label: "Remote project", TabCount: 1}},
		Tabs:     []herdr.Tab{{ID: "rt1", ProjectID: "r1", Label: "remote tab", PaneCount: 1}},
	}
}

func TestBeginDeviceSwitchRefusals(t *testing.T) {
	s := deviceTestShell(t)
	a, b := testDeviceKey(testDeviceA), testDeviceKey(testDeviceB)

	for _, name := range []string{"default", "gone", ""} {
		if s.beginDeviceSwitch(name) != nil {
			t.Errorf("beginDeviceSwitch(%q) must refuse a name that is not a saved device", name)
		}
	}
	if s.beginDeviceSwitch(b) != nil || s.devices.switching != nil {
		t.Fatal("a disabled device must not start a switch")
	}
	if !strings.Contains(s.pendingToast, "Old laptop is disabled") {
		t.Fatalf("toast = %q, want it to say the device is disabled", s.pendingToast)
	}

	first := s.beginDeviceSwitch(a)
	if first == nil || first.id != testDeviceA || first.label != "Build box" || s.devices.switching != first {
		t.Fatalf("beginDeviceSwitch(A) = %+v", first)
	}
	if s.beginDeviceSwitch(a) != nil {
		t.Fatal("the same pick while connecting must not start a second dial")
	}

	// The device in use needs no switch — unless it was lost.
	s.cancelDeviceSwitch()
	s.activeInstance = a
	if s.beginDeviceSwitch(a) != nil {
		t.Fatal("picking the device in use must do nothing")
	}
	s.devices.lost = &deviceOutage{instance: a, kind: herdr.FailureNetwork}
	if s.beginDeviceSwitch(a) == nil {
		t.Fatal("picking a lost device in use must reconnect it")
	}
}

// A failed dial leaves the window exactly as it was: same instance, not
// loading, not offline, no error text — only the device's own record and a
// toast with the recovery change.
func TestFailedDeviceSwitchLeavesTheWindowAlone(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantKind   herdr.FailureKind
		wantAction string
		wantText   string
	}{
		{"auth", errors.New("Permission denied (publickey)."), herdr.FailureAuth, "Sign In", "Sign in to Build box to connect"},
		{"host key", errors.New("Host key verification failed."), herdr.FailureHostKey, "Sign In", "Build box's host key isn't trusted yet"},
		{"network", errors.New("ssh: connect to host build port 22: Connection timed out"), herdr.FailureNetwork, "Check Again", "Couldn't reach Build box"},
		{"timeout", fmt.Errorf("dial: %w", context.DeadlineExceeded), herdr.FailureNetwork, "Check Again", "Couldn't reach Build box"},
		{"too old", fmt.Errorf("remote: %w", herdr.ErrProtocolTooOld), herdr.FailureServer, "Set Up", "Herdr on Build box is too old"},
		{"no server", errors.New("channel 3: open failed: connect failed: No such file"), herdr.FailureServer, "Set Up", "Herdr isn't running on Build box"},
		{"other", errors.New("something odd"), herdr.FailureOther, "Check Again", "Couldn't connect to Build box"},
	}
	for _, tc := range cases {
		s := deviceTestShell(t)
		generation, status := s.generation.Load(), s.status
		sw := s.beginDeviceSwitch(testDeviceKey(testDeviceA))
		s.finishDeviceSwitch(sw.token, deviceDial{err: tc.err})

		if s.activeInstance != "default" || s.loading || s.offline || s.errText != "" || s.status != status || s.bootstrapFailed {
			t.Errorf("%s: failed switch touched the window: active=%q loading=%v offline=%v err=%q status=%q", tc.name, s.activeInstance, s.loading, s.offline, s.errText, s.status)
		}
		if s.generation.Load() != generation {
			t.Errorf("%s: a failed switch must not invalidate the work in flight", tc.name)
		}
		if len(s.projection.Projects) != 1 || s.projection.Projects[0].ID != "w1" {
			t.Errorf("%s: the current projection was replaced", tc.name)
		}
		if s.devices.switching != nil {
			t.Errorf("%s: the attempt is still recorded in flight", tc.name)
		}
		if got := s.devices.failed[testDeviceA]; got.Kind != tc.wantKind {
			t.Errorf("%s: recorded kind = %q, want %q", tc.name, got.Kind, tc.wantKind)
		}
		if s.pendingToastAction.message != tc.wantText || s.pendingToastAction.label != tc.wantAction || s.pendingToastAction.action == nil {
			t.Errorf("%s: toast = %+v, want %q with %q", tc.name, s.pendingToastAction, tc.wantText, tc.wantAction)
		}
	}
}

func TestSuccessfulDeviceSwitchEntersTheDevice(t *testing.T) {
	s := deviceTestShell(t)
	a := testDeviceKey(testDeviceA)
	s.devices.fail(testDeviceA, herdr.FailureAuth, "Permission denied")
	s.devices.lost = &deviceOutage{instance: "stale"}
	generation := s.generation.Load()

	sw := s.beginDeviceSwitch(a)
	s.loading = true // some local mutation was in flight
	s.finishDeviceSwitch(sw.token, deviceDial{projection: deviceProjection()})

	if s.activeInstance != a {
		t.Fatalf("activeInstance = %q, want the device", s.activeInstance)
	}
	if len(s.projection.Projects) != 1 || s.projection.Projects[0].ID != "r1" {
		t.Fatalf("projection = %+v, want the device's", s.projection.Projects)
	}
	if s.loading || s.offline || s.errText != "" {
		t.Fatalf("entering left loading=%v offline=%v err=%q", s.loading, s.offline, s.errText)
	}
	if s.generation.Load() == generation {
		t.Fatal("entering a device must invalidate in-flight local results")
	}
	if s.devices.lastLocal != "default" {
		t.Fatalf("lastLocal = %q, want the session left behind", s.devices.lastLocal)
	}
	if _, stale := s.devices.failed[testDeviceA]; stale || s.devices.lost != nil || s.devices.switching != nil {
		t.Fatalf("success kept old facts: %+v", s.devices)
	}
	if !s.activeIsRemote() {
		t.Fatal("activeIsRemote() must be true on a device")
	}
}

func TestStaleDeviceSwitchResultsAreDropped(t *testing.T) {
	s := deviceTestShell(t)
	a := testDeviceKey(testDeviceA)

	// Cancelled: the late success is dropped untouched.
	sw := s.beginDeviceSwitch(a)
	s.cancelDeviceSwitch()
	if s.devices.switching != nil {
		t.Fatal("cancel left the attempt recorded")
	}
	s.finishDeviceSwitch(sw.token, deviceDial{projection: deviceProjection()})
	if s.activeInstance != "default" {
		t.Fatalf("a cancelled switch's late result changed the instance to %q", s.activeInstance)
	}
	s.finishDeviceSwitch(sw.token, deviceDial{err: errors.New("Permission denied")})
	if len(s.devices.failed) != 0 || s.pendingToastAction.message != "" {
		t.Fatalf("a cancelled switch's late failure was recorded: %+v / %+v", s.devices.failed, s.pendingToastAction)
	}

	// Superseded: only the newest attempt lands.
	first := s.beginDeviceSwitch(a)
	s.devices.switching = nil // a second attempt for another target replaces it
	second := &deviceSwitch{name: a, id: testDeviceA, label: "Build box", token: s.devices.nextToken(), cancel: func() {}}
	s.devices.switching = second
	s.finishDeviceSwitch(first.token, deviceDial{projection: deviceProjection()})
	if s.activeInstance != "default" || s.devices.switching != second {
		t.Fatal("a superseded result must not land or clear the newer attempt")
	}
	s.finishDeviceSwitch(second.token, deviceDial{projection: deviceProjection()})
	if s.activeInstance != a {
		t.Fatal("the newest attempt must land")
	}
}

func TestCancelDeviceSwitchStopsTheDial(t *testing.T) {
	s := deviceTestShell(t)
	sw := s.beginDeviceSwitch(testDeviceKey(testDeviceA))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	sw.cancel = cancel
	s.cancelDeviceSwitch()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("Cancel did not cancel the dial's context")
	}
	s.cancelDeviceSwitch() // idempotent
}

func TestDeviceFailureKindClassification(t *testing.T) {
	cases := []struct {
		err  error
		want herdr.FailureKind
	}{
		{context.DeadlineExceeded, herdr.FailureNetwork},
		{fmt.Errorf("wrapped: %w", context.DeadlineExceeded), herdr.FailureNetwork},
		{herdr.ErrProtocolTooOld, herdr.FailureServer},
		{errors.New("Permission denied (publickey,password)."), herdr.FailureAuth},
		{errors.New("Could not resolve hostname build"), herdr.FailureNetwork},
		{errors.New("nothing recognizable"), herdr.FailureOther},
	}
	for _, tc := range cases {
		if got := deviceFailureKind(tc.err); got != tc.want {
			t.Errorf("deviceFailureKind(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// The watcher's hooks mark the device in use lost, and its recovery clears
// the mark and offers a reconnect toast; both ignore a device that is no
// longer in use or a window generation that moved on.
func TestWatcherHooksMarkAndClearALostDevice(t *testing.T) {
	s := deviceTestShell(t)
	s.uiApplyOverride = func(fn func()) { fn() }
	a := testDeviceKey(testDeviceA)
	s.activeInstance = a
	generation := s.generation.Load()

	s.noteDeviceLost("default", generation, errors.New("x")) // local: never lost
	if s.devices.lost != nil {
		t.Fatal("a local instance was marked lost")
	}
	s.noteDeviceLost(a, generation+1, errors.New("Connection timed out")) // stale generation
	if s.devices.lost != nil {
		t.Fatal("a stale generation marked the device lost")
	}
	s.noteDeviceLost(a, generation, errors.New("ssh: Connection timed out\nmore"))
	if s.devices.lostFor(a) == nil || s.devices.lost.kind != herdr.FailureNetwork {
		t.Fatalf("device not marked lost: %+v", s.devices.lost)
	}
	if got := s.devices.failed[testDeviceA]; got.Kind != herdr.FailureNetwork {
		t.Fatalf("loss not recorded on the device: %+v", got)
	}
	since := s.devices.lost.since
	s.noteDeviceLost(a, generation, errors.New("Permission denied"))
	if !s.devices.lost.since.Equal(since) || s.devices.lost.kind != herdr.FailureAuth {
		t.Fatalf("a repeated report must update the cause and keep the start: %+v", s.devices.lost)
	}

	s.noteDeviceRecovered(a, generation+1, deviceProjection()) // stale generation
	if s.devices.lostFor(a) == nil {
		t.Fatal("a stale generation cleared the loss")
	}
	s.noteDeviceRecovered(a, generation, deviceProjection())
	if s.devices.lost != nil {
		t.Fatal("recovery kept the loss")
	}
	if _, stale := s.devices.failed[testDeviceA]; stale {
		t.Fatal("recovery kept the failure")
	}
	if s.projection.Projects[0].ID != "r1" {
		t.Fatalf("recovery did not adopt the fresh projection: %+v", s.projection.Projects)
	}
	if s.pendingToast != "Build box reconnected" {
		t.Fatalf("toast = %q", s.pendingToast)
	}
}

func TestRemoteRetryDelayBacksOffAndCaps(t *testing.T) {
	want := []time.Duration{600 * time.Millisecond, 1200 * time.Millisecond, 2400 * time.Millisecond, 4800 * time.Millisecond, 9600 * time.Millisecond, 19200 * time.Millisecond, remoteRetryCap, remoteRetryCap}
	for failures, expected := range want {
		if got := remoteRetryDelay(failures); got != expected {
			t.Errorf("remoteRetryDelay(%d) = %v, want %v", failures, got, expected)
		}
	}
	if got := remoteRetryDelay(1000); got != remoteRetryCap {
		t.Errorf("remoteRetryDelay(1000) = %v, want the cap", got)
	}
}

// With no local session left, returning to this Mac leaves the chooser — it
// never keeps the device that went away.
func TestReturnToThisMacWithoutLocalSessions(t *testing.T) {
	s := deviceTestShell(t)
	s.instances = []herdr.Instance{s.instances[2]}
	s.activeInstance = s.instances[0].Name
	s.devices.lost = &deviceOutage{instance: s.activeInstance}
	s.returnToThisMac()
	if s.activeInstance != "" || s.devices.lost != nil || len(s.projection.Projects) != 0 {
		t.Fatalf("still on the device: active=%q lost=%v projects=%v", s.activeInstance, s.devices.lost, s.projection.Projects)
	}
}

func TestDeviceBannerAndStage(t *testing.T) {
	s := deviceTestShell(t)
	a := testDeviceKey(testDeviceA)
	s.router.Replace(routeWorkspace)

	// Connecting: a banner with Cancel; the window stays interactive.
	tester := ui.NewTester(s.View, 1200, 800)
	if tester.HasText("Connecting to Build box…") {
		t.Fatal("banner shown with nothing in flight")
	}
	s.beginDeviceSwitch(a)
	tester = ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	if !tester.HasText("Connecting to Build box…") || !tester.HasText("Cancel") {
		t.Fatalf("no Connecting banner with Cancel; texts=%q", tester.Texts())
	}
	if err := tester.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if s.devices.switching != nil {
		t.Fatal("Cancel in the banner did not cancel the switch")
	}

	// Lost: the banner says so and offers the way out and the recovery.
	s.activeInstance = a
	s.devices.lost = &deviceOutage{instance: a, kind: herdr.FailureAuth}
	tester = ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	for _, want := range []string{"Sign in to Build box to reconnect.", "Sign In…", "Switch to This Mac"} {
		if !tester.HasText(want) {
			t.Fatalf("lost banner missing %q; texts=%q", want, tester.Texts())
		}
	}
	s.devices.lost = &deviceOutage{instance: a, kind: herdr.FailureServer}
	tester = ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	if !tester.HasText("Set Up…") {
		t.Fatalf("a missing Herdr must offer Set Up…; texts=%q", tester.Texts())
	}
	s.devices.lost = &deviceOutage{instance: a, kind: herdr.FailureNetwork}
	tester = ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	if !tester.HasText("Build box stopped answering. Solco keeps retrying.") || tester.HasText("Sign In…") || tester.HasText("Set Up…") {
		t.Fatalf("network loss banner wrong; texts=%q", tester.Texts())
	}
}

func commandCenterAction(s *Shell, id string) (commandcenter.Action, bool) {
	for _, action := range s.buildCommandCenterActions() {
		if action.ID == id {
			return action, true
		}
	}
	return commandcenter.Action{}, false
}

func TestCommandCenterDeviceActions(t *testing.T) {
	s := deviceTestShell(t)
	a, b := testDeviceKey(testDeviceA), testDeviceKey(testDeviceB)

	add, ok := commandCenterAction(s, "device.add")
	if !ok || !add.Available || add.Section != "Devices" || add.Title != "Add Device" {
		t.Fatalf("Add Device action = %+v (found=%v)", add, ok)
	}
	if _, ok := commandCenterAction(s, "device.switch.local"); ok {
		t.Fatal("Device: This Mac offered while no device is in use")
	}
	toA, ok := commandCenterAction(s, "device.switch."+a)
	if !ok || !toA.Available || toA.Title != "Device: Build box" || toA.Section != "Devices" {
		t.Fatalf("switch-to-A action = %+v (found=%v)", toA, ok)
	}
	toB, ok := commandCenterAction(s, "device.switch."+b)
	if !ok || toB.Available {
		t.Fatalf("a disabled device must not be offered: %+v (found=%v)", toB, ok)
	}
	index := commandcenter.Build(s.buildCommandCenterActions())
	titles := func(query string) []string {
		var out []string
		for _, result := range index.Query(query, commandcenter.ScopeAll) {
			out = append(out, result.Action.Title)
		}
		return out
	}
	if got := titles("build"); len(got) == 0 || got[0] != "Device: Build box" {
		t.Fatalf("searching the device's name found %q", got)
	}
	if got := titles("old laptop"); len(got) != 0 {
		t.Fatalf("a disabled device is searchable: %q", got)
	}
	if got := titles("ssh"); !containsText(got, "Add Device") {
		t.Fatalf("searching ssh must find Add Device; got %q", got)
	}

	// In use and answering: not offered. Lost: offered as the reconnect.
	// This Mac appears once a device is in use.
	s.activeInstance = a
	if _, ok := commandCenterAction(s, "device.switch."+a); !ok {
		t.Fatal("setup: action must still exist")
	}
	if action, _ := commandCenterAction(s, "device.switch."+a); action.Available {
		t.Fatal("the device in use and answering is offered")
	}
	if action, ok := commandCenterAction(s, "device.switch.local"); !ok || !action.Available || action.Title != "Device: This Mac" {
		t.Fatalf("Device: This Mac = %+v (found=%v)", action, ok)
	}
	s.devices.lost = &deviceOutage{instance: a}
	if action, _ := commandCenterAction(s, "device.switch."+a); !action.Available {
		t.Fatal("a lost device must be offered as the way to reconnect")
	}
}

func TestCommandCenterRunsDeviceActions(t *testing.T) {
	s := deviceTestShell(t)
	s.openCommandCenter(commandcenter.ScopeAll)
	add, _ := commandCenterAction(s, "device.add")
	s.executeCommandCenterResult(commandcenter.Result{Action: add})
	if !s.devices.add.open || s.commandCenterOpen {
		t.Fatalf("Add Device: sheet open=%v, palette open=%v", s.devices.add.open, s.commandCenterOpen)
	}

	// A stale device target is refused with recoverable text and no switch.
	stale := commandcenter.Action{Kind: commandcenter.TargetRoute, TargetID: deviceActionPrefix + testDeviceKey("00000000000000000000000000000000")}
	s.executeCommandCenterResult(commandcenter.Result{Action: stale})
	if !strings.Contains(s.status, "That device is gone") || s.devices.switching != nil {
		t.Fatalf("stale device target: status=%q switching=%v", s.status, s.devices.switching)
	}
	// A local name smuggled into a device action is refused the same way.
	notDevice := commandcenter.Action{Kind: commandcenter.TargetRoute, TargetID: deviceActionPrefix + "default"}
	s.status = ""
	s.executeCommandCenterResult(commandcenter.Result{Action: notDevice})
	if !strings.Contains(s.status, "That device is gone") || s.devices.switching != nil {
		t.Fatalf("local name as a device target: status=%q", s.status)
	}
}
