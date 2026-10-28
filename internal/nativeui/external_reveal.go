package nativeui

/**
 * [INPUT]: 依赖 Herdr 投影存在性判定（tabByID）、selection.go 的 selectTab/selectProject、runtime.Projection
 * [OUTPUT]: 对外提供 Shell.RouteExternalURL（main.go 的 OnOpenURL 接缝）、parseRevealURL、revealTarget
 * [POS]: 外部应用（如 My DevTools 的 prox）请求"切到某个 Herdr tab"的唯一入口：Solco 刻意不订阅 Herdr 全局 focus 事件，
 *        所以外部聚焦只能显式走 solco://reveal?session=&workspace=&tab=，由本窗口把本地选择切过去
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"log/slog"
	"net/url"
	"time"

	"github.com/thewei/solco/internal/herdr"
)

// revealPendingTTL bounds how long a reveal waits for its tab to appear in
// a projection (a tab created a moment ago, or an instance still opening).
const revealPendingTTL = 15 * time.Second

// revealTarget is what solco://reveal names: an optional Herdr session plus a
// tab and/or project (Herdr workspace) ID.
type revealTarget struct {
	Session   string
	ProjectID string
	TabID     string
}

type revealRequest struct {
	target revealTarget
	at     time.Time
}

// parseRevealURL accepts solco://reveal?session=…&workspace=…&tab=…; at least
// one of workspace/tab is required.
func parseRevealURL(raw string) (revealTarget, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "solco" || u.Host != "reveal" {
		return revealTarget{}, false
	}
	q := u.Query()
	target := revealTarget{Session: q.Get("session"), ProjectID: q.Get("workspace"), TabID: q.Get("tab")}
	if target.ProjectID == "" && target.TabID == "" {
		return revealTarget{}, false
	}
	return target, true
}

// RouteExternalURL is the main.go seam for MyGo's OnOpenURL: it marshals onto
// the window lane and navigates this window to the requested tab.
func (s *Shell) RouteExternalURL(raw string) {
	target, ok := parseRevealURL(raw)
	if !ok {
		slog.Warn("ignored unsupported deep link")
		return
	}
	s.applyOnWindow(func() { s.revealExternal(target) })
}

// revealExternal records the request and applies it as soon as its session is
// the active instance and its tab is in the projection; otherwise it opens the
// session / re-reads the projection and lets applyProjection finish the job.
func (s *Shell) revealExternal(target revealTarget) {
	s.pendingReveal = &revealRequest{target: target, at: time.Now()}
	slog.Info("external reveal requested", "session", target.Session, "workspace", target.ProjectID, "tab", target.TabID)
	if s.activeInstance == "" {
		return // applyProjection resumes it once the instance is ready.
	}
	if !s.revealTargetsActiveInstance(target) {
		s.openInstance(herdr.ParseRef(target.Session).Session)
		return
	}
	if s.tryPendingReveal() {
		return
	}
	s.refreshProjectionForReveal()
}

func (s *Shell) revealTargetsActiveInstance(target revealTarget) bool {
	if target.Session == "" {
		return true
	}
	active := herdr.ParseRef(s.activeInstance)
	return !active.IsRemote() && active.Session == herdr.ParseRef(target.Session).Session
}

// tryPendingReveal navigates to the pending target when it is resolvable now.
// An expired request is dropped; an unresolvable one stays for the next pass.
func (s *Shell) tryPendingReveal() bool {
	req := s.pendingReveal
	if req == nil {
		return false
	}
	if time.Since(req.at) > revealPendingTTL {
		s.pendingReveal = nil
		return false
	}
	if s.activeInstance == "" || !s.revealTargetsActiveInstance(req.target) {
		return false
	}
	switch {
	case req.target.TabID != "" && tabByID(s.projection, req.target.TabID) != nil:
		s.pendingReveal = nil
		s.hideQuickPanel()
		s.router.Push(routeWorkspace)
		s.selectTab(req.target.TabID)
		s.showViewTerminal()
		return true
	case req.target.TabID == "" && projectExists(s.projection, req.target.ProjectID):
		s.pendingReveal = nil
		s.hideQuickPanel()
		s.router.Push(routeWorkspace)
		s.selectProject(req.target.ProjectID)
		s.showViewTerminal()
		return true
	}
	return false
}

// refreshProjectionForReveal re-reads the active instance once: the watcher
// may not have delivered a tab that was created an instant before the link.
func (s *Shell) refreshProjectionForReveal() {
	if s.win == nil || s.runtime == nil {
		return
	}
	instance := s.activeInstance
	go func() {
		projection, err := s.runtime.Projection(instance)
		s.applyOnWindow(func() {
			if instance != s.activeInstance || s.pendingReveal == nil {
				return
			}
			if err != nil {
				slog.Warn("projection refresh for reveal failed", "error", err)
				return
			}
			s.applyProjection(projection, false)
		})
	}()
}
