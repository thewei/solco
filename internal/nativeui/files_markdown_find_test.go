package nativeui

/**
 * [INPUT]: files_markdown_find.go（buildFindIndex、markMatches、markdownPreview.findFrame/rowSource）、files_markdown.go 的 markdownBody
 * [OUTPUT]: 2026-11-08 渲染视图查找回归：只数看得见的文字、命中只包可精确定位的文字、⌘F 开条、输入即搜、Enter/⇧Enter 步进并把命中行带到眼前、Esc 关条并清掉所有标记、小文档第一次命中时改按行显示且版式不变、<mark> 真的画出底色
 * [POS]: 只验证查找的接线与标记变换；查找条本身由 MyGo ui.FindBar 的测试覆盖
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"fmt"
	"image"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
)

func TestFindIndexCountsOnlyWhatShows(t *testing.T) {
	doc := "# The Needle heading\n\n" +
		"A paragraph with a needle, a [needle link](https://needle.example/needle) and `needle()` code.\n\n" +
		"![needle alt](pic.png)\n\n" +
		"| head | needle |\n|---|---|\n| cell needle | x |\n\n" +
		"```go\nneedle := 1\nother := 2\nneedle++\n```\n\n" +
		"```mermaid\ngraph TD\n  needle --> b\n```\n\n" +
		"<div>needle in html</div>\n\n" +
		"$$\nneedle^2\n$$\n\n" +
		"<https://needle.dev>\n"
	hits := buildFindIndex(doc).search("needle")
	// heading 1, paragraph 3 (word, link text, code span), table 2, code 2
	// lines, and the address an autolink shows as its text; not the image's
	// description, the diagram, the HTML, the math, nor a link's address.
	if len(hits) != 9 {
		t.Fatalf("hits = %d, want the 9 that show as text (heading, paragraph ×3, table ×2, code ×2, autolink); %v", len(hits), hits)
	}
	for i := 1; i < len(hits); i++ {
		if hits[i].at < hits[i-1].at {
			t.Fatalf("hits must be in document order: %v", hits)
		}
	}
	if got := buildFindIndex(doc).search("NEEDLE"); len(got) != 0 {
		t.Fatalf("search takes the lower-cased query it is given, got %d for upper case", len(got))
	}
	if got := buildFindIndex(doc).search(""); got != nil {
		t.Fatal("an empty query finds nothing")
	}
	// A letter in a long document must not hold a hit for each of them.
	many := strings.Repeat("a a a a a a a a a a\n\n", mdFindCap/5)
	if got := buildFindIndex(many).search("a"); len(got) != mdFindCap {
		t.Fatalf("hits are capped at %d, got %d", mdFindCap, len(got))
	}
}

func TestMarkMatchesWrapsWhatItCanPlace(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		emph      int
		want      string
	}{
		{"plain, case kept", "a Needle b needle", -1, "a <mark>Needle</mark> b <mark>needle</mark>"},
		{"current among two", "a Needle b needle", 1, "a <mark>Needle</mark> b <mark><u>needle</u></mark>"},
		{"adjacent", "needleneedle", 0, "<mark><u>needle</u></mark><mark>needle</mark>"},
		{"in bold", "**needle** here", -1, "**<mark>needle</mark>** here"},
		{"in a link's text, not its address", "[the needle](https://needle.example)", -1, "[the <mark>needle</mark>](https://needle.example)"},
		{"a code span as a whole", "call `needle()` now", -1, "call <mark>`needle()`</mark> now"},
		{"not an image's description", "![needle](pic.png)", -1, "![needle](pic.png)"},
		{"not an autolink", "<https://needle.dev>", -1, "<https://needle.dev>"},
		{"heading", "# A needle", -1, "# A <mark>needle</mark>"},
		{"table cells", "| a | needle |\n|---|---|\n| needle | b |", -1, "| a | <mark>needle</mark> |\n|---|---|\n| <mark>needle</mark> | b |"},
		{"no match", "nothing here", -1, "nothing here"},
	} {
		if got, _ := markMatches(tc.src, "needle", tc.emph); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
	// Source the parser does not read as the text itself is left as it was:
	// the marks would land in the wrong place.
	if got, _ := markMatches(`a needle\* b`, "needle", -1); strings.Contains(got, "<mark>") && !strings.Contains(got, `<mark>needle</mark>\*`) {
		t.Errorf("an escape beside a match must not misplace the mark: %q", got)
	}
}

// TestMarkIsPaintedAsABackground: the premise of the whole approach is that
// the view draws <mark> as a background behind the text; pin it in pixels.
func TestMarkIsPaintedAsABackground(t *testing.T) {
	s, _, root := filesOpsTestShell(t)
	paint := func(src string) int {
		mp := newMarkdownPreview(root+"/a.md", root, src, nil)
		return markPixels(renderRows(t, s, mp, 200).Image())
	}
	plain, marked := paint("a needle here\n"), paint("a <mark>needle</mark> here\n")
	if marked < plain+150 {
		t.Fatalf("<mark> must paint a yellow background: %d vs %d pixels", marked, plain)
	}
}

// markPixels counts the pixels painted in the theme's mark yellow over white
// paper (about (253, 237, 173)).
func markPixels(img *image.RGBA) int {
	n := 0
	for i := 0; i+3 < len(img.Pix); i += 4 {
		if r, g, b := img.Pix[i], img.Pix[i+1], img.Pix[i+2]; r > 240 && g > 220 && b < 200 && r-b > 50 {
			n++
		}
	}
	return n
}

func findDoc() (doc string, rowsOf map[string]string) {
	var b strings.Builder
	for i := range 3000 {
		switch i {
		case 100, 1500, 2900:
			fmt.Fprintf(&b, "paragraph %d needle here\n\n", i)
		default:
			fmt.Fprintf(&b, "paragraph %d text\n\n", i)
		}
	}
	return b.String(), nil
}

func TestFindBarSearchesStepsAndScrolls(t *testing.T) {
	s, _, root := filesOpsTestShell(t)
	doc, _ := findDoc()
	mp := newMarkdownPreview(root+"/long.md", root, doc, nil)
	if mp.rows == nil {
		t.Fatal("the document must show as rows")
	}
	tester := ui.NewTester(func(c *ui.Context) { s.markdownBody(c, mp) }, 900, 600)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	for range 2 {
		tester.Frame()
	}
	f := &mp.find
	if f.open {
		t.Fatal("the bar starts closed")
	}

	tester.Key(ui.Cmd, ui.KeyF)
	tester.Frame()
	if !f.open {
		t.Fatal("⌘F must open the find bar")
	}
	tester.Type("needle")
	tester.Frame()
	tester.Frame()
	if len(f.hits) != 3 || f.current != 0 {
		t.Fatalf("three hits, the first current: hits=%d current=%d", len(f.hits), f.current)
	}
	if !tester.HasText("paragraph 100 needle here") || tester.HasText("paragraph 0 text") {
		t.Fatalf("the first hit's row must be brought into view: %.200q", tester.Texts())
	}
	if src := mp.rowSource(mp.find.rowOf[0]); !strings.Contains(src, "<mark><u>needle</u></mark>") {
		t.Fatalf("the current hit is marked and underlined: %q", src)
	}

	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	tester.Frame()
	if f.current != 1 || !tester.HasText("paragraph 1500 needle here") || tester.HasText("paragraph 100 needle here") {
		t.Fatalf("Enter goes to the next hit and shows it: current=%d texts=%.200q", f.current, tester.Texts())
	}
	if src := mp.rowSource(mp.find.rowOf[0]); strings.Contains(src, "<u>") || !strings.Contains(src, "<mark>needle</mark>") {
		t.Fatalf("a hit that is no longer current keeps its mark but not the underline: %q", src)
	}
	tester.Key(ui.Shift, ui.KeyEnter)
	tester.Frame()
	tester.Frame()
	if f.current != 0 || !tester.HasText("paragraph 100 needle here") {
		t.Fatalf("Shift+Enter goes back: current=%d", f.current)
	}
	tester.Key(ui.Shift, ui.KeyEnter) // round the end
	tester.Frame()
	tester.Frame()
	if f.current != 2 || !tester.HasText("paragraph 2900 needle here") {
		t.Fatalf("the hits go round: current=%d", f.current)
	}

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if f.open || f.hits != nil || len(f.marked) != 0 {
		t.Fatalf("Escape closes the bar and clears every mark: open=%v hits=%d marked=%d", f.open, len(f.hits), len(f.marked))
	}
	if src := mp.rowSource(mp.rows.rowAt(strings.Index(doc, "paragraph 2900"))); strings.Contains(src, "<mark>") {
		t.Fatalf("a closed bar leaves no mark in any row: %q", src)
	}
}

// TestFindFlipsASmallDocumentToRowsWithoutMovingIt: a small document is one
// view, which cannot be scrolled to a hit; the first hit shows it as rows,
// which sit where the one view had them.
func TestFindFlipsASmallDocumentToRowsWithoutMovingIt(t *testing.T) {
	s, _, root := filesOpsTestShell(t)
	doc, tokens := rowsFixture()
	mp := newMarkdownPreview(root+"/small.md", root, doc, nil)
	if mp.rows != nil {
		t.Fatal("the fixture is small: one view")
	}
	tester := ui.NewTester(func(c *ui.Context) { s.markdownBody(c, mp) }, 900, 6000)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	for range 3 {
		tester.Frame()
	}
	before := map[string]ui.Rect{}
	for _, tok := range tokens {
		r, ok := findContaining(tester, tok)
		if !ok {
			t.Fatalf("%q missing before the search", tok)
		}
		before[tok] = r
	}
	mp.find.open, mp.find.query = true, "paragraph"
	for range 3 {
		tester.Frame()
	}
	if mp.rows == nil || len(mp.find.hits) == 0 {
		t.Fatalf("a hit must show the document as rows: rows=%v hits=%d", mp.rows != nil, len(mp.find.hits))
	}
	// The find bar takes a strip above the document; the rows sit as before
	// under it.
	var shift float32
	for _, tok := range tokens {
		r, ok := findContaining(tester, tok)
		if !ok {
			t.Fatalf("%q missing after the flip", tok)
		}
		d := r.Y - before[tok].Y
		if shift == 0 {
			shift = d
		}
		if d-shift > 1 || shift-d > 1 || r.X != before[tok].X {
			t.Errorf("%q moved by %.1f (the bar's own %.1f)", tok, d, shift)
		}
	}
	mp.find.open = false
	tester.Frame()
	if mp.rows == nil {
		t.Fatal("closing the bar must not flip the view back: the document would jump")
	}
}

func TestMarkMatchesNumbersCodeHitsAmongTheRowsOwn(t *testing.T) {
	src := "a needle\n\n```\nneedle\nneedle\n```\n\n```mermaid\nneedle --> b\n```\n\nb needle\n"
	// Hits in order: paragraph 0, code 1 and 2, (the diagram has none), paragraph 3.
	out, bases := markMatches(src, "needle", 3)
	if len(bases) != 1 || bases[0] != 1 {
		t.Fatalf("the code block's hits begin at the row's 2nd (number 1), the diagram has none: %v", bases)
	}
	if !strings.Contains(out, "b <mark><u>needle</u></mark>") || !strings.Contains(out, "a <mark>needle</mark>") {
		t.Fatalf("the last paragraph's hit is the current one: %q", out)
	}
	if strings.Contains(out, "```\n<mark>") {
		t.Fatalf("a code block's lines carry no tags: %q", out)
	}
}

func TestMarkLineCutsTokensAtTheMatchAndUnderlinesTheCurrent(t *testing.T) {
	th := markdown.DefaultTheme(ui.LightTheme())
	ct := codeEditorTheme(ui.LightTheme())
	line := `needle := "a Needle"`
	l := editor.LanguageNamed("go")
	if l == nil {
		t.Skip("no Go tokenizer")
	}
	toks, _ := l.Tokenizer.Tokenize(line, 0, nil)
	hit := 0
	spans := markLine(line, toks, ct, "needle", th, &hit, 1)
	var text strings.Builder
	var marked []string
	for _, sp := range spans {
		text.WriteString(sp.Text)
		if sp.Background == th.Mark {
			marked = append(marked, sp.Text)
			if sp.Underline != (sp.Text == "Needle") {
				t.Errorf("only the current hit (number 1, the second) is underlined: %q underline=%v", sp.Text, sp.Underline)
			}
		}
	}
	if text.String() != line {
		t.Fatalf("the spans must read as the line: %q", text.String())
	}
	if strings.Join(marked, "|") != "needle|Needle" || hit != 2 {
		t.Fatalf("both matches are marked, case kept: %q, %d hits", marked, hit)
	}
	// Token colors survive the cut: every piece of the string is its color,
	// which is not the code's.
	var inString, outside []ui.Color
	at, quote := 0, strings.Index(line, `"`)
	for _, sp := range spans {
		if at >= quote {
			inString = append(inString, sp.Color)
		} else if strings.TrimSpace(sp.Text) != "" {
			outside = append(outside, sp.Color)
		}
		at += len(sp.Text)
	}
	for _, c := range inString {
		if c != inString[0] {
			t.Fatalf("the pieces of a string keep its color: %v", inString)
		}
	}
	if len(inString) < 3 || len(outside) == 0 || inString[0] == outside[0] {
		t.Errorf("the string has its own color, cut in at least three pieces: string %v, code %v", inString, outside)
	}
}

// TestCodeBlockUnderlinesOnlyTheCurrentHit pins the wiring from the find bar's
// current hit to the code block that draws its own marks: whichever hit is
// current, the underline is under that one and no other, in a paragraph or in
// either line of a code block (the real window once underlined the second hit
// of a block while the first paragraph's was current).
func TestCodeBlockUnderlinesOnlyTheCurrentHit(t *testing.T) {
	s, _, root := filesOpsTestShell(t)
	doc := "a needle line\n\n```\nneedle one\nneedle two\n```\n\nb needle line\n"
	const hits = 4
	var imgs [hits]*image.RGBA
	var rects map[string]ui.Rect
	for current := range hits {
		mp := newMarkdownPreview(root+"/u.md", root, doc, nil)
		mp.find.open, mp.find.query = true, "needle"
		tester := ui.NewTester(func(c *ui.Context) { s.markdownBody(c, mp) }, 900, 500)
		tester.SetPreferences(ui.Preferences{ReduceMotion: true})
		for range 3 {
			tester.Frame()
		}
		if len(mp.find.hits) != hits {
			t.Fatalf("four hits in the document, got %d", len(mp.find.hits))
		}
		mp.find.current = current
		for range 2 {
			tester.Frame()
		}
		imgs[current] = tester.Image()
		rects = map[string]ui.Rect{}
		for key, token := range map[string]string{"first": "a needle line", "code": "needle one", "last": "b needle line"} {
			r, ok := findContaining(tester, token)
			if !ok {
				t.Fatalf("%q missing: %q", token, tester.Texts())
			}
			rects[key] = r
		}
	}
	codeLine := rects["code"].H / 2 // two lines in one run
	bands := [hits]struct {
		name     string
		from, to float32
	}{
		{"first paragraph", rects["first"].Y, rects["first"].Y + rects["first"].H},
		{"code line one", rects["code"].Y, rects["code"].Y + codeLine},
		{"code line two", rects["code"].Y + codeLine, rects["code"].Y + 2*codeLine},
		{"last paragraph", rects["last"].Y, rects["last"].Y + rects["last"].H},
	}
	// The underline of hit k is what only image k has: a pixel that differs
	// from the same pixel in every other image. The bar above the page counts
	// "1 of 4", "2 of 4"…, which differs too, so the page is read from its
	// first line down.
	top := int(rects["first"].Y) - 6
	for k, band := range bands {
		minY, maxY, n := imgs[k].Bounds().Max.Y, -1, 0
		for y := top; y < imgs[k].Bounds().Max.Y; y++ {
			for x := 0; x < imgs[k].Bounds().Max.X; x++ {
				i := y*imgs[k].Stride + x*4
				only := true
				for j := range hits {
					if j != k && imgs[j].Pix[i] == imgs[k].Pix[i] && imgs[j].Pix[i+1] == imgs[k].Pix[i+1] && imgs[j].Pix[i+2] == imgs[k].Pix[i+2] {
						only = false
						break
					}
				}
				if only {
					minY, maxY, n = min(minY, y), max(maxY, y), n+1
				}
			}
		}
		if n == 0 {
			t.Errorf("current hit %d (%s): no underline painted", k, band.name)
			continue
		}
		if float32(minY) < band.from-3 || float32(maxY) > band.to+3 {
			t.Errorf("current hit %d (%s): underline pixels span rows %d..%d, outside the hit's line %.0f..%.0f", k, band.name, minY, maxY, band.from, band.to)
		}
	}
}

// TestMarkedCodeBlockLooksLikeTheViewsOwn pins markedCode against
// DefaultCodeBlock: a block that gains marks must not move by a pixel — the
// text of the lines, the header and what comes after stay where they were.
func TestMarkedCodeBlockLooksLikeTheViewsOwn(t *testing.T) {
	s, _, root := filesOpsTestShell(t)
	doc := "intro paragraph\n\n```go\nfunc alpha() {}\nfunc beta() {}\nfunc gamma() {}\n```\n\nafter paragraph\n"
	var painted int
	probe := func(query string) map[string]ui.Rect {
		mp := newMarkdownPreview(root+"/c.md", root, doc, nil)
		mp.find.open, mp.find.query = query != "", query
		tester := ui.NewTester(func(c *ui.Context) { s.markdownBody(c, mp) }, 900, 800)
		tester.SetPreferences(ui.Preferences{ReduceMotion: true})
		for range 3 {
			tester.Frame()
		}
		painted = markPixels(tester.Image())
		out := map[string]ui.Rect{}
		for key, token := range map[string]string{"lines": "alpha", "after": "after paragraph", "intro": "intro paragraph"} {
			r, ok := findContaining(tester, token)
			if !ok {
				t.Fatalf("query %q: %q missing: %q", query, token, tester.Texts())
			}
			out[key] = r
		}
		return out
	}
	plain := probe("")
	if painted != 0 {
		t.Fatalf("without a query nothing is marked: %d pixels", painted)
	}
	marked := probe("beta")
	if painted < 100 {
		t.Fatalf("the hit in the code block must be drawn on a background: %d mark pixels", painted)
	}
	// The find bar moves everything by the same amount; compare within the page.
	shift := marked["intro"].Y - plain["intro"].Y
	for key, p := range plain {
		m := marked[key]
		near := func(a, b float32) bool { return a-b < 0.5 && b-a < 0.5 }
		if !near(m.X, p.X) || !near(m.W, p.W) || !near(m.H, p.H) || !near(m.Y-shift, p.Y) {
			t.Errorf("%s: plain %+v, with marks %+v (find bar shift %.1f): the marked block must lay out as the view's own", key, p, m, shift)
		}
	}
}
