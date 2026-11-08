package nativeui

/**
 * [INPUT]: sidePanelRepoShell / filesOpsTestShell 夹具、headless Tester、herdr 投影里的第二个 pane
 * [OUTPUT]: 2026-11-08「面板只是列表」的回归——Files 行点击在中心 Detail 卡里预览（面板里没有预览）、Detail 与 Chat 来回切换并保留详情、Esc 离开、窄窗的浮层面板在选中后收起、换 pane 离开卡片、面板收起时 Detail(changes) 仍是活读
 * [POS]: detail_surface.go 的行为契约；状态机本身由 workspace_surface_test.go 覆盖
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
)

// A Files row opens its preview in the center's Detail card, not in the panel:
// the panel keeps only the tree, the card carries the file's name and size and
// a way back and out.
func TestFilesRowClickOpensThePreviewInTheDetailCard(t *testing.T) {
	s, tt, root := filesOpsTestShell(t)
	goFile := writePreviewFile(t, root, "main.go", []byte("package main\n\nfunc main() {}\n"))
	s.refreshFiles()
	settleFrames(tt, 3)
	if err := tt.Click("main.go"); err != nil {
		t.Fatalf("the file is not in the panel's tree: %v", err)
	}
	settleFrames(tt, 3)
	if s.surface.current() != WorkspaceSurfaceDetail || s.surface.detailKind() != DetailFile {
		t.Fatalf("a Files click: surface=%v kind=%q, want the file's Detail card", s.surface.current(), s.surface.detailKind())
	}
	if s.rightPanel.selectedFilePath != goFile {
		t.Fatalf("selection = %q, want %q", s.rightPanel.selectedFilePath, goFile)
	}
	card, ok := sidePanelCardBounds(tt)
	if !ok {
		t.Fatal("the panel must stay on screen beside its detail")
	}
	size, ok := tt.Find("29 B")
	if !ok {
		t.Fatalf("the card's viewer bar lost the file's size: %q", tt.Texts())
	}
	if size.X+size.W > card.X {
		t.Errorf("the preview is drawn inside the panel (x %.0f–%.0f, card from %.0f); it belongs in the center", size.X, size.X+size.W, card.X)
	}
	for _, label := range []string{"Close Detail", "Back to Terminal"} {
		if !tt.HasText(label) {
			t.Errorf("the Detail card lost %q: %q", label, tt.Texts())
		}
	}
}

// The Detail card and the Chat switch without closing anything: Back lands on
// the Chat and the Chat's header offers the detail again until Close forgets it.
func TestDetailCardSwitchesWithTheChatAndKeepsItsDetail(t *testing.T) {
	s := sidePanelRepoShell(t)
	s.projection.Agents = []herdr.Agent{{PaneID: "p1", Kind: "omp", Status: "idle"}}
	s.surface.openChat(s.workspaceContext())
	tt := sidePanelTester(s, 1512, 900)
	if err := NewChatBridge(s).ReviewChanges(t.Context(), "notes.txt"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tt, 3)
	if s.surface.current() != WorkspaceSurfaceDetail {
		t.Fatalf("Review changes left the surface on %v", s.surface.current())
	}
	if tt.HasText("Show Changes") {
		t.Fatal("the way back to the detail is drawn while the detail is on screen")
	}

	if err := tt.Click("Back to Chat"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tt, 3)
	if s.surface.current() != WorkspaceSurfaceChat || s.surface.detailKind() != DetailChanges {
		t.Fatalf("Back to Chat: surface=%v detail=%q, want the Chat with the detail kept", s.surface.current(), s.surface.detailKind())
	}
	if err := tt.Click("Show Changes"); err != nil {
		t.Fatalf("the Chat header lost the way back to the detail: %v", err)
	}
	settleFrames(tt, 3)
	if s.surface.current() != WorkspaceSurfaceDetail {
		t.Fatalf("Show Changes landed on %v", s.surface.current())
	}

	if err := tt.Click("Close Detail"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tt, 3)
	if s.surface.current() != WorkspaceSurfaceChat || s.surface.detailKind() != "" {
		t.Fatalf("Close Detail: surface=%v detail=%q, want the Chat and nothing kept", s.surface.current(), s.surface.detailKind())
	}
	if tt.HasText("Show Changes") {
		t.Fatalf("a closed detail is still offered: %q", tt.Texts())
	}
}

// Esc on the card is Back — the card stays remembered — unless the floating
// panel is up, which Esc closes first.
func TestEscapeLeavesTheDetailCard(t *testing.T) {
	s := sidePanelRepoShell(t)
	s.sidePanel.open = true
	tt := sidePanelTester(s, 1512, 900)
	s.sidePanelSelectChange("notes.txt", gdViewCombined)
	settleFrames(tt, 3)
	if s.surface.current() != WorkspaceSurfaceDetail {
		t.Fatalf("surface = %v, want the Detail card", s.surface.current())
	}
	tt.Key(0, ui.KeyEscape)
	settleFrames(tt, 3)
	if s.surface.current() != WorkspaceSurfaceTerminal || s.surface.detailKind() != DetailChanges {
		t.Fatalf("Esc: surface=%v detail=%q, want the Terminal with the detail kept", s.surface.current(), s.surface.detailKind())
	}
}

// Esc is the find bar's before it is the card's: with the bar of a formatted
// Markdown file open, the first Esc takes it down and the card stays (the bar
// used to stay open behind a card that had already stepped away); the next one
// steps out as before.
func TestEscapeClosesTheMarkdownFindBeforeLeavingTheCard(t *testing.T) {
	s, tt, root := filesOpsTestShell(t)
	writePreviewFile(t, root, "notes.md", []byte("# Notes\n\nsome needle text, another needle\n"))
	s.refreshFiles()
	settleFrames(tt, 3)
	if err := tt.Click("notes.md"); err != nil {
		t.Fatalf("the file is not in the panel's tree: %v", err)
	}
	settleFrames(tt, 6)
	mp := s.rightPanel.nativeMarkdown
	if mp == nil || s.surface.current() != WorkspaceSurfaceDetail {
		t.Fatalf("the Markdown file must show formatted in its card: markdown=%v surface=%v", mp != nil, s.surface.current())
	}
	tt.Key(ui.Cmd, ui.KeyF)
	settleFrames(tt, 3)
	tt.Type("needle")
	settleFrames(tt, 3)
	if !mp.find.open || len(mp.find.hits) != 2 {
		t.Fatalf("⌘F and a query must leave the bar open on two hits: open=%v hits=%d", mp.find.open, len(mp.find.hits))
	}

	// With the cursor still in the bar's field the field takes Esc itself; the
	// shell sees it only once focus is anywhere else — a click on the page.
	heading, ok := tt.Find("Notes")
	if !ok {
		t.Fatalf("the heading is not drawn: %q", tt.Texts())
	}
	tt.ClickAt(heading.X+heading.W/2, heading.Y+heading.H/2)
	settleFrames(tt, 2)
	tt.Key(0, ui.KeyEscape)
	settleFrames(tt, 3)
	if mp.find.open || mp.find.hits != nil {
		t.Fatalf("the first Esc closes the bar and clears its hits: open=%v hits=%d", mp.find.open, len(mp.find.hits))
	}
	if s.surface.current() != WorkspaceSurfaceDetail {
		t.Fatalf("the first Esc belongs to the bar; the card must stay, got %v", s.surface.current())
	}

	tt.Key(0, ui.KeyEscape)
	settleFrames(tt, 3)
	if s.surface.current() == WorkspaceSurfaceDetail {
		t.Fatal("with no bar open Esc steps out of the card")
	}

	// A bar left open behind the source view is not on screen, so it does not
	// hold the Esc: the card steps out.
	if err := tt.Click("notes.md"); err != nil {
		t.Fatalf("the picked row no longer brings the card back: %v", err)
	}
	settleFrames(tt, 3)
	tt.Key(ui.Cmd, ui.KeyF)
	settleFrames(tt, 3)
	tt.Type("needle")
	settleFrames(tt, 3)
	if !mp.find.open {
		t.Fatal("the bar must be open before the source view takes the page")
	}
	if err := tt.Click("Source"); err != nil {
		t.Fatalf("the card has no Source switch: %v", err)
	}
	settleFrames(tt, 3)
	tt.Key(0, ui.KeyEscape)
	settleFrames(tt, 3)
	if s.surface.current() == WorkspaceSurfaceDetail {
		t.Fatal("a find bar behind the source view held the Esc")
	}
}

// In a window too narrow to dock the panel it floats over the center; picking
// an item in it opens the card and the floating panel steps away, so the card
// is what shows.
func TestPickingInAFloatingPanelShowsTheCard(t *testing.T) {
	s := sidePanelRepoShell(t)
	s.sidePanel.open = true
	tt := sidePanelTester(s, 1000, 800)
	if !s.sidePanelOverlayUp() {
		t.Fatal("the panel must float in a 1000 px window")
	}
	if err := tt.Click("notes.txt"); err != nil {
		t.Fatalf("the changed file is not in the floating panel: %v", err)
	}
	settleFrames(tt, 3)
	if s.surface.current() != WorkspaceSurfaceDetail {
		t.Fatalf("surface = %v, want the Detail card", s.surface.current())
	}
	if s.sidePanel.open {
		t.Fatal("the floating panel stayed over the card it opened")
	}
}

// Another pane is navigation away from the card: the Detail belongs to the
// pane that was selected when it came up.
func TestSelectingAnotherPaneLeavesTheDetailCard(t *testing.T) {
	s := sidePanelRepoShell(t)
	s.projection.Panes = append(s.projection.Panes,
		herdr.Pane{ID: "p2", ProjectID: "w1", TerminalID: "term-2", Label: "builder", TabID: "t1", CWD: s.projection.Panes[0].CWD})
	s.sidePanel.open = true
	tt := sidePanelTester(s, 1512, 900)
	s.sidePanelSelectChange("notes.txt", gdViewCombined)
	settleFrames(tt, 3)
	if s.surface.current() != WorkspaceSurfaceDetail {
		t.Fatalf("surface = %v, want the Detail card", s.surface.current())
	}
	s.selectPane("p2")
	settleFrames(tt, 3)
	if s.surface.current() == WorkspaceSurfaceDetail {
		t.Fatal("the card stayed up over another pane")
	}
}

// The worktree keeps being read while the card shows it, even with the panel
// shut: the card is a live view, the panel's re-reading timer is not its only
// owner.
func TestChangesDetailKeepsTheWorktreeLiveWithTheListShut(t *testing.T) {
	s := sidePanelRepoShell(t)
	tt := sidePanelTester(s, 1512, 900)
	if s.changesOnScreen() {
		t.Fatal("nothing shows the changes yet")
	}
	s.sidePanel.open = true
	s.sidePanelSelectChange("notes.txt", gdViewCombined)
	settleFrames(tt, 2)
	s.closeSidePanel()
	settleFrames(tt, 2)
	if !s.changesOnScreen() {
		t.Fatal("the Detail card shows the changes: they must stay live with the panel shut")
	}
	s.closeDetail()
	if s.changesOnScreen() {
		t.Fatal("with the card gone and the panel shut nothing reads the worktree")
	}
}

// Detail titles: the changes card counts files; a file card names the file and
// keeps its folder as a muted run with the full path in the tooltip.
func TestDetailTitleNamesWhatTheCardShows(t *testing.T) {
	s := sidePanelRepoShell(t)
	s.surface.openDetail(DetailChanges, s.selectedPaneID)
	title, facts, _ := s.detailTitle()
	if title != "Changes" || facts != "2 files" {
		t.Fatalf("changes title = %q / %q, want Changes / 2 files", title, facts)
	}
	root := s.rightPanel.currentRoot
	if root == "" {
		root = s.git.root
		s.rightPanel.currentRoot = root
	}
	s.rightPanel.selectedFilePath = filepath.Join(root, "docs", "guide.md")
	s.surface.openDetail(DetailFile, s.selectedPaneID)
	title, facts, tip := s.detailTitle()
	if title != "guide.md" || facts != "docs" || tip != s.rightPanel.selectedFilePath {
		t.Fatalf("file title = %q / %q / %q", title, facts, tip)
	}
}
