package nativeui

/**
 * [INPUT]: device_add.go 的 Add Device 草稿/校验/等待态机，machines.go 的 forget/enable/rename 落地，headless ui.Tester
 * [OUTPUT]: 默认名、本地校验（空目标/非法目标/Named 缺会话名/重名）、别名建议、等待/失败/停止的状态迁移、过期令牌丢弃、表单错误呈现、忘记/启用失败不改动状态的回归测试
 * [POS]: 设备接入与管理流程的测试；校验与状态迁移都在不起终端、不调 Herdr 的路径上完成，真实接力由 Computer Use 实机验证
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/sshconfig"
)

func TestDefaultDeviceLabel(t *testing.T) {
	cases := []struct{ target, session, want string }{
		{"deploy@build.example.com", "", "build.example.com"},
		{"build", "", "build"},
		{"deploy@build", "agents", "build/agents"},
		{"  deploy@build  ", "  agents ", "build/agents"},
		{"", "", "host"},
	}
	for _, tc := range cases {
		if got := defaultDeviceLabel(tc.target, tc.session); got != tc.want {
			t.Errorf("defaultDeviceLabel(%q, %q) = %q, want %q", tc.target, tc.session, got, tc.want)
		}
	}
}

func TestDeviceAddValues(t *testing.T) {
	add := deviceAdd{target: " me@box ", named: addSessionAuto, session: "ignored", label: " Box "}
	if target, session, label := add.values(); target != "me@box" || session != "" || label != "Box" {
		t.Fatalf("auto values = %q %q %q; the session must be left to Herdr", target, session, label)
	}
	add.named = addSessionName
	add.session = " agents "
	if _, session, _ := add.values(); session != "agents" {
		t.Fatalf("named session = %q", session)
	}
}

func TestDeviceAddProblems(t *testing.T) {
	s := deviceTestShell(t)
	cases := []struct {
		name string
		add  deviceAdd
		want string // substring of the problem; "" means it can go ahead
	}{
		{"empty target", deviceAdd{}, "Enter an SSH target"},
		{"blank target", deviceAdd{target: "   "}, "Enter an SSH target"},
		{"option-like target", deviceAdd{target: "-oProxyCommand=x"}, "isn't valid"},
		{"named without a name", deviceAdd{target: "me@box", named: addSessionName}, "Enter the session name"},
		{"duplicate explicit label", deviceAdd{target: "me@box", label: "build BOX"}, "already exists"},
		{"fine", deviceAdd{target: "me@box"}, ""},
		{"fine, named", deviceAdd{target: "me@box", named: addSessionName, session: "agents"}, ""},
	}
	for _, tc := range cases {
		got := s.deviceAddProblem(tc.add)
		if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%s: problem = %q, want %q", tc.name, got, tc.want)
		}
	}

	// A default name that collides with a saved device is a duplicate too.
	s.instances = append(s.instances, herdr.Instance{Name: herdr.MachineInstanceKey("11111111111111111111111111111111"), DisplayName: "box", Machine: &herdr.Machine{ID: "11111111111111111111111111111111", Label: "box", Target: "me@box", Enabled: true}})
	if got := s.deviceAddProblem(deviceAdd{target: "other@box"}); !strings.Contains(got, "already exists") {
		t.Fatalf("a default label that is taken must be refused, got %q", got)
	}
}

func TestNewMachineFindsOnlyAProfileThatWasNotThereBefore(t *testing.T) {
	known := map[string]bool{"a": true, "b": true}
	machines := []herdr.Machine{{ID: "a"}, {ID: "b"}}
	if newMachine(machines, known) != nil {
		t.Fatal("no new profile, yet one was found")
	}
	machines = append(machines, herdr.Machine{ID: "c", Label: "C"})
	if got := newMachine(machines, known); got == nil || got.ID != "c" {
		t.Fatalf("newMachine = %+v, want c", got)
	}
	if newMachine(nil, nil) != nil {
		t.Fatal("empty list found a profile")
	}
}

func TestDeviceAddAliasesAreConcreteAndCapped(t *testing.T) {
	s := deviceTestShell(t)
	host := func(patterns ...string) sshconfig.Entry {
		return sshconfig.Entry{Kind: sshconfig.KindHost, Patterns: patterns}
	}
	s.hosts.entries = []sshconfig.Entry{
		host("*"), host("a", "b"), {Kind: sshconfig.KindMatch, Match: "all"},
		host("alpha"), host("alpha"), host("beta"), host("gamma"), host("delta"), host("eps"), host("zeta"), host("eta"),
	}
	aliases := s.deviceAddAliases()
	if len(aliases) != addAliasChips || aliases[0] != "alpha" || aliases[1] != "beta" {
		t.Fatalf("aliases = %q, want %d concrete, de-duplicated aliases starting alpha, beta", aliases, addAliasChips)
	}
}

func TestOpeningTheAddSheetResetsAndClosesThePanel(t *testing.T) {
	s := deviceTestShell(t)
	s.devices.open = true
	s.devices.add = deviceAdd{phase: addFailed, err: "old", label: "stale"}
	s.openDeviceAdd(" deploy@box ")
	add := s.devices.add
	if !add.open || add.phase != addForm || add.err != "" || add.label != "" || add.target != "deploy@box" || s.devices.open {
		t.Fatalf("openDeviceAdd left %+v, panel open=%v", add, s.devices.open)
	}
}

// A wait belongs to the sheet that started it: Stop Waiting returns to the
// form with the draft kept, and a poll that lands afterwards is dropped.
func TestDeviceAddWaitStopsAndStaleResultsAreDropped(t *testing.T) {
	s := deviceTestShell(t)
	s.openDeviceAdd("deploy@box")
	add := &s.devices.add
	add.label = "Mine"
	cancelled := false
	add.phase = addWaiting
	add.token = s.devices.nextToken()
	token := add.token
	add.cancel = func() { cancelled = true }

	s.stopDeviceAdd()
	if add.phase != addForm || !cancelled || add.cancel != nil || add.task != nil {
		t.Fatalf("stopDeviceAdd left %+v (cancelled=%v)", *add, cancelled)
	}
	if add.target != "deploy@box" || add.label != "Mine" {
		t.Fatalf("stopping lost the draft: %+v", *add)
	}
	s.failDeviceAdd(token, "late")
	if add.phase != addForm || add.err != "" {
		t.Fatalf("a stale failure landed: %+v", *add)
	}
	s.finishDeviceAdd(token, herdr.Machine{ID: "x", Label: "X"})
	if !add.open || add.phase != addForm {
		t.Fatalf("a stale success closed the sheet: %+v", *add)
	}
}

func TestDeviceAddFailureKeepsTheDraft(t *testing.T) {
	s := deviceTestShell(t)
	s.openDeviceAdd("deploy@box")
	add := &s.devices.add
	add.phase = addWaiting
	add.token = s.devices.nextToken()
	s.failDeviceAdd(add.token, "Herdr stopped before saving the device (exit 1). Check the Terminal window for why.")
	if add.phase != addFailed || add.err == "" || add.target != "deploy@box" || !add.open {
		t.Fatalf("failure state wrong: %+v", *add)
	}
}

func TestAddSheetShowsValidationAndFailureInline(t *testing.T) {
	s := deviceTestShell(t)
	s.openDeviceAdd("")
	tester := deviceTester(t, s)
	// The target field has the focus on open: Enter submits the empty draft.
	tester.Key(0, ui.KeyEnter)
	settleFrames(tester, 2)
	if !tester.HasText("Enter an SSH target, like user@host.") {
		t.Fatalf("empty submit shows no inline problem; texts=%q", tester.Texts())
	}
	if s.devices.add.phase != addForm {
		t.Fatalf("an invalid draft left the form: phase=%v", s.devices.add.phase)
	}
	// An error speaks about the draft it was raised for: editing retires it.
	s.devices.add.target = "me@box"
	tester = ui.NewTester(s.View, 1200, 800)
	settleFrames(tester, 3)
	if tester.HasText("Enter an SSH target") {
		t.Fatalf("the error outlived the edit that answered it; texts=%q", tester.Texts())
	}
	s.devices.add.target = ""
	tester = ui.NewTester(s.View, 1200, 800)
	settleFrames(tester, 3)
	if !tester.HasText("Enter an SSH target, like user@host.") {
		t.Fatalf("going back to the draft that was refused must show its error again; texts=%q", tester.Texts())
	}

	// A failed wait shows its reason with Try Again; Try Again returns to the form.
	add := &s.devices.add
	add.target = "deploy@box"
	add.phase, add.err = addFailed, "Herdr stopped before saving the device (exit 1). Check the Terminal window for why."
	tester = ui.NewTester(s.View, 1200, 800)
	settleFrames(tester, 3)
	for _, want := range []string{add.err, "Try Again", "Cancel"} {
		if !tester.HasText(want) {
			t.Fatalf("failed sheet missing %q; texts=%q", want, tester.Texts())
		}
	}
	if err := tester.Click("Try Again"); err != nil {
		t.Fatal(err)
	}
	if add.phase != addForm || add.err != "" || add.target != "deploy@box" {
		t.Fatalf("Try Again lost the draft or stayed failed: %+v", *add)
	}

	// The waiting sheet names what Solco waits for and offers Stop Waiting.
	add.phase = addWaiting
	tester = ui.NewTester(s.View, 1200, 800)
	settleFrames(tester, 3)
	for _, want := range []string{"Waiting for Herdr…", "Stop Waiting"} {
		if !tester.HasText(want) {
			t.Fatalf("waiting sheet missing %q; texts=%q", want, tester.Texts())
		}
	}
	if err := tester.Click("Stop Waiting"); err != nil {
		t.Fatal(err)
	}
	if add.phase != addForm {
		t.Fatalf("Stop Waiting left phase %v", add.phase)
	}
}

func TestRenameMachineRefusesATakenLabel(t *testing.T) {
	s := deviceTestShell(t)
	s.renameMachine(testDeviceB, " build BOX ")
	if s.pendingToast != "A device named build BOX already exists" {
		t.Fatalf("toast = %q", s.pendingToast)
	}
}

func TestFinishForgetOfAnIdleDevice(t *testing.T) {
	s := deviceTestShell(t)
	a := testDeviceKey(testDeviceA)
	s.machineStates = map[string]herdr.MachineState{testDeviceA: {Reachable: true}}
	s.devices.fail(testDeviceA, herdr.FailureAuth, "x")
	s.devices.setMaster(testDeviceA, true)
	pending := s.beginDeviceSwitch(a)
	ctx := false
	pending.cancel = func() { ctx = true }

	remaining := []herdr.Instance{s.instances[0], s.instances[1], s.instances[3]}
	s.finishForget(testDeviceA, a, "Build box", remaining, nil)

	if s.pendingToast != "Forgot Build box" || len(s.instances) != 3 || s.machineInstanceByID(testDeviceA) != nil {
		t.Fatalf("forget left toast=%q instances=%d", s.pendingToast, len(s.instances))
	}
	if _, ok := s.machineStates[testDeviceA]; ok {
		t.Fatal("the verdict of a forgotten device stayed")
	}
	if _, ok := s.devices.failed[testDeviceA]; ok || s.devices.master[testDeviceA] {
		t.Fatal("the facts of a forgotten device stayed")
	}
	if s.devices.switching != nil || !ctx {
		t.Fatal("a switch to the forgotten device was not cancelled")
	}
	if s.activeInstance != "default" {
		t.Fatalf("forgetting an idle device moved the window to %q", s.activeInstance)
	}
}

func TestFinishForgetOfTheDeviceInUseLeavesIt(t *testing.T) {
	s := deviceTestShell(t)
	a := testDeviceKey(testDeviceA)
	s.instances = []herdr.Instance{s.instances[2]} // no local session to go back to
	s.activeInstance = a
	s.devices.lost = &deviceOutage{instance: a}
	s.finishForget(testDeviceA, a, "Build box", nil, nil)
	if s.activeInstance != "" || s.devices.lost != nil || s.devices.lostFor(a) != nil {
		t.Fatalf("still on the forgotten device: active=%q lost=%v", s.activeInstance, s.devices.lost)
	}
}

func TestFailedForgetAndEnableChangeNothing(t *testing.T) {
	s := deviceTestShell(t)
	before := len(s.instances)
	s.finishForget(testDeviceA, testDeviceKey(testDeviceA), "Build box", nil, errors.New("herdr: profile is busy"))
	if s.pendingToast != "Couldn't forget Build box: herdr: profile is busy" || len(s.instances) != before || s.machineInstanceByID(testDeviceA) == nil {
		t.Fatalf("a failed forget changed state: toast=%q instances=%d", s.pendingToast, len(s.instances))
	}
	s.finishSetDeviceEnabled(testDeviceB, testDeviceKey(testDeviceB), "Old laptop", true, errors.New("nope"))
	if s.pendingToast != "Couldn't enable Old laptop: nope" {
		t.Fatalf("toast = %q", s.pendingToast)
	}
	s.finishSetDeviceEnabled(testDeviceA, testDeviceKey(testDeviceA), "Build box", false, errors.New("nope"))
	if s.pendingToast != "Couldn't disable Build box: nope" {
		t.Fatalf("toast = %q", s.pendingToast)
	}
}

func TestTargetShared(t *testing.T) {
	instances := []herdr.Instance{{Name: "default"}, {Name: "m1", Machine: &herdr.Machine{Target: "me@box"}}}
	if !targetShared(instances, "me@box") || targetShared(instances, "me@other") || targetShared(nil, "me@box") {
		t.Fatal("targetShared must report only a remaining profile with the same target")
	}
}

// The header's Check Devices tries every idle device for real and lets the rows
// speak: a failure is recorded and a success clears the record, but neither
// pops a toast (one per device would only talk over each other). A check asked
// for on one device still answers with a toast.
func TestQuietDeviceCheckRecordsTheVerdictWithoutAToast(t *testing.T) {
	s := deviceTestShell(t)
	a := testDeviceKey(testDeviceA)
	s.devices.fail(testDeviceA, herdr.FailureNetwork, "connect refused")

	s.finishDeviceCheck(testDeviceA, a, "Build box", herdr.MachineState{Reachable: true}, nil, false, nil, true)
	if _, stale := s.devices.failed[testDeviceA]; stale {
		t.Fatal("a quiet success must clear the failure recorded against the device")
	}
	if s.pendingToast != "" || s.pendingToastAction.message != "" {
		t.Fatalf("a quiet success toasted: %q / %+v", s.pendingToast, s.pendingToastAction)
	}

	s.finishDeviceCheck(testDeviceA, a, "Build box", herdr.MachineState{}, nil, false, errors.New("ssh: connect to host build port 22: Connection refused"), true)
	if got := s.devices.failed[testDeviceA]; got.Kind != herdr.FailureNetwork {
		t.Fatalf("a quiet failure must still be recorded, got %+v", got)
	}
	if s.pendingToast != "" || s.pendingToastAction.message != "" {
		t.Fatalf("a quiet failure toasted: %q / %+v", s.pendingToast, s.pendingToastAction)
	}

	s.devices.clearFailure(testDeviceA)
	s.finishDeviceCheck(testDeviceA, a, "Build box", herdr.MachineState{Reachable: true}, nil, false, nil, false)
	if s.pendingToast != "Build box is online" {
		t.Fatalf("a check asked for on one device must answer, toast = %q", s.pendingToast)
	}
}

func TestCheckingAnyDevice(t *testing.T) {
	var d deviceUI
	if d.checkingAny() {
		t.Fatal("nothing is being checked")
	}
	d.setChecking(testDeviceA, true)
	if !d.checkingAny() {
		t.Fatal("a device is being checked")
	}
	d.setChecking(testDeviceA, false)
	if d.checkingAny() {
		t.Fatal("the check is over")
	}
}
