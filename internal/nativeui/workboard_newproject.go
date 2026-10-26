package nativeui

/**
 * [INPUT]: 依赖 mygo.Dialog.Open（系统文件夹选择器）、internal/workboard 的 Client.AddProject（无任务登记）/Pin/CoordinatorControl、workboardWrite 单一写通道
 * [OUTPUT]: newProjectDraft、openNewProject（选文件夹 → 弹模态）、newProjectDialog（文件夹、立即启动协调者）、submitNewProject
 * [POS]: New Project 的唯一入口（Projects 区标题的 “+”）。不要求第一个任务：先登记文件夹（插件无“创建 Project”动词，AddProject 借一条瞬时自动化登记），再 pin（无任务也留在侧栏），可选立即起协调者。不自建任何存储；归档沿用 Project 菜单的 Archive Project…，彻底删除插件没有 API，故不做
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
)

// newProjectDraft is the open New Project dialog.
type newProjectDraft struct {
	open        bool
	folder      string
	coordinator bool
}

// pickProjectFolder is the folder picker; a test swaps it.
var pickProjectFolder = func() (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Choose the Project's working folder", Directory: true})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

// openNewProject picks a folder on this machine, then asks for the first task.
func (s *Shell) openNewProject() {
	if !s.workboardAvailable() {
		return
	}
	if s.activeIsRemote() {
		// The picker only sees this machine's disk; the plugin on a remote
		// Session needs a path that exists there.
		s.pendingToast = "Switch to a local Session to add a Project from a local folder"
		return
	}
	s.hideQuickPanel()
	go func() {
		folder, err := pickProjectFolder()
		s.applyOnUI(func() {
			if err != nil || strings.TrimSpace(folder) == "" {
				return
			}
			s.workboard.newProject = newProjectDraft{open: true, folder: folder, coordinator: true}
		})
	}()
}

// newProjectDialog renders the dialog while it is open.
func (s *Shell) newProjectDialog(c *ui.Context) {
	d := &s.workboard.newProject
	if !d.open {
		return
	}
	ui.Modal(c, &d.open, func() {
		sp := Spacing()
		t := c.Theme()
		ui.Column(c).Width(420).MaxWidth(480).Gap(sp.L).Children(func() {
			modalTitle(c, "New Project")
			ui.Column(c).FillWidth().Gap(sp.XS).Children(func() {
				ui.Text(c, filepath.Base(d.folder)).FontSize(Typography().Body).SingleLine()
				ui.Text(c, d.folder).FontSize(Typography().Caption).TextColor(t.TextMuted).SingleLine().Tooltip(d.folder)
			})
			ui.Checkbox(c, &d.coordinator, "Start its coordinator now")
			ui.Text(c, "The folder becomes a Project with no task. It and its coordinator's conversation are kept until you archive it.").
				FontSize(Typography().Caption).TextColor(t.TextMuted)
			ui.Row(c).FillWidth().Gap(sp.M).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					d.open = false
				}
				if ui.PrimaryButton(c, "Create").Clicked() {
					s.submitNewProject()
				}
			})
		})
	})
}

// submitNewProject registers the folder through the plugin, pins the Project
// so it stays in the sidebar, and starts its coordinator when asked.
func (s *Shell) submitNewProject() {
	d := s.workboard.newProject
	s.workboard.newProject = newProjectDraft{}
	if !s.workboardAvailable() {
		return
	}
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	slog.Info("work board write requested", "op", "new-project", "coordinator", d.coordinator)
	toast := ""
	s.workboardWrite("new-project", func(ctx context.Context) error {
		name, err := client.AddProject(ctx, d.folder)
		if err != nil {
			return err
		}
		toast = "Added project " + name
		if err := client.Pin(ctx, name); err != nil {
			toast += "; couldn't pin it: " + workboardFailureToast(err)
			return nil
		}
		if d.coordinator {
			if err := client.CoordinatorControl(ctx, name, "restart"); err != nil {
				toast += "; its coordinator didn't start: " + workboardFailureToast(err)
			}
		}
		return nil
	}, func() { s.pendingToast = toast })
}
