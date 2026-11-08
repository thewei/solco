package nativeui

/**
 * [INPUT]: side_panel.go / side_panel_pages.go, a real temp Git repository with changes (tempGitRepo), the headless ui.Tester
 * [OUTPUT]: the right panel's contract — geometry, never opening by itself, ⌥⌘B / title-bar button / ✕, overlay Esc and outside click, stepping aside on Commit, page choice, a list click opening the center's Detail card (the panel holds only the list), commit return, retired-session inertness
 * [POS]: 2026-11-07 决定「Solco Git and the right panel」第 2 条与 2026-11-08 决定「The panel is a list」的回归防线
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/gitworkbench"
	"github.com/thewei/solco/internal/herdr"
)

// sidePanelRepoShell is a shell bound to a real Git repository with two
// changes (a modified README.md and an untracked notes.txt), resolved the
// production way: the repo root and the snapshot arrive through the apply
// lane, which the override runs under a lock so the test can wait for it.
func sidePanelRepoShell(t *testing.T) *Shell {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := tempGitRepo(t)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("demo\nchanged line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	shell := NewShell()
	shell.activeInstance = "inst-1"
	shell.projection = herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: "demo", CWD: root}},
		Tabs:     []herdr.Tab{{ID: "t1", Label: "main", ProjectID: "w1"}},
		Panes:    []herdr.Pane{{ID: "p1", TerminalID: "term-1", Label: "editor", TabID: "t1", CWD: root}},
	}
	var mu sync.Mutex
	shell.uiApplyOverride = func(fn func()) {
		mu.Lock()
		fn()
		mu.Unlock()
	}
	shell.reconcileLocalSelection(shell.projection)
	shell.syncWorkspaceForSelection()
	deadline := time.Now().Add(15 * time.Second)
	for {
		mu.Lock()
		ready := shell.git.root == root && shell.git.snapshot != nil && len(shell.git.snapshot.Files) == 2
		mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("git context did not resolve: root=%q snapshot=%v", shell.git.root, shell.git.snapshot)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Frames run on the test goroutine from here; nothing else applies.
	shell.uiApplyOverride = nil
	return shell
}

// showChangesPanel stands in for the retired Diff surface in the fixtures that
// exercise the Changes list and the diff (2026-11-08): the right panel opens
// on Changes and the file is chosen as a click on its row would, which puts
// the diff cards in the center's Detail card. A fixture with a seeded
// snapshot and no live repository keeps it: the root resolves to itself and
// no refresh replaces the seed.
func showChangesPanel(s *Shell) {
	seedChangesFixture(s)
	s.openSidePanelChanges("")
}

// seedChangesFixture keeps a seeded snapshot: the repository root resolves to
// itself and no refresh replaces the seed.
func seedChangesFixture(s *Shell) {
	if s.git != nil && s.git.root != "" {
		if _, known := s.git.rootOf[s.git.root]; !known {
			s.git.rootOf[s.git.root] = s.git.root
			s.git.refreshing = true
		}
	}
}

// showChangesList is showChangesPanel without the center's Detail card: the
// panel open on its Changes list and nothing picked, the Terminal (or the
// Chat) left exactly where it was. The fixtures that exercise the list, or a
// surface that must stay as it was, use it; opening a file is a click on a
// row, and that one opens the card.
func showChangesList(s *Shell) {
	seedChangesFixture(s)
	s.sidePanel.zoomed = false
	s.sidePanel.open = true
	s.sidePanel.page = sidePanelChanges
	if !s.sidePanelAvailable() {
		s.landSelectedPane()
	}
}

func sidePanelTester(s *Shell, w, h int) *ui.Tester {
	tt := ui.NewTester(s.View, w, h)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	return tt
}

// sidePanelCardBounds is the card's box, and whether the panel is on screen.
func sidePanelCardBounds(tt *ui.Tester) (ui.Rect, bool) {
	info, ok := tt.Access("Right panel")
	return info.Bounds, ok
}

// The geometry is one pure function: the card docks at its remembered width
// as long as the center keeps its floor, narrows toward the minimum before
// it takes more of the center, and floats below that. The numbers are the
// 2026-11-07 design's: 1156 with the 252 sidebar and 904 without are where
// the last docked pixel is.
func TestSidePanelLayout(t *testing.T) {
	cases := []struct {
		name                    string
		windowW, sidebarW, want float32
		wantWidth               float32
		wantOverlay             bool
	}{
		{"wide window docks at the default", 1512, 252, 0, 400, false},
		{"remembered width is kept", 1800, 252, 520, 520, false},
		{"a wider-than-room wish narrows toward the floor", 1300, 252, 700, 464, false},
		{"a too-small wish rises to the minimum", 1512, 252, 100, 320, false},
		{"1156 is the last docked width (sidebar open)", 1156, 252, 0, 320, false},
		{"1155 floats (sidebar open)", 1155, 252, 0, 400, true},
		{"904 is the last docked width (sidebar shut)", 904, 0, 0, 320, false},
		{"903 floats (sidebar shut)", 903, 0, 0, 400, true},
		{"a narrow window floats at its default", 700, 252, 0, 400, true},
		{"a very narrow window floats inside the content", 600, 252, 0, 332, true},
		{"nothing to show in a collapsed window", 200, 252, 0, 0, true},
	}
	for _, tc := range cases {
		width, overlay := sidePanelLayout(tc.windowW, tc.sidebarW, tc.want)
		if width != tc.wantWidth || overlay != tc.wantOverlay {
			t.Errorf("%s: sidePanelLayout(%v, %v, %v) = (%v, %v), want (%v, %v)",
				tc.name, tc.windowW, tc.sidebarW, tc.want, width, overlay, tc.wantWidth, tc.wantOverlay)
		}
	}
	if got := sidePanelMaxWidth(1512, 252); got != 676 {
		t.Errorf("max drag width at 1512/252 = %v, want 676", got)
	}
	if got := sidePanelMaxWidth(900, 252); got != sidePanelMinWidth {
		t.Errorf("max drag width in a window that cannot dock = %v, want the minimum %v", got, sidePanelMinWidth)
	}
	for in, want := range map[int]int{10: 320, 460: 460, 9999: sidePanelStoredMax} {
		if got := clampSessionSidePanelWidth(in); got != want {
			t.Errorf("clampSessionSidePanelWidth(%d) = %d, want %d", in, got, want)
		}
	}
}

// It never opens by itself: no frame, selection, surface change or page
// visit opens it; only the user's toggle does (2026-11-07 decision).
func TestSidePanelNeverOpensByItself(t *testing.T) {
	s := sidePanelRepoShell(t)
	tt := sidePanelTester(s, 1280, 800)
	check := func(step string) {
		t.Helper()
		tt.Frame()
		if s.sidePanel.open {
			t.Fatalf("%s opened the right panel", step)
		}
		if _, ok := sidePanelCardBounds(tt); ok {
			t.Fatalf("%s drew the right panel", step)
		}
	}
	check("the first frame")
	s.selectPane("p1")
	check("selecting a pane")
	s.showSurface(WorkspaceSurfaceChat)
	check("opening Chat")
	s.showSurface(WorkspaceSurfaceTerminal)
	check("returning to the Terminal")
	s.router.Replace(routeHistory)
	check("visiting Sessions")
	s.router.Replace(routeWorkspace)
	check("coming back to the workspace")
	s.git.snapshot.Files = append(s.git.snapshot.Files, s.git.snapshot.Files[0])
	check("new changes appearing")
}

// ⌥⌘B, the title bar's right-most button and the panel's ✕ all work; ⌘B
// alone stays the sidebar's.
func TestSidePanelOpensAndClosesFromEveryEntry(t *testing.T) {
	s := sidePanelRepoShell(t)
	tt := sidePanelTester(s, 1280, 800)

	tt.Key(ui.Alt|ui.Super, ui.KeyB)
	tt.Frame()
	card, ok := sidePanelCardBounds(tt)
	if !ok || !s.sidePanel.open {
		t.Fatalf("⌥⌘B did not open the panel (open=%v shown=%v)", s.sidePanel.open, ok)
	}
	if !near(card.W, 400) || !near(card.X+card.W, 1280-gorexGap) {
		t.Fatalf("docked card = %+v, want 400 wide ending %d from the window's right edge", card, gorexGap)
	}
	tt.Key(ui.Alt|ui.Super, ui.KeyB)
	tt.Frame()
	if _, ok := sidePanelCardBounds(tt); ok || s.sidePanel.open {
		t.Fatal("⌥⌘B did not close the panel")
	}

	if err := tt.Click("Show Right Panel"); err != nil {
		t.Fatalf("the title bar button is missing: %v", err)
	}
	tt.Frame()
	if _, ok := sidePanelCardBounds(tt); !ok {
		t.Fatal("the title bar button did not open the panel")
	}
	if err := tt.Click("Close Right Panel"); err != nil {
		t.Fatalf("the panel's ✕ is missing: %v", err)
	}
	tt.Frame()
	if _, ok := sidePanelCardBounds(tt); ok || s.sidePanel.open {
		t.Fatal("✕ did not close the panel")
	}

	sidebar := s.sidebarCollapsed
	tt.Key(ui.Super, ui.KeyB)
	tt.Frame()
	if s.sidebarCollapsed == sidebar || s.sidePanel.open {
		t.Fatalf("⌘B must toggle the sidebar only: sidebar was %v now %v, panel open=%v", sidebar, s.sidebarCollapsed, s.sidePanel.open)
	}
}

// The title bar button is a view of the panel's state: it names the action,
// and exists wherever the page can carry the panel.
func TestSidePanelToggleButtonFollowsAvailability(t *testing.T) {
	s := sidePanelRepoShell(t)
	tt := sidePanelTester(s, 1280, 800)
	if _, ok := tt.Access("Show Right Panel"); !ok {
		t.Fatal("no Show Right Panel button on the workspace")
	}
	s.toggleSidePanel()
	tt.Frame()
	if _, ok := tt.Access("Hide Right Panel"); !ok {
		t.Fatal("an open panel's button must say Hide")
	}
	s.router.Replace(routeSettings)
	tt.Frame()
	for _, name := range []string{"Show Right Panel", "Hide Right Panel"} {
		if _, ok := tt.Access(name); ok {
			t.Fatalf("%q must not appear outside the workspace", name)
		}
	}
}

// Wherever the panel docks the card has the right width; where it cannot,
// it floats at the same width inside the content area.
func TestSidePanelDocksOrFloatsByWindowWidth(t *testing.T) {
	for _, tc := range []struct {
		w       int
		overlay bool
		width   float32
	}{
		{1512, false, 400},
		{1156, false, 320},
		{1155, true, 400},
		{900, true, 400},
	} {
		s := sidePanelRepoShell(t)
		s.sidePanel.open = true
		tt := sidePanelTester(s, tc.w, 800)
		tt.Frame()
		card, ok := sidePanelCardBounds(tt)
		if !ok {
			t.Fatalf("%dpx: no panel card", tc.w)
		}
		if s.sidePanel.overlay != tc.overlay {
			t.Fatalf("%dpx: overlay = %v, want %v", tc.w, s.sidePanel.overlay, tc.overlay)
		}
		if !near(card.W, tc.width) || !near(card.X+card.W, float32(tc.w)-gorexGap) {
			t.Fatalf("%dpx: card = %+v, want %v wide ending %d inside the right edge", tc.w, card, tc.width, gorexGap)
		}
	}
}

// A dragged width is remembered and used; a double-click on the seam
// restores the default. (The seam's drag itself commits on release; the
// state it commits is what the layout reads.)
func TestSidePanelRemembersDraggedWidth(t *testing.T) {
	s := sidePanelRepoShell(t)
	s.sidePanel.open = true
	s.sidePanel.width = 520
	tt := sidePanelTester(s, 1512, 800)
	card, ok := sidePanelCardBounds(tt)
	if !ok || !near(card.W, 520) {
		t.Fatalf("a remembered 520 drew %+v (shown=%v)", card, ok)
	}
	s.sidePanel.width = 0
	tt.Frame()
	if card, _ = sidePanelCardBounds(tt); !near(card.W, 400) {
		t.Fatalf("width 0 should be the default, drew %+v", card)
	}
}

// Floating, Esc closes the panel and so does a click on the center outside
// it; docked, Esc is left alone.
func TestSidePanelOverlayClosesWithEscAndOutsideClick(t *testing.T) {
	s := sidePanelRepoShell(t)
	s.sidePanel.open = true
	tt := sidePanelTester(s, 1000, 700)
	tt.Frame()
	if !s.sidePanelOverlayUp() {
		t.Fatalf("a 1000px window must float the panel (open=%v overlay=%v)", s.sidePanel.open, s.sidePanel.overlay)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if s.sidePanel.open {
		t.Fatal("Esc did not close the floating panel")
	}

	s.sidePanel.open = true
	tt.Frame()
	card, ok := sidePanelCardBounds(tt)
	if !ok {
		t.Fatal("reopened panel is missing")
	}
	tt.ClickAt(card.X-100, card.Y+card.H/2)
	tt.Frame()
	if s.sidePanel.open {
		t.Fatal("a click outside did not close the floating panel")
	}

	wide := sidePanelRepoShell(t)
	wide.sidePanel.open = true
	ttw := sidePanelTester(wide, 1512, 800)
	ttw.Key(0, ui.KeyEscape)
	ttw.Frame()
	if !wide.sidePanel.open {
		t.Fatal("Esc must not close a docked panel")
	}
}

// The Commit surface owns the whole center: the panel steps aside (no card, no
// button) while it is open, and returns with the surface it left, still
// wanted. (The Diff surface, the other one it used to step aside for, retired
// with the Git page, 2026-11-08.)
func TestSidePanelStepsAsideOnCommit(t *testing.T) {
	s := sidePanelRepoShell(t)
	s.sidePanel.open = true
	tt := sidePanelTester(s, 1280, 800)
	if _, ok := sidePanelCardBounds(tt); !ok {
		t.Fatal("panel missing on the Terminal")
	}
	s.openCommitSurface()
	tt.Frame()
	if _, ok := sidePanelCardBounds(tt); ok {
		t.Fatal("the panel stayed beside the Commit surface")
	}
	if _, ok := tt.Access("Hide Right Panel"); ok {
		t.Fatal("the toggle button stayed on the Commit surface")
	}
	if !s.sidePanel.open {
		t.Fatal("stepping aside must not forget the user's wish")
	}
	s.cancelCommitSurface()
	tt.Frame()
	if _, ok := sidePanelCardBounds(tt); !ok {
		t.Fatal("the panel did not come back with the Terminal")
	}
}

// Asking for the panel from another page lands the workspace first.
func TestSidePanelToggleFromAnotherPageLandsTheWorkspace(t *testing.T) {
	s := sidePanelRepoShell(t)
	tt := sidePanelTester(s, 1280, 800)
	s.router.Replace(routeSettings)
	tt.Frame()
	s.toggleSidePanel()
	tt.Frame()
	if s.router.Path() != routeWorkspace || !s.sidePanel.open {
		t.Fatalf("toggle from Settings: route=%q open=%v, want the workspace with the panel open", s.router.Path(), s.sidePanel.open)
	}
	if _, ok := sidePanelCardBounds(tt); !ok {
		t.Fatal("the panel is not on screen after landing")
	}
}

// Chat's "Review changes" has no Diff page to land on any more (2026-11-08): it
// opens the right panel on Changes with the card's file picked and the diff in
// the center's Detail card, which comes back to the Chat on "Back to Chat".
func TestChatReviewChangesOpensThePanelOnChanges(t *testing.T) {
	s := sidePanelRepoShell(t)
	s.projection.Agents = []herdr.Agent{{PaneID: "p1", Kind: "omp", Status: "idle"}} // the pane is an Agent: Chat is a place to go back to
	s.surface.openChat(s.workspaceContext())
	tt := sidePanelTester(s, 1512, 900)
	tt.Frame()
	if _, ok := sidePanelCardBounds(tt); ok {
		t.Fatal("the panel must stay shut until the user asks")
	}
	if err := NewChatBridge(s).ReviewChanges(t.Context(), "notes.txt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !s.sidePanel.open || s.sidePanelPageNow() != sidePanelChanges {
		t.Fatalf("Review changes: open=%v page=%q, want the panel on Changes", s.sidePanel.open, s.sidePanelPageNow())
	}
	if got := s.surface.diff.SelectedPath; got != "notes.txt" {
		t.Fatalf("the card's file was not picked: selected %q", got)
	}
	if s.surface.current() != WorkspaceSurfaceDetail || s.surface.detailKind() != DetailChanges ||
		s.surface.detailOrigin() != WorkspaceSurfaceChat {
		t.Fatalf("the diff belongs in the center's Detail card over the Chat: surface=%v kind=%v origin=%v",
			s.surface.current(), s.surface.detailKind(), s.surface.detailOrigin())
	}
	if _, ok := sidePanelCardBounds(tt); !ok {
		t.Fatal("the panel is not on screen after Review changes")
	}
	if err := tt.Click("Back to Chat"); err != nil {
		t.Fatalf("the Detail card has no way back to the Chat: %v", err)
	}
	tt.Frame()
	if got := s.surface.current(); got != WorkspaceSurfaceChat {
		t.Fatalf("Back to Chat landed on %v", got)
	}
}

// Changes shows the worktree's files, and the first open picks Changes when
// there are changes and Files when there are none; the choice then sticks.
func TestSidePanelPageChoice(t *testing.T) {
	s := sidePanelRepoShell(t)
	if got := s.sidePanelPageNow(); got != sidePanelChanges {
		t.Fatalf("first open with changes = %q, want changes", got)
	}
	files := s.git.snapshot.Files
	s.git.snapshot.Files = nil
	if got := s.sidePanelPageNow(); got != sidePanelFiles {
		t.Fatalf("first open on a clean tree = %q, want files", got)
	}
	s.git.snapshot.Files = files

	s.sidePanel.open = true
	tt := sidePanelTester(s, 1280, 800)
	tt.Frame()
	card, ok := sidePanelCardBounds(tt)
	if !ok {
		t.Fatal("no card")
	}
	for _, name := range []string{"README.md", "notes.txt"} {
		r, found := tt.Find(name)
		if !found || r.X < card.X || r.X+r.W > card.X+card.W {
			t.Fatalf("changed file %q is not inside the panel card %+v (found=%v at %+v)", name, card, found, r)
		}
	}
	if s.sidePanel.page != sidePanelChanges {
		t.Fatalf("the first open should make the page sticky, have %q", s.sidePanel.page)
	}
	s.git.snapshot.Files = nil
	if got := s.sidePanelPageNow(); got != sidePanelChanges {
		t.Fatalf("the remembered page changed to %q when the tree went clean", got)
	}
	s.setSidePanelPage(sidePanelFiles)
	tt.Frame()
	if got := s.sidePanelPageNow(); got != sidePanelFiles {
		t.Fatalf("switching to Files left page %q", got)
	}
}

// A file clicked in the panel's list selects it and opens its diff in the
// center's Detail card, over the Terminal it came from; the card's back button
// names that Terminal and Close forgets the detail.
func TestSidePanelFileClickOpensTheDetailCard(t *testing.T) {
	s := sidePanelRepoShell(t)
	s.sidePanel.open = true
	tt := sidePanelTester(s, 1280, 800)
	tt.Frame()
	if s.surface.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("the Terminal is the center before any pick, got %v", s.surface.current())
	}
	// The real click path: the list row's click, handled while the panel
	// builds, selects the file and opens the card in the center.
	if err := tt.Click("notes.txt"); err != nil {
		t.Fatalf("the changed file is not in the panel: %v", err)
	}
	tt.Frame()
	if s.surface.current() != WorkspaceSurfaceDetail || s.surface.detailKind() != DetailChanges ||
		s.surface.detailOrigin() != WorkspaceSurfaceTerminal {
		t.Fatalf("a panel file click: surface=%v kind=%v origin=%v, want the Detail card over the Terminal",
			s.surface.current(), s.surface.detailKind(), s.surface.detailOrigin())
	}
	if s.surface.diff.SelectedPath != "notes.txt" {
		t.Fatalf("selected path after the click = %q, want notes.txt", s.surface.diff.SelectedPath)
	}
	s.sidePanelSelectChange("README.md", gdViewCombined)
	tt.Frame()
	if s.surface.diff.SelectedPath != "README.md" {
		t.Fatalf("selected path = %q, want README.md", s.surface.diff.SelectedPath)
	}
	if err := tt.Click("Close Detail"); err != nil {
		t.Fatalf("the Detail card has no Close: %v", err)
	}
	tt.Frame()
	if s.surface.current() != WorkspaceSurfaceTerminal || s.surface.detailKind() != "" {
		t.Fatalf("Close Detail: surface=%v kind=%q, want the Terminal and no detail kept",
			s.surface.current(), s.surface.detailKind())
	}
	if !s.sidePanel.open {
		t.Fatal("closing the detail must leave the panel's list where it was")
	}
}

// The panel is a list and nothing else (2026-11-08): a picked file's diff is a
// card in the center, so no file header — Viewed, Copy Path, Open in Editor —
// is ever drawn inside the panel, and the center's card has them all in a
// window with room.
func TestSidePanelHoldsOnlyTheList(t *testing.T) {
	s := sidePanelRepoShell(t)
	s.sidePanel.open = true
	tt := sidePanelTester(s, 1512, 900)
	tt.Frame()
	if _, ok := sidePanelCardBounds(tt); !ok {
		t.Fatal("no panel card")
	}
	if err := tt.Click("notes.txt"); err != nil {
		t.Fatalf("the changed file is not in the panel: %v", err)
	}
	settleFrames(tt, 4)
	card, _ := sidePanelCardBounds(tt)
	for _, name := range []string{"Viewed", "Open in Editor", "Copy Path"} {
		r, found := tt.Find(name)
		if !found {
			t.Fatalf("the Detail card lost %q: %q", name, tt.Texts())
		}
		if r.X+r.W > card.X {
			t.Errorf("%q is drawn at x %.0f–%.0f, inside the panel card that starts at x %.0f", name, r.X, r.X+r.W, card.X)
		}
	}
}

// The Staged list stands no taller than its own rows. It used to keep the
// dragged height (220, capped to half the short list), so a single staged file
// took a 106-point pane and left the Unstaged rows above it a row or two of
// room: a changed file sat hidden behind the Staged header (real-machine run,
// 2026-11-08).
func TestSidePanelStagedListDoesNotStarveTheUnstagedRows(t *testing.T) {
	s, tt := sidebarSplitTestShell(t, t.TempDir())
	s.git.snapshot.Files = []gitworkbench.ChangeFile{
		{Path: "src/main.go", Status: gitworkbench.StatusModified, Unstaged: true, Additions: 1},
		{Path: "notes.txt", Status: gitworkbench.StatusModified, Unstaged: true, Additions: 1},
		{Path: "extra.txt", Status: gitworkbench.StatusModified, Unstaged: true, Additions: 1},
		{Path: "README.md", Status: gitworkbench.StatusModified, Staged: true, Additions: 3},
	}
	s.rebuildDiffRows()
	tt.Frame()
	staged, ok := tt.Find("Staged Files")
	if !ok {
		t.Fatalf("no Staged Files header: %q", tt.Texts())
	}
	// src/ (folder), main.go, extra.txt and notes.txt: four Unstaged rows.
	for _, name := range []string{"main.go", "extra.txt", "notes.txt"} {
		row, ok := tt.Find(name)
		if !ok {
			t.Fatalf("no %s row: %q", name, tt.Texts())
		}
		if row.Y+row.H > staged.Y+1 {
			t.Errorf("%s (y %.0f–%.0f) sits behind the Staged Files header at y %.0f", name, row.Y, row.Y+row.H, staged.Y)
		}
	}
	// And the staged row is inside the list, above the Diff.
	row, ok := tt.Find("README.md")
	if !ok {
		t.Fatalf("no README.md row: %q", tt.Texts())
	}
	if row.Y < staged.Y {
		t.Errorf("the staged README.md row (y %.0f) is above its own header (y %.0f)", row.Y, staged.Y)
	}
}

// The divider is live: the Staged list stands at the dragged height (within the
// cap), so a taller Staged pane starts higher. Height on a list that Grow(1) had
// given a 0% basis did nothing, and the two lists shared the room equally.
func TestSidePanelDividerSetsTheStagedListHeight(t *testing.T) {
	s, tt := sidebarSplitTestShell(t, t.TempDir())
	files := []gitworkbench.ChangeFile{
		{Path: "u1.txt", Status: gitworkbench.StatusModified, Unstaged: true, Additions: 1},
		{Path: "u2.txt", Status: gitworkbench.StatusModified, Unstaged: true, Additions: 1},
		{Path: "u3.txt", Status: gitworkbench.StatusModified, Unstaged: true, Additions: 1},
	}
	for i := range 8 {
		files = append(files, gitworkbench.ChangeFile{
			Path: "s" + itoa(i) + ".txt", Status: gitworkbench.StatusModified, Staged: true, Additions: 1})
	}
	s.git.snapshot.Files = files
	s.rebuildDiffRows()

	headerAt := func(dragged float32) float32 {
		s.stagedPaneHeight = dragged
		tt.Frame()
		tt.Frame()
		r, ok := tt.Find("Staged Files")
		if !ok {
			t.Fatalf("no Staged Files header at %v: %q", dragged, tt.Texts())
		}
		return r.Y
	}
	short, tall := headerAt(60), headerAt(s.stagedPaneCap())
	if tall > short-30 {
		t.Errorf("a taller Staged pane must start higher: header at y %.0f for 60 points, y %.0f for the cap %.0f", short, tall, s.stagedPaneCap())
	}
}

// A real click on Commit works with the composer idle. The press focuses the
// form, which opens its description row and moves the buttons to the options
// row; they must stay the same elements through it, or the release lands on a
// new button and the click is lost — the first click only opened the form
// (real-machine run, 2026-11-08).
func TestQuickCommitButtonSurvivesTheComposerOpening(t *testing.T) {
	s, tt := sidebarSplitTestShell(t, t.TempDir())
	button, ok := tt.Find("Commit")
	if !ok {
		t.Fatalf("no Commit button: %q", tt.Texts())
	}
	x, y := button.X+button.W/2, button.Y+button.H/2
	tt.Move(x, y)
	tt.Frame()
	tt.Press(x, y)
	tt.Frame() // the press focused the form: it opens and the buttons move
	tt.Frame()
	tt.Release(x, y)
	tt.Frame()
	if s.surface.current() != WorkspaceSurfaceCommit || !s.commitDialogOpen {
		t.Fatalf("one press and release on Commit left the surface at %v (dialog open %v)", s.surface.current(), s.commitDialogOpen)
	}
}

// The Staged list never takes the panel's whole short list.
func TestSidePanelStagedListCap(t *testing.T) {
	s := NewShell()
	for list, want := range map[float32]float32{300: 150, 100: 60, 340: 170} {
		s.sidePanelListH = list
		if got := s.stagedPaneCap(); got != want {
			t.Fatalf("panel cap for a %v list = %v, want %v", list, got, want)
		}
	}
}

// A Commit begun beside the panel returns to the Terminal (or Chat) it began
// on once it is through or cancelled, with the panel as it was.
func TestCommitBeganBesideThePanelReturnsToItsSurface(t *testing.T) {
	s := sidePanelRepoShell(t)
	for _, from := range []WorkspaceSurfaceKind{WorkspaceSurfaceTerminal, WorkspaceSurfaceChat} {
		s.sidePanel.open = true
		s.surface.mu.Lock()
		s.surface.kind = from
		s.surface.mu.Unlock()
		s.openCommitSurface()
		if s.surface.current() != WorkspaceSurfaceCommit || s.surface.previous != from {
			t.Fatalf("from %q: commit opened as %q from %q", from, s.surface.current(), s.surface.previous)
		}
		s.cancelCommitSurface()
		if s.surface.current() != from || !s.sidePanel.open {
			t.Fatalf("from %q: after a cancel: surface %q, panel open %v", from, s.surface.current(), s.sidePanel.open)
		}
	}
}

// The whole production path: Commit opened beside an open panel, the
// preflight captured, the commit made in a real repository — and the center
// is the Terminal again, not the Diff, with the panel still open.
func TestSidePanelCommitThroughTheRealPathReturnsToTheTerminal(t *testing.T) {
	s := sidePanelRepoShell(t)
	var mu sync.Mutex
	s.uiApplyOverride = func(fn func()) {
		mu.Lock()
		fn()
		mu.Unlock()
	}
	wait := func(what string, done func() bool) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			mu.Lock()
			ok := done()
			mu.Unlock()
			if ok {
				return
			}
			if time.Now().After(deadline) {
				mu.Lock()
				defer mu.Unlock()
				files := -1
				if s.git.snapshot != nil {
					files = len(s.git.snapshot.Files)
				}
				t.Fatalf("timed out waiting for %s: committing=%v err=%q files=%d surface=%q",
					what, s.git.committing, s.surface.commit.ErrText, files, s.surface.current())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	s.sidePanel.open = true
	mu.Lock()
	s.openCommitSurface()
	mu.Unlock()
	wait("the commit preflight", func() bool { return s.git.preflight != nil && s.git.preflightSnap != nil })
	mu.Lock()
	s.submitCommit("panel commit", "", s.selectedCommitPaths())
	mu.Unlock()
	wait("the commit to land and the tree to read clean", func() bool {
		return !s.git.committing && s.git.snapshot != nil && len(s.git.snapshot.Files) == 0
	})
	mu.Lock()
	cur, open := s.surface.current(), s.sidePanel.open
	mu.Unlock()
	if cur != WorkspaceSurfaceTerminal || !open {
		t.Fatalf("after a panel commit: surface %q, panel open %v — want the Terminal, open", cur, open)
	}
	out, err := exec.Command("git", "-C", s.git.root, "log", "-1", "--format=%s").Output()
	if err != nil || string(out) != "panel commit\n" {
		t.Fatalf("git log = %q (%v), want the panel commit", out, err)
	}
	s.uiApplyOverride = nil
}

// The panel's refresh timer is never armed without a window, and stops with
// the panel.
func TestSidePanelRefreshTimerNeedsAWindow(t *testing.T) {
	s := sidePanelRepoShell(t)
	s.sidePanelArmTick()
	if s.sidePanel.tick != nil {
		t.Fatal("a headless shell armed a refresh timer")
	}
	s.sidePanel.tick = time.NewTimer(time.Hour)
	s.closeSidePanel()
	if s.sidePanel.tick != nil || s.sidePanel.open {
		t.Fatal("closing must stop the refresh timer")
	}
}

// Esc inside Chat leaves Chat for the Terminal — except while the floating
// panel is up, when it closes the panel first.
func TestSidePanelEscapeClosesTheOverlayBeforeLeavingChat(t *testing.T) {
	s := sidePanelRepoShell(t)
	s.surface.mu.Lock()
	s.surface.kind = WorkspaceSurfaceChat
	s.surface.mu.Unlock()
	s.sidePanel.open = true
	tt := sidePanelTester(s, 1000, 700)
	tt.Frame()
	if !s.sidePanelOverlayUp() {
		t.Fatal("the panel should float at 1000px")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if s.sidePanel.open {
		t.Fatal("Esc did not close the floating panel over Chat")
	}
	if s.surface.current() != WorkspaceSurfaceChat {
		t.Fatalf("the same Esc left Chat for %q", s.surface.current())
	}
}
