package nativeui

/**
 * [INPUT]: 依赖 mygo.Dialog.Open（系统文件夹选择器）、internal/workboard 的 Client.AddProject/Pin/CoordinatorControl、workboardWrite 单一写通道
 * [OUTPUT]: newProjectDraft、openNewProject（选文件夹 → 弹模态）、newProjectDialog（文件夹、第一个任务标题、立即启动协调者）、submitNewProject
 * [POS]: New Project 的唯一入口。插件没有“创建 Project”动词——Project 在某文件夹第一次加任务时登记（repos.json），所以对话框必须带第一个任务标题；登记后立刻 pin（任务清空后仍留在侧栏），可选启动协调者。不自建任何存储；归档沿用 Project 菜单的 Archive Project…，彻底删除插件没有 API，故不做
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/wh-studio/herdr-client/internal/herdr"
)

// newProjectDraft is the open New Project dialog.
type newProjectDraft struct {
	open        bool
	folder      string
	title       string
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
			ui.TextInput(c, &d.title).Label("First task").Placeholder("What to do first").AutoFocus().FillWidth()
			ui.Checkbox(c, &d.coordinator, "Start its coordinator now")
			ui.Text(c, "The plugin adds a Project when its first task is added; the Project and its coordinator's conversation are kept until you archive it.").
				FontSize(Typography().Caption).TextColor(t.TextMuted)
			ui.Row(c).FillWidth().Gap(sp.M).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					d.open = false
				}
				if ui.PrimaryButton(c, "Create").Clicked() && strings.TrimSpace(d.title) != "" {
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
		name, err := client.AddProject(ctx, d.folder, d.title)
		if err != nil {
			return err
		}
		label := name
		if label == "" {
			label = filepath.Base(d.folder)
		}
		toast = "Added project " + label
		if name == "" {
			return nil
		}
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
