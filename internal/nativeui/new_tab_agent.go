package nativeui

/**
 * [INPUT]: app.SessionStarter（桌面与手机共用的「启动新会话」服务层：Kinds / StartSession）、Shell.recentLaunchKinds（MRU，随 settings.Session 持久）、herdr.Manager.Projection
 * [OUTPUT]: 提供 Tab 条的「+」菜单按钮（newTabControl）：点 + 总是弹菜单（Terminal 在前，其后 MRU 排序的 Agent，上次用的标 last used）；⌘T/侧栏行 + 仍用 lastLaunchKind；Agent 且默认 Chat 时转 new_chat_draft；orderLaunchKinds / rememberLaunchKind / startNewTab
 * [POS]: 新建 Tab 选 Agent 的唯一入口（2026-11-01 用户拍板，design: designs/ux-audit-4 议题 7，v1 不含 split-with-Agent）；Agent 走既有 launch transaction（与 Continue、手机 launch 同一个 SessionStarter），不造第二条启动路径；种类清单在后台读取，渲染路径不做 IO
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/egoist/mygo/ui"
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

// newTabControl is the Tab bar's "+": one button that always opens the kind
// menu — Terminal first, then the Agents, the last-used kind marked and first
// among them (2026-11-02, design: designs/mobile-projection). ⌘T and the
// sidebar rows' + still start the last-used kind directly.
func (s *Shell) newTabControl(c *ui.Context, k *gorexColors, project herdr.Project) {
	s.ensureLaunchKinds()
	last := s.lastLaunchKind(s.launchKinds)
	add := gorexIconButton(c, k, iconPlus, "New Tab", "", false, gorexIconBtn, gorexIconGlyph)
	tipped(c, add, "New Tab in "+project.Label, tipKeys(acceleratorKeycaps(accelNewTab)))
	if len(s.launchKinds) < 2 {
		// Catalog still loading (or Terminal only): the + starts a Terminal.
		if add.Clicked() {
			s.startNewTab(project, last)
		}
		return
	}
	add.Menu(func(m *ui.Menu) {
		ordered := orderLaunchKinds(s.launchKinds, s.recentLaunchKinds)
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
		m.Separator()
		for _, kind := range ordered {
			if kind.ID != app.TerminalKind {
				pick(kind)
			}
		}
	})
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
	if s.newChatDraftAvailable() {
		s.beginNewChatDraft(project, kind)
		return
	}
	spec := app.SessionSpec{
		RequestID:   fmt.Sprintf("tab-%s-%d", kind.ID, time.Now().UnixNano()),
		WorkspaceID: project.ID,
		CWD:         project.CWD,
		Agent:       history.AgentID(kind.ID),
	}
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
			}
			if result.PaneID == "" || s.activeInstance != instance {
				return
			}
			if projection != nil {
				s.applyProjection(*projection, false)
			}
			s.showPane(result.PaneID)
		})
	})
}
