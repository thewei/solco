package nativeui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

/**
 * [INPUT]: 依赖 nativeui 包内非测试源码里 regularButton / regularPrimaryButton 调用链上的 .FontSize(…) 字面覆盖，以及图标按钮/下拉（gdIconButton/gorexIconButton/gorexIconButtonDisabled/gorexPulldown/gorexIconTab）调用链上的 .Size(…)
 * [OUTPUT]: 对外提供按钮尺寸回归：Button 的字号只来自 compactButton / compactPrimaryButton 两档（或默认档），图标按钮的尺寸只来自 gorexIconBtn/gorexPaneBtn/gorexRowBtn 三档，业务代码不在按钮上内联覆盖；图标按钮圆角只来自 iconButtonRadius（TestIconButtonCornerHasOneSource，28/24 档同为 Radius.Row）
 * [POS]: 2026-10-14 同组件同尺寸审计的守卫测试；Commit 两个主按钮曾各自内联 Body / BodySmall，与同排兄弟按钮高度不一
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// buttonFontOverride matches a regularButton or regularPrimaryButton that
// restates a font size in its own call chain.
var buttonFontOverride = regexp.MustCompile(`(?:regularButton|regularPrimaryButton)\(c[^\n]*\)\.FontSize\(`)

// TestButtonSizeComesFromTheTiers pins the shared-component rule for buttons:
// a Button is the regular tier by default and the compact tier through
// compactButton / compactPrimaryButton — never a size restated at the call
// site, which is how two buttons in one row ended up two heights.
func TestButtonSizeComesFromTheTiers(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	buttons := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "ds_components.go" {
			continue // the tiers themselves are where a size is set
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		buttons += strings.Count(string(src), "regularButton(c") + strings.Count(string(src), "regularPrimaryButton(c")
		for _, line := range strings.Split(string(src), "\n") {
			if buttonFontOverride.MatchString(line) {
				t.Errorf("%s: %q restates a button font size; use the default tier or compactButton / compactPrimaryButton", name, strings.TrimSpace(line))
			}
		}
	}
	if buttons < 30 {
		t.Fatalf("found only %d Button call sites; the scan no longer sees the sources", buttons)
	}
}

// iconButtonConstructors are the shared icon buttons; each takes its size as an
// argument (the gorexIconBtn / gorexPaneBtn tiers) or from its tier.
var iconButtonConstructors = map[string]bool{
	"gdIconButton": true, "gorexIconButton": true, "gorexIconButtonDisabled": true, "gorexPulldown": true, "gorexIconTab": true,
}

// iconButtonSizeArg is where each constructor takes its size: the button's
// edge for gorexIconButton and gorexIconButtonDisabled, the pull-down's
// height for gorexPulldown.
var iconButtonSizeArg = map[string]int{"gorexIconButton": 6, "gorexIconButtonDisabled": 5, "gorexPulldown": 4}

// iconButtonSizeRadius matches a corner worked out of a button's own size
// (size / 2.6, the old rule that made the 28 button 10.8 and the 24 one 9.2).
var iconButtonSizeRadius = regexp.MustCompile(`Radius\([^\n]*/ *2\.6\)`)

// TestIconButtonCornerHasOneSource pins the shadcn corner (2026-10-29): both
// icon tiers share the row radius, the 20 row tier stays proportionally round,
// and no icon button works its corner out by hand again — iconButtonRadius is
// where it comes from.
func TestIconButtonCornerHasOneSource(t *testing.T) {
	for _, size := range []float32{gorexIconBtn, gorexPaneBtn} {
		if got := iconButtonRadius(size); got != Radius().Row {
			t.Errorf("icon button of %.0f has corner %.2f, want the row radius %.0f", size, got, Radius().Row)
		}
	}
	if got := iconButtonRadius(gorexRowBtn); got <= 0 || got >= Radius().Row {
		t.Errorf("row-tier icon button has corner %.2f, want a smaller proportional round under %.0f", got, Radius().Row)
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(src), "\n") {
			if iconButtonSizeRadius.MatchString(line) {
				t.Errorf("%s: %q works an icon button's corner out by hand; use iconButtonRadius", name, strings.TrimSpace(line))
			}
		}
	}
}

// TestIconButtonSizeComesFromTheTiers is the same rule for icon buttons: a
// shared icon button is 28/16 (toolbar), 24/14 (dense) or 20/12 (inside a
// hovered row) by the size it is built with — never a .Size(w, h) chained on
// afterwards (how a Dismiss button became a third, 22-point size) and never a
// number typed at the call site (how row actions came in 18, 20 and 22).
func TestIconButtonSizeComesFromTheTiers(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	built := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if fn, isIdent := call.Fun.(*ast.Ident); isIdent && iconButtonConstructors[fn.Name] {
				built++
				if i, ok := iconButtonSizeArg[fn.Name]; ok && i < len(call.Args) {
					if lit, isLit := call.Args[i].(*ast.BasicLit); isLit {
						t.Errorf("%s: %s is built at the literal size %s; use a tier (gorexIconBtn / gorexPaneBtn / gorexRowBtn)", fset.Position(call.Pos()), fn.Name, lit.Value)
					}
				}
				return true
			}
			if sel, isSel := call.Fun.(*ast.SelectorExpr); isSel && sel.Sel.Name == "Size" {
				if root := callRoot(sel.X); iconButtonConstructors[root] {
					t.Errorf("%s: %s(...).Size(...) restates an icon button size; build it at a tier (gorexIconBtn / gorexPaneBtn / gorexRowBtn)", fset.Position(call.Pos()), root)
				}
			}
			return true
		})
	}
	if built < 30 {
		t.Fatalf("found only %d icon button call sites; the scan no longer sees the sources", built)
	}
}
