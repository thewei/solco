package nativeui

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"image"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

/**
 * [INPUT]: 依赖 controls.go 的控件脸（regularButton / regularPrimaryButton / textField / textArea / searchField / comboField / selectField / segmentedControl）、components.go 的 ghostPill、solcoTheme、ui.Tester 的像素采样、nativeui 非测试源码的 AST
 * [OUTPUT]: 对外提供控件脸回归：控件脸既不自画焦点环也不关 MyGo 的（保持原来的纤细环）、ghostPill 键盘聚焦可见、同一排的控件同高、圆角只有 Radius().Row 一个来源、分段控件（含聚焦态）与官方 ui.Segmented 像素一致、业务代码不再手搓控件脸
 * [POS]: 2026-10-29 Phase 3（按钮 / 输入框 / 分段控件对齐 shadcn nova）的守卫；此前同一角色在 150 处各自带 7pt 圆角，主按钮比同排取消钮矮 2px；焦点环试过 nova 的 3px 贴边光晕，用户更喜欢原来的纤细环，已回退并钉住
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func settle(tt *ui.Tester) {
	tt.Frame()
	tt.Frame()
}

// ── focus ring ──────────────────────────────────────────────────────────

// focusRingCalls lists the calls in file that paint a focus ring of the app's
// own or switch MyGo's off.
func focusRingCalls(fset *token.FileSet, file *ast.File) []string {
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, isSel := call.Fun.(*ast.SelectorExpr); isSel && (sel.Sel.Name == "FocusRing" || sel.Sel.Name == "DrawOver") {
			found = append(found, fmt.Sprintf("%s: .%s(…)", fset.Position(call.Pos()), sel.Sel.Name))
		}
		return true
	})
	return found
}

// TestControlsDrawNoFocusRing pins the focus decision of 2026-10-29: the
// control faces — the two files that make them — leave the ring to MyGo, a 2px
// line one pixel off the edge. shadcn's nova ring (a flush 3px halo, painted
// over the control with MyGo's own switched off) was built and tried that day,
// and the app kept the slimmer one.
func TestControlsDrawNoFocusRing(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range []string{"controls.go", "ds_components.go"} {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range focusRingCalls(fset, file) {
			t.Errorf("%s — the control faces leave focus to MyGo's own ring", call)
		}
	}

	// A guard that finds nothing proves nothing: fed a face that switches the
	// ring off and paints its own, the scan finds both.
	bad, err := parser.ParseFile(fset, "x.go", `package x
func f(el ui.Element) ui.Element {
	return el.FocusRing(false).DrawOver(func(p *ui.Painter, r ui.Rect) {})
}`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := focusRingCalls(fset, bad); len(got) != 2 {
		t.Errorf("the scan found %d of the 2 ring calls in a face that has them: %v", len(got), got)
	}
}

// TestGhostPillShowsKeyboardFocus pins the one control that gained its ring in
// Phase 3: the ghost pill switched MyGo's ring off and showed nothing at all
// while the keyboard stood on it. The ring it shows now is MyGo's — two pixels
// wide with one clear pixel against the pill's edge — and only the keyboard
// brings it up.
func TestGhostPillShowsKeyboardFocus(t *testing.T) {
	for _, dark := range []bool{false, true} {
		th := solcoTheme(dark)
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(th)
			ui.Column(c).FillWidth().FillHeight().Background(th.Background).Padding(24).Children(func() {
				ghostPill(c, "pill", "Quick action", nil)
			})
		}, 300, 120)
		tt.SetDark(dark)
		tt.SetPreferences(ui.Preferences{ReduceMotion: true})
		settle(tt)
		settle(tt)
		pill, ok := tt.Find("Quick action")
		if !ok {
			t.Fatalf("dark=%v: the pill is missing; texts=%q", dark, tt.Texts())
		}
		x, top := int(pill.X+pill.W/2), int(pill.Y)
		restGap, restRing := pixelAt(tt, x, top-1), pixelAt(tt, x, top-2)

		tt.Key(0, ui.KeyTab)
		settle(tt)
		if got := pixelAt(tt, x, top-2); rgbDiff(got, restRing) < 30 {
			t.Errorf("dark=%v: no ring 2px above the pill the keyboard stands on: %v at rest, %v focused", dark, restRing, got)
		}
		if got := pixelAt(tt, x, top-1); rgbDiff(got, restGap) > 2 {
			t.Errorf("dark=%v: the pixel against the pill's edge changed %v → %v: the ring is MyGo's, with a clear pixel between", dark, restGap, got)
		}
	}
}

// ── one height, one corner ──────────────────────────────────────────────

// TestControlVariantsShareOneHeight pins the same-row rule: every regular
// control is one height — the primary button too, which MyGo draws without a
// border and so two pixels short of its outlined neighbour — and the compact
// pair share the compact one.
func TestControlVariantsShareOneHeight(t *testing.T) {
	for _, dark := range []bool{false, true} {
		heights := map[string]float32{}
		var seg int
		var text, choice string
		choice = "One"
		view := func(c *ui.Context) {
			th := solcoTheme(dark)
			c.SetTheme(th)
			ui.Column(c).FillWidth().FillHeight().Background(th.Background).Padding(24).Gap(12).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					heights["Button"] = regularButton(c, "Cancel").Bounds().H
					heights["PrimaryButton"] = regularPrimaryButton(c, "Save").Bounds().H
					heights["TextInput"] = textField(c, &text).Width(100).Bounds().H
					heights["SearchField"] = searchField(c, &text).Width(100).Bounds().H
					heights["Select"] = selectField(c, &choice, []string{"One", "Two"}).Width(90).Bounds().H
					heights["Combobox"] = comboField(c, &choice, []string{"One", "Two"}).Width(110).Bounds().H
					heights["segmentedControl"] = segmentedControl(c, &seg, "Alpha", "Beta").Bounds().H
				})
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					heights["compactButton"] = compactButton(c, "Cancel").Bounds().H
					heights["compactPrimaryButton"] = compactPrimaryButton(c, "Save").Bounds().H
				})
			})
		}
		tt := ui.NewTester(view, 900, 200)
		tt.SetDark(dark)
		settleFrames(tt, 3)

		regular := []string{"Button", "PrimaryButton", "TextInput", "SearchField", "Select", "Combobox", "segmentedControl"}
		for _, name := range regular {
			if h := heights[name]; math.Abs(float64(h-heights["Button"])) > 0.02 {
				t.Errorf("dark=%v: %s is %.2f high, Button is %.2f — one row, one height", dark, name, h, heights["Button"])
			}
		}
		if h := heights["Button"]; math.Abs(float64(h-Controls().RegularHeight)) > 0.5 {
			t.Errorf("dark=%v: the regular tier is %.2f high, want about %.0f", dark, h, Controls().RegularHeight)
		}
		if a, b := heights["compactButton"], heights["compactPrimaryButton"]; math.Abs(float64(a-b)) > 0.02 {
			t.Errorf("dark=%v: compactButton %.2f vs compactPrimaryButton %.2f", dark, a, b)
		}
		if h := heights["compactButton"]; math.Abs(float64(h-Controls().CompactHeight)) > 0.5 {
			t.Errorf("dark=%v: the compact tier is %.2f high, want about %.0f", dark, h, Controls().CompactHeight)
		}
	}
}

// TestControlCornerHasOneSource pins the shadcn corner (2026-10-29): MyGo's
// controls take their corner from the theme, the theme takes it from
// Radius().Row — the number the 28 icon buttons wear — and no source file
// types its own into the theme again.
func TestControlCornerHasOneSource(t *testing.T) {
	for _, dark := range []bool{false, true} {
		if got := solcoTheme(dark).Radius; got != Radius().Row {
			t.Errorf("dark=%v: the theme's corner is %v, want Radius().Row = %v", dark, got, Radius().Row)
		}
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
				return true
			}
			sel, ok := assign.Lhs[0].(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Radius" {
				return true
			}
			if _, literal := assign.Rhs[0].(*ast.BasicLit); literal {
				t.Errorf("%s: a typed corner assigned to a theme; take it from Radius()", fset.Position(assign.Pos()))
			}
			return true
		})
	}
}

// ── segmented control ───────────────────────────────────────────────────

// segmentedImage renders one segmented control — the official ui.Segmented or
// segmentedControl — in a row of the given height (0: its own), hovered on its
// first segment and/or with the keyboard on it.
func segmentedImage(dark, official bool, rowHeight float32, hoverFirst, focused bool) *image.RGBA {
	chosen := 1
	view := func(c *ui.Context) {
		th := solcoTheme(dark)
		c.SetTheme(th)
		ui.Column(c).FillWidth().FillHeight().Background(th.Background).Padding(24).Children(func() {
			row := ui.Row(c).AlignItems(ui.Stretch)
			if rowHeight > 0 {
				row.Height(rowHeight)
			}
			row.Children(func() {
				if official {
					ui.Segmented(c, &chosen, "Alpha", "Beta", "Gamma")
				} else {
					segmentedControl(c, &chosen, "Alpha", "Beta", "Gamma")
				}
			})
		})
	}
	tt := ui.NewTester(view, 420, 120)
	tt.SetDark(dark)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	settle(tt)
	settle(tt)
	if hoverFirst {
		r, ok := tt.Find("Alpha")
		if !ok {
			panic("segmentedImage: the first segment is missing")
		}
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		settle(tt)
	}
	if focused {
		tt.Key(0, ui.KeyTab)
		settle(tt)
	}
	img := tt.Image()
	out := image.NewRGBA(img.Bounds())
	copy(out.Pix, img.Pix)
	return out
}

// TestSegmentedControlMatchesTheOfficialFace pins segmentedControl to
// ui.Segmented pixel for pixel — track, chosen chip, hover, text, and the ring
// on the segment the keyboard stands on — natural height and stretched, light
// and dark: the control is MyGo's, only its name is Solco's.
func TestSegmentedControlMatchesTheOfficialFace(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, rowHeight := range []float32{0, 36} {
			for _, hover := range []bool{false, true} {
				for _, focused := range []bool{false, true} {
					want := segmentedImage(dark, true, rowHeight, hover, focused)
					got := segmentedImage(dark, false, rowHeight, hover, focused)
					differing, first := 0, image.Point{}
					for y := 0; y < want.Bounds().Dy(); y++ {
						for x := 0; x < want.Bounds().Dx(); x++ {
							i := want.PixOffset(x, y)
							a := [4]uint8{want.Pix[i], want.Pix[i+1], want.Pix[i+2], want.Pix[i+3]}
							b := [4]uint8{got.Pix[i], got.Pix[i+1], got.Pix[i+2], got.Pix[i+3]}
							if rgbDiff(a, b) > 3 {
								if differing == 0 {
									first = image.Pt(x, y)
								}
								differing++
							}
						}
					}
					if differing > 0 {
						t.Errorf("dark=%v rowHeight=%v hover=%v focused=%v: segmentedControl differs from ui.Segmented in %d pixels, first at %v", dark, rowHeight, hover, focused, differing, first)
					}
				}
			}
		}
	}
}

// TestSegmentedControlChoosesWhatIsClicked pins that the look-alike still
// works: a click chooses the segment, which is named by its text.
func TestSegmentedControlChoosesWhatIsClicked(t *testing.T) {
	chosen := 0
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(solcoTheme(false))
		ui.Column(c).FillWidth().Padding(24).Children(func() {
			segmentedControl(c, &chosen, "Alpha", "Beta", "Gamma").Label("Letters")
		})
	}, 420, 120)
	settle(tt)
	for want, label := range []string{"Alpha", "Beta", "Gamma"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		settle(tt)
		if chosen != want {
			t.Fatalf("clicking %s chose %d, want %d", label, chosen, want)
		}
	}
}

// ── one face ────────────────────────────────────────────────────────────

// rawControls are MyGo's constructors for the controls controls.go gives
// Solco's face. ui.NumberInput (a field welded to its steppers), Checkbox,
// Switch and Slider stay MyGo's own.
var rawControls = map[string]bool{
	"Button": true, "PrimaryButton": true, "TextInput": true, "TextArea": true,
	"SearchField": true, "Combobox": true, "Select": true, "Segmented": true,
}

// controlFaces are the Solco controls, whose look comes from controls.go.
var controlFaces = map[string]bool{
	"regularButton": true, "regularPrimaryButton": true, "compactButton": true, "compactPrimaryButton": true,
	"textField": true, "textArea": true, "searchField": true, "comboField": true, "selectField": true,
	"segmentedControl": true,
}

// faceMethods restate a control's look. A control takes its look from the
// face, so a call site that chains one is a second source for it.
var faceMethods = map[string]bool{
	"Radius": true, "Border": true, "BorderColor": true, "BorderWidth": true, "Background": true,
	"Shadow": true, "FocusRing": true, "DrawOver": true, "TextColor": true, "FontSize": true,
	"FontWeight": true, "Padding": true,
}

// controlFaceScan reads one source file for the places that build a control
// around controls.go: a raw MyGo constructor, or a Solco control whose look
// is restated in its own call chain. faces counts the Solco controls built.
func controlFaceScan(fset *token.FileSet, file *ast.File, name string) (raw, restated []string, faces int) {
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if fn, isIdent := call.Fun.(*ast.Ident); isIdent {
			if controlFaces[fn.Name] {
				faces++
			}
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		where := fset.Position(call.Pos())
		if pkg, isPkg := sel.X.(*ast.Ident); isPkg && pkg.Name == "ui" && rawControls[sel.Sel.Name] {
			raw = append(raw, fmt.Sprintf("%s: ui.%s", where, sel.Sel.Name))
			return true
		}
		if faceMethods[sel.Sel.Name] && controlFaces[callRoot(sel.X)] {
			if name == "ds_components.go" && sel.Sel.Name == "FontSize" {
				return true // the compact tiers are where a size is set
			}
			restated = append(restated, fmt.Sprintf("%s: %s(…).%s(…)", where, callRoot(sel.X), sel.Sel.Name))
		}
		return true
	})
	return raw, restated, faces
}

// TestControlsHaveOneFace pins the single source for control looks
// (2026-10-29): controls.go is the only file that names MyGo's button, input,
// select or segmented constructors, and no call site restates a radius,
// border, background, padding or ring on a control it got from there.
func TestControlsHaveOneFace(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	faces, owned := 0, 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		raw, restated, n := controlFaceScan(fset, file, name)
		if name == "controls.go" {
			owned = len(raw)
			continue
		}
		faces += n
		for _, r := range raw {
			t.Errorf("%s — a control built around controls.go; use regularButton / textField / searchField / selectField / comboField / segmentedControl", r)
		}
		for _, r := range restated {
			t.Errorf("%s — restates a control's look; the face in controls.go owns it", r)
		}
	}
	if faces < 100 {
		t.Fatalf("saw only %d Solco control calls; the scan no longer finds the call sites", faces)
	}
	if owned < len(rawControls)-1 { // every raw constructor but Segmented, which segmentedControl builds from its base
		t.Fatalf("controls.go names only %d raw constructors; the scan no longer sees the owner", owned)
	}
}

// TestControlFaceGuardSeesWhatItGuards proves the scan above by feeding it
// sources that break each rule: a guard that finds nothing proves nothing.
func TestControlFaceGuardSeesWhatItGuards(t *testing.T) {
	cases := []struct {
		name, src     string
		raw, restated int
	}{
		{"clean", `package x
func f(c *ui.Context) {
	regularButton(c, "Save").Width(80).OnClick(func() {}).Disabled(true)
	textField(c, &q).Label("Name").Placeholder("x")
}`, 0, 0},
		{"a raw button", `package x
func f(c *ui.Context) { ui.Button(c, "Save") }`, 1, 0},
		{"a raw segmented", `package x
func f(c *ui.Context) { ui.Segmented(c, &i, "a", "b") }`, 1, 0},
		{"a raw field dressed", `package x
func f(c *ui.Context) { ui.TextInput(c, &q).Radius(4) }`, 1, 0},
		{"a restated corner", `package x
func f(c *ui.Context) { regularButton(c, "Save").Radius(4) }`, 0, 1},
		{"a restated border, on its own line", `package x
func f(c *ui.Context) {
	textField(c, &q).
		Width(200).
		Border(2, ui.Transparent)
}`, 0, 1},
		{"a restated ring", `package x
func f(c *ui.Context) { segmentedControl(c, &i, "a", "b").FocusRing(true) }`, 0, 1},
		{"a restated background after a layout call", `package x
func f(c *ui.Context) { searchField(c, &q).FillWidth().Background(ui.Transparent) }`, 0, 1},
		{"NumberInput is MyGo's", `package x
func f(c *ui.Context) { ui.NumberInput(c, &n, 1, 9, 1).Label("Size") }`, 0, 0},
	}
	for _, tc := range cases {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "x.go", tc.src, 0)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		raw, restated, _ := controlFaceScan(fset, file, "x.go")
		if len(raw) != tc.raw || len(restated) != tc.restated {
			t.Errorf("%s: scan found %d raw / %d restated (%v / %v), want %d / %d", tc.name, len(raw), len(restated), raw, restated, tc.raw, tc.restated)
		}
	}
}
