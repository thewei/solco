package nativeui

/**
 * [INPUT]: 依赖 internal/herdr 的 Machine/MachineState/FailureKind/ClassifyFailure（Herdr 机器档案与 Solco 共享 SSH 连接的失败分类）、Shell 的 instances/machineStates、design_system 的 StatusTone
 * [OUTPUT]: 对外提供 deviceUI（页脚设备切换器的本地交互状态：面板开关、共享连接/失败/检查/终端接力记录、进行中的切换、掉线、Add 表单）、deviceStatus + deviceStatusOf（把 Herdr 判定、Solco 共享连接、Solco 自己见到的最近失败合成设备行唯一的状态）、deviceFailure、deviceFactsFor
 * [POS]: 设备切换器的状态与派生层：纯数据与纯函数（无 UI、无 IO），device_switcher/device_switch/device_recover/device_add 共同读写；设备档案本身仍只在 Herdr（machine list/status），这里只记 Solco 自己观察到的事实，从不镜像第二份设备注册表
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"strings"
	"time"

	"github.com/thewei/solco/internal/herdr"
)

const (
	// deviceSwitchTimeout bounds one switch so the Connecting banner can never
	// stick: the bridge bring-up has its own limits, this is the backstop.
	deviceSwitchTimeout = 45 * time.Second
	// deviceTaskLimit is how long Solco waits for a Terminal handoff (sign-in,
	// set up, add) before it stops looking; the Terminal window stays.
	deviceTaskLimit = 10 * time.Minute
	// deviceSearchFrom is the row count from which the switcher panel opens
	// with a search field.
	deviceSearchFrom = 10
)

// deviceStatus is the one state a device row shows.
type deviceStatus int

const (
	deviceUnknown     deviceStatus = iota // never checked
	deviceChecking                        // a check is running
	deviceOnline                          // Solco can reach its Herdr
	deviceSignIn                          // authentication is needed first
	deviceAttention                       // reachable at best, Herdr needs setup or an update
	deviceUnreachable                     // the host did not answer
	deviceDisabled                        // the profile is switched off in Herdr
	deviceConnecting                      // a switch to it is in flight
	deviceLost                            // the device in use stopped answering
)

// label is the status as a row's second line reads it.
func (d deviceStatus) label() string {
	switch d {
	case deviceChecking:
		return "Checking…"
	case deviceOnline:
		return "Online"
	case deviceSignIn:
		return "Sign-in needed"
	case deviceAttention:
		return "Needs setup"
	case deviceUnreachable:
		return "Unreachable"
	case deviceDisabled:
		return "Disabled"
	case deviceConnecting:
		return "Connecting…"
	case deviceLost:
		return "Connection lost"
	default:
		return "Not checked"
	}
}

// tone is the status dot's color, through the shared status vocabulary.
func (d deviceStatus) tone() StatusTone {
	switch d {
	case deviceOnline:
		return ToneSuccess
	case deviceSignIn, deviceAttention, deviceLost:
		return ToneAttention
	case deviceUnreachable:
		return ToneWarning
	case deviceConnecting:
		return ToneInfo
	default:
		return ToneMuted
	}
}

// needsSignIn / needsSetUp name the recovery a status asks for.
func (d deviceStatus) needsSignIn() bool { return d == deviceSignIn }
func (d deviceStatus) needsSetUp() bool  { return d == deviceAttention }

// deviceFailure is the last thing that went wrong when Solco itself tried to
// reach a device — Solco's bridge is its own SSH connection, so its verdict
// outranks Herdr's `machine status`, which uses Herdr's own.
type deviceFailure struct {
	Kind   herdr.FailureKind
	Detail string
}

// signInKind reports a failure that only an interactive sign-in resolves.
func (f deviceFailure) signInKind() bool {
	return f.Kind == herdr.FailureAuth || f.Kind == herdr.FailureHostKey
}

// failureStatus maps a failure kind to the row state that offers its recovery.
func failureStatus(kind herdr.FailureKind) deviceStatus {
	switch kind {
	case herdr.FailureAuth, herdr.FailureHostKey:
		return deviceSignIn
	case herdr.FailureNetwork:
		return deviceUnreachable
	default:
		return deviceAttention
	}
}

// deviceFacts is everything a row's state is folded from.
type deviceFacts struct {
	Machine    herdr.Machine
	Verdict    herdr.MachineState // Herdr's latest `machine status`
	Known      bool               // Verdict is a real verdict, not the zero value
	Master     bool               // Solco's shared SSH connection to the target is alive
	Failure    *deviceFailure     // Solco's own last failure
	Checking   bool               // an explicit check of this device is running
	Refreshing bool               // the switcher's background status check is out
	Connecting bool
	Active     bool // the device in use
	Lost       bool // the device in use stopped answering
}

// deviceStatusOf folds the facts into one state. Order matters: a disabled
// profile is disabled whatever else is known; the device in use is online
// unless it was lost; an explicit check in flight shows over what the last
// one found; Solco's own failure beats Herdr's verdict (but a live shared
// connection proves sign-in, so it retires a sign-in failure); the passive
// background check only speaks for a device nobody has a verdict for.
func deviceStatusOf(f deviceFacts) deviceStatus {
	switch {
	case !f.Machine.Enabled:
		return deviceDisabled
	case f.Connecting:
		return deviceConnecting
	case f.Active && f.Lost:
		return deviceLost
	case f.Active:
		return deviceOnline
	case f.Checking:
		return deviceChecking
	case f.Failure != nil && !(f.Master && f.Failure.signInKind()):
		return failureStatus(f.Failure.Kind)
	case f.Refreshing && !f.Known:
		return deviceChecking
	case f.Known:
		return verdictStatus(f.Verdict, f.Master)
	case f.Master:
		return deviceOnline
	}
	return deviceUnknown
}

// verdictStatus reads Herdr's `machine status` verdict. An auth verdict is
// moot when Solco holds a live shared connection to the target.
func verdictStatus(state herdr.MachineState, master bool) deviceStatus {
	signedIn := func() deviceStatus {
		if master {
			return deviceOnline
		}
		return deviceSignIn
	}
	switch {
	case state.Reachable:
		return deviceOnline
	case strings.Contains(state.Status, "auth"):
		return signedIn()
	case strings.Contains(state.Status, "unreach"):
		return deviceUnreachable
	case state.Attention:
		switch herdr.ClassifyFailure(state.Message) {
		case herdr.FailureAuth, herdr.FailureHostKey:
			return signedIn()
		case herdr.FailureNetwork:
			return deviceUnreachable
		}
		return deviceAttention
	}
	return deviceUnknown
}

// detail is the one-line reason behind a status, for a row's tooltip.
func (f deviceFacts) detail() string {
	switch {
	case f.Failure != nil && f.Failure.Detail != "":
		return f.Failure.Detail
	case f.Known && f.Verdict.Message != "":
		return firstLineBounded(f.Verdict.Message, 160)
	}
	return ""
}

// firstLineBounded is the first line of text, cut to at most limit runes.
func firstLineBounded(text string, limit int) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return text
}

// deviceSwitch is a device switch in flight: the first half of the
// transaction (connect, prove the remote Herdr answers, read its projection)
// runs off the UI lane while the device in use stays untouched.
type deviceSwitch struct {
	name   string // instance key switched to
	id     string // Herdr profile id
	label  string
	token  uint64
	cancel context.CancelFunc
	since  time.Time
}

// deviceOutage marks the device in use as no longer answering; the watcher
// raises it and clears it when its event stream comes back.
type deviceOutage struct {
	instance string
	kind     herdr.FailureKind
	detail   string
	since    time.Time
}

// deviceTask is a Terminal handoff in flight for one device: its sign-in or
// its Herdr set up. Solco reads only the exit code and re-checks the device.
type deviceTask struct {
	kind   string // "sign-in" | "set-up"
	task   *herdr.TerminalTask
	cancel context.CancelFunc
}

// deviceUI is the footer switcher's local state: what Solco itself has seen
// of each device, the switch or handoff in flight, and the Add form. Maps are
// keyed by Herdr profile id and made on first write.
type deviceUI struct {
	open   bool
	filter string

	master   map[string]bool          // shared SSH connection alive (herdr.MasterLive)
	failed   map[string]deviceFailure // Solco's last failure per device
	checking map[string]bool
	waiting  map[string]*deviceTask

	switching *deviceSwitch
	tokens    uint64
	lost      *deviceOutage
	lastLocal string // the local session in use before the last switch to a device
	add       deviceAdd
}

func (d *deviceUI) nextToken() uint64 {
	d.tokens++
	return d.tokens
}

func (d *deviceUI) fail(id string, kind herdr.FailureKind, detail string) {
	if d.failed == nil {
		d.failed = make(map[string]deviceFailure)
	}
	d.failed[id] = deviceFailure{Kind: kind, Detail: firstLineBounded(detail, 160)}
}

func (d *deviceUI) clearFailure(id string) { delete(d.failed, id) }

func (d *deviceUI) setMaster(id string, live bool) {
	if d.master == nil {
		d.master = make(map[string]bool)
	}
	d.master[id] = live
}

func (d *deviceUI) setChecking(id string, on bool) {
	if d.checking == nil {
		d.checking = make(map[string]bool)
	}
	if on {
		d.checking[id] = true
		return
	}
	delete(d.checking, id)
}

// checkingAny reports whether any device is being tried right now.
func (d *deviceUI) checkingAny() bool { return len(d.checking) > 0 }

// forget drops everything remembered about a device that was forgotten.
func (d *deviceUI) forget(id string) {
	delete(d.master, id)
	delete(d.failed, id)
	delete(d.checking, id)
	if task := d.waiting[id]; task != nil {
		task.cancel()
		delete(d.waiting, id)
	}
}

// lostFor returns the loss record when instance is the device in use that
// stopped answering.
func (d *deviceUI) lostFor(instance string) *deviceOutage {
	if d.lost != nil && d.lost.instance == instance {
		return d.lost
	}
	return nil
}

// deviceFactsFor gathers one device's facts from the shell.
func (s *Shell) deviceFactsFor(inst herdr.Instance) deviceFacts {
	machine := *inst.Machine
	d := &s.devices
	verdict, known := s.machineStates[machine.ID]
	known = known && (verdict.Reachable || verdict.Attention)
	facts := deviceFacts{
		Machine:    machine,
		Verdict:    verdict,
		Known:      known,
		Master:     d.master[machine.ID],
		Checking:   d.checking[machine.ID],
		Refreshing: s.machineStatusRunning,
		Connecting: d.switching != nil && d.switching.id == machine.ID,
		Active:     inst.Name == s.activeInstance,
		Lost:       d.lostFor(inst.Name) != nil,
	}
	if failure, ok := d.failed[machine.ID]; ok {
		facts.Failure = &failure
	}
	return facts
}

// deviceStatusFor is the state of one saved device.
func (s *Shell) deviceStatusFor(inst herdr.Instance) deviceStatus {
	return deviceStatusOf(s.deviceFactsFor(inst))
}

// instanceByName finds a listed instance by key.
func (s *Shell) instanceByName(name string) *herdr.Instance {
	for index := range s.instances {
		if s.instances[index].Name == name {
			return &s.instances[index]
		}
	}
	return nil
}

// machineInstanceByID finds the saved device with the Herdr profile id.
func (s *Shell) machineInstanceByID(id string) *herdr.Instance {
	for index := range s.instances {
		if m := s.instances[index].Machine; m != nil && m.ID == id {
			return &s.instances[index]
		}
	}
	return nil
}

// localInstances returns this Mac's Herdr sessions (the saved devices'
// entries are left out).
func (s *Shell) localInstances() []herdr.Instance {
	local := make([]herdr.Instance, 0, len(s.instances))
	for _, instance := range s.instances {
		if instance.Machine == nil {
			local = append(local, instance)
		}
	}
	return local
}
