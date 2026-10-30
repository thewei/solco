package nativeui

/**
 * [INPUT]: 依赖 internal/herdr 的 StartMachineAdd（Terminal 接力：`herdr machine add`）/ListMachines/ValidateTarget、Hosts 页的 ~/.ssh/config 索引（别名原样交给 Herdr：Herdr 的 ssh 配置 Include 用户的 ~/.ssh/config，Port/IdentityFile/ProxyJump 随别名保留）、controls.go 的 textField/segmentedControl/regularButton、dialogs 的 modalTitle
 * [OUTPUT]: 对外提供 Add Device 面板 deviceAddDialog（SSH target + ~/.ssh/config 别名快捷项、Remote session Auto/Named、可选名称；本地先校验空值/缺会话名/重名；等待态 Waiting for Herdr… + Stop Waiting；失败态 Try Again 保留已填值）与 openDeviceAdd（面板底部、Hosts 页、命令面板的同一入口）、submit/等待/收尾流程
 * [POS]: 设备接入流程：认证、安装、重启的审批全在用户自己的 Terminal 里由 Herdr 询问，Solco 只轮询本地 `machine list`（不走 SSH）看档案是否出现、读脚本退出码区分失败；成功只出 Toast「Added X · Switch」，不自动切换；设备档案仍只存在于 Herdr
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
)

// addPhase is where the Add Device sheet is: asking, waiting for Terminal, or
// reporting that Herdr stopped before saving the device.
type addPhase int

const (
	addForm addPhase = iota
	addWaiting
	addFailed
)

const (
	addPollEvery   = 2 * time.Second
	addAliasChips  = 6
	addSessionAuto = 0
	addSessionName = 1
)

// addDraft is what the Add Device form holds, compared to know whether an
// error still speaks about what is on screen.
type addDraft struct {
	target, session, label string
	named                  int
}

// deviceAdd is the Add Device sheet's draft and its wait in flight.
type deviceAdd struct {
	open    bool
	phase   addPhase
	target  string
	named   int // addSessionAuto | addSessionName
	session string
	label   string
	err     string
	errFor  addDraft // the draft err was raised for; any edit retires it

	token  uint64 // a poll belongs to the sheet that started it
	task   *herdr.TerminalTask
	cancel context.CancelFunc
}

// openDeviceAdd opens the Add Device sheet, with the SSH target filled in when
// the caller knows it (the Hosts page's registration).
func (s *Shell) openDeviceAdd(target string) {
	s.devices.open = false
	s.stopDeviceAdd()
	s.devices.add = deviceAdd{open: true, target: strings.TrimSpace(target)}
	s.ensureHostsIndex()
}

// addRemoteMachine is the Hosts page's way in: the same sheet, target filled.
func (s *Shell) addRemoteMachine(target string) { s.openDeviceAdd(target) }

// defaultDeviceLabel is the name Herdr gives a device that is added without
// one: the host, with the session behind it when one is named.
func defaultDeviceLabel(target, session string) string {
	host := strings.TrimSpace(target)
	if i := strings.LastIndex(host, "@"); i >= 0 {
		host = host[i+1:]
	}
	if host == "" {
		host = "host"
	}
	if session = strings.TrimSpace(session); session != "" {
		return host + "/" + session
	}
	return host
}

// draft is the form's current content.
func (add deviceAdd) draft() addDraft {
	return addDraft{target: add.target, session: add.session, label: add.label, named: add.named}
}

// formError is the error the form shows: the last one raised, until the draft
// it was about is edited.
func (add deviceAdd) formError() string {
	if add.err != "" && add.errFor == add.draft() {
		return add.err
	}
	return ""
}

// values reads the draft: the trimmed target, the session ("" lets Herdr
// pick) and the label ("" takes Herdr's default).
func (add deviceAdd) values() (target, session, label string) {
	target = strings.TrimSpace(add.target)
	if add.named == addSessionName {
		session = strings.TrimSpace(add.session)
	}
	return target, session, strings.TrimSpace(add.label)
}

// deviceAddProblem is what is wrong with the draft before Terminal opens, in
// words the sheet shows beside the field; "" means it can go ahead.
func (s *Shell) deviceAddProblem(add deviceAdd) string {
	target, session, label := add.values()
	switch {
	case target == "":
		return "Enter an SSH target, like user@host."
	case herdr.ValidateTarget(target) != nil:
		return "That SSH target isn't valid."
	case add.named == addSessionName && session == "":
		return "Enter the session name, or choose Auto."
	}
	if label == "" {
		label = defaultDeviceLabel(target, session)
	}
	for _, instance := range s.machineInstances() {
		if strings.EqualFold(instance.Machine.Label, label) {
			return "A device named " + label + " already exists. Give this one another name."
		}
	}
	return ""
}

// deviceAddAliases are the ~/.ssh/config hosts the sheet offers under the
// target field: concrete aliases only, a handful of them.
func (s *Shell) deviceAddAliases() []string {
	var aliases []string
	seen := make(map[string]bool)
	for _, entry := range s.hosts.entries {
		alias := entry.Alias()
		if alias == "" || seen[alias] {
			continue
		}
		seen[alias] = true
		aliases = append(aliases, alias)
		if len(aliases) == addAliasChips {
			break
		}
	}
	return aliases
}

// submitDeviceAdd validates the draft and hands the rest to Terminal. The
// target goes to Herdr as typed: an ssh-config alias keeps its Port, identity
// and jump host there, which a flattened user@host would lose.
func (s *Shell) submitDeviceAdd() {
	add := &s.devices.add
	if problem := s.deviceAddProblem(*add); problem != "" {
		add.err, add.errFor = problem, add.draft()
		return
	}
	add.err = ""
	s.startDeviceAdd(add.values())
}

// startDeviceAdd opens Terminal in `herdr machine add` and starts watching for
// the profile to appear.
func (s *Shell) startDeviceAdd(target, session, label string) {
	add := &s.devices.add
	task, err := s.runtime.StartMachineAdd(target, label, session)
	if err != nil {
		add.err, add.errFor = "Couldn't open Terminal: "+firstLineBounded(err.Error(), 120), add.draft()
		return
	}
	known := make(map[string]bool)
	for _, instance := range s.machineInstances() {
		known[instance.Machine.ID] = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), deviceTaskLimit)
	add.phase, add.task, add.cancel = addWaiting, task, cancel
	add.token = s.devices.nextToken()
	slog.Info("device add started", "session_named", session != "")
	go s.watchDeviceAdd(ctx, add.token, task, known)
}

// newMachine is the first saved profile that was not there before.
func newMachine(machines []herdr.Machine, known map[string]bool) *herdr.Machine {
	for index := range machines {
		if !known[machines[index].ID] {
			return &machines[index]
		}
	}
	return nil
}

// watchDeviceAdd polls Herdr's local profile list (no SSH) until the new
// profile shows, the script ends without one, or the wait times out.
func (s *Shell) watchDeviceAdd(ctx context.Context, token uint64, task *herdr.TerminalTask, known map[string]bool) {
	ticker := time.NewTicker(addPollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.onUI(func() { s.failDeviceAdd(token, "Still waiting on Terminal. Finish there, then try again.") })
			return
		case <-ticker.C:
		}
		if machines, err := s.runtime.ListMachines(); err == nil {
			if added := newMachine(machines, known); added != nil {
				s.onUI(func() { s.finishDeviceAdd(token, *added) })
				return
			}
		}
		if code, done := task.Result(); done {
			// The profile may have been saved just before the script ended.
			if machines, err := s.runtime.ListMachines(); err == nil {
				if added := newMachine(machines, known); added != nil {
					s.onUI(func() { s.finishDeviceAdd(token, *added) })
					return
				}
			}
			s.onUI(func() {
				s.failDeviceAdd(token, fmt.Sprintf("Herdr stopped before saving the device (exit %d). Check the Terminal window for why.", code))
			})
			return
		}
	}
}

// finishDeviceAdd lands a saved device: the sheet closes, the list reloads,
// and a toast offers the switch — adding never changes what is in use.
func (s *Shell) finishDeviceAdd(token uint64, machine herdr.Machine) {
	add := &s.devices.add
	if add.token != token || add.phase != addWaiting {
		return
	}
	s.stopDeviceAdd()
	add.open = false
	label, name := machine.Label, herdr.MachineInstanceKey(machine.ID)
	slog.Info("device added", "machine_id", machine.ID)
	s.reloadInstanceList(func(err error) {
		s.pendingToastAction = toastAction{message: "Added " + label, label: "Switch", action: func() { s.switchToDevice(name) }}
		s.refreshMachineStatus(true)
	})
}

// failDeviceAdd shows why Herdr did not save the device, keeping the draft.
func (s *Shell) failDeviceAdd(token uint64, message string) {
	add := &s.devices.add
	if add.token != token || add.phase != addWaiting {
		return
	}
	s.stopDeviceAdd()
	add.phase, add.err = addFailed, message
	slog.Warn("device add did not finish")
}

// stopDeviceAdd ends the wait in flight, if any: the poll goes stale, the
// script's folder goes, and a sheet that was waiting is a form again. The
// Terminal window stays; it is the user's.
func (s *Shell) stopDeviceAdd() {
	add := &s.devices.add
	if add.cancel != nil {
		add.cancel()
		add.cancel = nil
	}
	if add.task != nil {
		add.task.Cleanup()
		add.task = nil
	}
	add.token = s.devices.nextToken()
	if add.phase == addWaiting {
		add.phase = addForm
	}
}

// deviceAddDialog renders the Add Device sheet while it is open. Closing it
// (Escape, the backdrop) while it waits stops the wait.
func (s *Shell) deviceAddDialog(c *ui.Context) {
	add := &s.devices.add
	if !add.open {
		if add.phase == addWaiting {
			s.stopDeviceAdd()
		}
		return
	}
	ui.Modal(c, &add.open, func() {
		sp := Spacing()
		ui.Column(c).Width(420).MaxWidth(480).Gap(sp.L).Children(func() {
			modalTitle(c, "Add Device")
			switch add.phase {
			case addWaiting:
				s.deviceAddWaiting(c)
			case addFailed:
				s.deviceAddFailed(c)
			default:
				s.deviceAddForm(c)
			}
		})
	})
}

// deviceAddForm asks for the three things Herdr's `machine add` takes.
func (s *Shell) deviceAddForm(c *ui.Context) {
	add := &s.devices.add
	t := c.Theme()
	sp := Spacing()
	typ := Typography()
	field := func(title string, body func()) {
		ui.Column(c).FillWidth().Gap(sp.XS).Children(func() {
			ui.Text(c, title).FontSize(typ.BodySmall).TextColor(t.TextMuted)
			body()
		})
	}
	submit := false
	field("SSH target", func() {
		if textField(c, &add.target).Label("SSH target").Placeholder("user@host").AutoFocus().FillWidth().Submitted() {
			submit = true
		}
		if aliases := s.deviceAddAliases(); len(aliases) > 0 {
			ui.Row(c).FillWidth().Gap(sp.XS).Wrap().AlignItems(ui.Center).Children(func() {
				ui.Text(c, "From your ssh config").FontSize(typ.Caption).TextColor(t.TextMuted)
				for _, alias := range aliases {
					if compactButton(c, alias).Clicked() {
						add.target = alias
					}
				}
			})
		}
	})
	field("Remote session", func() {
		segmentedControl(c, &add.named, "Auto", "Named").FillWidth()
		if add.named == addSessionName {
			if textField(c, &add.session).Label("Session name").Placeholder("default").AutoFocus().FillWidth().Submitted() {
				submit = true
			}
			return
		}
		ui.Text(c, "Herdr picks the one running session on the device, asks when there are several, and uses default when there is none.").
			FontSize(typ.Caption).TextColor(t.TextMuted)
	})
	field("Name (optional)", func() {
		target, session, _ := add.values()
		if textField(c, &add.label).Label("Name").Placeholder(defaultDeviceLabel(target, session)).FillWidth().Submitted() {
			submit = true
		}
	})
	ui.Text(c, "Solco opens Terminal to finish the setup there. Sign-in prompts and approvals are answered in Terminal, never in Solco.").
		FontSize(typ.Caption).TextColor(t.TextMuted)
	if message := add.formError(); message != "" {
		ui.Text(c, message).FontSize(typ.Caption).TextColor(designTokens(t.Dark).StatusColor(ToneError, t.Dark))
	}
	ui.Row(c).FillWidth().Gap(sp.M).Justify(ui.End).Children(func() {
		if regularButton(c, "Cancel").Clicked() {
			add.open = false
		}
		if regularPrimaryButton(c, "Add Device").Clicked() {
			submit = true
		}
	})
	if submit {
		s.submitDeviceAdd()
	}
}

// deviceAddWaiting is the sheet while Terminal holds the work.
func (s *Shell) deviceAddWaiting(c *ui.Context) {
	add := &s.devices.add
	t := c.Theme()
	sp := Spacing()
	typ := Typography()
	target, _, _ := add.values()
	ui.Row(c).FillWidth().Gap(sp.M).AlignItems(ui.Center).Children(func() {
		ui.Spinner(c).Size(18, 18)
		ui.Column(c).Grow(1).MinWidth(0).Gap(sp.XXS).Children(func() {
			ui.Text(c, "Waiting for Herdr…").FontWeight(WeightSemibold)
			ui.Text(c, "Finish the steps in the Terminal window that just opened. Solco adds "+target+" when Herdr saves it.").
				FontSize(typ.Caption).TextColor(t.TextMuted)
		})
	})
	ui.Row(c).FillWidth().Gap(sp.M).Justify(ui.End).Children(func() {
		if regularButton(c, "Stop Waiting").Clicked() {
			s.stopDeviceAdd()
		}
	})
}

// deviceAddFailed is the sheet after Herdr stopped without saving: its reason,
// and the draft intact for another go.
func (s *Shell) deviceAddFailed(c *ui.Context) {
	add := &s.devices.add
	sp := Spacing()
	ui.Text(c, add.err).FontSize(Typography().Body).TextColor(designTokens(c.Theme().Dark).StatusColor(ToneError, c.Theme().Dark))
	ui.Row(c).FillWidth().Gap(sp.M).Justify(ui.End).Children(func() {
		if regularButton(c, "Cancel").Clicked() {
			add.open = false
		}
		if regularPrimaryButton(c, "Try Again").Clicked() {
			add.phase, add.err = addForm, ""
		}
	})
}
