package nativeui

import (
	"errors"
	"log/slog"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/herdr"
)

/**
 * [INPUT]: app.SessionKind（launch 菜单的种类）、Shell.surface（WorkspacePrimarySurface 状态机）、workspaceEmptyBanner/compactPrimaryButton（chat_ui.go 既有空态控件）、Shell.applyTabSelection（selection.go 唯一选择收口）
 * [OUTPUT]: Shell.pendingLaunch（启动持有的主表面状态）、Shell.beginLaunch/finishLaunch/failLaunch/expireLaunch、Shell.launchHold、Shell.adoptPendingLaunchTab、Shell.retireForeignLaunch、Shell.launchOpenTerminal、Shell.renderLaunchState、errTransactionNoPane
 * [POS]: 2026-11-03 用户拍板的启动落地面：New Pane 选 Agent 从点击瞬间以 Launching 态持有 Chat 主表面（agent pane 在后台启动），Loading 期间无 composer 即禁止输入；失败卡给出 Open Terminal 主动入口。hold 期间 syncWorkspaceForSelection/reconcilePaneSurface 一切记忆/可用性决策让位——Chat↔Terminal 不再由过渡投影的时序决定。无第二运行时，无新 Herdr 方法，无对话内容入日志。
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// launchPatience bounds how long one StartSession transaction may hold the
// launching Chat before the failure card offers the Terminal exit.
const launchPatience = 45 * time.Second

// errTransactionNoPane marks a transaction that answered without a pane:
// nothing verifiable was started.
var errTransactionNoPane = errors.New("the launch returned no pane")

// pendingLaunch is one in-flight New Pane → Agent launch. From the click the
// Chat surface is held in its launching state so pane-surface memory, chat
// availability and transitional projections never flip the primary surface
// while the pane boots in the background (2026-11-03, amends the 2026-11-02
// surface-flash loop: landing by intent was not enough — every intermediate
// projection still got a vote).
type pendingLaunch struct {
	RequestID string
	ProjectID string
	Kind      app.SessionKind
	At        time.Time
	Failed    bool
	ErrText   string
}

// launchHold reports whether an in-flight launch owns the primary surface.
// UI lane only.
func (s *Shell) launchHold() bool {
	return s.pendingLaunch != nil
}

// beginLaunch records the launch intent and lands the Chat surface in its
// launching state at click time — before any Herdr object exists. The render
// path shows the launching card (no composer, so nothing can be typed into
// a pane that is not there yet).
func (s *Shell) beginLaunch(projectID string, kind app.SessionKind, requestID string) {
	s.pendingLaunch = &pendingLaunch{RequestID: requestID, ProjectID: projectID, Kind: kind, At: time.Now()}
	if s.router.Path() != routeWorkspace {
		s.router.Push(routeWorkspace)
	}
	s.surface.openChat(s.workspaceContext())
	slog.Info("agent launch held chat", "kind", kind.ID)
	time.AfterFunc(launchPatience, func() { s.applyOnWindow(s.expireLaunch) })
}

// finishLaunch clears the hold once the pane landed; the normal surface
// rules take over from the landed Chat.
func (s *Shell) finishLaunch() {
	s.pendingLaunch = nil
}

// failLaunch marks the held Chat with the failure card. The hold stays so
// the card remains until the user acts (Open Terminal) or a late success
// lands normally.
func (s *Shell) failLaunch(label string, err error) {
	if s.pendingLaunch == nil {
		return
	}
	s.pendingLaunch.Failed = true
	s.pendingLaunch.ErrText = label + " did not start: " + err.Error()
}

// expireLaunch fails a launch whose transaction never answered. A launch
// that was superseded (a newer click) or already resolved is left alone.
func (s *Shell) expireLaunch() {
	l := s.pendingLaunch
	if l == nil || l.Failed {
		return
	}
	if time.Since(l.At) < launchPatience-2*time.Second {
		return
	}
	l.Failed = true
	l.ErrText = "The agent did not answer in time. You can open its Terminal to check on it."
}

// launchOpenTerminal is the failure card's action: the user explicitly
// leaves the held Chat for the Terminal to sort the launch out. From here
// the ordinary pane-surface memory rules apply again.
func (s *Shell) launchOpenTerminal() {
	s.pendingLaunch = nil
	s.showViewTerminal()
}

// adoptPendingLaunchTab selects the pending launch's new Tab the moment a
// watcher projection carries it — the proactive switch happens at the click
// and follows the Tab as soon as Herdr creates it, never only after the
// transaction returns (2026-11-03 user report). The first Tab of the
// pending workspace that is new since the previous projection wins; a
// concurrent launch elsewhere cannot be told apart and is not tried.
func (s *Shell) adoptPendingLaunchTab(p herdr.Projection) {
	l := s.pendingLaunch
	if l == nil || l.Failed {
		return
	}
	for i := range p.Tabs {
		tab := &p.Tabs[i]
		if tab.ProjectID != l.ProjectID || tabByID(s.projection, tab.ID) != nil {
			continue
		}
		// A runtime reconcile: the selection lands without a history push
		// (selection.go: adopt never rides the Router), and the hold keeps
		// the Chat surface through the following selection sync.
		s.selectedProjectID = tab.ProjectID
		s.selectedTabID = tab.ID
		s.selectedPaneID = ""
		s.setProjectExpanded(tab.ProjectID, true)
		s.setTabExpanded(tab.ID, true)
		s.syncSelectionPresentation()
		return
	}
}

// retireForeignLaunch drops the hold when the user navigates into another
// project while the launch is in flight: their navigation wins, the launch
// keeps running in the background.
func (s *Shell) retireForeignLaunch(projectID string) {
	if l := s.pendingLaunch; l != nil && projectID != "" && projectID != l.ProjectID {
		s.pendingLaunch = nil
	}
}

// renderLaunchState is the Chat surface body while a launch holds it:
// launching shows a spinner and the agent label (no composer — nothing may
// be typed into a pane that does not exist yet); failed shows the error and
// the explicit Open Terminal exit.
func (s *Shell) renderLaunchState(c *ui.Context) {
	l := s.pendingLaunch
	if l == nil {
		return
	}
	if l.Failed {
		workspaceEmptyBanner(c, iconChat, "Could not start "+l.Kind.Label, l.ErrText, func() {
			if compactPrimaryButton(c, "Open Terminal").Clicked() {
				s.launchOpenTerminal()
			}
		})
		return
	}
	sp := Spacing()
	t := c.Theme()
	ui.Column(c).Grow(1).MinWidth(0).Center().Gap(sp.M).Padding(sp.XL).Children(func() {
		ui.Spinner(c).Size(24, 24)
		ui.Text(c, "Starting "+l.Kind.Label+"…").FontSize(Typography().Body).FontWeight(WeightSemibold).TextColor(t.Text)
		ui.Text(c, "The agent pane starts in the background. Chat opens here as soon as it is ready.").
			FontSize(Typography().BodySmall).TextColor(t.TextMuted)
	})
}
