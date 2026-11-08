package nativeui

/**
 * [INPUT]: 官方 MyGo headless Tester、右侧面板 Changes 页夹具（分支）与 Files 目录夹具
 * [OUTPUT]: 验证侧栏层级共用的网格（首层对齐分区标题、逐层缩进一格、导向线连续）、分区不再有 "···"、悬停 "+" 与右键菜单、面板两页与 Sessions 共用头部的项目/分支快速切换、折叠头同一语法与唯一 disclosure chevron
 * [POS]: 侧栏树与头部交互回归；网格常量只在 sidebar_tree.go 一处生效
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/gitworkbench"
	"github.com/thewei/solco/internal/herdr"
)

// treeTestShell is a shell with the right panel open on Changes: the seeded
// snapshot's local changes under the shared project head, whose branch
// pull-down lists first-level leaves and a folder with two leaves.
func treeTestShell(t *testing.T) (*Shell, *ui.Tester, string) {
	t.Helper()
	root := t.TempDir()
	shell := gitSurfaceTestShell(t, root)
	shell.git.rootOf[root] = root
	shell.git.refreshing = true // keep the seeded snapshot
	shell.git.branches = []gitworkbench.Branch{
		{Name: "main", Current: true},
		{Name: "develop"},
		{Name: "feature/login"},
		{Name: "feature/signup"},
	}
	if shell.router.Path() != routeWorkspace {
		shell.router.Push(routeWorkspace)
	}
	showChangesPanel(shell)
	shell.rebuildDiffRows()
	tester := ui.NewTester(shell.View, 1200, 800)
	tester.Frame()
	tester.Frame()
	return shell, tester, root
}

func near(a, b float32) bool { return a-b < 0.75 && b-a < 0.75 }

func TestTreeGridContract(t *testing.T) {
	if got, want := treeGlyphX(0), treeRowPad+treeChevronW+treeGlyphGap; got != want {
		t.Fatalf("first-level glyph x = %v, want chevron slot + gap = %v", got, want)
	}
	if got := treeGlyphX(3) - treeGlyphX(2); got != TreeIndentWidth {
		t.Fatalf("a level steps %v, want one indent %v", got, float32(TreeIndentWidth))
	}
	if got, want := treeGuideX(1), treeChevronX(1)+treeChevronW/2; got != want {
		t.Fatalf("guide runs at %v, want the middle of the chevron slot %v", got, want)
	}
}

func TestTreeRowHeightFollowsDensity(t *testing.T) {
	shell := NewShell()
	want := map[string][2]float32{"compact": {26, 24}, "": {30, 28}, "comfortable": {34, 32}}
	for density, heights := range want {
		shell.settings.General.Density = density
		if got := shell.sidebarRowHeight(); got != heights[0] {
			t.Fatalf("density %q: workspace row %v, want %v", density, got, heights[0])
		}
		if got := shell.treeRowHeight(); got != heights[1] {
			t.Fatalf("density %q: tree row %v, want %v", density, got, heights[1])
		}
	}
}

func rgbDiff(a, b [4]uint8) int {
	d := 0
	for i := 0; i < 3; i++ {
		if a[i] > b[i] {
			d += int(a[i] - b[i])
		} else {
			d += int(b[i] - a[i])
		}
	}
	return d
}

func pixelAt(tester *ui.Tester, x, y int) [4]uint8 {
	img := tester.Image()
	i := img.PixOffset(x, y)
	return [4]uint8{img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]}
}

func TestCreateDialogRefusesAChangedRepository(t *testing.T) {
	shell, _, _ := treeTestShell(t)
	shell.openCreateDialog("git-create-branch", "New Branch", "Branch name")
	target := shell.dialogTarget
	shell.git.root = "/somewhere/else"
	shell.submitTextDialog("git-create-branch", target, "v2")
	if shell.pendingToast == "" || shell.git.opBusy {
		t.Fatalf("a create submitted after the repository changed must be refused (toast %q, busy %v)", shell.pendingToast, shell.git.opBusy)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCreateDialogMakesTheBranch(t *testing.T) {
	dir := t.TempDir()
	gitScript(t, dir,
		[]string{"init", "-q", "-b", "main"},
		[]string{"-c", "user.email=a@b.c", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	)
	shell := gitSurfaceTestShell(t, dir)
	shell.git.rootOf[dir] = dir
	shell.git.refreshing = true
	shell.openNewBranchDialog()
	shell.submitTextDialog(shell.dialogKind, shell.dialogTarget, "topic")
	waitFor(t, 5*time.Second, func() bool { return gitOut(t, dir, "branch", "--show-current") == "topic" })
}

// The shared head: project and branch each open a search pull-down.
func TestSidebarHeadSwitchesProjectsAndKeepsTheChangesPage(t *testing.T) {
	shell, tester, _ := treeTestShell(t)
	other := t.TempDir()
	shell.projection.Projects = append(shell.projection.Projects, herdr.Project{ID: "w2", Label: "elsewhere", CWD: other})
	shell.projection.Tabs = append(shell.projection.Tabs, herdr.Tab{ID: "t2", Label: "main", ProjectID: "w2"})
	shell.projection.Panes = append(shell.projection.Panes, herdr.Pane{ID: "p2", ProjectID: "w2", TerminalID: "term-2", Label: "editor", TabID: "t2", CWD: other})
	shell.git.rootOf[other] = other
	tester.Frame()

	if err := tester.Click("Switch project"); err != nil {
		t.Fatalf("project chip missing: %v; texts=%q", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	if !shell.projectMenuOpen || !tester.HasText("elsewhere") || !tester.HasText("Search projects") {
		t.Fatalf("project pull-down: open %v; texts=%q", shell.projectMenuOpen, tester.Texts())
	}
	if err := tester.Click("elsewhere"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if shell.selectedProjectID != "w2" || shell.projectMenuOpen {
		t.Fatalf("selected %q, menu open %v", shell.selectedProjectID, shell.projectMenuOpen)
	}
	if !shell.sidePanel.open || shell.sidePanel.page != sidePanelChanges || shell.git.root != other {
		t.Fatalf("panel open %v page %q root %q: switching a project from Changes must stay on it, on the new repository", shell.sidePanel.open, shell.sidePanel.page, shell.git.root)
	}
}

func TestSidebarHeadProjectSearchFiltersAndEnterPicksTheFirstMatch(t *testing.T) {
	shell, tester, _ := treeTestShell(t)
	other := t.TempDir()
	shell.projection.Projects = append(shell.projection.Projects, herdr.Project{ID: "w2", Label: "elsewhere", CWD: other})
	shell.projection.Tabs = append(shell.projection.Tabs, herdr.Tab{ID: "t2", Label: "main", ProjectID: "w2"})
	shell.projection.Panes = append(shell.projection.Panes, herdr.Pane{ID: "p2", ProjectID: "w2", TerminalID: "term-2", Label: "editor", TabID: "t2", CWD: other})
	shell.git.rootOf[other] = other
	if err := tester.Click("Switch project"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	tester.Frame()
	tester.Type("else")
	tester.Frame()
	if tester.HasText("No matching projects") || !tester.HasText("elsewhere") {
		t.Fatalf("search lost the match; texts=%q", tester.Texts())
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if shell.selectedProjectID != "w2" {
		t.Fatalf("Enter must pick the first match; selected %q", shell.selectedProjectID)
	}
}

func TestSidebarHeadBranchPulldownSearchesSwitchesAndCreates(t *testing.T) {
	shell, tester, _ := treeTestShell(t)
	if err := tester.Click("Switch branch"); err != nil {
		t.Fatalf("branch chip missing: %v; texts=%q", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	for _, want := range []string{"Search branches", "develop", "feature/login", "New Branch…"} {
		if !tester.HasText(want) {
			t.Fatalf("branch pull-down missing %q; texts=%q", want, tester.Texts())
		}
	}
	tester.Type("sign")
	tester.Frame()
	if tester.HasText("feature/login") || !tester.HasText("feature/signup") {
		t.Fatalf("search must keep only matches; texts=%q", tester.Texts())
	}
	// The seeded snapshot has local changes, so the switch asks first.
	if err := tester.Click("feature/signup"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if shell.branchMenuOpen || !shell.confirmOpen || shell.confirmKind != "git-switch-branch" || shell.confirmTarget != "feature/signup" {
		t.Fatalf("switch: menu open %v confirm %v %q/%q", shell.branchMenuOpen, shell.confirmOpen, shell.confirmKind, shell.confirmTarget)
	}
	shell.confirmOpen = false
	tester.Frame() // the confirm's modal leaves before the next click
	if err := tester.Click("Switch branch"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	tester.Frame()
	if err := tester.Click("New Branch…"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if shell.branchMenuOpen || !shell.dialogOpen || shell.dialogKind != "git-create-branch" {
		t.Fatalf("New Branch…: menu open %v dialog %v %q", shell.branchMenuOpen, shell.dialogOpen, shell.dialogKind)
	}
}

func TestSidebarHeadBranchRowsCarryMergeAndDeleteInTheirContextMenu(t *testing.T) {
	shell, tester, _ := treeTestShell(t)
	if err := tester.Click("Switch branch"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	tester.Frame()
	if err := tester.RightClick("develop"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	menu := strings.Join(tester.Menu(), "|")
	if !strings.Contains(menu, "Merge into Current…") || !strings.Contains(menu, "Delete Branch…") {
		t.Fatalf("branch row menu = %q", menu)
	}
	if err := tester.ChooseMenuItem("Merge into Current…"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !shell.mergeDialogOpen || shell.mergeTarget != "develop" || shell.branchMenuOpen {
		t.Fatalf("merge dialog %v target %q menu open %v", shell.mergeDialogOpen, shell.mergeTarget, shell.branchMenuOpen)
	}
}

// The chip's pull-down opens again after it closed (the 2026-10-06 pin:
// a click-counter toggle used to swallow every click after the first).
func TestSidebarHeadPulldownReopens(t *testing.T) {
	shell, tester, _ := treeTestShell(t)
	for round := 1; round <= 2; round++ {
		if err := tester.Click("Switch branch"); err != nil {
			t.Fatal(err)
		}
		tester.Frame()
		tester.Frame()
		if !shell.branchMenuOpen || !tester.HasText("Search branches") {
			t.Fatalf("round %d: pull-down did not open; texts=%q", round, tester.Texts())
		}
		tester.ClickAt(900, 700) // outside: the backdrop closes it
		tester.Frame()
		if shell.branchMenuOpen {
			t.Fatalf("round %d: outside click did not close the pull-down", round)
		}
	}
}

func TestSidebarHeadWithoutRepositoryHidesTheBranchChip(t *testing.T) {
	shell, tester, _ := treeTestShell(t)
	shell.git.root = ""
	shell.git.snapshot = nil
	tester.Frame()
	if _, ok := tester.Find("Switch branch"); ok {
		t.Fatal("no repository, no branch to switch")
	}
	if _, ok := tester.Find("Switch project"); !ok {
		t.Fatalf("the project chip stays; texts=%q", tester.Texts())
	}
}

// The panel's Files page wears the same head and the same grid: file titles share the
// folder titles' column, and the old text "Refresh" button is an icon.
func TestFilesPanelSharesTheHeadAndTheGrid(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"README.md", filepath.Join("src", "main.go")} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	shell := gitSurfaceTestShell(t, dir)
	shell.git.rootOf[dir] = dir
	shell.git.refreshing = true
	// The sidebar names the project too; folded away, the head's chip is the
	// only "demo" on screen.
	shell.sidebarCollapsed = true
	showFilesPanel(shell)
	tester := ui.NewTester(shell.View, 1200, 800)
	settleFrames(tester, 12)

	for _, want := range []string{"Switch project", "Switch branch", "Refresh files"} {
		if _, ok := tester.Find(want); !ok {
			t.Fatalf("Files head missing %q; texts=%q", want, tester.Texts())
		}
	}
	if _, ok := tester.Find("Refresh"); ok {
		t.Fatal("the text Refresh button must be gone")
	}
	folder, ok1 := tester.Find("src")
	file, ok2 := tester.Find("README.md")
	if !ok1 || !ok2 {
		t.Fatalf("rows missing; texts=%q", tester.Texts())
	}
	if !near(folder.X, file.X) {
		t.Fatalf("folder title at %.1f, file title at %.1f: siblings share one title column", folder.X, file.X)
	}
	project, _ := tester.Find("demo")
	if !near(folder.X-project.X, treeChevronW+treeTitleGap) && !near(folder.X, project.X) {
		t.Fatalf("project title at %.1f vs tree titles at %.1f: the head must sit on the grid", project.X, folder.X)
	}
}

// workspaceTreeShell is a Workspace-route shell with two projects, one tab
// holding two panes, and the first pane selected.
func workspaceTreeShell() *Shell {
	s := NewShell()
	s.settings.General.TabsPlacement = "sidebar" // the tree tests read the Tab rows
	s.loading = false
	s.instances = []herdr.Instance{{Name: "default", DisplayName: "Workspace Alpha", Running: true, Default: true}}
	s.activeInstance = "default"
	s.projection = herdr.Projection{
		Protocol:         22,
		FocusedProjectID: "w1",
		FocusedTabID:     "t1",
		FocusedPaneID:    "p1",
		Projects: []herdr.Project{
			{ID: "w1", Label: "Project Alpha", TabCount: 1, PaneCount: 2},
			{ID: "w2", Label: "Project Menu", TabCount: 0, PaneCount: 0},
		},
		Tabs: []herdr.Tab{{ID: "t1", ProjectID: "w1", Label: "Main Tab", PaneCount: 2}},
		Panes: []herdr.Pane{
			{ID: "p1", TerminalID: "term1", ProjectID: "w1", TabID: "t1", Label: "Pane One"},
			{ID: "p2", TerminalID: "term2", ProjectID: "w1", TabID: "t1", Label: "Pane Two"},
		},
	}
	s.selectedProjectID = "w1"
	s.selectedTabID = "t1"
	s.selectedPaneID = "p1"
	s.selected = "pane:p1"
	return s
}

// The Workspace tree is a plain list: no row carries a kebab of its own named
// "More actions". The one hover action is a project's "⋯" (2026-11-08 U10, in
// the place of the old "+"): it opens the very menu the right click opens —
// New Tab ▸, Rename Project…, Close Project… — for the row it was pressed on.
func TestWorkspaceTreeRowsDropTheKebabAndProjectsOfferTheirMenuOnHover(t *testing.T) {
	s := workspaceTreeShell()
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	for _, text := range tester.Texts() {
		if strings.Contains(text, "More actions") {
			t.Fatalf("a Workspace tree row still carries a kebab: %q", text)
		}
	}
	if rect, ok := tester.Find("Actions for Project Menu"); ok && rect.W > 0 {
		t.Fatal("the ⋯ belongs under the pointer only")
	}
	if tester.HasText("New tab in Project Menu") {
		t.Fatal("the hover + is gone: the ⋯ menu carries New Tab")
	}
	row, ok := tester.Find("Project Menu")
	if !ok {
		t.Fatalf("project row missing: %q", tester.Texts())
	}
	tester.Move(row.X+row.W/2, row.Y+row.H/2)
	tester.Frame()
	tester.Frame()
	if rect, ok := tester.Find("Actions for Project Menu"); !ok || rect.W == 0 {
		t.Fatalf("hovering a project must offer ⋯: %q", tester.Texts())
	}
	if err := tester.Click("Actions for Project Menu"); err != nil {
		t.Fatal(err)
	}
	menu := tester.Menu()
	for _, want := range []string{"New Tab", "Rename Project…", "Close Project…"} {
		if !containsText(menu, want) {
			t.Fatalf("the ⋯ menu %q lacks %q", menu, want)
		}
	}
	if s.selectedProjectID != "w1" {
		t.Fatalf("pressing ⋯ also activated the row: selected project %q", s.selectedProjectID)
	}
	tester.CloseMenu()
	// The same items as the right click, in the same order.
	if err := tester.RightClick("Project Menu"); err != nil {
		t.Fatal(err)
	}
	if right := tester.Menu(); !slices.Equal(right, menu) {
		t.Fatalf("right-click menu %q differs from the ⋯ menu %q", right, menu)
	}
	tester.CloseMenu()
	tab, _ := tester.Find("Main Tab")
	tester.Move(tab.X+tab.W/2, tab.Y+tab.H/2)
	tester.Frame()
	tester.Frame()
	if tester.HasText("New tab in Main Tab") || tester.HasText("Actions for Main Tab") {
		t.Fatalf("tab rows have no hover action: %q", tester.Texts())
	}
}

// The title bar's secondary actions are one labeled pull-down, not "…".
func TestTitleBarActionsPulldownCarriesTheWorkspaceMenu(t *testing.T) {
	s := workspaceTreeShell()
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	if err := tester.Click("Actions"); err != nil {
		t.Fatalf("title bar has no Actions pull-down: %v; texts=%q", err, tester.Texts())
	}
	tester.Frame()
	menu := tester.Menu()
	for _, want := range []string{"Split Right", "Rename Pane…", "New Tab", "Refresh Runtime"} {
		if !containsText(menu, want) {
			t.Fatalf("Actions menu %q missing %q", menu, want)
		}
	}
	tester.CloseMenu()
}

// The Actions menu's New Tab is the project row's New Tab (newProjectTab): it
// needs a project the runtime still reports, and offers nothing otherwise.
func TestTitleBarNewTabNeedsAProjectTheRuntimeStillHas(t *testing.T) {
	s := workspaceTreeShell()
	s.selectedProjectID = "gone"
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	if err := tester.Click("Actions"); err != nil {
		t.Fatalf("title bar has no Actions pull-down: %v; texts=%q", err, tester.Texts())
	}
	tester.Frame()
	menu := tester.Menu()
	if !containsText(menu, "Refresh Runtime") {
		t.Fatalf("Actions menu did not open: %q", menu)
	}
	if containsText(menu, "New Tab") {
		t.Fatalf("Actions menu offers New Tab for a project that is gone: %q", menu)
	}
	tester.CloseMenu()
}

// TestSectionHeadersWearOneGrammar pins the collapsible header across the
// surfaces that have one: the Changes panes (right panel) and the Workspace
// sections all draw the same row — one height, the title the same distance
// after the chevron slot, one title size.
func TestSectionHeadersWearOneGrammar(t *testing.T) {
	_, git, _ := treeTestShell(t)
	ws := ui.NewTester(rightPanelTestShell(t, t.TempDir()).View, 1200, 800)
	settleFrames(ws, 3)
	probes := []struct {
		name   string
		tester *ui.Tester
		title  string
	}{
		{"changes panes", git, "Unstaged Files"},
		{"changes panes", git, "Staged Files"},
		{"workspace sections", ws, "Agents"},
		{"workspace sections", ws, "Workspace"},
	}
	var want [3]float32
	for i, p := range probes {
		row, ok := p.tester.Find("Toggle " + p.title)
		if !ok {
			t.Fatalf("%s: no header row %q; texts=%q", p.name, "Toggle "+p.title, p.tester.Texts())
		}
		title, ok := p.tester.Find(p.title)
		if !ok {
			t.Fatalf("%s: no title %q", p.name, p.title)
		}
		got := [3]float32{title.X - row.X, row.H, title.H}
		if i == 0 {
			want = got
			continue
		}
		for k, label := range []string{"title offset", "row height", "title height"} {
			if math.Abs(float64(got[k]-want[k])) > 0.5 {
				t.Errorf("%s %q: %s %.1f, the Changes panes' %.1f", p.name, p.title, label, got[k], want[k])
			}
		}
	}
}

// TestOneDisclosureChevron pins the section header's chevron to the one
// rotating treeChevron: no second collapsible header with a chevron of its
// own — hand-rotated (Animate "rot") or swapped between the right- and
// down-pointing glyphs. The other chevrons are deliberate: the diff file
// card's own 15pt one, the Workspace tree rows' hover chevron, and the
// title-bar breadcrumb's separators, which disclose nothing.
func TestOneDisclosureChevron(t *testing.T) {
	rotates := regexp.MustCompile(`Animate\("rot"`)
	swaps := regexp.MustCompile(`\biconChevron\b`)
	allowedRotate := map[string]bool{"sidebar_tree.go": true, "gd_surface.go": true}
	allowedSwap := map[string]bool{"icons_gen.go": true, "components.go": true, "breadcrumb_switcher.go": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	rotated, swapped := map[string]int{}, map[string]int{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		rotated[name] = len(rotates.FindAllIndex(src, -1))
		swapped[name] = len(swaps.FindAllIndex(src, -1))
	}
	for name, n := range rotated {
		if n > 0 && !allowedRotate[name] {
			t.Errorf("%s hand-rotates a chevron (%d×): a collapsible header draws treeChevron", name, n)
		}
	}
	for name, n := range swapped {
		if n > 0 && !allowedSwap[name] {
			t.Errorf("%s swaps the right-pointing chevron (%d×): a collapsible header draws treeChevron", name, n)
		}
	}
	if rotated["sidebar_tree.go"] == 0 || rotated["gd_surface.go"] == 0 || swapped["components.go"] == 0 {
		t.Fatalf("the scan no longer finds the known chevrons (rotate %v, swap %v)", rotated, swapped)
	}
}
