package nativeui

/**
 * [INPUT]: files_markdown_rich.go（typesetTeX、inlineMath/mathBlock、diagramOf/drawMermaid/mermaidBlock、drawnCode）、files_markdown.go
 * [OUTPUT]: 数学与 Mermaid 的回归：公式有界且带缓存、\def 炸弹被拒、行内公式成为文字里的对象、块级公式居中且排不出时留 TeX、Mermaid 后台画而不卡帧、画不出来给原因并留源码、随外观换色、敌意源码有界
 * [POS]: 用户 2026-11-08「Markdown 里的数学公式 / Mermaid 要支持」；文档里的 TeX 与 Mermaid 是不可信输入，上限与超时是这些测试守的契约
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"image"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// inkColumns returns the leftmost and rightmost columns, in rows [y0, y1), whose
// pixels differ from the page's (the corner pixel's).
func inkColumns(img *image.RGBA, y0, y1 int) (left, right int) {
	b := img.Bounds()
	left, right = b.Max.X, -1
	bg := img.Pix[0:3]
	for y := y0; y < y1 && y < b.Max.Y; y++ {
		for x := 0; x < b.Max.X; x++ {
			i := y*img.Stride + x*4
			if img.Pix[i] != bg[0] || img.Pix[i+1] != bg[1] || img.Pix[i+2] != bg[2] {
				left, right = min(left, x), max(right, x)
			}
		}
	}
	return left, right
}

func TestTypesetTeXDrawsFormulasAndRefusesWhatIsUnsafe(t *testing.T) {
	for _, c := range []struct {
		tex string
		ok  bool
	}{
		{`\frac{a}{b}`, true},
		{`x_i`, true},
		{`\sum_{i=1}^{n} i^2`, true},
		{`\begin{pmatrix} a & b \\ c & d \end{pmatrix}`, true},
		{`A \iff B \leftarrow C \left( x \right)`, true}, // \iff, \leftarrow, \left are not \if…, \let, \l…
		{`\def\a{b}\a`, false},
		{`\edef\a{b}\a`, false},
		{`\newcommand{\a}{b}\a`, false},
		{`\let\a=b`, false},
		{`\csname a\endcsname`, false},
		{`\frac{`, false}, // not TeX the engine reads
		{``, false},
		{"   \n", false},
		{strings.Repeat("x+", texCap/2+1), false}, // past the size bound
	} {
		f := typesetTeX(c.tex, 15, false)
		if (f != nil) != c.ok {
			t.Errorf("typesetTeX(%.40q) drawn = %v, want %v", c.tex, f != nil, c.ok)
			continue
		}
		if f != nil && (f.width <= 0 || f.height+f.depth <= 0 || f.svg == nil) {
			t.Errorf("typesetTeX(%q) = %+v: no box", c.tex, f)
		}
	}
}

// TestTypesetTeXBoundsHostileFormulas: the engine expands macros without limits
// of its own that matter (a few bytes of nested \def ask for 2^40 tokens), so a
// formula is refused before it gets there; what is left cannot take a frame.
func TestTypesetTeXBoundsHostileFormulas(t *testing.T) {
	bomb := `\def\d#1{#1#1}` + strings.Repeat(`\d{`, 40) + "x" + strings.Repeat("}", 40)
	deep := strings.Repeat(`\frac{`, 300) + "x" + strings.Repeat("}{y}", 300)
	wide := strings.Repeat(`\begin{pmatrix}a&b&c&d\\`, 1) + strings.Repeat("a&b&c&d\\\\", 150) + `\end{pmatrix}`
	for name, tex := range map[string]string{"macro bomb": bomb, "deep fractions": deep, "tall matrix": wide} {
		start := time.Now()
		typesetTeX(tex, 15, true)
		if took := time.Since(start); took > texBudget+500*time.Millisecond {
			t.Errorf("%s took %v: past the formula budget", name, took)
		}
	}
	if typesetTeX(bomb, 15, true) != nil {
		t.Error("the macro bomb must be refused outright")
	}
}

// TestTeXBoundsHoldTheirValues pins the formula bounds by number: the tests
// above state them through the constants, which a changed constant would
// carry along. A formula past 4 KiB is refused even when it is cheap to set
// (TeX ignores the spaces), and one wider than 4096 DIPs is refused although
// it is short and its drawing small (a \qquad is two ems of nothing).
func TestTeXBoundsHoldTheirValues(t *testing.T) {
	spaced := func(n int) string { return "x" + strings.Repeat(" ", n) + "+y" }
	if typesetTeX(spaced(4000), 15, false) == nil {
		t.Error("a formula just under 4 KiB is set")
	}
	if typesetTeX(spaced(4100), 15, false) != nil {
		t.Error("a formula over 4 KiB is refused, however cheap")
	}
	wide := func(n int) string { return "a" + strings.Repeat(`\qquad `, n) + "b" }
	if f := typesetTeX(wide(130), 15, false); f == nil || f.width > 4096 {
		t.Errorf("a formula just under 4096 DIPs wide is set: %+v", f)
	}
	if typesetTeX(wide(160), 15, false) != nil {
		t.Error("a formula over 4096 DIPs wide is refused")
	}
}

func TestTypesetTeXRemembersFormulasAndFailures(t *testing.T) {
	a, b := typesetTeX(`\frac{p}{q}`, 17, false), typesetTeX(`\frac{p}{q}`, 17, false)
	if a == nil || a != b {
		t.Fatalf("a formula is typeset once per size: %p %p", a, b)
	}
	if typesetTeX(`\frac{p}{q}`, 17, true) == a {
		t.Error("display style is another formula")
	}
	start := time.Now()
	for range 100 {
		if typesetTeX(`\def\z{z}\z`, 17, false) != nil {
			t.Fatal("refused formula drawn")
		}
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Errorf("a refused formula costs a look-up, not a retry: %v", time.Since(start))
	}
}

func TestInlineMathIsAnObjectOfTheTextAndFailureShowsTeX(t *testing.T) {
	s, _, root := filesOpsTestShell(t)
	mp := newMarkdownPreview(root+"/m.md", root, "Let $x_i$ be small, $\\def\\q{1}\\q$ stays text, and it costs $5 or $10.\n", nil)
	tester := renderRows(t, s, mp, 200)
	texts := strings.Join(tester.Texts(), "\n")
	if !strings.Contains(texts, "Let \uFFFC be small") {
		t.Errorf("x_i is an object of the text (U+FFFC): %q", texts)
	}
	if !strings.Contains(texts, "\\def\\q{1}\\q stays text") || !strings.Contains(texts, "costs $5 or $10") {
		t.Errorf("math the engine refuses shows its TeX, and money stays as typed: %q", texts)
	}
}

func TestDisplayMathIsCenteredAndAFenceIsMathToo(t *testing.T) {
	s, _, root := filesOpsTestShell(t)
	for name, doc := range map[string]string{
		"$$ block":   "$$\nx^2 + y^2 = z^2\n$$\n",
		"math fence": "```math\nx^2 + y^2 = z^2\n```\n",
	} {
		mp := newMarkdownPreview(root+"/m.md", root, doc, nil)
		tester := renderRows(t, s, mp, 200)
		if tester.HasText("x^2 + y^2 = z^2") {
			t.Errorf("%s: shows its TeX instead of the formula: %q", name, tester.Texts())
		}
		left, right := inkColumns(tester.Image(), 0, 200)
		if right < 0 {
			t.Fatalf("%s: nothing drawn", name)
		}
		if mid, view := float32(left+right)/2, float32(tester.Image().Bounds().Dx())/2; mid < view-6 || mid > view+6 {
			t.Errorf("%s: formula spans %d..%d, its middle %.0f is not the view's (%.0f)", name, left, right, mid, view)
		}
	}
}

func TestDisplayMathThatCannotBeTypesetShowsItsTeX(t *testing.T) {
	s, _, root := filesOpsTestShell(t)
	mp := newMarkdownPreview(root+"/m.md", root, "$$\n\\frac{\n$$\n", nil)
	tester := renderRows(t, s, mp, 200)
	if !tester.HasText("\\frac{") {
		t.Errorf("math the engine does not read shows as its TeX: %q", tester.Texts())
	}
}

func TestMathFollowsTheAppearanceOfTheText(t *testing.T) {
	s, _, root := filesOpsTestShell(t)
	for _, dark := range []bool{false, true} {
		mp := newMarkdownPreview(root+"/m.md", root, "$$\n\\int_0^1 x\\,dx\n$$\n", nil)
		tester := ui.NewTester(func(c *ui.Context) { s.markdownBody(c, mp) }, 760, 200)
		tester.SetPreferences(ui.Preferences{ReduceMotion: true})
		tester.SetDark(dark)
		for range 3 {
			tester.Frame()
		}
		img := tester.Image()
		bg := img.Pix[0]
		var ink, lum int
		for i := 0; i+3 < len(img.Pix); i += 4 {
			if d := int(img.Pix[i]) - int(bg); d > 60 || d < -60 {
				ink++
				lum += int(img.Pix[i])
			}
		}
		if ink < 100 {
			t.Fatalf("dark=%v: formula not drawn (%d ink pixels)", dark, ink)
		}
		if avg := lum / ink; dark && avg < 150 || !dark && avg > 100 {
			t.Errorf("dark=%v: ink averages %d on a background of %d: the formula must take the text color", dark, avg, bg)
		}
	}
}

// waitDiagram runs frames until the diagram of the preview has been drawn.
func waitDiagram(t *testing.T, tester *ui.Tester, token string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tester.Frame()
		if !tester.HasText(token) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("still %q after 10 s: %q", token, tester.Texts())
}

func resetMermaid() {
	mermaidCache.Lock()
	clear(mermaidCache.items)
	mermaidCache.Unlock()
}

func TestMermaidDrawsInTheBackgroundThenShowsThePicture(t *testing.T) {
	resetMermaid()
	s, _, root := filesOpsTestShell(t)
	doc := "before\n\n```mermaid\nflowchart LR\n  A[Start] --> B{Ok?}\n  B -->|yes| C[Done]\n```\n\nafter\n"
	mp := newMarkdownPreview(root+"/m.md", root, doc, nil)
	tester := ui.NewTester(func(c *ui.Context) { s.markdownBody(c, mp) }, 760, 600)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	tester.Frame()
	if !tester.HasText("Drawing diagram…") {
		t.Fatalf("the first frame must not wait for the drawing: %q", tester.Texts())
	}
	waitDiagram(t, tester, "Drawing diagram…")
	if tester.HasText("flowchart LR") || tester.HasText("A[Start] --> B{Ok?}") {
		t.Errorf("a drawn diagram shows no source: %q", tester.Texts())
	}
	if !tester.HasText("before") || !tester.HasText("after") {
		t.Errorf("the text around the diagram stays: %q", tester.Texts())
	}
}

func TestMermaidDiagramIsCenteredInTheView(t *testing.T) {
	resetMermaid()
	s, _, root := filesOpsTestShell(t)
	mp := newMarkdownPreview(root+"/m.md", root, "```mermaid\nflowchart LR\n  A[Start] --> B[Done]\n```\n", nil)
	tester := ui.NewTester(func(c *ui.Context) { s.markdownBody(c, mp) }, 900, 400)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	tester.Frame()
	waitDiagram(t, tester, "Drawing diagram…")
	left, right := inkColumns(tester.Image(), 0, 400)
	if right-left < 100 {
		t.Fatalf("the diagram is drawn, spanning %d columns", right-left)
	}
	if mid := float32(left+right) / 2; mid < 450-12 || mid > 450+12 {
		t.Errorf("the diagram spans %d..%d, its middle %.0f is not the view's (450)", left, right, mid)
	}
}

func TestMermaidThatCannotBeDrawnSaysWhyAndKeepsItsSource(t *testing.T) {
	resetMermaid()
	s, _, root := filesOpsTestShell(t)
	mp := newMarkdownPreview(root+"/m.md", root, "```mermaid\nthis is not a diagram\n```\n", nil)
	tester := ui.NewTester(func(c *ui.Context) { s.markdownBody(c, mp) }, 760, 400)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	tester.Frame()
	waitDiagram(t, tester, "Drawing diagram…")
	texts := strings.Join(tester.Texts(), "\n")
	if !strings.Contains(texts, "Mermaid: ") || !strings.Contains(texts, "this is not a diagram") {
		t.Errorf("a diagram that fails says why above its source: %q", texts)
	}
}

func TestMermaidFollowsTheAppearanceAndIsDrawnOncePerLook(t *testing.T) {
	resetMermaid()
	s, _, root := filesOpsTestShell(t)
	doc := "```mermaid\nflowchart LR\n  A --> B\n```\n"
	var shots [2]*image.RGBA
	for i, dark := range []bool{false, true} {
		mp := newMarkdownPreview(root+"/m.md", root, doc, nil)
		tester := ui.NewTester(func(c *ui.Context) { s.markdownBody(c, mp) }, 760, 300)
		tester.SetPreferences(ui.Preferences{ReduceMotion: true})
		tester.SetDark(dark)
		tester.Frame()
		waitDiagram(t, tester, "Drawing diagram…")
		shots[i] = tester.Image()
	}
	mermaidCache.Lock()
	n := len(mermaidCache.items)
	mermaidCache.Unlock()
	if n != 2 {
		t.Errorf("one drawing per appearance, got %d", n)
	}
	if shots[0].Pix[0] == shots[1].Pix[0] {
		t.Error("the two appearances drew the same page")
	}
	// The diagram's own ink must be readable on each: its darkest or lightest pixel.
	for i, dark := range []bool{false, true} {
		img := shots[i]
		bg := int(img.Pix[0])
		best := 0
		for p := 0; p+3 < len(img.Pix); p += 4 {
			d := int(img.Pix[p]) - bg
			if d < 0 {
				d = -d
			}
			best = max(best, d)
		}
		if best < 80 {
			t.Errorf("dark=%v: the diagram's strongest ink differs from the page by only %d", dark, best)
		}
	}
}

// TestMermaidIsBoundedForHostileSource: pele's limits are tightened and
// enforced before drawing, and what is drawn is bounded in pixels, so a few
// bytes of source cannot take the app.
func TestMermaidIsBoundedForHostileSource(t *testing.T) {
	th := ui.LightTheme()
	for name, src := range map[string]string{
		"source past the cap": "flowchart LR\n" + strings.Repeat("  A --> B\n", mermaidSource/10+10),
		"edge explosion":      "flowchart LR\n  " + strings.Repeat("A & B & C & D & E & F & G & H & I & J & K & L & M & N & O & P --> ", 3) + "Z & Y & X & W & V & U & T & S & R & Q & P2 & O2 & N2 & M2 & L2 & K2\n",
		"not a diagram":       "<script>alert(1)</script>",
	} {
		start := time.Now()
		bmp, _, _, why := drawMermaid(src, th)
		if bmp != nil || why == "" {
			t.Errorf("%s: drawn (%v) with reason %q", name, bmp != nil, why)
		}
		if took := time.Since(start); took > 3*time.Second {
			t.Errorf("%s: took %v to refuse", name, took)
		}
	}
	// Links and images in a diagram are not followed or drawn: the picture carries none.
	bmp, w, h, why := drawMermaid("flowchart LR\n  A[\"<img src=x onerror=alert(1)>\"] --> B\n  click A \"https://example.com\"\n", th)
	if why != "" || bmp == nil || w <= 0 || h <= 0 {
		t.Errorf("a diagram with a click link still draws: %q", why)
	}
}

// TestMermaidBoundsHoldTheirValues pins the diagram bounds by number, each on
// its own: the test above states the source bound through the constant and
// trips the edge bound first, so neither would notice the other moving. A
// comment is source that draws nothing, so the source bound is the only one in
// play; the same line repeated is cheap source with too many edges.
func TestMermaidBoundsHoldTheirValues(t *testing.T) {
	th := ui.LightTheme()
	comment := func(n int) string { return "flowchart LR\n%% " + strings.Repeat("x", n) + "\n  A --> B\n" }
	if bmp, _, _, why := drawMermaid(comment(19_000), th); bmp == nil {
		t.Errorf("a diagram under 20,000 characters is drawn: %q", why)
	}
	if bmp, _, _, why := drawMermaid(comment(21_000), th); bmp != nil || why == "" {
		t.Errorf("a diagram over 20,000 characters is refused, whatever it holds: drawn=%v why=%q", bmp != nil, why)
	}
	if bmp, _, _, why := drawMermaid("flowchart LR\n"+strings.Repeat("  A --> B\n", 700), th); bmp != nil || why == "" {
		t.Errorf("a flowchart of more than 600 edges is refused: drawn=%v why=%q", bmp != nil, why)
	}
}

func TestDrawnCodeIsNotSearchable(t *testing.T) {
	for lang, want := range map[string]bool{"mermaid": true, "Mermaid": true, "math": true, "go": false, "": false, "latex": false} {
		if got := drawnCode(lang); got != want {
			t.Errorf("drawnCode(%q) = %v, want %v", lang, got, want)
		}
	}
	idx := buildFindIndex("```math\nfrac needle\n```\n\n```mermaid\nneedle --> b\n```\n\n```go\nneedle()\n```\n\nneedle\n")
	if hits := idx.search("needle"); len(hits) != 2 {
		t.Errorf("only the Go code and the paragraph hold text to find in: %d hits", len(hits))
	}
}
