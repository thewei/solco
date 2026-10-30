package nativeui

/**
 * [INPUT]: 依赖 internal/herdr 的 StartMachineSignIn/StartMachineSetup（Terminal 接力任务与退出码 marker）、MachineStatus/ConnectMachine/MasterLive、deviceFailureToast、device_state 的记录
 * [OUTPUT]: 对外提供设备恢复动作：signInDevice（Terminal 里建立 Solco 自己的 OpenSSH 共享连接，支持口令/MFA；Solco 不收集任何凭据）、setUpDevice（Terminal 里跑 Herdr 自带的远程客户端：host key、安装/升级）、checkDevice/checkDevices（真连一次：Herdr 判定 + Solco 自己的桥接，Solco 的结果为准；checkDevices 是切换器头部检查钮，对每台未在用的已启用设备静默各试一次，结果只落在状态行上）、startDeviceTask/stopDeviceTask（接力任务的后台等待，只读退出码，超时/Stop Waiting 可退）
 * [POS]: 设备状态变坏之后的出路：状态行菜单、失败 toast、掉线横幅都落到这里；所有交互在用户自己的 Terminal 里完成，Solco 只在它结束后重新检查，绝不代答、不存口令
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/thewei/solco/internal/herdr"
)

const (
	deviceTaskPoll   = 700 * time.Millisecond
	deviceProbeEvery = 5 * time.Second
)

// startDeviceTask watches one Terminal handoff for a device. It polls the
// script's exit marker (only the exit code — never output) and, when probe is
// given, whether the work is already visibly done. finished runs on the UI
// lane exactly once: exited says the script ended (code is its exit code),
// probed that the probe saw the work done; neither means Solco stopped
// waiting (timeout). stopDeviceTask retires the watch silently.
func (s *Shell) startDeviceTask(id, kind string, task *herdr.TerminalTask, probe func() bool, finished func(exited bool, code int, probed bool)) {
	d := &s.devices
	ctx, cancel := context.WithTimeout(context.Background(), deviceTaskLimit)
	entry := &deviceTask{kind: kind, task: task, cancel: cancel}
	if d.waiting == nil {
		d.waiting = make(map[string]*deviceTask)
	}
	d.waiting[id] = entry
	land := func(exited bool, code int, probed bool) {
		s.onUI(func() {
			if d.waiting[id] != entry {
				return // stopped by the user, or the device was forgotten
			}
			delete(d.waiting, id)
			cancel()
			finished(exited, code, probed)
		})
	}
	go func() {
		defer task.Cleanup()
		ticker := time.NewTicker(deviceTaskPoll)
		defer ticker.Stop()
		var probedAt time.Time
		for {
			select {
			case <-ctx.Done():
				land(false, 0, false)
				return
			case <-ticker.C:
			}
			if code, done := task.Result(); done {
				land(true, code, probe != nil && probe())
				return
			}
			if probe != nil && time.Since(probedAt) >= deviceProbeEvery {
				probedAt = time.Now()
				if probe() {
					land(false, 0, true)
					return
				}
			}
		}
	}()
}

// stopDeviceTask stops waiting for a device's Terminal handoff. The Terminal
// window stays; it is the user's.
func (s *Shell) stopDeviceTask(id string) {
	if entry := s.devices.waiting[id]; entry != nil {
		delete(s.devices.waiting, id)
		entry.cancel()
	}
}

// signInDevice opens Terminal to sign in to a device: one interactive ssh
// there leaves an OpenSSH shared connection that every later Solco connection
// to that target rides, so a passphrase or a one-time code is typed once, into
// ssh, and never into Solco.
func (s *Shell) signInDevice(id string) {
	inst := s.machineInstanceByID(id)
	if inst == nil || s.devices.waiting[id] != nil {
		return
	}
	machine, label, name := *inst.Machine, inst.DisplayName, inst.Name
	task, err := s.runtime.StartMachineSignIn(machine.Target)
	if err != nil {
		s.pendingToast = "Couldn't open Terminal: " + firstLineBounded(err.Error(), 120)
		return
	}
	slog.Info("device sign-in started", "machine_id", id)
	s.startDeviceTask(id, "sign-in", task, nil, func(exited bool, code int, _ bool) {
		if !exited {
			s.pendingToast = "Stopped waiting for the sign-in to " + label
			return
		}
		go func() {
			live := code == 0 && herdr.MasterLive(machine.Target)
			s.onUI(func() { s.finishDeviceSignIn(id, name, label, live) })
		}()
	})
}

// finishDeviceSignIn lands a sign-in: a live shared connection is the proof.
// The device in use that was lost reconnects at once; any other device offers
// its Switch, never switching on its own.
func (s *Shell) finishDeviceSignIn(id, name, label string, live bool) {
	d := &s.devices
	d.setMaster(id, live)
	if !live {
		s.pendingToast = "Sign-in to " + label + " didn't finish"
		return
	}
	d.clearFailure(id)
	slog.Info("device sign-in finished", "machine_id", id)
	switch {
	case d.lostFor(name) != nil:
		s.switchToDevice(name)
	case name != s.activeInstance:
		s.pendingToastAction = toastAction{message: "Signed in to " + label, label: "Switch", action: func() { s.switchToDevice(name) }}
	}
}

// setUpDevice opens Terminal in Herdr's own remote client for a device: it
// trusts the host key, installs or updates the remote Herdr and shows its
// prompts — approvals Solco's background connections never answer. Setup is
// done when Herdr's check sees the remote answer or the client exits; either
// way the device is then checked for real.
func (s *Shell) setUpDevice(id string) {
	inst := s.machineInstanceByID(id)
	if inst == nil || s.devices.waiting[id] != nil {
		return
	}
	machine, label := *inst.Machine, inst.DisplayName
	task, err := s.runtime.StartMachineSetup(machine)
	if err != nil {
		s.pendingToast = "Couldn't open Terminal: " + firstLineBounded(err.Error(), 120)
		return
	}
	slog.Info("device set up started", "machine_id", id)
	probe := func() bool {
		state, err := s.runtime.MachineStatus(id)
		return err == nil && state.Reachable
	}
	s.startDeviceTask(id, "set-up", task, probe, func(exited bool, _ int, probed bool) {
		if !exited && !probed {
			s.pendingToast = "Stopped waiting for the set up of " + label
			return
		}
		s.checkDevice(id)
	})
}

// checkDevice tries one device for real and answers with a toast.
func (s *Shell) checkDevice(id string) { s.tryDevice(id, false) }

// checkDevices is the switcher header's button: every enabled device that is
// not the one in use gets a real try, and the rows tell the result. A toast
// per device would only talk over each other, so these tries are quiet.
func (s *Shell) checkDevices() {
	for _, instance := range s.machineInstances() {
		if instance.Name != s.activeInstance {
			s.tryDevice(instance.Machine.ID, true)
		}
	}
}

// tryDevice tries a device for real: Herdr's own status check and Solco's
// bridge (the connection that will actually carry the work) run side by side,
// and Solco's outcome is the verdict. A success clears what had been recorded
// against the device; a failure is recorded and, unless quiet, offers its
// recovery.
func (s *Shell) tryDevice(id string, quiet bool) {
	inst := s.machineInstanceByID(id)
	if inst == nil || !inst.Machine.Enabled || s.devices.checking[id] {
		return
	}
	machine, label, name := *inst.Machine, inst.DisplayName, inst.Name
	s.devices.setChecking(id, true)
	go func() {
		var (
			state     herdr.MachineState
			statusErr error
			wg        sync.WaitGroup
		)
		wg.Add(1)
		go func() {
			defer wg.Done()
			state, statusErr = s.runtime.MachineStatus(id)
		}()
		ctx, cancel := context.WithTimeout(context.Background(), deviceSwitchTimeout)
		err := s.runtime.ConnectMachine(ctx, id)
		cancel()
		wg.Wait()
		live := herdr.MasterLive(machine.Target)
		s.onUI(func() { s.finishDeviceCheck(id, name, label, state, statusErr, live, err, quiet) })
	}()
}

// finishDeviceCheck lands one check on the UI lane. A device in use that was
// lost and answers again reconnects at once instead of waiting for the
// watcher's next try.
func (s *Shell) finishDeviceCheck(id, name, label string, state herdr.MachineState, statusErr error, live bool, err error, quiet bool) {
	d := &s.devices
	d.setChecking(id, false)
	d.setMaster(id, live)
	if statusErr == nil {
		if s.machineStates == nil {
			s.machineStates = make(map[string]herdr.MachineState)
		}
		s.machineStates[id] = state
	}
	if err != nil {
		kind := deviceFailureKind(err)
		d.fail(id, kind, err.Error())
		slog.Warn("device check failed", "machine_id", id, "kind", string(kind), "error", err)
		if !quiet {
			s.pendingToastAction = s.deviceFailureToast(label, id, kind, err)
		}
		return
	}
	d.clearFailure(id)
	if d.lostFor(name) != nil {
		s.switchToDevice(name)
		return
	}
	if !quiet {
		s.pendingToast = label + " is online"
	}
}
