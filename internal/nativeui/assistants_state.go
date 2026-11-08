package nativeui

/**
 * [INPUT]: 依赖 automations_ui 的插件能力门（Availability.Root）、internal/personalagent 的 CLI 客户端、herdr.ParseRef/SocketPath
 * [OUTPUT]: （手机缝与变更帧见 remote_personal_agent.go）assistantsState（门、概览缓存、当前选中 Assistant、页面 WebView 句柄）、assistantsAvailable（唯一显示门）、refreshAssistants（后台重读概览）、assistantsClientFor（桥与刷新共用的客户端）、newAssistantsClient/assistantsGo 测试缝
 * [POS]: 产品决策 2026-11-03（Assistants）：Assistants 的权威是 com.whstudio.solco.automations 插件 daemon 里的 SQLite 核心，Solco 只经它的 `assistant …` CLI 取数与下命令；本文件只缓存侧栏要画的概览，渲染路径零 IO，插件缺失/版本不符/远程实例时一切出口关闭、界面零痕迹
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"log/slog"
	"time"

	"github.com/egoist/mygo"

	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/personalagent"
)

const (
	// assistantsRefreshTimeout bounds one overview read.
	assistantsRefreshTimeout = 15 * time.Second
	// assistantsRefreshGap throttles sidebar-driven refreshes: the section
	// repaints often, the overview changes on the scale of seconds.
	assistantsRefreshGap = 5 * time.Second
)

// assistantsState is the Shell's view of the Assistants core: the gate answer,
// the overview the sidebar draws, and the page's WebView. The plugin is the
// only owner of Assistant/Goal/Task/Memory/Approval data; nothing here is a
// mirror beyond the counters of the last read.
type assistantsState struct {
	// ready is the gate: the plugin answered `assistant overview` with this
	// schema. It closes with the automations gate or on a refused read.
	ready    bool
	probing  bool
	overview personalagent.Overview
	loadedAt time.Time
	loading  bool
	// Archived identities are cached for the Settings page only; the plugin
	// remains authoritative and reads happen off the UI lane.
	archived       []personalagent.Assistant
	archivedGroups []personalagent.Group
	archiveLoaded  bool
	archiveLoading bool
	archiveError   string
	// selected is the Assistant the page is on (client-local navigation).
	selected string
	// selectedGroup is the Group the page is on ("" = an Assistant's chat);
	// choosing either clears the other.
	selectedGroup string
	// intent is the sidebar's outstanding ask of the page (assistants_intent.go).
	intent intentState
	// webview is the page's embedded WebView; broken latches a failed creation.
	webview *mygo.WebView
	broken  bool
	// fingerprint is the counters' digest at the last read the phones were told about.
	fingerprint string
	// defaultTried latches the one attempt per gate opening to make the
	// default Primary Assistant on an empty core.
	defaultTried bool
	// previewKey is the catalog key of the Coordinator conversation the shared
	// Chat shows read-only while no live Coordinator is bound (assistants_preview.go).
	previewKey string
	// previewScanAt is when a preview last asked for a catalog round (throttle).
	previewScanAt time.Time
}

var (
	newAssistantsClient = func(root, session string) *personalagent.Client {
		return personalagent.NewClient(root, []string{"HERDR_SOCKET_PATH=" + herdr.SocketPath(session)})
	}
	assistantsGo = func(f func()) { go f() }
)

// assistantsAvailable is the one gate every Assistants surface consults: the
// plugin is installed on this machine (a remote Herdr instance never shows
// it) and its Assistants core answered.
func (s *Shell) assistantsAvailable() bool {
	return s.automationsAvailable() && s.assistants.ready
}

// assistantsClientFor returns a client for the plugin the automations gate
// found, or nil while the gate is closed. Safe to call from any goroutine
// after the gate has been read once on the window lane (see bridge).
func (s *Shell) assistantsClientFor() *personalagent.Client {
	if !s.automationsAvailable() {
		return nil
	}
	return newAssistantsClient(s.automations.avail.Root, herdr.ParseRef(s.activeInstance).Session)
}

// refreshAssistants re-reads the overview in the background. The first read
// is also the capability probe: a refused read leaves the gate closed. force
// skips the throttle; a read in flight is never doubled.
func (s *Shell) refreshAssistants(force bool) {
	st := &s.assistants
	if !s.automationsAvailable() || st.loading {
		return
	}
	now := timeNow()
	if !force && !st.loadedAt.IsZero() && now.Sub(st.loadedAt) < assistantsRefreshGap {
		return
	}
	client := s.assistantsClientFor()
	if client == nil {
		return
	}
	st.loading = true
	assistantsGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), assistantsRefreshTimeout)
		defer cancel()
		overview, err := client.Overview(ctx)
		s.applyOnWindow(func() { s.applyAssistantsOverview(overview, err) })
	})
}

// applyAssistantsOverview stores one read. A failed read closes the gate only
// when it was never open (an older plugin); an open gate keeps the last rows.
func (s *Shell) applyAssistantsOverview(overview personalagent.Overview, err error) {
	st := &s.assistants
	st.loading = false
	if !s.automationsAvailable() {
		s.resetAssistants()
		return
	}
	st.loadedAt = timeNow()
	if err != nil {
		if !st.ready {
			slog.Info("assistants unavailable", "error", err)
		}
		return
	}
	if !st.ready {
		slog.Info("assistants available", "assistants", len(overview.Assistants))
	}
	st.ready = true
	st.overview = overview
	// The waiting number counts the "needs you" inbox: re-project it.
	defer s.republishWaiting()
	if len(overview.Assistants) == 0 {
		s.createDefaultPrimary()
	}
	s.syncRemoteAssistants()
	s.notifyAssistantsChanged()
	if st.selected != "" && st.overviewRow(st.selected) == nil {
		st.selected = ""
	}
	if st.selectedGroup != "" && st.overviewGroup(st.selectedGroup) == nil {
		st.selectedGroup = ""
	}
}

// defaultPrimaryName / defaultPrimaryBrief are the Primary Assistant a fresh
// install gets (2026-11-07): the person's one always-present assistant, which
// can plan work and manage the others. Created once, with a fixed requestId so
// two windows (or a retry) make one.
const (
	defaultPrimaryRequestID = "default-primary"
	defaultPrimaryName      = "Solco"
	defaultPrimaryBrief     = "Your main assistant: it plans the work and can create, rename, archive and hand tasks to your other assistants."
)

// createDefaultPrimary makes the default Primary on an empty core, once per gate
// opening, off the window lane. A failure is only logged; the page's own New
// Assistant stays available.
func (s *Shell) createDefaultPrimary() {
	st := &s.assistants
	if st.defaultTried {
		return
	}
	client := s.assistantsClientFor()
	if client == nil {
		return
	}
	st.defaultTried = true
	assistantsGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), assistantsRefreshTimeout)
		defer cancel()
		a, err := client.CreateAssistant(ctx, defaultPrimaryRequestID, defaultPrimaryName, defaultPrimaryBrief)
		if err != nil {
			slog.Info("default primary assistant not created", "error", err)
			return
		}
		s.applyOnWindow(func() {
			if s.assistants.selected == "" {
				s.assistants.selected = a.ID
			}
			s.refreshAssistants(true)
		})
	})
}

// applyAssistantsOverviewFromBridge lets a page read (which already did the IO
// on the bridge goroutine) refresh the sidebar's counters with no second CLI
// call.
func (s *Shell) applyAssistantsOverviewFromBridge(overview personalagent.Overview) {
	s.applyOnWindow(func() { s.applyAssistantsOverview(overview, nil) })
}

// resetAssistants closes the gate and drops every cached row: a plugin that
// went away takes the surface with it. The WebView handle is kept (the window
// owns it).
func (s *Shell) resetAssistants() {
	s.assistants = assistantsState{webview: s.assistants.webview, broken: s.assistants.broken}
	s.syncRemoteAssistants()
}

// overviewRow finds one Assistant's counters.
func (st *assistantsState) overviewRow(id string) *personalagent.OverviewRow {
	for i := range st.overview.Assistants {
		if st.overview.Assistants[i].ID == id {
			return &st.overview.Assistants[i]
		}
	}
	return nil
}

// overviewGroup finds one Group's row.
func (st *assistantsState) overviewGroup(id string) *personalagent.OverviewGroup {
	for i := range st.overview.Groups {
		if st.overview.Groups[i].ID == id {
			return &st.overview.Groups[i]
		}
	}
	return nil
}
