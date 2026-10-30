package nativeui

/**
 * [INPUT]: device_state.go 的纯派生（deviceStatusOf/verdictStatus/failureStatus/firstLineBounded）与 deviceUI 的惰性 map
 * [OUTPUT]: 设备行状态优先级、Herdr 判定与 Solco 共享连接的合成、失败分类到恢复动作的映射的回归测试；deviceTestShell 供 device_* 测试共用的“本机两个会话 + 一台可用设备 + 一台停用设备”夹具
 * [POS]: 设备切换器状态层的测试；只测纯函数与内存状态，不触网、不起进程
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"testing"

	"github.com/thewei/solco/internal/herdr"
)

const (
	testDeviceA = "0123456789abcdef0123456789abcdef"
	testDeviceB = "fedcba9876543210fedcba9876543210"
)

// deviceTestShell is a Shell on this Mac's "default" session with two saved
// devices: A (enabled, "Build box") and B (disabled, "Old laptop").
func deviceTestShell(t *testing.T) *Shell {
	t.Helper()
	s := NewShell()
	s.loading = false
	s.hosts.loaded = true // never read the developer's real ~/.ssh/config
	s.instances = []herdr.Instance{
		{Name: "default", DisplayName: "Default", Running: true, Default: true},
		{Name: "aux", DisplayName: "Aux"},
		{Name: herdr.MachineInstanceKey(testDeviceA), DisplayName: "Build box", Machine: &herdr.Machine{ID: testDeviceA, Label: "Build box", Target: "deploy@build", Session: "default", Enabled: true}},
		{Name: herdr.MachineInstanceKey(testDeviceB), DisplayName: "Old laptop", Machine: &herdr.Machine{ID: testDeviceB, Label: "Old laptop", Target: "me@old", Session: "default", Enabled: false}},
	}
	s.activeInstance = "default"
	s.projection = herdr.Projection{Protocol: 22, Projects: []herdr.Project{{ID: "w1", Label: "Local"}}}
	return s
}

func testDeviceKey(id string) string { return herdr.MachineInstanceKey(id) }

func TestDeviceStatusOf(t *testing.T) {
	on := herdr.Machine{ID: testDeviceA, Enabled: true}
	off := herdr.Machine{ID: testDeviceA, Enabled: false}
	auth := &deviceFailure{Kind: herdr.FailureAuth}
	net := &deviceFailure{Kind: herdr.FailureNetwork}
	server := &deviceFailure{Kind: herdr.FailureServer}
	reachable := herdr.MachineState{Reachable: true, Status: "reachable"}
	authVerdict := herdr.MachineState{Attention: true, Status: "auth required"}

	cases := []struct {
		name  string
		facts deviceFacts
		want  deviceStatus
	}{
		{"nothing known", deviceFacts{Machine: on}, deviceUnknown},
		{"disabled beats everything", deviceFacts{Machine: off, Active: true, Connecting: true, Failure: auth}, deviceDisabled},
		{"connecting beats active", deviceFacts{Machine: on, Active: true, Connecting: true}, deviceConnecting},
		{"active and lost", deviceFacts{Machine: on, Active: true, Lost: true, Failure: net}, deviceLost},
		{"active is online whatever failed before", deviceFacts{Machine: on, Active: true, Failure: auth}, deviceOnline},
		{"explicit check shows over an old failure", deviceFacts{Machine: on, Checking: true, Failure: auth, Known: true, Verdict: reachable}, deviceChecking},
		{"auth failure needs sign-in", deviceFacts{Machine: on, Failure: auth}, deviceSignIn},
		{"host key failure needs sign-in", deviceFacts{Machine: on, Failure: &deviceFailure{Kind: herdr.FailureHostKey}}, deviceSignIn},
		{"network failure is unreachable", deviceFacts{Machine: on, Failure: net, Known: true, Verdict: reachable}, deviceUnreachable},
		{"server failure needs setup", deviceFacts{Machine: on, Failure: server}, deviceAttention},
		{"other failure needs setup", deviceFacts{Machine: on, Failure: &deviceFailure{Kind: herdr.FailureOther}}, deviceAttention},
		{"a live shared connection retires a sign-in failure", deviceFacts{Machine: on, Failure: auth, Master: true}, deviceOnline},
		{"a live shared connection does not hide a network failure", deviceFacts{Machine: on, Failure: net, Master: true}, deviceUnreachable},
		{"background refresh speaks for the unknown", deviceFacts{Machine: on, Refreshing: true}, deviceChecking},
		{"background refresh keeps a known verdict", deviceFacts{Machine: on, Refreshing: true, Known: true, Verdict: reachable}, deviceOnline},
		{"background refresh keeps a failure", deviceFacts{Machine: on, Refreshing: true, Failure: auth}, deviceSignIn},
		{"reachable verdict", deviceFacts{Machine: on, Known: true, Verdict: reachable}, deviceOnline},
		{"auth verdict without a connection", deviceFacts{Machine: on, Known: true, Verdict: authVerdict}, deviceSignIn},
		{"auth verdict with a live connection", deviceFacts{Machine: on, Known: true, Verdict: authVerdict, Master: true}, deviceOnline},
		{"live connection alone is online", deviceFacts{Machine: on, Master: true}, deviceOnline},
	}
	for _, tc := range cases {
		if got := deviceStatusOf(tc.facts); got != tc.want {
			t.Errorf("%s: deviceStatusOf = %q (%d), want %q (%d)", tc.name, got.label(), got, tc.want.label(), tc.want)
		}
	}
}

func TestVerdictStatus(t *testing.T) {
	cases := []struct {
		name   string
		state  herdr.MachineState
		master bool
		want   deviceStatus
	}{
		{"reachable", herdr.MachineState{Reachable: true}, false, deviceOnline},
		{"auth required", herdr.MachineState{Attention: true, Status: "auth required"}, false, deviceSignIn},
		{"unreachable word", herdr.MachineState{Attention: true, Status: "unreachable"}, false, deviceUnreachable},
		{"message names a timeout", herdr.MachineState{Attention: true, Status: "error", Message: "ssh: connect to host build port 22: Connection timed out"}, false, deviceUnreachable},
		{"message names a bad key", herdr.MachineState{Attention: true, Status: "error", Message: "Host key verification failed."}, false, deviceSignIn},
		{"message names a bad key, connection live", herdr.MachineState{Attention: true, Status: "error", Message: "Host key verification failed."}, true, deviceOnline},
		{"error without a known cause", herdr.MachineState{Attention: true, Status: "error", Message: "something odd"}, false, deviceAttention},
		{"no verdict", herdr.MachineState{}, false, deviceUnknown},
	}
	for _, tc := range cases {
		if got := verdictStatus(tc.state, tc.master); got != tc.want {
			t.Errorf("%s: verdictStatus = %q, want %q", tc.name, got.label(), tc.want.label())
		}
	}
}

// Every state names itself and a tone; the recovery predicates cover exactly
// the two states that offer one.
func TestDeviceStatusVocabulary(t *testing.T) {
	seen := map[string]deviceStatus{}
	for status := deviceUnknown; status <= deviceLost; status++ {
		label := status.label()
		if label == "" {
			t.Errorf("status %d has no label", status)
		}
		if other, dup := seen[label]; dup {
			t.Errorf("statuses %d and %d share the label %q", other, status, label)
		}
		seen[label] = status
		if (status.needsSignIn() || status.needsSetUp()) && status != deviceSignIn && status != deviceAttention {
			t.Errorf("%q offers a recovery it should not", label)
		}
	}
	if !deviceSignIn.needsSignIn() || !deviceAttention.needsSetUp() {
		t.Fatal("Sign-in needed offers Sign In, Needs setup offers Set Up")
	}
}

func TestFirstLineBounded(t *testing.T) {
	cases := []struct {
		in    string
		limit int
		want  string
	}{
		{"  plain  ", 20, "plain"},
		{"first\nsecond", 20, "first"},
		{"abcdefghij", 5, "abcd…"},
		{"héllo wörld", 6, "héllo…"},
		{"", 5, ""},
	}
	for _, tc := range cases {
		if got := firstLineBounded(tc.in, tc.limit); got != tc.want {
			t.Errorf("firstLineBounded(%q, %d) = %q, want %q", tc.in, tc.limit, got, tc.want)
		}
	}
}

// The state maps are made on first write; reads and deletes on a fresh value
// are safe, and forgetting a device drops everything remembered about it.
func TestDeviceUIMapsAreLazyAndForgetIsComplete(t *testing.T) {
	var d deviceUI
	d.clearFailure("x")
	d.forget("x")
	if d.lostFor("anything") != nil {
		t.Fatal("a fresh deviceUI reports a loss")
	}
	d.fail(testDeviceA, herdr.FailureAuth, "Permission denied (publickey).\nsecond line")
	d.setMaster(testDeviceA, true)
	d.setChecking(testDeviceA, true)
	if got := d.failed[testDeviceA]; got.Kind != herdr.FailureAuth || got.Detail != "Permission denied (publickey)." {
		t.Fatalf("recorded failure = %+v, want the auth failure's first line", got)
	}
	d.setChecking(testDeviceA, false)
	if d.checking[testDeviceA] {
		t.Fatal("setChecking(false) kept the flag")
	}
	cancelled := false
	d.waiting = map[string]*deviceTask{testDeviceA: {cancel: func() { cancelled = true }}}
	d.forget(testDeviceA)
	if _, ok := d.failed[testDeviceA]; ok || d.master[testDeviceA] || d.checking[testDeviceA] || d.waiting[testDeviceA] != nil || !cancelled {
		t.Fatalf("forget left state behind: %+v", d)
	}
	if first, second := d.nextToken(), d.nextToken(); second != first+1 {
		t.Fatalf("tokens %d, %d are not strictly increasing", first, second)
	}
}

func TestDeviceFactsAndStatusFromTheShell(t *testing.T) {
	s := deviceTestShell(t)
	a := *s.machineInstanceByID(testDeviceA)
	b := *s.machineInstanceByID(testDeviceB)

	if got := s.deviceStatusFor(a); got != deviceUnknown {
		t.Fatalf("fresh device = %q, want Not checked", got.label())
	}
	if got := s.deviceStatusFor(b); got != deviceDisabled {
		t.Fatalf("disabled device = %q", got.label())
	}

	s.machineStates = map[string]herdr.MachineState{testDeviceA: {Reachable: true, Status: "reachable"}}
	if got := s.deviceStatusFor(a); got != deviceOnline {
		t.Fatalf("reachable verdict = %q, want Online", got.label())
	}

	s.devices.fail(testDeviceA, herdr.FailureAuth, "Permission denied")
	if got := s.deviceStatusFor(a); got != deviceSignIn {
		t.Fatalf("Solco's own auth failure must outrank Herdr's reachable verdict, got %q", got.label())
	}
	facts := s.deviceFactsFor(a)
	if facts.detail() != "Permission denied" {
		t.Fatalf("detail = %q, want the failure's text", facts.detail())
	}

	s.devices.setMaster(testDeviceA, true)
	if got := s.deviceStatusFor(a); got != deviceOnline {
		t.Fatalf("a live shared connection must retire the sign-in failure, got %q", got.label())
	}

	s.devices.setChecking(testDeviceA, true)
	if got := s.deviceStatusFor(a); got != deviceChecking {
		t.Fatalf("explicit check = %q, want Checking…", got.label())
	}
	s.devices.setChecking(testDeviceA, false)

	// The device in use: online, then lost, then connecting again.
	s.activeInstance = a.Name
	if got := s.deviceStatusFor(a); got != deviceOnline {
		t.Fatalf("device in use = %q, want Online", got.label())
	}
	s.devices.lost = &deviceOutage{instance: a.Name, kind: herdr.FailureNetwork}
	if got := s.deviceStatusFor(a); got != deviceLost {
		t.Fatalf("device in use after losing its stream = %q, want Connection lost", got.label())
	}
	s.devices.switching = &deviceSwitch{name: a.Name, id: testDeviceA, cancel: func() {}}
	if got := s.deviceStatusFor(a); got != deviceConnecting {
		t.Fatalf("device being reconnected = %q, want Connecting…", got.label())
	}
}

func TestLocalInstancesAndLookups(t *testing.T) {
	s := deviceTestShell(t)
	local := s.localInstances()
	if len(local) != 2 || local[0].Name != "default" || local[1].Name != "aux" {
		t.Fatalf("localInstances = %+v, want default and aux", local)
	}
	if inst := s.instanceByName(testDeviceKey(testDeviceA)); inst == nil || inst.Machine == nil || inst.Machine.ID != testDeviceA {
		t.Fatalf("instanceByName(device A) = %+v", inst)
	}
	if s.instanceByName("gone") != nil || s.machineInstanceByID("gone") != nil {
		t.Fatal("lookups invented an instance")
	}
	if inst := s.machineInstanceByID(testDeviceB); inst == nil || inst.Name != testDeviceKey(testDeviceB) {
		t.Fatalf("machineInstanceByID(device B) = %+v", inst)
	}
}
