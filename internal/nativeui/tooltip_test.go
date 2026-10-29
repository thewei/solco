package nativeui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"image"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/settings"
)

/**
 * [INPUT]: 依赖 nativeui 的 tipped/tipKeys/tipColors 与 shortcutBinding.keycaps、Shell.sidebarToggle/surfaceToggle, mygo/ui 的 Tester 无头渲染（Move/Advance 驱动悬停、Image 像素量边）, go/parser 源码扫描
 * [OUTPUT]: 对外提供 tooltip 合约回归：nova 面（圆角 Row、内边距 6×12、无阴影）、箭头随翻转与夹紧指向锚点、键帽芯片尺寸与右内边距、禁用元素不出 tip、全应用只经 tipped 出 tooltip（源码扫描）、键帽拼写与"有效绑定"文案（改绑后提示不说谎）
 * [POS]: 2026-10-29 Phase 2 tooltip 单一外观的守卫测试；新增 tooltip 时由源码扫描拦下绕过 tipped 的写法
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// The nova numbers the tests hold the face to, written out here rather than
// read from tooltip.go so that changing a constant there fails a test:
// the arrow stands 5 points out of the tip, a Kbd chip is h-5 (20) high.
const (
	novaArrowRise = 5
	novaKbdHeight = 20
)

// tipWindow is a window holding one anchor box at at, tipped with text; the
// pointer then rests on the anchor until its tooltip shows.
func tipWindow(t *testing.T, dark bool, w, h int, at ui.Rect, text string, opts ...tipOption) *ui.Tester {
	t.Helper()
	tt := ui.NewTester(func(c *ui.Context) {
		anchor := ui.Box(c).Absolute().Left(at.X).Top(at.Y).Size(at.W, at.H).Role(ui.RoleButton).Label("anchor")
		tipped(c, anchor, text, opts...)
	}, w, h)
	tt.SetDark(dark)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	settleFrames(tt, 2)
	tt.Move(at.X+at.W/2, at.Y+at.H/2)
	tt.Frame()
	tt.Advance(700 * time.Millisecond)
	tt.Frame()
	return tt
}

// tipEdges measures a tooltip's box from the text inside it: walking out
// of the text along its middle line and along a column near its left end,
// while the pixels still wear the tip's fill. The box is in pixels, its Max
// the first pixel outside.
func tipEdges(t *testing.T, tt *ui.Tester, text ui.Rect) (box image.Rectangle, fill [4]uint8) {
	t.Helper()
	ym, xc := int(text.Y+text.H/2), int(text.X)+3
	fill = pixelAt(tt, int(text.X)-2, ym)
	walk := func(x, y, dx, dy int) int {
		for steps := 0; steps < 200; steps++ {
			if rgbDiff(pixelAt(tt, x+dx*steps, y+dy*steps), fill) > 8 {
				return steps
			}
		}
		t.Fatalf("a tooltip edge was not found from (%d,%d) toward (%d,%d)", x, y, dx, dy)
		return 0
	}
	left := walk(int(text.X)-1, ym, -1, 0)
	right := walk(int(text.X+text.W)+1, ym, 1, 0)
	up := walk(xc, int(text.Y)-1, 0, -1)
	down := walk(xc, int(text.Y+text.H)+1, 0, 1)
	box = image.Rect(int(text.X)-1-left, int(text.Y)-1-up, int(text.X+text.W)+1+right, int(text.Y+text.H)+1+down)
	return box, fill
}

// cornerInsets measures a box's top-left corner from the pixel box of its
// edge: for each of the first rows below the top edge, how many pixels in
// from the left edge the fill is mostly there.
func cornerInsets(tt *ui.Tester, box image.Rectangle, fill [4]uint8, rows int) []int {
	insets := make([]int, rows)
	for row := range insets {
		for insets[row] < 30 && rgbDiff(pixelAt(tt, box.Min.X+insets[row], box.Min.Y+row), fill) > 40 {
			insets[row]++
		}
	}
	return insets
}

// plainBoxInsets is cornerInsets of a single box of the tooltip's fill and
// the given radius, standing where the tooltip's own box does.
func plainBoxInsets(dark bool, rect ui.Rect, radius float32, box image.Rectangle, rows int) []int {
	ref := ui.NewTester(func(c *ui.Context) {
		fill, _ := tipColors(c.Theme())
		ui.Box(c).Absolute().Left(rect.X).Top(rect.Y).Size(rect.W, rect.H).Radius(radius).Background(fill)
	}, 600, 400)
	ref.SetDark(dark)
	settleFrames(ref, 2)
	return cornerInsets(ref, box, pixelAt(ref, int(rect.X+rect.W/2), int(rect.Y+rect.H/2)), rows)
}

// TestTooltipWearsTheNovaFace pins the shared face: the row radius, 6×12
// padding, no shadow — the shadcn nova tooltip, light and dark.
func TestTooltipWearsTheNovaFace(t *testing.T) {
	sp := Spacing()
	for _, dark := range []bool{false, true} {
		at := ui.Rect{X: 200, Y: 200, W: 60, H: 28}
		tt := tipWindow(t, dark, 600, 400, at, "Open the thing")
		text, ok := tt.Find("Open the thing")
		if !ok {
			t.Fatalf("dark=%v: no tooltip after the pointer rested; texts=%q", dark, tt.Texts())
		}
		box, fill := tipEdges(t, tt, text)
		if got := float32(int(text.X) - box.Min.X); got < sp.L-1 || got > sp.L+1 {
			t.Errorf("dark=%v: left padding %.0f, want %.0f", dark, got, sp.L)
		}
		if got := float32(box.Max.X - int(text.X+text.W)); got < sp.L-1 || got > sp.L+1 {
			t.Errorf("dark=%v: right padding %.0f, want %.0f", dark, got, sp.L)
		}
		if got := float32(int(text.Y) - box.Min.Y); got < sp.S-1 || got > sp.S+1 {
			t.Errorf("dark=%v: top padding %.0f, want %.0f", dark, got, sp.S)
		}
		if got := float32(box.Max.Y - int(text.Y+text.H)); got < sp.S-1 || got > sp.S+1 {
			t.Errorf("dark=%v: bottom padding %.0f, want %.0f", dark, got, sp.S)
		}

		// The corner is the row radius: the first rows of the curve start
		// where those of a plain box of that radius do (MyGo's corners are
		// continuous, so the curve is measured against a reference rather
		// than against a circle), and not where a 6 or a 10 starts them.
		rect := ui.Rect{X: text.X - sp.L, Y: text.Y - sp.S, W: text.W + 2*sp.L, H: text.H + 2*sp.S}
		got, want := cornerInsets(tt, box, fill, 5), plainBoxInsets(dark, rect, Radius().Row, box, 5)
		for row := 1; row < len(got); row++ {
			if d := got[row] - want[row]; d < -1 || d > 1 {
				t.Errorf("dark=%v: corner inset by row %v, a radius-%.0f box has %v", dark, got, Radius().Row, want)
				break
			}
		}

		// No shadow: just outside the box the page is as it is far away.
		page := pixelAt(tt, box.Min.X-40, (box.Min.Y+box.Max.Y)/2)
		mid := (box.Min.Y + box.Max.Y) / 2
		for _, x := range []int{box.Min.X - 3, box.Max.X + 2} {
			if d := rgbDiff(pixelAt(tt, x, mid), page); d > 3 {
				t.Errorf("dark=%v: the page at x=%d beside the tip is off by %d; the tip casts a shadow", dark, x, d)
			}
		}
	}
}

// TestTooltipArrowPointsAtItsAnchor pins the nub: it stands out of the foot
// of a tip above its anchor and out of the head of one below it, at the
// anchor's middle even where the window edge pushes the tip aside, and
// nowhere else along the edge.
func TestTooltipArrowPointsAtItsAnchor(t *testing.T) {
	const label = "Open the thing, a bit longer"
	for _, dark := range []bool{false, true} {
		for _, tc := range []struct {
			name  string
			at    ui.Rect
			above bool
		}{
			{"above", ui.Rect{X: 300, Y: 250, W: 60, H: 28}, true},
			{"below", ui.Rect{X: 300, Y: 4, W: 60, H: 28}, false},
			{"pushed along by the window's left edge", ui.Rect{X: 8, Y: 250, W: 28, H: 28}, true},
		} {
			tt := tipWindow(t, dark, 600, 400, tc.at, label)
			text, ok := tt.Find(label)
			if !ok {
				t.Fatalf("dark=%v %s: no tooltip", dark, tc.name)
			}
			box, fill := tipEdges(t, tt, text)
			page := pixelAt(tt, 590, 390)
			cx := int(tc.at.X + tc.at.W/2)
			edge, step := box.Max.Y, 1 // the nub stands out of the foot
			if !tc.above {
				edge, step = box.Min.Y-1, -1 // out of the head
			}
			at := func(x, rise int) [4]uint8 { return pixelAt(tt, x, edge+step*rise) }
			if d := rgbDiff(at(cx, 2), fill); d > 24 {
				t.Errorf("dark=%v %s: no nub under the anchor's middle at x=%d (diff %d from the tip's fill)", dark, tc.name, cx, d)
			}
			if d := rgbDiff(at(cx, novaArrowRise+4), page); d > 3 {
				t.Errorf("dark=%v %s: the nub reaches beyond its %d points (diff %d from the page)", dark, tc.name, novaArrowRise, d)
			}
			for _, off := range []int{-14, 14} {
				if d := rgbDiff(at(cx+off, 2), page); d > 8 {
					t.Errorf("dark=%v %s: the nub is wider than its base: x=%d is filled (diff %d from the page)", dark, tc.name, cx+off, d)
				}
			}
		}
	}
}

// TestTooltipKeycapsAreChips pins the shortcut hint: each key a chip 20
// high and at least 20 wide on a quiet wash of the text color, the tip's
// right padding shrunk to 6 beside it, as shadcn's Kbd in a tooltip.
func TestTooltipKeycapsAreChips(t *testing.T) {
	sp := Spacing()
	for _, dark := range []bool{false, true} {
		at := ui.Rect{X: 200, Y: 200, W: 60, H: 28}
		tt := tipWindow(t, dark, 600, 400, at, "Hide Sidebar", tipKeys([]string{"⌘", "B"}))
		text, ok := tt.Find("Hide Sidebar")
		if !ok {
			t.Fatalf("dark=%v: no tooltip", dark)
		}
		cmd, okCmd := tt.Find("⌘")
		key, okKey := tt.Find("B")
		if !okCmd || !okKey {
			t.Fatalf("dark=%v: keycaps missing (⌘=%v B=%v); texts=%q", dark, okCmd, okKey, tt.Texts())
		}
		fill := pixelAt(tt, int(text.X)-2, int(text.Y+text.H/2))
		chip := pixelAt(tt, int(cmd.X)-2, int(cmd.Y+cmd.H/2))
		if d := rgbDiff(chip, fill); d < 6 {
			t.Errorf("dark=%v: the keycap wears no wash on the tip's fill (diff %d)", dark, d)
		}
		// Height: out of the glyph's box, up and down the glyph's column,
		// while the pixels are the chip's.
		xc := int(cmd.X + cmd.W/2)
		up, down := 1, 0
		for up < 40 && rgbDiff(pixelAt(tt, xc, int(cmd.Y)-up), chip) <= 4 {
			up++
		}
		for down < 40 && rgbDiff(pixelAt(tt, xc, int(cmd.Y+cmd.H)+down), chip) <= 4 {
			down++
		}
		if height := int(cmd.H) + (up - 1) + down; height < novaKbdHeight-1 || height > novaKbdHeight+1 {
			t.Errorf("dark=%v: a keycap is %d high, want %d", dark, height, novaKbdHeight)
		}
		// The last chip ends 6 from the tip's right edge (has-kbd:pr-1.5).
		ym := int(key.Y + key.H/2)
		right := int(key.X + key.W)
		for rgbDiff(pixelAt(tt, right+1, ym), chip) <= 4 {
			right++
		}
		edge := right + 1
		for rgbDiff(pixelAt(tt, edge, ym), fill) <= 8 {
			edge++
		}
		if pad := float32(edge - right - 1); pad < sp.S-1 || pad > sp.S+1 {
			t.Errorf("dark=%v: %.0f between the last keycap and the tip's edge, want %.0f", dark, pad, sp.S)
		}
	}
}

// TestDisabledAnchorShowsNoTooltip keeps MyGo's rule through tipped: an
// element that is disabled shows no tip, as with Element.Tooltip.
func TestDisabledAnchorShowsNoTooltip(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		anchor := ui.ButtonBase(c).Absolute().Left(200).Top(200).Size(60, 28).Label("off").Disabled(true)
		tipped(c, anchor, "Never shown")
	}, 600, 400)
	settleFrames(tt, 2)
	tt.Move(230, 214)
	tt.Frame()
	tt.Advance(700 * time.Millisecond)
	tt.Frame()
	if tt.HasText("Never shown") {
		t.Fatalf("a disabled anchor shows its tooltip")
	}
}

// TestTooltipsHaveOneFace is the source guard: no production file asks MyGo
// for a tooltip itself — Element.Tooltip and ui.TooltipBase are called from
// tooltip.go alone, so no tip can wear another radius, padding or shadow.
func TestTooltipsHaveOneFace(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || file == "tooltip.go" {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			// Calls only: a treeRowSpec's Tooltip field is data, not MyGo's.
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "Tooltip" || sel.Sel.Name == "TooltipBase") {
				t.Errorf("%s calls %s itself; go through tipped (tooltip.go)", fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
}

// TestShortcutKeycaps pins how a binding is spelled as keycaps: modifiers in
// the macOS order (⌃⌥⇧⌘), then the key.
func TestShortcutKeycaps(t *testing.T) {
	for _, tc := range []struct {
		spec string
		want string
	}{
		{"Cmd+K", "⌘ K"},
		{"Cmd+Comma", "⌘ ,"},
		{"Cmd+[", "⌘ ["},
		{"Cmd+]", "⌘ ]"},
		{"Cmd+Shift+C", "⇧ ⌘ C"},
		{"Shift+Cmd+C", "⇧ ⌘ C"},
		{"Ctrl+Alt+Shift+Cmd+B", "⌃ ⌥ ⇧ ⌘ B"},
		{"Cmd+Enter", "⌘ ↩"},
		{"Cmd+Left", "⌘ ←"},
		{"Cmd+F5", "⌘ F5"},
		{"Cmd+7", "⌘ 7"},
	} {
		binding, ok := parseShortcut(tc.spec)
		if !ok {
			t.Fatalf("%q does not parse", tc.spec)
		}
		if got := strings.Join(binding.keycaps(), " "); got != tc.want {
			t.Errorf("keycaps(%q) = %q, want %q", tc.spec, got, tc.want)
		}
	}
}

// hoverTip rests the pointer on the element showing label until its tooltip
// shows.
func hoverTip(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("no element %q; texts=%q", label, tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Advance(700 * time.Millisecond)
	tt.Frame()
}

// TestTitlebarTooltipsNameTheEffectiveShortcuts pins the 2026-10-29 rule that
// a shortcut shown in a tooltip is the binding that really fires: Back and
// Forward follow the configured route shortcuts, the sidebar toggle its
// fixed binding. Rebinding Back changes its keycaps.
func TestTitlebarTooltipsNameTheEffectiveShortcuts(t *testing.T) {
	s := NewShell()
	tt := ui.NewTester(s.View, 1200, 800)
	settleFrames(tt, 3)
	hoverTip(t, tt, "Hide Sidebar")
	if texts := tt.Texts(); countTextExact(texts, "⌘") != 1 || countTextExact(texts, "B") != 1 {
		t.Errorf("sidebar toggle tooltip does not read Hide Sidebar ⌘ B; texts=%q", texts)
	}
	tt.Move(1100, 700)
	settleHover(tt)

	hoverTip(t, tt, "Back")
	if texts := tt.Texts(); countTextExact(texts, "⌘") != 1 || countTextExact(texts, "[") != 1 {
		t.Errorf("Back tooltip does not read ⌘ [ with the default binding; texts=%q", texts)
	}
	tt.Move(1100, 700)
	settleHover(tt)

	s.settings.Shortcuts = settings.ShortcutSettings{Back: "Cmd+Shift+Left"}
	settleFrames(tt, 2)
	hoverTip(t, tt, "Back")
	if texts := tt.Texts(); countTextExact(texts, "[") != 0 || countTextExact(texts, "⇧") != 1 || countTextExact(texts, "←") != 1 {
		t.Errorf("after rebinding Back to ⇧⌘←, its tooltip reads %q", texts)
	}
}

// TestChatSwitchTooltipNamesItsFixedShortcut pins ⇧⌘C on the Chat switch.
func TestChatSwitchTooltipNamesItsFixedShortcut(t *testing.T) {
	s := NewShell()
	tt := ui.NewTester(func(c *ui.Context) {
		k := gorexColorsOf(c.Theme().Dark)
		ui.Row(c).Absolute().Left(300).Top(300).Children(func() {
			s.surfaceToggle(c, k, WorkspaceSurfaceTerminal, "p1")
		})
	}, 800, 600)
	settleFrames(tt, 3)
	hoverTip(t, tt, "Chat")
	texts := tt.Texts()
	if !tt.HasText("Switch to Chat") || countTextExact(texts, "⇧") != 1 || countTextExact(texts, "⌘") != 1 || countTextExact(texts, "C") != 1 {
		t.Errorf("Chat switch tooltip reads %q, want Switch to Chat with ⇧ ⌘ C", texts)
	}
}
