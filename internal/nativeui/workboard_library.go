package nativeui

/**
 * [INPUT]: 依赖 internal/projectlibrary（Solco 自有的 Project 资料库）、internal/workboard Client.Settings/SetSetting/UnsetSetting/Details（只借插件的 instructions 设置与 details 读取）、workboardWrite 同款后台 + applyOnWindow 通道、mygo.Dialog.Open
 * [OUTPUT]: workboardLibraryState、libraryStore、reloadLibrary、syncLibraryBlock（把托管块拼进 worker/reviewer/coordinator 三处 instructions）、addLibraryPaths/setLibraryMode/removeLibraryItem、projectReports/loadProjectReports（Reports 数据）、libraryBrief（New Task 的 “What the agent will get” 预览）、pickLibraryFiles（测试可换的选文件器）
 * [POS]: 上下文管理的原生一半——文件在 <userDataDir>/ProjectLibrary/<project>/，Agent 只读路径、不内联内容；Always/Reference 两档经托管块写进插件的 instructions（不碰用户自己的话，只替换块内），Task 档只经新任务描述里的 @path 行送达；插件仍是任务权威，本层不存任务状态，也不另开 Agent 通道；渲染路径无 IO（列表在打开/改动时读一次）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/projectlibrary"
	"github.com/thewei/solco/internal/workboard"
)

// reportsDoneCap bounds the details reads one Reports view opening makes.
const reportsDoneCap = 16

// workboardLibraryState is the open Library view's cache.
type workboardLibraryState struct {
	store   *projectlibrary.Store
	project string // the Project the cached items belong to
	items   []projectlibrary.Item
	block   string // what the instructions carry for this Project right now
	note    string // how the last delivery to the plugin went
	loading bool   // a Reports read is in flight
}

// libraryStore opens the Solco-owned store; nil where there is no user data
// directory (headless tests inject their own).
func (s *Shell) libraryStore() *projectlibrary.Store {
	lib := &s.workboard.library
	if lib.store == nil && s.userDataDir != "" {
		lib.store = projectlibrary.New(filepath.Join(s.userDataDir, "ProjectLibrary"))
	}
	return lib.store
}

// reloadLibrary reads the Project's items and managed block once.
func (s *Shell) reloadLibrary(project string) {
	lib := &s.workboard.library
	lib.project, lib.items, lib.block = project, nil, ""
	store := s.libraryStore()
	if store == nil || project == "" {
		return
	}
	items, err := store.List(project)
	if err != nil {
		lib.note = "Could not read the library: " + err.Error()
		return
	}
	lib.items = items
	lib.block, _ = store.Block(project)
}

// libraryTargets are the three instruction settings the managed block rides in.
var libraryTargets = []struct {
	name  string
	scope func(project string) workboard.Scope
}{
	{"worker.instructions", func(p string) workboard.Scope { return workboard.Scope{Repo: p} }},
	{"reviewer.instructions", func(p string) workboard.Scope { return workboard.Scope{Repo: p} }},
	{"coordinator.instructions", func(p string) workboard.Scope { return workboard.Scope{Repo: p, Coordinator: true} }},
}

// syncLibraryBlock splices block into each target's own value, leaving the
// user's words alone, and writes only what changed. An empty block removes the
// managed block; a value that ends up empty is unset so the inherited one
// shows again. Every target is tried; the errors are joined.
func syncLibraryBlock(ctx context.Context, client *workboard.Client, project, block string) error {
	var errs []error
	for _, target := range libraryTargets {
		scope := target.scope(project)
		view, err := client.Settings(ctx, scope)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", target.name, err))
			continue
		}
		current := ""
		for _, setting := range view.Settings {
			if setting.Name == target.name && setting.Own {
				current = setting.ValueText()
			}
		}
		if strings.TrimSpace(current) == "none" {
			current = ""
		}
		next := projectlibrary.Splice(current, block)
		if strings.TrimSpace(next) == strings.TrimSpace(current) {
			continue
		}
		if strings.TrimSpace(next) == "" {
			_, err = client.UnsetSetting(ctx, scope, target.name)
		} else {
			_, err = client.SetSetting(ctx, scope, target.name, next)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", target.name, err))
		}
	}
	return errors.Join(errs...)
}

// libraryChanged reloads the view and delivers the new block to the plugin
// off the UI lane.
func (s *Shell) libraryChanged(project string) {
	s.reloadLibrary(project)
	store := s.libraryStore()
	if store == nil || !s.workboardAvailable() || project == workboardOtherProject {
		return
	}
	block := s.workboard.library.block
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	slog.Info("project library sync requested", "project", project, "has_block", block != "")
	workboardGo(func() {
		_ = store.WriteIndex(project)
		ctx, cancel := context.WithTimeout(context.Background(), workboardRefreshTimeout)
		defer cancel()
		err := syncLibraryBlock(ctx, client, project, block)
		s.applyOnWindow(func() {
			note := "Delivered to this project's worker, reviewer and coordinator instructions."
			if block == "" {
				note = "Nothing is delivered to the agents' instructions."
			}
			if err != nil {
				slog.Warn("project library sync failed", "project", project, "error", err)
				note = "Could not update the plugin's instructions: " + clipPreview(err.Error(), 200, 2)
			}
			s.workboard.library.note = note
		})
	})
}

// pickLibraryFiles is the file picker; a test swaps it.
var pickLibraryFiles = func() ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Add files to the Project Library", Multiple: true})
}

// addLibraryPaths copies each file into the library under mode; the first
// refusal is the toast (the rest are still added).
func (s *Shell) addLibraryPaths(project string, mode projectlibrary.Mode, paths []string) []projectlibrary.Item {
	store := s.libraryStore()
	if store == nil {
		return nil
	}
	var added []projectlibrary.Item
	var first error
	for _, path := range paths {
		item, err := store.AddFile(project, path)
		if err == nil && mode != projectlibrary.Reference {
			err = store.SetMode(project, item.ID, mode)
			item.Mode = mode
		}
		if err != nil {
			if first == nil {
				first = fmt.Errorf("%s: %w", filepath.Base(path), err)
			}
			continue
		}
		added = append(added, item)
	}
	if first != nil {
		s.pendingToast = clipPreview(first.Error(), workboardToastMax, 2)
	}
	if len(added) > 0 {
		s.libraryChanged(project)
	}
	return added
}

func (s *Shell) setLibraryMode(project, id string, mode projectlibrary.Mode) {
	store := s.libraryStore()
	if store == nil {
		return
	}
	if err := store.SetMode(project, id, mode); err != nil {
		s.pendingToast = clipPreview(err.Error(), workboardToastMax, 2)
	}
	s.libraryChanged(project)
}

func (s *Shell) removeLibraryItem(project, id string) {
	if store := s.libraryStore(); store != nil {
		if err := store.Remove(project, id); err != nil {
			s.pendingToast = clipPreview(err.Error(), workboardToastMax, 2)
		}
	}
	s.libraryChanged(project)
}

// sizeText is a file size in the units a person reads.
func sizeText(n int64) string {
	switch {
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
	case n >= 1<<10:
		return strconv.FormatInt(n>>10, 10) + " KB"
	}
	return strconv.FormatInt(n, 10) + " B"
}

// reportEntry is one task whose plugin details carry a report.
type reportEntry struct {
	Task workboard.Task
	Link string
}

// projectReports lists the Project's tasks that have a report, from the
// cached details (open and finished alike).
func (s *Shell) projectReports(project workboardProject) []reportEntry {
	var out []reportEntry
	add := func(tasks []workboard.Task) {
		for _, task := range tasks {
			if d, ok := s.workboard.board.details[task.ID]; ok && d.detail.ReportLink != "" {
				out = append(out, reportEntry{Task: task, Link: d.detail.ReportLink})
			}
		}
	}
	for _, group := range project.Groups {
		add(group.Tasks)
	}
	add(project.Done)
	return out
}

// loadProjectReports reads the details finished tasks lack, because the board
// refresh only reads details for tasks still in motion.
func (s *Shell) loadProjectReports(project workboardProject) {
	lib := &s.workboard.library
	if lib.loading || !s.workboardAvailable() {
		return
	}
	var want []string
	for _, task := range project.Done {
		if _, ok := s.workboard.board.details[task.ID]; !ok && len(want) < reportsDoneCap {
			want = append(want, task.ID)
		}
	}
	if len(want) == 0 {
		return
	}
	lib.loading = true
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	workboardGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), workboardRefreshTimeout)
		defer cancel()
		got := map[string]workboard.Detail{}
		for _, id := range want {
			if d, err := client.Details(ctx, id); err == nil {
				got[id] = d
			}
		}
		s.applyOnWindow(func() {
			s.workboard.library.loading = false
			if s.workboard.board.details == nil {
				s.workboard.board.details = map[string]workboardDetail{}
			}
			for id, d := range got {
				// A key no list row carries: the next refresh replaces it.
				s.workboard.board.details[id] = workboardDetail{key: "reports", detail: d}
			}
		})
	})
}

// briefModel is what a new task's agent will be handed, in words.
type briefModel struct {
	Always   []string // named in every agent's instructions
	Attached []string // sent with this task only
	OnDemand int      // listed in INDEX.md
}

// libraryBrief sorts a Project's items into what the preview shows; attached
// is the set of item ids the user ticked for this task.
func libraryBrief(items []projectlibrary.Item, attached map[string]bool) briefModel {
	var b briefModel
	for _, item := range items {
		switch {
		case attached[item.ID]:
			b.Attached = append(b.Attached, item.Name)
		case item.Mode == projectlibrary.Always:
			b.Always = append(b.Always, item.Name)
		case item.Mode == projectlibrary.Reference:
			b.OnDemand++
		}
	}
	return b
}
