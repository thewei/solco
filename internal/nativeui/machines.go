package nativeui

import (
	"log/slog"
	"strings"
	"time"

	"github.com/thewei/solco/internal/herdr"
)

// Remote machine presentation state. Saved SSH machines are Herdr's own
// remote-connection machinery; this file only drives herdr's sanctioned
// management commands for the device switcher (device_switcher.go) and the
// Hosts page. The profile list lives in Herdr's client state — never a
// Solco-side registry.

const machineStatusRetryWindow = 30 * time.Second

// machineMenuSuffix renders the availability suffix for one machine row as
// plain text (the Hosts list); the device switcher draws its own state rows.
func machineMenuSuffix(machine *herdr.Machine, state herdr.MachineState, known bool) string {
	if machine != nil && !machine.Enabled {
		return " — disabled"
	}
	if !known {
		return ""
	}
	switch {
	case state.Reachable:
		return " — reachable"
	case state.Attention && state.Message != "":
		message := []rune(state.Message)
		if len(message) > 48 {
			message = append(message[:45], '…')
		}
		return " — " + string(message)
	case state.Attention:
		return " — needs attention"
	default:
		return " — unreachable"
	}
}

// machineInstances returns the saved-machine entries of the instance list.
func (s *Shell) machineInstances() []herdr.Instance {
	machines := make([]herdr.Instance, 0, 2)
	for _, instance := range s.instances {
		if instance.Machine != nil {
			machines = append(machines, instance)
		}
	}
	return machines
}

// activeIsRemote reports whether the active instance belongs to a remote
// machine. Local-folder workflows (New Workspace from Folder, local file
// panels) are meaningless against remote instance paths.
func (s *Shell) activeIsRemote() bool {
	active := s.activeInstanceInfo()
	return active != nil && active.Machine != nil
}

// refreshMachineStatus runs one throttled background availability check for
// all saved machines (fresh SSH round trips, so seconds) and re-reads whether
// Solco's shared SSH connection to each target is alive. The device switcher's
// open and the machine flows trigger it; results only refresh the rows.
func (s *Shell) refreshMachineStatus(force bool) {
	if s.win == nil {
		return
	}
	machines := s.machineInstances()
	if !force {
		if s.machineStatusRunning || time.Since(s.machineStatusAt) < machineStatusRetryWindow {
			return
		}
		if len(machines) == 0 {
			return
		}
	}
	targets := make(map[string]string, len(machines))
	for _, instance := range machines {
		targets[instance.Machine.ID] = instance.Machine.Target
	}
	s.machineStatusRunning = true
	go func() {
		states, err := s.runtime.MachineStates()
		masters := make(map[string]bool, len(targets))
		for id, target := range targets {
			masters[id] = herdr.MasterLive(target)
		}
		s.applyOnUI(func() {
			s.machineStatusRunning = false
			s.machineStatusAt = time.Now()
			for id, live := range masters {
				s.devices.setMaster(id, live)
			}
			if err != nil {
				slog.Warn("machine status check failed", "error", err)
				return
			}
			s.machineStates = states
		})
	}()
}

// reloadInstanceList re-reads the instance list (this Mac's sessions and the
// saved devices) off the UI lane and lands it without touching loading or
// offline: the list is a view of Herdr's registry, not a runtime mutation, so
// a failed read must not take the window offline. after runs on the UI lane
// with the read's error.
func (s *Shell) reloadInstanceList(after func(err error)) {
	go func() {
		instances, err := s.runtime.ListAllInstances()
		s.onUI(func() {
			if err == nil {
				s.instances = instances
			} else {
				slog.Warn("instance list reload failed", "error", err)
			}
			if after != nil {
				after(err)
			}
		})
	}()
}

// renameMachine renames a saved device's display label. A name another device
// already carries is refused here, before Herdr is asked.
func (s *Shell) renameMachine(id, label string) {
	label = strings.TrimSpace(label)
	for _, instance := range s.machineInstances() {
		if instance.Machine.ID != id && strings.EqualFold(instance.Machine.Label, label) {
			s.pendingToast = "A device named " + label + " already exists"
			return
		}
	}
	go func() {
		err := s.runtime.RenameMachine(id, label)
		s.onUI(func() {
			if err != nil {
				s.pendingToast = "Couldn't rename the device: " + firstLineBounded(err.Error(), 120)
				return
			}
			s.reloadInstanceList(nil)
		})
	}()
}

// setDeviceEnabled switches a saved device on or off in Herdr without
// forgetting it. Disabling the device in use returns the window to this Mac;
// a disabled device's SSH bridge closes once the window is off it.
func (s *Shell) setDeviceEnabled(id string, enabled bool) {
	inst := s.machineInstanceByID(id)
	if inst == nil {
		return
	}
	label, name := inst.DisplayName, inst.Name
	go func() {
		var err error
		if enabled {
			err = s.runtime.EnableMachine(id)
		} else {
			err = s.runtime.DisableMachine(id)
		}
		s.onUI(func() { s.finishSetDeviceEnabled(id, name, label, enabled, err) })
	}()
}

// finishSetDeviceEnabled lands the Enable/Disable result on the UI lane.
func (s *Shell) finishSetDeviceEnabled(id, name, label string, enabled bool, err error) {
	if err != nil {
		verb := "enable"
		if !enabled {
			verb = "disable"
		}
		s.pendingToast = "Couldn't " + verb + " " + label + ": " + firstLineBounded(err.Error(), 120)
		return
	}
	s.reloadInstanceList(func(error) {
		if enabled {
			return
		}
		if sw := s.devices.switching; sw != nil && sw.id == id {
			s.cancelDeviceSwitch()
		}
		s.stopDeviceTask(id)
		if s.activeInstance == name {
			s.returnToThisMac()
		}
		go herdr.CloseBridge(id)
	})
}

// forgetMachine removes a saved device from Herdr. Only that device
// disconnects; its remote session keeps running on its own machine. The
// shared SSH connection Solco holds is closed unless another saved device
// points at the same target; forgetting the device in use returns the window
// to this Mac, and the device's SSH bridge closes once the window is off it.
func (s *Shell) forgetMachine(id string) {
	inst := s.machineInstanceByID(id)
	if inst == nil {
		return
	}
	machine, label, name := *inst.Machine, inst.DisplayName, inst.Name
	go func() {
		err := s.runtime.RemoveMachine(id)
		var instances []herdr.Instance
		if err == nil {
			instances, err = s.runtime.ListAllInstances()
		}
		if err == nil && !targetShared(instances, machine.Target) {
			herdr.CloseMaster(machine.Target)
		}
		s.onUI(func() { s.finishForget(id, name, label, instances, err) })
	}()
}

// targetShared reports whether a saved device still points at target: its SSH
// connection is then still in use and stays open.
func targetShared(instances []herdr.Instance, target string) bool {
	for _, instance := range instances {
		if instance.Machine != nil && instance.Machine.Target == target {
			return true
		}
	}
	return false
}

// finishForget lands a forget on the UI lane: everything remembered about the
// device goes, a switch to it in flight is cancelled, and the window leaves it
// if it was in use.
func (s *Shell) finishForget(id, name, label string, instances []herdr.Instance, err error) {
	if err != nil {
		s.pendingToast = "Couldn't forget " + label + ": " + firstLineBounded(err.Error(), 120)
		return
	}
	if sw := s.devices.switching; sw != nil && sw.id == id {
		s.cancelDeviceSwitch()
	}
	delete(s.machineStates, id)
	s.devices.forget(id)
	if s.devices.lostFor(name) != nil {
		s.devices.lost = nil
	}
	s.instances = instances
	if s.activeInstance == name {
		s.returnToThisMac()
	}
	go herdr.CloseBridge(id)
	s.pendingToast = "Forgot " + label
}
