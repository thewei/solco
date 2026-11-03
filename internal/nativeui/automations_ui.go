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
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/thewei/solco/internal/automations"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/remotehost"
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
	watcher    automations.Watcher // list → notifiable steps (automations_notify.go)
	// remote is the read-only snapshot the Host's goroutines serve the phone
	// from (remote_automations.go); the window thread is its only writer.
	remote atomic.Pointer[remotehost.Automations]
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
		s.syncRemoteAutomations()
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
		s.automations.watcher.Reset()
		s.syncRemoteAutomations()
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
	var before string
	if sel := list.selected; sel != "" {
		before = list.lastRunKey(sel)
	}
	list.result = result
	s.notifyAutomationSteps(result.Automations)
	s.syncRemoteAutomations()
	if sel := list.selected; sel != "" {
		switch {
		case list.automation(sel) == nil:
			list.selected = "" // the row the drawer held is gone
		case list.lastRunKey(sel) != before:
			// A run started or finished since the drawer read its history;
			// without this the drawer kept showing the old list until it was
			// reopened.
			list.historyLoading = ""
			s.loadAutomationHistory(sel)
			// A run that ends while its drawer is on screen is being read.
			if s.router != nil && s.router.Path() == routeAutomations {
				s.markAutomationSeen(sel)
			}
		}
	}
	s.pollAutomationsWhileActive()
}

// initialLoad is whether the page is waiting for its very first read — the
// only time a spinner belongs on it. Later background refreshes (the Header
// count, the due-window poll) keep showing the last answer; reading them as
// "loading" flipped an empty list between "No automations" and a spinner
// every few seconds.
func (l *automationsList) initialLoad() bool {
	return l.loading && l.loadedAt.IsZero()
}

// lastRunKey identifies the newest run the list shows for one automation, by
// id and status, so a change in either is noticed.
func (l *automationsList) lastRunKey(name string) string {
	a := l.automation(name)
	if a == nil || a.LastRun == nil {
		return ""
	}
	return a.LastRun.RunID + "/" + a.LastRun.Status
}

// schedulerMissing is whether something should be firing and nothing is: the
// read succeeded, an automation is waiting on its schedule, and the plugin
// reports no daemon.
func (l *automationsList) schedulerMissing() bool {
	if l.loadedAt.IsZero() || l.failed || l.result.Scheduler != 0 {
		return false
	}
	for _, a := range l.result.Automations {
		if !a.Draft && !a.Disabled {
			return true
		}
	}
	return false
}

// busy is whether the list should be re-read on the short timer: a run is
// going, or a scheduled one is due within the window — so the step that
// follows (started, finished) reaches the notifications without the page open.
func (l *automationsList) busy(now time.Time) bool {
	if l.active() {
		return true
	}
	for _, a := range l.result.Automations {
		if a.Draft || a.Disabled || a.NextRun == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, a.NextRun); err == nil && t.Sub(now) < automationsDueWindow {
			return true
		}
	}
	return false
}

// active is whether any run is still going; the list is then
// re-read on a short timer so its status turns without the user reopening
// the page.
func (l *automationsList) active() bool {
	for _, a := range l.result.Automations {
		if a.LastRun != nil && (a.LastRun.Status == "running" || a.LastRun.Status == "queued" || a.LastRun.Status == "scheduled") {
			return true
		}
	}
	return false
}

// pollAutomationsWhileActive schedules one more read while a run is in
// flight or about to start, on any page — the steps are notified from these
// reads. One timer at a time; each read decides whether to arm the next.
func (s *Shell) pollAutomationsWhileActive() {
	list := &s.automations.list
	if automationsPollGap <= 0 || list.pollPending || !s.automationsAvailable() || !list.busy(timeNow()) {
		return
	}
	list.pollPending = true
	automationsGo(func() {
		time.Sleep(automationsPollGap)
		s.applyOnWindow(func() {
			s.automations.list.pollPending = false
			s.refreshAutomations(true)
		})
	})
}

// startAutomationsScheduler launches the plugin's daemon (it logs beside the
// plugin's state) and re-reads the list once it has had time to register.
func (s *Shell) startAutomationsScheduler() {
	if !s.automationsAvailable() {
		return
	}
	logPath := ""
	if state := s.automations.list.result.State; state != "" {
		logPath = filepath.Join(state, "daemon.log")
	}
	client := newAutomationsClient(s.automations.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	slog.Info("automations scheduler start requested")
	automationsGo(func() {
		err := client.StartScheduler(logPath)
		time.Sleep(automationsStartSettle)
		s.applyOnWindow(func() {
			if err != nil {
				slog.Warn("automations scheduler start failed", "error", err)
				s.pendingToast = automationsFailureToast(err)
			} else {
				s.pendingToast = "Scheduler started."
			}
			s.refreshAutomations(true)
		})
	})
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
	// automationsPollGap is the wait between reads while a run is going
	// (0 disables polling); automationsStartSettle is how long a freshly
	// started daemon gets to write its pid before the list is re-read.
	automationsPollGap = 8 * time.Second
	// automationsDueWindow is how close a scheduled run must be before the
	// list is polled on the short timer to catch its start.
	automationsDueWindow   = 90 * time.Second
	automationsStartSettle = 1500 * time.Millisecond
)
