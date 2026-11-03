package nativeui

import (
	"errors"
	"testing"
	"time"

	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/herdr"
)

/**
 * [INPUT]: chat_launch.go 的 pendingLaunch 状态机、tabBarShell fixture、fakeStarter.err
 * [OUTPUT]: 启动持有 Chat 的回归——点击即 Chat（无 pane 也无 composer）、无卡过渡投影不翻转表面、新 Tab 先于事务返回被选中、失败卡经 Open Terminal 主动退出、外项目导航弃 hold、看门狗只超时自己的 launch
 * [POS]: 2026-11-03 New Pane 选 Agent 的启动落地面回归（surface 全程 Chat；投诉 A 的主动 Tab 切换）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// chatFirstShell is a tabBarShell with Agent view = chat and the surface
// parked on Terminal, so only the launch itself decides the surface.
func chatFirstShell() *Shell {
	s := tabBarShell(1, "t1")
	s.settings.General.AgentView = "chat"
	s.paneSurfaceMemo = nil
	s.surface.resetToTerminal(s.workspaceContext())
	return s
}

func TestBeginLaunchHoldsChatBeforeAnythingExists(t *testing.T) {
	s := chatFirstShell()
	s.beginLaunch("w1", app.SessionKind{ID: "pi", Label: "Pi"}, "req-1")
	if s.pendingLaunch == nil || s.pendingLaunch.Failed {
		t.Fatalf("pendingLaunch = %+v, want an in-flight hold", s.pendingLaunch)
	}
	if s.surface.current() != WorkspaceSurfaceChat {
		t.Fatalf("surface = %v, want Chat from the click", s.surface.current())
	}
	if s.router.Path() != routeWorkspace {
		t.Fatalf("route = %q, want the workspace page", s.router.Path())
	}
}

func TestLaunchHoldKeepsChatThroughACardlessProjection(t *testing.T) {
	s := chatFirstShell()
	s.beginLaunch("w1", app.SessionKind{ID: "pi", Label: "Pi"}, "req-1")
	// The transitional projection Herdr delivers while the transaction runs:
	// the new tab exists, its pane carries no agent card yet.
	next := s.projection
	next.Tabs = append(next.Tabs, herdr.Tab{ID: "t9", ProjectID: "w1", Label: "Pi"})
	next.Panes = append(next.Panes, herdr.Pane{ID: "p9", ProjectID: "w1", TabID: "t9"})
	s.adoptPendingLaunchTab(next)
	if s.selectedTabID != "t9" {
		t.Fatalf("selection = %q, want the new tab adopted at once", s.selectedTabID)
	}
	// The context changed with the adopted tab: the hold must keep Chat even
	// though the pane has no card yet — exactly the flip the user reported.
	s.syncWorkspaceForSelection()
	if s.surface.current() != WorkspaceSurfaceChat {
		t.Fatalf("surface = %v, want the launch to hold Chat through the card-less projection", s.surface.current())
	}
	s.reconcilePaneSurface()
	if s.surface.current() != WorkspaceSurfaceChat {
		t.Fatalf("surface = %v, want Chat after reconcile", s.surface.current())
	}
}

func TestStartNewTabHoldsAndClearsTheLaunch(t *testing.T) {
	fake := &fakeStarter{kinds: testKinds, pane: "p-new"}
	s := chatFirstShell()
	s.newTabStarter = fake
	s.runtime = nil
	s.projection.Panes = append(s.projection.Panes, herdr.Pane{ID: "p-new", ProjectID: "w1", TabID: "t1"})
	s.projection.Agents = append(s.projection.Agents, herdr.Agent{PaneID: "p-new", Kind: "pi", TerminalID: "term-new", Status: "working"})
	s.startNewTab(s.projection.Projects[0], app.SessionKind{ID: "pi", Label: "Pi"})
	if s.pendingLaunch != nil {
		t.Fatalf("the hold must clear once the pane landed: %+v", s.pendingLaunch)
	}
	if s.selectedPaneID != "p-new" || s.surface.current() != WorkspaceSurfaceChat {
		t.Fatalf("sel=%q surface=%v, want the pane landed on Chat", s.selectedPaneID, s.surface.current())
	}
}

func TestLaunchFailureHoldsChatWithAnOpenTerminalExit(t *testing.T) {
	fake := &fakeStarter{kinds: testKinds, err: errors.New("boom")}
	s := chatFirstShell()
	s.newTabStarter = fake
	s.startNewTab(s.projection.Projects[0], app.SessionKind{ID: "pi", Label: "Pi"})
	if s.pendingLaunch == nil || !s.pendingLaunch.Failed {
		t.Fatalf("pendingLaunch = %+v, want a failed hold", s.pendingLaunch)
	}
	if s.surface.current() != WorkspaceSurfaceChat {
		t.Fatalf("surface = %v, want the failure card to keep Chat", s.surface.current())
	}
	s.launchOpenTerminal()
	if s.pendingLaunch != nil || s.surface.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("hold=%v surface=%v, want the explicit Terminal exit to clear the hold", s.pendingLaunch, s.surface.current())
	}
}

func TestForeignNavigationRetiresTheLaunchHold(t *testing.T) {
	s := chatFirstShell()
	s.projection.Projects = append(s.projection.Projects, herdr.Project{ID: "w2", Label: "Other"})
	s.projection.Tabs = append(s.projection.Tabs, herdr.Tab{ID: "w2t", ProjectID: "w2", Label: "Other tab"})
	s.projection.Panes = append(s.projection.Panes, herdr.Pane{ID: "w2p", ProjectID: "w2", TabID: "w2t"})
	s.beginLaunch("w1", app.SessionKind{ID: "pi", Label: "Pi"}, "req-1")
	s.applyTabSelection("t1")
	if s.pendingLaunch == nil {
		t.Fatal("a same-project selection must not retire the hold")
	}
	s.applyTabSelection("w2t")
	if s.pendingLaunch != nil {
		t.Fatal("navigating into another project must retire the hold")
	}
}

func TestExpireLaunchTimesOutOnlyItsOwnLaunch(t *testing.T) {
	s := chatFirstShell()
	s.beginLaunch("w1", app.SessionKind{ID: "pi", Label: "Pi"}, "req-1")
	s.expireLaunch()
	if s.pendingLaunch.Failed {
		t.Fatal("a fresh launch must not expire")
	}
	s.pendingLaunch.At = time.Now().Add(-launchPatience)
	s.expireLaunch()
	if !s.pendingLaunch.Failed {
		t.Fatal("a launch past its patience must expire into the failure card")
	}
}
