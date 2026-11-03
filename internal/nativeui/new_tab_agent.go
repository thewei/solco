package nativeui

/**
 * [INPUT]: app.SessionStarter（桌面与手机共用的「启动新会话」服务层：Kinds / StartSession）、agent.PermissionFullAccess、Shell.recentLaunchKinds（MRU，随 settings.Session 持久）、herdr.Manager.Projection
 * [OUTPUT]: 提供 Tab 条与侧栏的「+」菜单按钮（newTabControl / newTabMenu）：点 + 总是弹菜单（Terminal 在前，其后 MRU 排序的 Agent，上次用的标 last used；Antigravity CLI 默认传参全部权限）；⌘T/侧栏行 + 仍用 lastLaunchKind；选 Agent 直接经 SessionStarter 默认参数启动，Settings 默认 Chat 时待新 pane 的 Agent 卡出现即进入 Chat（空会话邀请态，会话身份由 Chat 渲染路径/hook 之后补绑；completeAgentOpen，不再经空白 draft）；orderLaunchKinds / rememberLaunchKind / startNewTab
 * [POS]: 新建 Tab/Pane 选 Agent 的唯一入口（2026-11-01 用户拍板，design: designs/ux-audit-4 议题 7，v1 不含 split-with-Agent；2026-11-02 增 Antigravity CLI 默认全权限）；Agent 走既有 launch transaction（与 Continue、手机 launch 同一个 SessionStarter），不造第二条启动路径；种类清单在后台读取，渲染路径不做 IO
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

// recentLaunchKindsKept bounds the remembered kinds (every kind may be listed).
const recentLaunchKindsKept = 8

// newTabStarter is the launch service the split button drives; production is
// app.LaunchService.Sessions(), tests inject a fake.
type newTabStarter interface {
	Kinds() []app.SessionKind
	StartSession(ctx context.Context, instance string, spec app.SessionSpec) (app.SessionResult, error)
}

// orderLaunchKinds is the menu order: Agents the user launched, newest first,
// then the Agents never launched in the catalog's own order, and Terminal last.
func orderLaunchKinds(kinds []app.SessionKind, recent []string) []app.SessionKind {
	var out, rest []app.SessionKind
	var terminal []app.SessionKind
	used := map[string]bool{}
	byID := map[string]app.SessionKind{}
	for _, kind := range kinds {
		byID[kind.ID] = kind
	}
	for _, id := range recent {
		if kind, ok := byID[id]; ok && id != app.TerminalKind && !used[id] {
			out = append(out, kind)
			used[id] = true
		}
	}
	for _, kind := range kinds {
		switch {
		case kind.ID == app.TerminalKind:
			terminal = append(terminal, kind)
		case !used[kind.ID]:
			rest = append(rest, kind)
		}
	}
	return append(append(out, rest...), terminal...)
}

// rememberLaunchKind moves id to the front of the MRU list.
func rememberLaunchKind(recent []string, id string) []string {
	kept := append(make([]string, 0, recentLaunchKindsKept), id)
	for _, other := range recent {
		if other != id && len(kept) < recentLaunchKindsKept {
			kept = append(kept, other)
		}
	}
	return kept
}

// lastLaunchKind is what the + button starts: the kind used last while the
// catalog still offers it, Terminal otherwise.
func (s *Shell) lastLaunchKind(kinds []app.SessionKind) app.SessionKind {
	for _, id := range s.recentLaunchKinds {
		for _, kind := range kinds {
			if kind.ID == id {
				return kind
			}
		}
	}
	return app.SessionKind{ID: app.TerminalKind, Label: "Terminal"}
}

func (s *Shell) newTabLauncher() newTabStarter {
	if s.newTabStarter != nil {
		return s.newTabStarter
	}
	if s.launch == nil {
		return nil
	}
	return s.launch.Sessions()
}

// ensureLaunchKinds reads the launchable kinds once, off the render path
// (Kinds may run `herdr agent start --help`).
func (s *Shell) ensureLaunchKinds() {
	if s.launchKinds != nil || s.launchKindsLoading {
		return
	}
	starter := s.newTabLauncher()
	if starter == nil {
		return
	}
	s.launchKindsLoading = true
	s.dispatch(func() {
		kinds := starter.Kinds()
		s.applyOnWindow(func() {
			s.launchKinds = kinds
			s.launchKindsLoading = false
		})
	})
}

// newTabMenu is the one kind menu behind every "+" that starts a Tab (Tab bar,
// Workspace row, Agent-group row, and the New Tab submenu of the context menus):
// Terminal first, then the Agents — the last-used one first and marked. A choice
// starts that kind in project; the catalog is read in the background, so until it
// arrives the menu holds Terminal alone.
func (s *Shell) newTabMenu(m *ui.Menu, project herdr.Project) {
	s.ensureLaunchKinds()
	last := s.lastLaunchKind(s.launchKinds)
	kinds := s.launchKinds
	if len(kinds) == 0 {
		kinds = []app.SessionKind{{ID: app.TerminalKind, Label: "Terminal"}}
	}
	ordered := orderLaunchKinds(kinds, s.recentLaunchKinds)
	pick := func(kind app.SessionKind) {
		label := kind.Label
		if kind.ID == last.ID && kind.ID != app.TerminalKind {
			label += "  ·  last used"
		}
		if m.Item(label).Chosen() {
			s.startNewTab(project, kind)
		}
	}
	for _, kind := range ordered {
		if kind.ID == app.TerminalKind {
			pick(kind)
		}
	}
	agents := false
	for _, kind := range ordered {
		if kind.ID != app.TerminalKind {
			if !agents {
				m.Separator()
				agents = true
			}
			pick(kind)
		}
	}
}

// newTabControl is the Tab bar's "+": one button that always opens the kind
// menu (newTabMenu). ⌘T starts the last-used kind directly — a key has no
// anchor for a menu.
func (s *Shell) newTabControl(c *ui.Context, k *gorexColors, project herdr.Project) {
	add := gorexIconButton(c, k, iconPlus, "New Tab", "", false, gorexIconBtn, gorexIconGlyph)
	tipped(c, add, "New Tab in "+project.Label, tipKeys(acceleratorKeycaps(accelNewTab)))
	add.Menu(func(m *ui.Menu) { s.newTabMenu(m, project) })
}

// startNewTab opens a Tab in project with kind: Terminal through the existing
// Herdr tab path, an Agent through the shared launch transaction, then selects
// the pane it created.
func (s *Shell) startNewTab(project herdr.Project, kind app.SessionKind) {
	s.recentLaunchKinds = rememberLaunchKind(s.recentLaunchKinds, kind.ID)
	if kind.ID == app.TerminalKind {
		s.newProjectTab(project)
		return
	}
	starter, instance := s.newTabLauncher(), s.activeInstance
	if starter == nil || instance == "" {
		return
	}
	requestID := fmt.Sprintf("tab-%s-%d", kind.ID, time.Now().UnixNano())
	spec := app.SessionSpec{
		RequestID:   requestID,
		WorkspaceID: project.ID,
		CWD:         project.CWD,
		Agent:       history.AgentID(kind.ID),
	}
	if spec.Agent == history.AgentAntigravity {
		spec.Permission = agent.PermissionFullAccess
	}
	// The launch holds the Chat surface from the click (2026-11-03): the
	// launching card shows immediately, the new Tab is followed the moment
	// a projection carries it, and no intermediate projection can flip the
	// surface while the pane boots in the background.
	s.beginLaunch(project.ID, kind, requestID)
	s.dispatch(func() {
		result, err := starter.StartSession(context.Background(), instance, spec)
		var projection *herdr.Projection
		if result.PaneID != "" && s.runtime != nil {
			if fresh, perr := s.runtime.Projection(instance); perr == nil {
				projection = &fresh
			}
		}
		s.applyOnWindow(func() {
			if err != nil {
				slog.Warn("new tab agent failed", "instance", instance, "agent", kind.ID, "error", err)
				s.pendingToast = "Could not start " + kind.Label + ": " + err.Error()
				s.failLaunch(kind.Label, err)
				return
			}
			if s.activeInstance != instance {
				s.finishLaunch()
				return
			}
			if result.PaneID == "" {
				s.failLaunch(kind.Label, errTransactionNoPane)
				return
			}
			if projection != nil {
				s.applyProjection(*projection, false)
			}
			// Terminal keeps its own landing; an Agent pane lands by launch
			// intent (see landLaunchedPane).
			s.landLaunchedPane(result.PaneID)
			if err == nil && s.settings.General.AgentChatFirst() {
				s.pendingAgentOpen = &pendingAgentOpen{PaneID: result.PaneID, At: time.Now()}
			}
			s.finishLaunch()
		})
	})
}

// landLaunchedPane selects a freshly launched pane and lands on the surface
// the launch intent calls for: an Agent pane opens Chat the moment its card
// ships with the launch (Settings → General → Agent view = Chat). showPane's
// hardwired Terminal view used to slam that Chat closed right after the
// selection sync opened it and then strand the pane until a later workbench
// rebuild re-ran completeAgentOpen — or forever when none came (2026-11-02
// surface-flash loop). Shared by the New Tab and the Continue launch paths.
func (s *Shell) landLaunchedPane(paneID string) {
	s.selectPane(paneID)
	if s.surface.current() == WorkspaceSurfaceChat {
		if s.router.Path() != routeWorkspace {
			s.router.Push(routeWorkspace)
		}
		return
	}
	s.showViewTerminal()
}

// pendingAgentOpen is a freshly launched Agent pane that lands on Chat as soon
// as its Agent card exists (Settings → General → Agent view = Chat).
type pendingAgentOpen struct {
	PaneID string
	At     time.Time
}

// agentOpenPatience is how long a launched Agent may take to show up as a
// card before the pane simply stays on its Terminal.
const agentOpenPatience = 15 * time.Second

// completeAgentOpen runs after every Agent-card rebuild (workbench.go): the
// moment the new pane's card exists, Chat opens on it. A session identity is
// not awaited — the page starts on its empty-session invitation and the Chat
// surface's own render path binds the transcript as soon as the Agent (or its
// hook) reports one. If the user moved to another pane meanwhile, nothing
// changes.
func (s *Shell) completeAgentOpen() {
	p := s.pendingAgentOpen
	if p == nil {
		return
	}
	if s.selectedPaneID != p.PaneID || paneByID(s.projection, p.PaneID) == nil {
		s.pendingAgentOpen = nil
		return
	}
	if _, ok := s.agentCardForPane(p.PaneID); !ok {
		if time.Since(p.At) > agentOpenPatience {
			s.pendingAgentOpen = nil
		}
		return
	}
	s.pendingAgentOpen = nil
	if s.router.Path() != routeWorkspace {
		s.router.Push(routeWorkspace)
	}
	s.rememberPaneSurface(p.PaneID, WorkspaceSurfaceChat)
	s.showSurface(WorkspaceSurfaceChat)
	s.rebindWorkspaceChat()
}
