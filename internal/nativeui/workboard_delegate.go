package nativeui

/**
 * [INPUT]: 依赖 internal/workboard 的 Client.TaskSettings（`show <id> --json`）/DelegateWith（`delegate <id> --<setting> V`）、workboard_settings_controls.go 的 workboardSettingControlWith（与设置页同一套选择型控件）、loadWorkboardModels、workboardWrite 单一写通道
 * [OUTPUT]: delegateDraft、openWorkboardDelegateWith、delegateDialog（模态：该任务当下生效的 worker.agent/worker.effort/worker.access/plan.approve/review.when/merge.by；只提交被改动的项）、submitDelegateWith
 * [POS]: To do 任务的 “Delegate with…” 动作（行右键菜单与任务页共用 workboardActionsForTask）：设置只作用于这一个任务，不写任何范围的设置；取值合法性与整体拒绝（被拒绝的选项不改动任务）全归插件；控件集合只选单值选择类设置——数值/文本类（limit.*、review.rounds…）留在 Settings 与插件看板
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"log/slog"

	"github.com/egoist/mygo/ui"
	"github.com/wh-studio/herdr-client/internal/herdr"
	"github.com/wh-studio/herdr-client/internal/workboard"
)

// delegateSettings are the settings the dialog offers, in its order.
var delegateSettings = []string{"worker.agent", "worker.effort", "worker.access", "plan.approve", "review.when", "merge.by"}

// delegateDraft is the open "Delegate with…" dialog: the task, what it runs on
// now, and the choices made on top.
type delegateDraft struct {
	open      bool
	id, title string
	view      workboard.SettingsView
	loading   bool
	failed    bool
	chosen    map[string]string
	order     []string // chosen names in the order they were first changed
}

// openWorkboardDelegateWith opens the dialog for a task and reads the
// settings it runs on.
func (s *Shell) openWorkboardDelegateWith(task workboard.Task) {
	if !s.workboardAvailable() {
		return
	}
	s.hideQuickPanel()
	title := task.Title
	if title == "" {
		title = task.ID
	}
	s.workboard.delegate = delegateDraft{open: true, id: task.ID, title: title, loading: true, chosen: map[string]string{}}
	if st := &s.workboard.settings; !st.modelsTried {
		st.modelsTried = true
		s.loadWorkboardModels()
	}
	id := task.ID
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	workboardGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), workboardRefreshTimeout)
		defer cancel()
		view, err := client.TaskSettings(ctx, id)
		s.applyOnWindow(func() {
			d := &s.workboard.delegate
			if !d.open || d.id != id {
				return // closed, or another task's dialog since
			}
			d.loading, d.failed, d.view = false, err != nil, view
		})
	})
}

// effective is the dialog's settings as the task would run them: what the
// plugin shows, with the choices on top.
func (d delegateDraft) effective() []workboard.Setting {
	out := make([]workboard.Setting, 0, len(delegateSettings))
	for _, name := range delegateSettings {
		for _, setting := range d.view.Settings {
			if setting.Name != name {
				continue
			}
			if v, ok := d.chosen[name]; ok {
				setting.Value, setting.From = v, "this task"
			}
			out = append(out, setting)
		}
	}
	return out
}

// choose notes one choice, keeping the order they were first made in.
func (d *delegateDraft) choose(name, value string) {
	if _, ok := d.chosen[name]; !ok {
		d.order = append(d.order, name)
	}
	d.chosen[name] = value
}

// delegateDialog renders the dialog while it is open.
func (s *Shell) delegateDialog(c *ui.Context) {
	d := &s.workboard.delegate
	if !d.open {
		return
	}
	ui.Modal(c, &d.open, func() {
		sp := Spacing()
		t := c.Theme()
		ui.Column(c).Width(480).MaxWidth(520).Gap(sp.L).Children(func() {
			modalTitle(c, "Delegate “"+d.title+"”")
			ui.Text(c, "These apply to this task only; nothing else changes.").FontSize(Typography().Caption).TextColor(t.TextMuted)
			switch {
			case d.loading:
				ui.Text(c, "Reading the task's settings…").TextColor(t.TextMuted)
			case d.failed:
				ui.Text(c, "Couldn't read this task's settings. Delegate runs it on what it has.").TextColor(t.TextMuted)
			default:
				effective := d.effective()
				for _, setting := range effective {
					setting := setting
					key := c.Key("dlg:" + setting.Name)
					ui.Row(key).FillWidth().Gap(sp.M).AlignItems(ui.Center).Children(func() {
						ui.Text(key, setting.Name).FontSize(Typography().BodySmall).Width(104).Shrink(0)
						s.workboardSettingControlWith(key, setting, effective, func(value string) { d.choose(setting.Name, value) })
					})
				}
			}
			ui.Row(c).FillWidth().Gap(sp.M).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					d.open = false
				}
				if ui.PrimaryButton(c, "Delegate").Disabled(d.loading).Clicked() {
					s.submitDelegateWith()
				}
			})
		})
	})
}

// submitDelegateWith closes the dialog and delegates with what was chosen.
func (s *Shell) submitDelegateWith() {
	d := s.workboard.delegate
	s.workboard.delegate = delegateDraft{}
	if !s.workboardAvailable() {
		return
	}
	var with []workboard.SettingValue
	for _, name := range d.order {
		with = append(with, workboard.SettingValue{Name: name, Value: d.chosen[name]})
	}
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	slog.Info("work board write requested", "op", "delegate", "task", d.id, "settings", len(with))
	s.workboardWrite("delegate", func(ctx context.Context) error { return client.DelegateWith(ctx, d.id, with) }, nil)
}
