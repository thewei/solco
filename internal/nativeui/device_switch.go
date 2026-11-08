package nativeui

/**
 * [INPUT]: 依赖 internal/herdr 的 ConnectMachine（可取消的桥接 bring-up）、Projection、FailureOf/ErrProtocolTooOld、ParseRef，Shell 的 enterInstance/openInstance/toastAction、device_state 的状态与派生、ds_components 的 inlineNoticeActions
 * [OUTPUT]: 对外提供事务式设备切换（switchToDevice → 后台 dialDevice → UI 线程 finishDeviceSwitch：成功才 enterInstance，失败/取消/过期结果一律不动当前设备，不置 offline/loading）、cancelDeviceSwitch、失败 toast（deviceFailureToast，按失败类型给恢复动作）、掉线记录（noteDeviceLost/noteDeviceRecovered，由 watcher 调用）、returnToThisMac（忘记/停用当前设备后回本机）、deviceBanner（Connecting/掉线横幅）与 deviceRouteStage（掉线时页面变暗并拦截指针）
 * [POS]: 设备切换的流程层：切换是事务，先在后台证明远端 Herdr 会应答，再一次性换入；Herdr 仍是工作区/会话/进程权威，这里只编排本地呈现
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
)

// deviceDial is the outcome of the first half of a switch.
type deviceDial struct {
	projection herdr.Projection
	err        error
}

// switchToDevice makes a saved device the one in use, as a transaction: the
// connection is proven off the UI lane while the current device keeps working,
// and only a success replaces it. A failure leaves everything as it was and
// offers the recovery its cause asks for; Cancel and any other pick retire an
// attempt in flight, and its late result is dropped.
func (s *Shell) switchToDevice(name string) {
	sw := s.beginDeviceSwitch(name)
	if sw == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), deviceSwitchTimeout)
	sw.cancel = cancel
	slog.Info("device switch started", "machine_id", sw.id)
	go func() {
		result := s.dialDevice(ctx, sw.name, sw.id)
		s.onUI(func() { s.finishDeviceSwitch(sw.token, result) })
	}()
}

// beginDeviceSwitch validates the pick and records the attempt; nil means
// there is nothing to start (not a saved device, disabled, already in use, or
// already being switched to).
func (s *Shell) beginDeviceSwitch(name string) *deviceSwitch {
	inst := s.instanceByName(name)
	if inst == nil || inst.Machine == nil {
		return nil
	}
	d := &s.devices
	switch {
	case !inst.Machine.Enabled:
		s.pendingToast = inst.DisplayName + " is disabled. Enable it from the device switcher."
		return nil
	case name == s.activeInstance && d.lostFor(name) == nil:
		return nil
	case d.switching != nil && d.switching.name == name:
		return nil
	}
	s.cancelDeviceSwitch()
	d.open = false
	d.switching = &deviceSwitch{
		name:   name,
		id:     inst.Machine.ID,
		label:  inst.DisplayName,
		token:  d.nextToken(),
		cancel: func() {},
		since:  time.Now(),
	}
	return d.switching
}

// dialDevice is the blocking half of a switch: bring the bridge up, prove the
// remote Herdr answers, read its projection. It touches no shell state.
func (s *Shell) dialDevice(ctx context.Context, name, id string) deviceDial {
	if err := s.runtime.ConnectMachine(ctx, id); err != nil {
		return deviceDial{err: err}
	}
	projection, err := s.runtime.Projection(name)
	return deviceDial{projection: projection, err: err}
}

// finishDeviceSwitch lands the result on the UI lane. A token that is no
// longer the attempt in flight (cancelled, superseded) is dropped untouched.
func (s *Shell) finishDeviceSwitch(token uint64, result deviceDial) {
	d := &s.devices
	sw := d.switching
	if sw == nil || sw.token != token {
		return
	}
	d.switching = nil
	sw.cancel()
	if result.err != nil {
		s.failDeviceSwitch(sw, result.err)
		return
	}
	if previous := s.activeInstanceInfo(); previous != nil && previous.Machine == nil {
		d.lastLocal = previous.Name
	}
	if s.activeInstance == sw.name {
		// Reconnecting the device in use: its terminal attachments died with
		// the old connection.
		s.closeAllTerminals()
	}
	s.generation.Add(1)
	s.enterInstance(sw.name, result.projection)
	d.lost = nil
	d.clearFailure(sw.id)
	slog.Info("device switch finished", "machine_id", sw.id, "elapsed_ms", time.Since(sw.since).Milliseconds())
}

// failDeviceSwitch records why the device would not open and offers the way
// forward. The device in use stays as it was: nothing here touches
// activeInstance, loading or offline.
func (s *Shell) failDeviceSwitch(sw *deviceSwitch, err error) {
	kind := deviceFailureKind(err)
	s.devices.fail(sw.id, kind, err.Error())
	slog.Warn("device switch failed", "machine_id", sw.id, "kind", string(kind), "error", err)
	s.pendingToastAction = s.deviceFailureToast(sw.label, sw.id, kind, err)
}

// cancelDeviceSwitch retires the attempt in flight, if any.
func (s *Shell) cancelDeviceSwitch() {
	sw := s.devices.switching
	if sw == nil {
		return
	}
	s.devices.switching = nil
	sw.cancel()
	slog.Info("device switch cancelled", "machine_id", sw.id)
}

// deviceFailureKind classifies a switch or probe error: the bridge types its
// own failures; a timeout is the host not answering; an outdated remote is a
// setup problem.
func deviceFailureKind(err error) herdr.FailureKind {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return herdr.FailureNetwork
	case errors.Is(err, herdr.ErrProtocolTooOld):
		return herdr.FailureServer
	}
	return herdr.FailureOf(err)
}

// deviceFailureToast is the toast a failed attempt shows: what happened, and
// the one action that resolves it.
func (s *Shell) deviceFailureToast(label, id string, kind herdr.FailureKind, err error) toastAction {
	signIn := func() { s.signInDevice(id) }
	setUp := func() { s.setUpDevice(id) }
	check := func() { s.checkDevice(id) }
	switch {
	case kind == herdr.FailureAuth:
		return toastAction{message: "Sign in to " + label + " to connect", label: "Sign In", action: signIn}
	case kind == herdr.FailureHostKey:
		return toastAction{message: label + "'s host key isn't trusted yet", label: "Sign In", action: signIn}
	case kind == herdr.FailureNetwork:
		return toastAction{message: "Couldn't reach " + label, label: "Check Again", action: check}
	case errors.Is(err, herdr.ErrProtocolTooOld):
		return toastAction{message: "Herdr on " + label + " is too old", label: "Set Up", action: setUp}
	case kind == herdr.FailureServer:
		return toastAction{message: "Herdr isn't running on " + label, label: "Set Up", action: setUp}
	}
	return toastAction{message: "Couldn't connect to " + label, label: "Check Again", action: check}
}

// noteDeviceLost is the watcher's report that the device in use stopped
// answering its event stream: the window shows the Connection lost banner and
// dims the page until the stream is back or the user leaves.
func (s *Shell) noteDeviceLost(instance string, generation uint64, err error) {
	if !herdr.ParseRef(instance).IsRemote() {
		return
	}
	kind := deviceFailureKind(err)
	detail := firstLineBounded(err.Error(), 160)
	s.applyOnUI(func() {
		if generation != s.generation.Load() || instance != s.activeInstance {
			return
		}
		d := &s.devices
		if d.lost == nil || d.lost.instance != instance {
			d.lost = &deviceOutage{instance: instance, since: time.Now()}
		}
		d.lost.kind, d.lost.detail = kind, detail
		if inst := s.instanceByName(instance); inst != nil && inst.Machine != nil {
			d.fail(inst.Machine.ID, kind, detail)
		}
	})
}

// noteDeviceRecovered is the watcher's report that the event stream of the
// device in use is back, with a fresh projection read through it. The terminal
// attachments died with the old connection, so they are rebuilt.
func (s *Shell) noteDeviceRecovered(instance string, generation uint64, projection herdr.Projection) {
	s.applyOnUI(func() {
		if generation != s.generation.Load() || instance != s.activeInstance || s.devices.lostFor(instance) == nil {
			return
		}
		s.devices.lost = nil
		label := instance
		if inst := s.instanceByName(instance); inst != nil {
			label = inst.DisplayName
			if inst.Machine != nil {
				s.devices.clearFailure(inst.Machine.ID)
			}
		}
		s.closeAllTerminals()
		s.applyProjection(projection, false)
		s.pendingToast = label + " reconnected"
	})
}

// returnToThisMac moves the window back to this Mac after the device in use
// went away (forgotten or disabled): the local session it came from, else the
// Herdr default. With no local session at all the window shows the chooser.
func (s *Shell) returnToThisMac() {
	local := s.localInstances()
	if next := preferredInstanceName(local, s.devices.lastLocal); next != "" {
		s.openInstance(next)
		return
	}
	if s.watchCancel != nil {
		s.watchCancel()
		s.watchCancel = nil
	}
	s.closeAllTerminals()
	s.activeInstance = ""
	s.projection = herdr.Projection{}
	s.clearLocalSelection()
	s.selected = ""
	s.devices.lost = nil
	s.status = "Choose a Session"
}

// deviceBanner is the strip under the title bar that says what the window is
// doing about a device: connecting to one (with Cancel), or the one in use
// having stopped answering. It sits above every route.
func (s *Shell) deviceBanner(c *ui.Context) {
	d := &s.devices
	if sw := d.switching; sw != nil {
		inlineNoticeActions(c, ToneInfo, "Connecting to "+sw.label+"…", func() {
			if regularButton(c, "Cancel").Clicked() {
				s.cancelDeviceSwitch()
			}
		})
		return
	}
	lost := d.lostFor(s.activeInstance)
	if lost == nil {
		return
	}
	label := s.instanceLabel(lost.instance)
	var id string
	if inst := s.instanceByName(lost.instance); inst != nil && inst.Machine != nil {
		id = inst.Machine.ID
	}
	signIn := lost.kind == herdr.FailureAuth || lost.kind == herdr.FailureHostKey
	setUp := lost.kind == herdr.FailureServer
	waiting := id != "" && d.waiting[id] != nil
	text := label + " stopped answering. Solco keeps retrying."
	switch {
	case waiting:
		text = "Finish in Terminal. Solco reconnects " + label + " when it is done."
	case signIn:
		text = "Sign in to " + label + " to reconnect."
	}
	inlineNoticeActions(c, ToneAttention, text, func() {
		if id != "" && !waiting {
			if signIn && regularButton(c, "Sign In…").Clicked() {
				s.signInDevice(id)
			}
			if setUp && regularButton(c, "Set Up…").Clicked() {
				s.setUpDevice(id)
			}
		}
		if regularButton(c, "Switch to This Mac").Clicked() {
			s.returnToThisMac()
		}
	})
}

// deviceRouteStage draws the route. While the device in use is lost its page
// is a stale picture: dimmed, with a transparent shield over it so a click
// cannot act on a device that is not answering. The sidebar, the title bar and
// the banner stay live, so the way out is always there.
func (s *Shell) deviceRouteStage(c *ui.Context) {
	if s.devices.lostFor(s.activeInstance) == nil {
		s.routeView(c)
		return
	}
	ui.Column(c).Grow(1).MinHeight(0).MinWidth(0).Opacity(0.5).Children(func() {
		s.routeView(c)
		ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0)
	})
}
