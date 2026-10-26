package nativeui

/**
 * [INPUT]: 依赖 internal/workboard 的 Attempt（`details --json` 的 attempts：letter/agent/standing/stat，形状取自插件源码 finish_line.rs）与 Client.Pick（`pick <id> <letter>`）、board 缓存的 details、workboardWrite 单一写通道
 * [OUTPUT]: pickDraft、openWorkboardPick、pickDialog（模态：在已通过（standing=passed）的 attempt 里明确选一个，Pick 即确认）、submitPick
 * [POS]: stage=pick（几个 attempt 都通过、等人保留一个）任务的 “Pick…” 动作（行右键菜单与任务页共用 workboardActionsForTask）：不默认选中任何一个，因为保留其一会关闭其余 attempt 的标签页（worktree 与分支留到任务结束）；只有 passed 的可选，是否接受由插件最终裁决；审阅者的对比与推荐留在插件看板（任务页 “Compare in Work Board”）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"log/slog"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/wh-studio/herdr-client/internal/herdr"
	"github.com/wh-studio/herdr-client/internal/workboard"
)

// pickDraft is the open "Pick…" dialog: the task, the attempts that passed,
// and which of them (an index, -1 for none yet) the user chose.
type pickDraft struct {
	open      bool
	id, title string
	passed    []workboard.Attempt
	chosen    int
}

// openWorkboardPick opens the dialog from the attempts the board last read
// for the task; without them there is nothing to choose from.
func (s *Shell) openWorkboardPick(task workboard.Task) {
	if !s.workboardAvailable() {
		return
	}
	s.hideQuickPanel()
	var passed []workboard.Attempt
	if d, ok := s.workboard.board.details[task.ID]; ok {
		for _, a := range d.detail.Attempts {
			if a.Standing == "passed" {
				passed = append(passed, a)
			}
		}
	}
	if len(passed) == 0 {
		s.pendingToast = "No attempt of " + task.ID + " is ready to pick yet"
		return
	}
	title := task.Title
	if title == "" {
		title = task.ID
	}
	s.workboard.pick = pickDraft{open: true, id: task.ID, title: title, passed: passed, chosen: -1}
}

func pickLabel(a workboard.Attempt) string {
	label := strings.ToUpper(a.Letter) + " " + fallbackText(a.Agent, "agent")
	if a.Stat != "" {
		label += " · " + a.Stat
	}
	return label
}

// pickDialog renders the dialog while it is open.
func (s *Shell) pickDialog(c *ui.Context) {
	d := &s.workboard.pick
	if !d.open {
		return
	}
	ui.Modal(c, &d.open, func() {
		sp := Spacing()
		t := c.Theme()
		ui.Column(c).Width(460).MaxWidth(520).Gap(sp.L).Children(func() {
			modalTitle(c, "Keep which attempt?")
			ui.Text(c, "“"+d.title+"” ("+d.id+"). The other attempts' tabs close; their worktrees and branches stay until the task is done.").
				FontSize(Typography().Caption).TextColor(t.TextMuted)
			labels := make([]string, len(d.passed))
			for i, a := range d.passed {
				labels[i] = pickLabel(a)
			}
			ui.Column(c).FillWidth().Gap(sp.S).Children(func() {
				for i, label := range labels {
					i := i
					key := c.Key("pick:" + d.passed[i].Letter)
					row := ui.ButtonBase(key).FillWidth().MinHeight(34).Radius(6).Padding(0, sp.M).AlignItems(ui.Center).Label(label)
					if tint, ok := rowTint(designTokens(t.Dark), d.chosen == i, row.Hovered()); ok {
						row.Background(tint)
					}
					row.Children(func() { ui.Text(key, label).FontSize(Typography().Body).SingleLine() })
					if row.Clicked() {
						d.chosen = i
					}
				}
			})
			ui.Row(c).FillWidth().Gap(sp.M).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					d.open = false
				}
				action := "Keep"
				if d.chosen >= 0 {
					action = "Keep " + strings.ToUpper(d.passed[d.chosen].Letter)
				}
				if ui.PrimaryButton(c, action).Disabled(d.chosen < 0).Clicked() {
					s.submitPick()
				}
			})
		})
	})
}

// submitPick closes the dialog and keeps the attempt that was chosen.
func (s *Shell) submitPick() {
	d := s.workboard.pick
	s.workboard.pick = pickDraft{}
	if !s.workboardAvailable() || d.chosen < 0 || d.chosen >= len(d.passed) {
		return
	}
	letter := d.passed[d.chosen].Letter
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	slog.Info("work board write requested", "op", "pick", "task", d.id, "letter", letter)
	s.workboardWrite("pick", func(ctx context.Context) error { return client.Pick(ctx, d.id, letter) },
		func() { s.pendingToast = "Kept attempt " + strings.ToUpper(letter) + " of " + d.id })
}
