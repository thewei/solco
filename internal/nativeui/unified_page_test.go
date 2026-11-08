package nativeui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/gitworkbench"
)

/**
 * [INPUT]: MyGo Tester, Settings routes, Git workbench snapshot and Workspace Chat gate
 * [OUTPUT]: Regression coverage for uniform Settings structure, compact Git clean surface (readable when narrow) and Terminal return
 * [POS]: UI-007/UI-008 implementation acceptance without touching live Herdr sessions
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func TestSettingsSectionsShareHeaderAndBoundedScrollableBody(t *testing.T) {
	for _, tc := range []struct {
		route string
		title string
		body  string
	}{
		{"/settings/general", "General", "Appearance"},
		{"/settings/terminal", "Terminal", "Font"},
		{"/settings/diagnostics", "Diagnostics", "About"},
	} {
		t.Run(tc.route, func(t *testing.T) {
			for _, size := range [][2]int{{960, 640}, {1200, 800}, {1512, 982}} {
				s := NewShell()
				s.loading = false
				s.router.Replace(tc.route)
				tester := ui.NewTester(s.View, size[0], size[1])
				if !tester.HasText(tc.title) || !tester.HasText(tc.body) {
					t.Fatalf("%dx%d lacks unified Settings header/body: %q", size[0], size[1], tester.Texts())
				}
			}
		})
	}
}

func TestTerminalSettingsTransparentBackgroundIsSingleControl(t *testing.T) {
	s := NewShell()
	s.loading = false
	s.router.Replace("/settings/terminal")
	tester := ui.NewTester(s.View, 1200, 800)
	count := 0
	for _, label := range tester.Texts() {
		if strings.TrimSpace(label) == "Transparent background" {
			count++
		}
	}
	// MyGo exposes each Field label once as AX label and once as visible
	// text. One control produces exactly two records, not one.
	if count != 2 {
		t.Fatalf("transparent background field labels = %d (want one control): %q", count, tester.Texts())
	}
}

func TestGitFilterEmptyStateCanRecover(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	s.git.rootOf[s.git.root] = s.git.root
	s.changes.filter = "not-a-matching-file"
	showChangesPanel(s)
	s.rebuildDiffRows()
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	if !tester.HasText("No matching files") || !tester.HasText("Clear File Filter") {
		t.Fatalf("file filter has no recovery: %q", tester.Texts())
	}
	if err := tester.Click("Clear File Filter"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if s.changes.filter != "" || !tester.HasText("src/main.go") {
		t.Fatalf("clearing file filter did not restore diff: %q", tester.Texts())
	}
}

// In the panel's narrowest card the clean state's column is ~300 px: the
// message must keep reading as words and the actions drop below it, instead of
// the text being squeezed into a column of single letters beside the buttons.
func TestCleanGitSurfaceKeepsItsMessageReadableWhenNarrow(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	s.solcoGit = &fakeSolcoGit{available: true}
	s.git.rootOf[s.git.root] = s.git.root
	s.git.snapshot = &gitworkbench.ChangesSnapshot{
		Root: s.git.root, Branch: "main", Signature: "clean", Files: nil,
	}
	showChangesPanel(s)
	s.sidePanel.width = sidePanelMinWidth
	s.rebuildDiffRows()

	narrow := ui.NewTester(s.View, 1280, 800)
	narrow.Frame()
	title, ok := narrow.Find("Working tree clean")
	if !ok {
		t.Fatalf("narrow clean surface lost its message: %q", narrow.Texts())
	}
	if title.W < 90 || title.H > 30 {
		t.Fatalf("message squeezed to %.0f x %.0f px in a %v px card, want one readable line", title.W, title.H, sidePanelMinWidth)
	}
	if !narrow.HasText("Open in Solco Git") {
		t.Fatalf("narrow clean surface lost %q: %q", "Open in Solco Git", narrow.Texts())
	}
	// The Terminal is the center beside the panel: a button that opens it
	// would do nothing (2026-11-08), so it is not drawn.
	if narrow.HasText("Open Terminal") {
		t.Fatalf("the clean panel offers Open Terminal beside a visible Terminal: %q", narrow.Texts())
	}

	// Room to spare keeps the page as it is: the message on one line, the
	// actions below the commits.
	s.sidePanel.width = 700
	wide := ui.NewTester(s.View, 1700, 800)
	wide.Frame()
	title, _ = wide.Find("Working tree clean")
	if title.H > 30 {
		t.Errorf("a wide clean surface wraps its message: title=%v", title)
	}
	if _, ok := wide.Find("Open Terminal"); ok {
		t.Error("a wide clean surface offers Open Terminal beside a visible Terminal")
	}
}

func TestCleanGitSurfaceOffersSolcoGitAndTerminalWithoutCenteredModal(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	link := &fakeSolcoGit{available: true}
	s.solcoGit = link
	s.git.rootOf[s.git.root] = s.git.root
	s.git.snapshot = &gitworkbench.ChangesSnapshot{
		Root: s.git.root, Branch: "main", Signature: "clean", Files: nil,
	}
	showChangesPanel(s)
	s.rebuildDiffRows()
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	for _, want := range []string{"Working tree clean", "Open in Solco Git"} {
		if !tester.HasText(want) {
			t.Fatalf("clean Git surface missing %q: %q", want, tester.Texts())
		}
	}
	if tester.HasText("Open Terminal") {
		t.Fatalf("the Terminal is already the center; Open Terminal would do nothing: %q", tester.Texts())
	}
	if tester.HasText("View All Commits") {
		t.Fatalf("the history page is gone; the clean state must not offer it: %q", tester.Texts())
	}
	if err := tester.Click("Open in Solco Git"); err != nil {
		t.Fatal(err)
	}
	if len(link.calls) != 1 {
		t.Fatalf("Open in Solco Git made %d link calls, want 1", len(link.calls))
	}
}

// Beside a Chat the clean panel's Open Terminal is a real way out: it lands
// the Terminal; beside the Terminal it is not drawn at all.
func TestCleanPanelOffersTheTerminalOnlyBesideAnotherSurface(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	s.solcoGit = &fakeSolcoGit{available: true}
	s.git.rootOf[s.git.root] = s.git.root
	s.git.snapshot = &gitworkbench.ChangesSnapshot{
		Root: s.git.root, Branch: "main", Signature: "clean", Files: nil,
	}
	showChangesList(s)
	s.rebuildDiffRows()
	s.surface.openChat(s.workspaceContext())
	tester := ui.NewTester(s.View, 1400, 800)
	tester.Frame()
	if !tester.HasText("Open Terminal") {
		t.Fatalf("beside the Chat the clean panel lost Open Terminal: %q", tester.Texts())
	}
	if err := tester.Click("Open Terminal"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if got := s.surface.current(); got != WorkspaceSurfaceTerminal {
		t.Fatalf("clean state return = %q, want Terminal", got)
	}
	if tester.HasText("Open Terminal") {
		t.Fatalf("Open Terminal stays drawn beside the Terminal it just opened: %q", tester.Texts())
	}
}
