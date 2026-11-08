/**
 * [INPUT]: 依赖 gorex_style.go 的 paneCardFaceOf、Design role 与对比度计算
 * [OUTPUT]: 验证 Terminal 卡片选中细边、统一纸色和阴影，以及高对比/透明终端边界
 * [POS]: gorex_style.go 卡片外观的回归约束；不接触 Herdr 或系统剪贴板
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package nativeui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestPaneCardFaceIsOneSurfaceToldByAQuietEdge 检查卡片共用 surface 纸色，
// 并用现有中性细边区分选中态。
func TestPaneCardFaceIsOneSurfaceToldByAQuietEdge(t *testing.T) {
	highContrast.Store(false)
	for _, dark := range []bool{false, true} {
		rest, selected := paneCardFaceOf(dark, false, false), paneCardFaceOf(dark, true, false)
		r := designRoles(dark)
		if rest.border.A != 0 || selected.border != gorexColorsOf(dark).cardBorder {
			t.Fatalf("dark=%v: only the selected card uses the palette hairline, got %+v / %+v", dark, rest.border, selected.border)
		}
		if rest.paper != r.Surface || selected.paper != r.Surface {
			t.Fatalf("dark=%v: paper = %+v / %+v, want the surface role %+v in both states", dark, rest.paper, selected.paper, r.Surface)
		}
		if rest.shadow.A == 0 || selected.shadow != rest.shadow || selected.shadowY != rest.shadowY || selected.shadowB != rest.shadowB {
			t.Fatalf("dark=%v: selected and resting cards keep the same depth shadow: %+v vs %+v", dark, rest, selected)
		}
	}
}

// TestDefaultTerminalBackgroundIsTheCardPaper keeps the pane card and the
// terminal one surface: the follow-window palettes draw on the resting paper.
func TestDefaultTerminalBackgroundIsTheCardPaper(t *testing.T) {
	highContrast.Store(false)
	light, dark := gorexTerminalThemes()
	if got, want := light.Background, paneCardFaceOf(false, false, false).paper; got != want {
		t.Fatalf("light terminal background = %+v, want the card paper %+v", got, want)
	}
	if got, want := dark.Background, paneCardFaceOf(true, false, false).paper; got != want {
		t.Fatalf("dark terminal background = %+v, want the card paper %+v", got, want)
	}
}

// TestPaneCardFaceKeepsAnEdgeWhereAShadowCannotCarryIt: Increase contrast and
// the Transparent-terminal paper keep a visible hairline, and the selected
// card a stronger ring.
func TestPaneCardFaceKeepsAnEdgeWhereAShadowCannotCarryIt(t *testing.T) {
	t.Cleanup(func() { highContrast.Store(false) })
	for _, dark := range []bool{false, true} {
		highContrast.Store(true)
		hc := paneCardFaceOf(dark, false, false)
		if hc.border.A == 0 {
			t.Fatalf("dark=%v: Increase contrast must draw the card hairline", dark)
		}
		if sel := paneCardFaceOf(dark, true, false); sel.border == hc.border {
			t.Fatalf("dark=%v: Increase contrast must ring the selected card", dark)
		}
		highContrast.Store(false)
		tr, trSel := paneCardFaceOf(dark, false, true), paneCardFaceOf(dark, true, true)
		if tr.shadow.A != 0 || trSel.shadow.A != 0 {
			t.Fatalf("dark=%v: a see-through card must not cast a shadow", dark)
		}
		if tr.border.A == 0 || trSel.border == tr.border {
			t.Fatalf("dark=%v: a see-through card keeps its hairline and rings the selected one", dark)
		}
	}
}

// TestPaneCardTextReadsOnTheDarkPaper keeps the header's secondary text,
// faint text and icons legible on the dark card paper.
func TestPaneCardTextReadsOnTheDarkPaper(t *testing.T) {
	highContrast.Store(false)
	k := gorexColorsOf(true)
	paper := paneCardFaceOf(true, false, false).paper
	for name, fg := range map[string]ui.Color{"muted": k.textMuted, "faint": k.textFaint, "icon": k.iconMuted} {
		if got := contrastRatio(fg, paper); got < 4.5 {
			t.Errorf("%s contrast %.2f < 4.5 on %+v", name, got, paper)
		}
	}
}

// TestPaneGroundIsLightOnly: only the light window paints the grouped grey
// behind the cards; the dark window keeps its near-black canvas.
func TestPaneGroundIsLightOnly(t *testing.T) {
	if _, ok := paneGround(true); ok {
		t.Fatal("the dark window must not paint a second ground")
	}
	ground, ok := paneGround(false)
	if !ok || ground != designRoles(false).Grouped {
		t.Fatalf("light ground = %+v ok=%v, want the grouped role", ground, ok)
	}
}

// TestContentGroundFollowsTheCards pins the 2026-11-08 header fusion: with
// Terminal cards on screen the content column (title bar, card gaps, the strip
// beside the right panel) is the cards' grey ground in a light window, and the
// plain canvas otherwise — in the dark window the two are already one colour.
func TestContentGroundFollowsTheCards(t *testing.T) {
	s := NewShell()
	s.surface.resetToTerminal(s.workspaceContext())
	canvas := s.tokens(false).Content
	if got := s.contentGround(false); got != canvas {
		t.Fatalf("no terminals: ground = %+v, want the canvas %+v", got, canvas)
	}
	s.terminals = map[string]*terminalSurface{"p1": {}}
	want, _ := paneGround(false)
	if got := s.contentGround(false); got != want {
		t.Fatalf("light with cards: ground = %+v, want %+v", got, want)
	}
	if got, want := s.contentGround(true), s.tokens(true).Content; got != want {
		t.Fatalf("dark: ground = %+v, want the canvas %+v", got, want)
	}
	// The Detail surface is a card too, with or without terminals behind it.
	s.terminals = nil
	s.surface.openDetail(DetailFile, "")
	if s.surface.current() != WorkspaceSurfaceDetail {
		t.Fatalf("surface = %v, want Detail", s.surface.current())
	}
	if got := s.contentGround(false); got != want {
		t.Fatalf("light Detail: ground = %+v, want %+v", got, want)
	}
}
