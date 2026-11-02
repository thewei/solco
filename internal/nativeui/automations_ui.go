package nativeui

/**
 * [INPUT]: 依赖 internal/automations 的能力门（Detect）、herdr.PluginListJSON、Shell 的本机/远程判断与 applyOnWindow
 * [OUTPUT]: automationsState（探测结果、去抖与列表缓存 list）、detectAutomations（异步探测）、automationsAvailable（唯一显示门）、refreshAutomations（后台重读，automationsGo/newAutomationsClient 测试缝）
 * [POS]: com.whstudio.solco.automations 插件在原生壳里的唯一入口层；插件未安装/未启用/版本越界/远程实例时本文件的一切出口都关闭，界面零痕迹；调度与运行状态权威仍归插件，本层不存状态镜像，列表只画插件 `list --json` 的答案
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"log/slog"
	"time"

	"github.com/thewei/solco/internal/automations"
	"github.com/thewei/solco/internal/herdr"
)

const (
	// automationsDetectGap is the smallest gap between two focus-triggered
	// detections: `herdr plugin list` spawns the Herdr CLI, and plugin
	// install state changes on the scale of minutes, not window hand-backs.
	automationsDetectGap = 30 * time.Second
	// automationsDetectTimeout bounds one detection; a stalled CLI leaves the
	// previous answer in place.
	automationsDetectTimeout = 8 * time.Second
	// automationsRefreshTimeout bounds one list or history read.
	automationsRefreshTimeout = 20 * time.Second
	// automationsRefreshGap throttles page-open refreshes.
	automationsRefreshGap = 5 * time.Second
)

// automationsState is the Shell's view of the com.whstudio.solco.automations
// plugin: the gate answer plus the list the page draws from.
type automationsState struct {
	avail      automations.Availability
	lastDetect time.Time
	detecting  bool
	list       automationsList
}

// automationsAvailable is the one gate every automations surface and command
// consults. Plugins are installed on this machine, so a remote Herdr
// instance never shows the page.
func (s *Shell) automationsAvailable() bool {
	return s.automations.avail.Supported && !s.activeIsRemote()
}

// detectAutomations re-checks the plugin in the background. force skips the
// focus debounce (startup); an in-flight detection is never doubled.
func (s *Shell) detectAutomations(force bool) {
	now := timeNow()
	if s.automations.detecting {
		return
	}
	if !force && !s.automations.lastDetect.IsZero() && now.Sub(s.automations.lastDetect) < automationsDetectGap {
		return
	}
	s.automations.detecting = true
	s.automations.lastDetect = now
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), automationsDetectTimeout)
		defer cancel()
		avail := automations.Unreadable()
		if raw, err := herdr.PluginListJSON(ctx); err == nil {
			avail = automations.Detect(raw)
		}
		s.applyOnWindow(func() { s.applyAutomationsAvailability(avail) })
	}()
}

// applyAutomationsAvailability stores one detection result. It logs only when
// the answer changes (operation, reason code and version — no plugin data).
func (s *Shell) applyAutomationsAvailability(avail automations.Availability) {
	s.automations.detecting = false
	prev := s.automations.avail
	s.automations.avail = avail
	if !avail.Supported {
		// Zero trace: a plugin that went away takes its page state with it.
		s.automations.list = automationsList{}
	}
	if avail.Supported && !prev.Supported {
		s.refreshAutomations(true) // first read as the gate opens
	}
	if prev == avail {
		return
	}
	slog.Info("automations plugin detection", "supported", avail.Supported, "reason", string(avail.Reason), "version", avail.Version)
}

// refreshAutomations re-reads the plugin's list in the background. force
// skips the throttle; a refresh in flight is never doubled; a closed gate
// does nothing.
func (s *Shell) refreshAutomations(force bool) {
	list := &s.automations.list
	if !s.automationsAvailable() || list.loading {
		return
	}
	now := timeNow()
	if !force && !list.loadedAt.IsZero() && now.Sub(list.loadedAt) < automationsRefreshGap {
		return
	}
	list.loading = true
	client := newAutomationsClient(s.automations.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	automationsGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), automationsRefreshTimeout)
		defer cancel()
		result, err := client.List(ctx)
		s.applyOnWindow(func() { s.applyAutomationsList(result, err) })
	})
}

// applyAutomationsList stores one refresh. A failed read keeps the previous
// rows and marks the list stale; a refresh that lands after the gate closed
// is dropped, so a removed plugin leaves nothing behind.
func (s *Shell) applyAutomationsList(result automations.ListResult, err error) {
	list := &s.automations.list
	list.loading = false
	if !s.automationsAvailable() {
		s.automations.list = automationsList{}
		return
	}
	list.loadedAt = timeNow()
	list.failed = err != nil
	if err != nil {
		list.errText = automationsFailureToast(err)
		slog.Warn("automations refresh failed", "error", err)
		return
	}
	list.errText = ""
	list.result = result
	if sel := list.selected; sel != "" && list.automation(sel) == nil {
		list.selected = "" // the row the drawer held is gone
	}
}

// loadAutomationHistory reads one automation's runs for the drawer.
func (s *Shell) loadAutomationHistory(name string) {
	if !s.automationsAvailable() {
		return
	}
	if s.automations.list.historyLoading == name {
		return
	}
	s.automations.list.historyLoading = name
	client := newAutomationsClient(s.automations.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	automationsGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), automationsRefreshTimeout)
		defer cancel()
		result, err := client.History(ctx, name)
		s.applyOnWindow(func() {
			list := &s.automations.list
			if list.selected != name {
				return // the drawer moved on
			}
			list.historyLoading = ""
			if err != nil {
				slog.Warn("automations history read failed", "name", name, "error", err)
				return
			}
			if list.history == nil {
				list.history = map[string]automations.HistoryResult{}
			}
			list.history[name] = result
		})
	})
}

var (
	newAutomationsClient = automations.NewClient
	automationsGo        = func(f func()) { go f() }
)
