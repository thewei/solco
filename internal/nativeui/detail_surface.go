package nativeui

/**
 * [INPUT]: 依赖 workspace_surface.go 的 Detail 状态（detail/detailFrom/detailPane）、side_panel.go 的面板状态与定时刷新、gd_surface.go 的 gdDiffSurface、files_native_preview.go 的预览条/体、gorex_style 的图标按钮与卡片面
 * [OUTPUT]: 对外提供 openDetail/showDetailSurface/leaveDetail/closeDetail（面板列表点击打开中心 Detail 卡片、与 Terminal/Chat 互相切换而不关闭、Close 才遗忘）、detailCardHost（Detail 摆成与 Terminal 同款的窗格卡片：同间距、paneCardFaceOf().dress 的纸色与柔影）、workspaceDetailSurface（卡片内容：统一头 + Changes 的 diff 卡片 / 文件预览）、detailReturnButton（Terminal/Chat 卡头里「回到被放下的详情」的单钮）、openFileDetail（Files 树点击与搜索命中的唯一入口）、detailOverflowItems
 * [POS]: 2026-11-08 决定「The panel is a list」：右侧面板只列条目，点击的条目在中心以卡片形式显示，形式同 Terminal 卡（圆角纸面 + 头行）；Terminal/Chat/Detail 是同一个 WorkspacePrimarySurface 的三个表面，一次只显示一个，绝不分屏；Detail 不是第二套 Git/Files 实现——Changes 用唯一的 gdDiffSurface，Files 用既有预览分级体
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

// openDetail puts kind's card in the center. The panel's list click is its
// only caller (Files rows, search hits, Changes rows, Chat's Review changes):
// a narrow window's floating panel closes first, so the card is what shows.
// The card is the third surface beside Terminal and Chat, never a split —
// its back button lands on the surface it came from and keeps it open.
func (s *Shell) openDetail(kind DetailKind) {
	if s.sidePanelOverlayUp() {
		s.closeSidePanel()
	}
	s.surface.openDetail(kind, s.selectedPaneID)
	if kind == DetailChanges {
		// The panel's re-reading timer lapses with the panel; the card keeps
		// the worktree it shows live.
		s.sidePanelArmTick()
	}
}

// openFileDetail is the Files list's click: record the file as the preview's
// selection and show its card.
func (s *Shell) openFileDetail(path string) {
	s.openFilePreview(path)
	s.openDetail(DetailFile)
}

// showDetailSurface brings the set-aside detail back: the Terminal and Chat
// headers' button, and a click on the list's already picked row.
func (s *Shell) showDetailSurface() {
	if s.holdCommitNavigation() {
		return
	}
	if s.sidePanelOverlayUp() {
		s.closeSidePanel()
	}
	if s.surface.showDetail(s.selectedPaneID) && s.surface.detailKind() == DetailChanges {
		s.sidePanelArmTick()
	}
}

// leaveDetail is the card's back button: the Terminal or Chat it was opened
// from, with the detail still open behind.
func (s *Shell) leaveDetail() {
	target := s.surface.detailOrigin()
	if target == WorkspaceSurfaceChat && !s.workspaceChatAvailable() {
		target = WorkspaceSurfaceTerminal
	}
	s.showSurface(target)
}

// closeDetail is Close: leave the card when it is up, then forget it.
func (s *Shell) closeDetail() {
	if s.surface.current() == WorkspaceSurfaceDetail {
		s.leaveDetail()
		if s.surface.current() == WorkspaceSurfaceDetail {
			return // held; nothing changed
		}
	}
	s.surface.forgetDetail()
}

// detailLabel names the detail for the buttons that bring it back.
func (s *Shell) detailLabel() string {
	if s.surface.detailKind() == DetailFile {
		if path := s.rightPanel.selectedFilePath; path != "" {
			return filepath.Base(path)
		}
		return "File"
	}
	return "Changes"
}

// detailGlyph is the icon of the detail's kind.
func (s *Shell) detailGlyph() *ui.SVG {
	if s.surface.detailKind() == DetailFile {
		return iconFile
	}
	return iconFileDiff
}

// detailReturnButton is the Terminal and Chat card headers' way back to a
// detail that was set aside: one icon button naming it, drawn only while one
// is open and not on screen — the counterpart of the card's back button.
func (s *Shell) detailReturnButton(c *ui.Context, k *gorexColors, size, glyph float32) {
	if s.surface.detailKind() == "" || s.surface.current() == WorkspaceSurfaceDetail {
		return
	}
	name := "Show " + s.detailLabel()
	if gorexIconButton(c, k, s.detailGlyph(), name, name, false, size, glyph).Clicked() {
		s.showDetailSurface()
	}
}

// detailCardHost seats the Detail card on the cards' ground the way the
// Terminal canvas seats its cards (2026-11-08): the same gap all round, the
// same paper, edge and soft shadow (paneCardFace.dress), so switching between
// Terminal and Detail changes what is in the card, not how the window looks.
func (s *Shell) detailCardHost(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(gorexGap).Children(func() {
		card := ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Radius(gorexCardR).Clip().Label("Detail")
		paneCardFaceOf(t.Dark, false, false).dress(card)
		card.Children(func() { s.workspaceDetailSurface(c) })
	})
}

// workspaceDetailSurface is the Detail card: the Terminal card's own form (the
// content paper, a header row) holding the picked item's detail — the
// worktree's diff cards for a change, the preview for a file.
func (s *Shell) workspaceDetailSurface(c *ui.Context) {
	t := c.Theme()
	k := gorexColorsOf(t.Dark)
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		s.detailHeader(c, k)
		switch s.surface.detailKind() {
		case DetailFile:
			s.detailFileBody(c)
		default:
			s.gdDiffSurface(c)
		}
	})
}

// detailFileBody is the file's preview: the strip of size and view choices,
// then the tier the plan chose.
func (s *Shell) detailFileBody(c *ui.Context) {
	selected := s.rightPanel.selectedFilePath
	if selected == "" {
		emptyState(c, "No file open", "Pick a file in the Files list.")
		return
	}
	s.filesPreviewBar(c, selected)
	s.filesPreviewBody(c, selected)
}

// detailHeader is the card's header row — no rule under it, one colour with the
// body like the right panel's (2026-11-08) — the anatomy of the Chat card's: the
// kind's glyph, what it shows, and on the right the single switch back to the
// surface it came from — the icon names the surface it lands on — and Close.
func (s *Shell) detailHeader(c *ui.Context, k *gorexColors) {
	title, facts, tip := s.detailTitle()
	ui.Row(c).FillWidth().Height(gorexHeaderH).Shrink(0).Padding(0, gorexGap, 0, 12).Gap(7).AlignItems(ui.Center).MinWidth(0).Children(func() {
		ui.Icon(c, s.detailGlyph()).Size(15, 15).TextColor(k.iconMuted).Shrink(0)
		name := ui.Text(c, title).FontSize(Typography().Body).FontWeight(WeightSemibold).TextColor(k.text).
			SingleLine().Ellipsis("…").Shrink(0).MaxWidthPercent(70)
		if tip != "" {
			tipped(c, name, tip)
		}
		if facts != "" {
			ui.Text(c, facts).FontSize(Typography().BodySmall).TextColor(k.textMuted).
				SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		}
		ui.Spacer(c)
		if s.surface.detailOrigin() == WorkspaceSurfaceChat && s.workspaceChatAvailable() {
			if gorexIconButton(c, k, iconChat, "Back to Chat", "Back to Chat", false, gorexIconBtn, gorexIconGlyph).Clicked() {
				s.leaveDetail()
			}
		} else if gorexIconButton(c, k, iconTerminal, "Back to Terminal", "Back to Terminal", false, gorexIconBtn, gorexIconGlyph).Clicked() {
			s.leaveDetail()
		}
		if gorexIconButton(c, k, iconClose, "Close Detail", "Close Detail", false, gorexIconBtn, gorexIconGlyph).Clicked() {
			s.closeDetail()
		}
	})
}

// detailTitle is the header's words: the title, a muted run after it, and the
// tooltip that spells the full path.
func (s *Shell) detailTitle() (title, facts, tip string) {
	if s.surface.detailKind() != DetailFile {
		if snap := s.gitSnapshot(); snap != nil && len(snap.Files) > 0 {
			facts = itoa(len(snap.Files)) + " file" + pluralS(len(snap.Files))
		}
		return "Changes", facts, ""
	}
	path := s.rightPanel.selectedFilePath
	if path == "" {
		return "File", "", ""
	}
	if root := s.rightPanel.currentRoot; root != "" {
		if rel, err := filepath.Rel(root, filepath.Dir(path)); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			facts = rel
		}
	}
	return filepath.Base(path), facts, path
}

// detailOverflowItems is the title bar's Actions menu on the card: the way
// back and out, then the selected pane's own items as Chat's menu keeps them.
func (s *Shell) detailOverflowItems(m *ui.Menu) {
	back := "Back to Terminal"
	if s.surface.detailOrigin() == WorkspaceSurfaceChat && s.workspaceChatAvailable() {
		back = "Back to Chat"
	}
	if m.Item(back).Chosen() {
		s.leaveDetail()
	}
	if m.Item("Close Detail").Chosen() {
		s.closeDetail()
	}
	m.Separator()
	if s.selectedPaneID != "" {
		label := "Pane"
		if pane := s.selectedPane(); pane != nil && pane.Label != "" {
			label = pane.Label
		}
		s.paneOverflowItems(m, s.selectedPaneID, label)
	}
}
