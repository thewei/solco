package nativeui

// [INPUT]: 依赖 git_clean_state.go 的 headCommits、page_history.go 的 sessionDayLabel/sessionsAreOneProject
// [OUTPUT]: 2026-10-30 UX 审计第二轮的纯数据守卫：干净态最近提交沿 HEAD 第一父链、Sessions 按天分组标签与单项目判定
// [POS]: designs/ux-audit-2 议题 2/3 的回归；渲染本身由真机对照原型
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
)

// TestHeadCommitsFollowTheFirstParentLine pins the clean page's "Recent
// Commits": the graph spans every branch, but the list is HEAD's own line —
// a newer commit on another branch must not appear, and the walk stops where
// the loaded window ends.
func TestHeadCommitsFollowTheFirstParentLine(t *testing.T) {
	commits := []gitworkbench.CommitInfo{
		{Hash: "other", Short: "oth", Parents: []string{"c"}},
		{Hash: "a", Short: "a", Parents: []string{"b"}},
		{Hash: "b", Short: "b", Parents: []string{"c", "side"}},
		{Hash: "c", Short: "c", Parents: []string{"gone"}},
	}
	got := headCommits(commits, "a", 5)
	if len(got) != 3 || got[0].Hash != "a" || got[1].Hash != "b" || got[2].Hash != "c" {
		t.Fatalf("headCommits = %+v, want a, b, c", got)
	}
	if got := headCommits(commits, "a", 2); len(got) != 2 {
		t.Fatalf("headCommits(n=2) = %d commits, want 2", len(got))
	}
	if got := headCommits(commits, "missing", 3); len(got) != 0 {
		t.Fatalf("an unknown HEAD listed %d commits, want none", len(got))
	}
}

// TestSessionDayLabels pins the day sections of the one-project Sessions
// list.
func TestSessionDayLabels(t *testing.T) {
	now := time.Date(2026, time.October, 30, 15, 0, 0, 0, time.Local)
	at := func(days int, hour int) int64 {
		d := now.AddDate(0, 0, -days)
		return time.Date(d.Year(), d.Month(), d.Day(), hour, 0, 0, 0, time.Local).UnixMilli()
	}
	for _, c := range []struct {
		ms   int64
		want string
	}{
		{at(0, 1), "Today"}, {at(1, 23), "Yesterday"}, {at(3, 9), "Last 7 days"},
		{at(12, 9), "Last 30 days"}, {at(45, 9), "September"}, {at(400, 9), "September 2025"}, {0, "Earlier"},
	} {
		if got := sessionDayLabel(c.ms, now); got != c.want {
			t.Errorf("sessionDayLabel(%s) = %q, want %q", time.UnixMilli(c.ms).Format(time.DateOnly), got, c.want)
		}
	}
}

// TestOneProjectSessionsDropTheCardHeader pins when the project card header
// goes: only a project-scoped list that holds one project (never the Trash,
// never All sessions, never several projects).
func TestOneProjectSessionsDropTheCardHeader(t *testing.T) {
	s := NewShell()
	s.git = &gitService{root: "/p"}
	one := []sessionGroupView{{path: "/p"}}
	two := []sessionGroupView{{path: "/p"}, {path: "/q"}}
	if !s.sessionsAreOneProject(one) {
		t.Fatal("a project-scoped list of one project kept its card header")
	}
	if s.sessionsAreOneProject(two) {
		t.Fatal("two projects lost their card headers")
	}
	s.hist.scopeAll = true
	if s.sessionsAreOneProject(one) {
		t.Fatal("All sessions lost its card header")
	}
	s.hist.scopeAll = false
	s.hist.showTrash = true
	if s.sessionsAreOneProject(one) {
		t.Fatal("the Trash lost its card header")
	}
}

// TestWorkspaceRowIsSelectedInTabBarMode pins the 2026-10-30 UX audit bug: with
// the Tabs in the title bar the tree has no Tab rows, so a selected Tab left
// the Workspace row unlit.
func TestWorkspaceRowIsSelectedInTabBarMode(t *testing.T) {
	s := NewShell()
	s.selectedTabID = "t1"
	s.settings.General.TabsPlacement = "sidebar"
	if s.workspaceRowSelected(true) {
		t.Fatal("with Tabs in the sidebar the Tab row carries the selection, not the Workspace row")
	}
	s.settings.General.TabsPlacement = "titlebar"
	if !s.workspaceRowSelected(true) {
		t.Fatal("with Tabs in the title bar the selected Workspace row must be lit")
	}
	if s.workspaceRowSelected(false) {
		t.Fatal("another Workspace's row must stay unlit")
	}
}

// openRowInfo hovers a session row and folds its info card with the ⓘ button:
// a click on the row itself now opens the session.
func openRowInfo(t *testing.T, tester *ui.Tester, title string) {
	t.Helper()
	row, ok := tester.Find(title)
	if !ok {
		t.Fatalf("session row %q missing; texts=%q", title, tester.Texts())
	}
	tester.Move(row.X+row.W/2, row.Y+row.H/2)
	tester.Frame()
	if err := tester.Click("Session info"); err != nil {
		t.Fatalf("hovering %q shows no Session info button: %v; texts=%q", title, err, tester.Texts())
	}
	tester.Frame()
}
