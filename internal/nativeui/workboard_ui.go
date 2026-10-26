package nativeui

/**
 * [INPUT]: 依赖 internal/workboard 的能力门（Detect）、herdr.PluginListJSON / Manager.InvokePluginAction/FocusPluginPane、Shell 的 mutateSelecting 与本机/远程判断
 * [OUTPUT]: workboardState（探测结果、去抖与任务缓存 board）、detectWorkboard（异步探测）、workboardAvailable（唯一显示门）、openWorkBoard（命令面板 Open Work Board 的执行体）
 * [POS]: herdr-projects 插件在原生壳里的唯一入口层；插件未安装/未启用/版本越界/远程实例时本文件的一切出口都关闭，界面零痕迹；任务权威仍归插件，本层不存任务状态
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"log/slog"
	"time"

	"github.com/wh-studio/herdr-client/internal/herdr"
	"github.com/wh-studio/herdr-client/internal/settings"
	"github.com/wh-studio/herdr-client/internal/workboard"
)

const (
	// workboardDetectGap is the smallest gap between two focus-triggered
	// detections: `herdr plugin list` spawns the Herdr CLI, and plugin
	// install state changes on the scale of minutes, not window hand-backs.
	workboardDetectGap = 30 * time.Second
	// workboardDetectTimeout bounds one detection; a stalled CLI leaves the
	// previous answer in place.
	workboardDetectTimeout = 8 * time.Second
	// workboardOpenTimeout bounds the plugin's open action.
	workboardOpenTimeout = 20 * time.Second
)

// workboardState is the Shell's view of the herdr-projects plugin.
type workboardState struct {
	avail         workboard.Availability
	lastDetect    time.Time
	detecting     bool
	board         workboardBoard
	page          workboardPage
	project       workboardProjectPage
	archived      workboardArchived
	newTask       newTaskDraft
	newAutomation newAutomationDraft
	delegate      delegateDraft
	pick          pickDraft
	settings      workboardSettingsState
}

// workboardAvailable is the one gate every workboard surface and command
// consults. Plugins are installed on this machine, so a remote Herdr
// instance never shows the board.
func (s *Shell) workboardAvailable() bool {
	return s.workboard.avail.Supported && !s.activeIsRemote()
}

// workboardBoardMode reports the default view: Project and task rows (and every
// other way into a native board page) open the plugin's own board Tab. The
// native pages are the opt-in "native" view (General.WorkboardView).
func (s *Shell) workboardBoardMode() bool { return !s.settings.General.WorkboardNative() }

// setWorkboardView persists the Native | Board choice (Settings → Work Board).
func (s *Shell) setWorkboardView(native bool) {
	view := "board"
	if native {
		view = "native"
	}
	s.applySettings(func(v *settings.Settings) error {
		v.General.WorkboardView = view
		return nil
	})
}

// detectWorkboard re-checks the plugin in the background. force skips the
// focus debounce (startup); an in-flight detection is never doubled.
func (s *Shell) detectWorkboard(force bool) {
	now := timeNow()
	if s.workboard.detecting {
		return
	}
	if !force && !s.workboard.lastDetect.IsZero() && now.Sub(s.workboard.lastDetect) < workboardDetectGap {
		return
	}
	s.workboard.detecting = true
	s.workboard.lastDetect = now
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), workboardDetectTimeout)
		defer cancel()
		avail := workboard.Unreadable()
		if raw, err := herdr.PluginListJSON(ctx); err == nil {
			avail = workboard.Detect(raw)
		}
		s.applyOnWindow(func() { s.applyWorkboardAvailability(avail) })
	}()
}

// applyWorkboardAvailability stores one detection result. It logs only when
// the answer changes (operation, reason code and version — no plugin data).
func (s *Shell) applyWorkboardAvailability(avail workboard.Availability) {
	s.workboard.detecting = false
	prev := s.workboard.avail
	s.workboard.avail = avail
	if !avail.Supported {
		// Zero trace: a plugin that went away takes its rows with it.
		s.workboard.board = workboardBoard{}
		s.workboard.page = workboardPage{}
		s.workboard.project = workboardProjectPage{}
		s.invalidateQuickPanel()
	}
	if avail.Supported && !prev.Supported {
		s.refreshWorkboardTasks(true) // first read as the gate opens
	}
	if prev == avail {
		return
	}
	slog.Info("work board plugin detection", "supported", avail.Supported, "reason", string(avail.Reason), "version", avail.Version)
}

// openWorkBoard runs the plugin's own open action — it keeps the plugin's
// one-board-per-machine rule — and adopts the focus Herdr ends on, so the
// board Tab shows in the native Terminal like any other Tab. A stale palette
// entry (plugin removed since the index was built) revalidates and does
// nothing.
func (s *Shell) openWorkBoard() {
	if !s.workboardAvailable() {
		s.status = "The work board is not available."
		return
	}
	instance := s.activeInstance
	s.showViewTerminal()
	s.mutateSelecting(func() (herdr.Projection, error) {
		ctx, cancel := context.WithTimeout(context.Background(), workboardOpenTimeout)
		defer cancel()
		opened, err := s.runtime.InvokePluginAction(ctx, instance, workboard.PluginID, workboard.OpenAction)
		if err != nil {
			return herdr.Projection{}, err
		}
		// The action opens the Tab but, invoked outside a Herdr client, leaves
		// the focus behind: ask Herdr to focus the board pane.
		return s.runtime.FocusPluginPane(ctx, instance, workboard.BoardPaneTitle, opened)
	})
}
