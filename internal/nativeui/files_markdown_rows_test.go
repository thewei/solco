package nativeui

/**
 * [INPUT]: files_markdown_rows.go（planMarkdownRows、mdRows）、files_markdown.go（markdownBody 的一视图 / 分行两种排法）
 * [OUTPUT]: 2026-11-08 大文档分行回归：小文档仍是一个视图；分行后每个块恰出现一次且顺序不变；块间距与一个视图完全一致；引用定义只附给用到它的行；长列表/长代码块切段后编号与围栏仍对；脚注留在文末；锚点、rowAt 指对行；2 MiB 的稠密文档一帧只构建看得见的行
 * [POS]: 只验证分行的规划与接线；Markdown 本身的渲染由 MyGo ui/markdown 的测试覆盖
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// rowsFixture is a document with every kind of top-level block next to every
// other, each carrying a unique token to find it by (a formula, drawn as a
// picture, has no text: the blocks after it show where it ends).
func rowsFixture() (doc string, tokens []string) {
	var b strings.Builder
	add := func(token, block string) {
		tokens = append(tokens, token)
		b.WriteString(strings.ReplaceAll(block, "@", token) + "\n\n")
	}
	add("hdone", "# Heading @ one")
	add("ptwo", "A paragraph @ with **bold** words and a [link](https://example.com).")
	add("hsecond", "## Second @ level")
	add("hthird", "### Third @ level")
	add("pafter", "Paragraph @ after two headings.")
	add("tightl", "- tight @ one\n- tight two\n- tight three")
	add("loosel", "- loose @ one\n\n- loose two\n\n- loose three")
	add("orderl", "1. ordered @ one\n2. ordered two\n3. ordered three")
	add("nestedl", "- parent @\n  - child one\n  - child two\n- second parent")
	add("quoteq", "> quote @ line\n> second line")
	add("codec", "```go\nfunc @() {}\n```")
	add("tablet", "| name | size |\n|---|---|\n| @ | 1 |\n| beta | 2 |")
	add("rulepara", "---\n\nParagraph @ after a rule.")
	add("indentc", "    indented @ code")
	b.WriteString("$$\nx^2 + y^2\n$$\n\n") // drawn as a picture: no text to find it by, but the blocks after it sit where it leaves them
	add("htmlb", "<div>\n@ html\n</div>")
	add("beforerule", "Paragraph @ before a rule.")
	add("tworules", "---\n\n***\n\nParagraph @ between rules.")
	add("hfour", "#### Fourth @ level")
	add("hfive", "##### Fifth @ level")
	add("hlast", "# Last @ heading")
	add("plast", "Last paragraph @.")
	return b.String(), tokens
}

// renderRows lays the preview's body out in a tall window and returns the
// tester.
func renderRows(t *testing.T, s *Shell, mp *markdownPreview, height int) *ui.Tester {
	t.Helper()
	tester := ui.NewTester(func(c *ui.Context) { s.markdownBody(c, mp) }, 900, height)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	for range 3 {
		tester.Frame()
	}
	return tester
}

// findContaining is the box of the first text of the frame that holds token.
func findContaining(tester *ui.Tester, token string) (ui.Rect, bool) {
	for _, text := range tester.Texts() {
		if strings.Contains(text, token) {
			return tester.Find(text)
		}
	}
	return ui.Rect{}, false
}

func TestPlanMarkdownRowsLeavesSmallDocumentsAsOneView(t *testing.T) {
	doc, _ := rowsFixture()
	if got := planMarkdownRows(doc, false); got != nil {
		t.Fatalf("a small document must stay one view, got %d rows", len(got.rows))
	}
	if got := planMarkdownRows(doc, true); got == nil || len(got.rows) == 0 {
		t.Fatal("a threshold of 0 must cut any document into rows")
	}
	var big strings.Builder
	for i := range markdownRowsThreshold + 10 {
		fmt.Fprintf(&big, "paragraph %d\n\n", i)
	}
	if got := planMarkdownRows(big.String(), false); got == nil || len(got.rows) != markdownRowsThreshold+10 {
		t.Fatalf("a document past the threshold is a row per block, got %v", got)
	}
}

// TestPlanMarkdownRowsKeepsEveryBlockOnceAndSpacesThemAsOneView is the heart
// of the cut: shown as rows, the document reads the same and sits where the
// one view puts it, block by block.
func TestPlanMarkdownRowsKeepsEveryBlockOnceAndSpacesThemAsOneView(t *testing.T) {
	s, _, root := filesOpsTestShell(t)
	doc, tokens := rowsFixture()
	file := writePreviewFile(t, root, "fixture.md", []byte(doc))

	one := newMarkdownPreview(file, root, doc, nil)
	one.rows = nil
	rows := newMarkdownPreview(file, root, doc, nil)
	rows.rows = planMarkdownRows(doc, true)

	a := renderRows(t, s, one, 6000)
	b := renderRows(t, s, rows, 6000)
	for _, token := range tokens {
		ra, okA := findContaining(a, token)
		rb, okB := findContaining(b, token)
		if !okA || !okB {
			t.Fatalf("%q must show in both (one view %v, rows %v)", token, okA, okB)
		}
		if math.Abs(float64(ra.Y-rb.Y)) > 1 || math.Abs(float64(ra.X-rb.X)) > 1 {
			t.Errorf("%q sits at (%.1f, %.1f) as one view and (%.1f, %.1f) as rows", token, ra.X, ra.Y, rb.X, rb.Y)
		}
	}
	// Nothing twice: the same text, run by run.
	if ta, tb := a.Texts(), b.Texts(); strings.Join(ta, "\n") != strings.Join(tb, "\n") {
		t.Errorf("the rows read differently from the one view:\none view %q\nrows     %q", ta, tb)
	}
}

func TestPlanMarkdownRowsBringLinkDefinitionsOnlyToTheRowsThatUseThem(t *testing.T) {
	doc := "# Title\n\nSee [the guide][Guide] and [the site][site].\n\n[guide]: ./guide.md\n\nAn unrelated paragraph.\n\n" +
		"Later, [again][GUIDE] and a [missing][nope].\n\n[Site]: https://example.com \"Site\"\n\n[guide]: ./second-definition-loses.md\n\nEnd.\n"
	plan := planMarkdownRows(doc, true)
	if plan == nil {
		t.Fatal("no plan")
	}
	var uses, plain []string
	for _, row := range plan.rows {
		if strings.Contains(row.src, "]:") {
			uses = append(uses, row.src)
		} else {
			plain = append(plain, row.src)
		}
	}
	if len(uses) != 2 {
		t.Fatalf("exactly the two rows that make references carry definitions, got %d: %q", len(uses), uses)
	}
	first, second := uses[0], uses[1]
	for _, want := range []string{"[guide]: ./guide.md", `[Site]: https://example.com "Site"`} {
		if !strings.Contains(first, want) {
			t.Errorf("the first row needs %q: %q", want, first)
		}
	}
	if !strings.Contains(second, "[guide]: ./guide.md") || strings.Contains(second, "second-definition") || strings.Contains(second, "[nope]:") || strings.Contains(second, "[Site]:") {
		t.Errorf("the second row takes the first definition of a label, case-folded, and nothing for a label with none: %q", second)
	}
	for _, p := range plain {
		if strings.Contains(p, "guide.md") || strings.Contains(p, "example.com") {
			t.Errorf("a row that makes no reference must not carry a definition: %q", p)
		}
	}
}

func TestPlanMarkdownRowsCutLongListsAndCodeKeepingTheirMeaning(t *testing.T) {
	var b strings.Builder
	b.WriteString("Intro\n\n")
	for i := range 200 { // lazily numbered, as many people write them
		fmt.Fprintf(&b, "1. item%d\n", i)
	}
	b.WriteString("\n")
	for i := range 90 {
		fmt.Fprintf(&b, "- bullet%d\n", i)
	}
	b.WriteString("\n```go\n")
	for i := range 400 {
		fmt.Fprintf(&b, "line%d := %d\n", i, i)
	}
	b.WriteString("```\n\nOutro\n")
	plan := planMarkdownRows(b.String(), true)

	var ordered, bullets, code []mdRow
	for _, row := range plan.rows {
		switch {
		case row.kind == parser.List && strings.HasPrefix(strings.TrimSpace(row.src), "-"):
			bullets = append(bullets, row)
		case row.kind == parser.List:
			ordered = append(ordered, row)
		case row.kind == parser.CodeBlock:
			code = append(code, row)
		}
	}
	if len(ordered) != 5 || len(bullets) != 3 || len(code) != 5 {
		t.Fatalf("200 items cut by %d, 90 by %d, 400 lines by %d: got %d, %d, %d rows", mdListChunk, mdListChunk, mdCodeChunk, len(ordered), len(bullets), len(code))
	}
	for i, row := range ordered {
		want := fmt.Sprintf("%d. item%d", i*mdListChunk+1, i*mdListChunk)
		if !strings.HasPrefix(row.src, want) {
			t.Errorf("ordered row %d must go on numbering (want it to start %q): %.40q", i, want, row.src)
		}
		if row.list != ordered[0].list {
			t.Errorf("the rows of one list share its number: %d vs %d", row.list, ordered[0].list)
		}
	}
	if bullets[0].list == ordered[0].list {
		t.Error("two lists must not share a number")
	}
	// Every chunk of code is a whole fence, and together they are all of it.
	var lines []string
	for i, row := range code {
		if !strings.HasPrefix(row.src, "```go\n") || !strings.HasSuffix(row.src, "\n```") {
			t.Errorf("code row %d must be a whole fence: %.30q … %.12q", i, row.src, row.src[max(len(row.src)-12, 0):])
		}
		body := strings.TrimSuffix(strings.TrimPrefix(row.src, "```go\n"), "\n```")
		lines = append(lines, strings.Split(body, "\n")...)
	}
	if len(lines) != 400 || lines[0] != "line0 := 0" || lines[399] != "line399 := 399" {
		t.Errorf("the chunks of code hold all 400 lines in order, got %d (%q … %q)", len(lines), lines[0], lines[len(lines)-1])
	}
	// A hit in a later chunk is in a later row.
	if a, z := plan.rowAt(strings.Index(b.String(), "line10 :=")), plan.rowAt(strings.Index(b.String(), "line390 :=")); a >= z {
		t.Errorf("rowAt must tell the first chunk of code (%d) from the last (%d)", a, z)
	}
}

// A fence that is drawn as a picture is one thing: cut into chunks, a Mermaid
// diagram or a formula would be several pieces of source that mean nothing.
func TestPlanMarkdownRowsDoNotCutFencesThatAreDrawn(t *testing.T) {
	long := func(lang string) string {
		var b strings.Builder
		b.WriteString("```" + lang + "\n")
		for i := range 400 {
			fmt.Fprintf(&b, "  n%d --> n%d\n", i, i+1)
		}
		b.WriteString("```\n")
		return b.String()
	}
	count := func(lang string) int {
		n := 0
		for _, row := range planMarkdownRows(long(lang), true).rows {
			if row.kind == parser.CodeBlock {
				n++
			}
		}
		return n
	}
	if got := count("mermaid"); got != 1 {
		t.Errorf("a diagram of 400 lines is one row, got %d", got)
	}
	if got := count("math"); got != 1 {
		t.Errorf("a formula fence is one row, got %d", got)
	}
	if got := count("go"); got != 5 {
		t.Errorf("code of 400 lines is cut into %d rows of %d, got %d", 5, mdCodeChunk, got)
	}
}

func TestPlanMarkdownRowsSwitchOnByBytesToo(t *testing.T) {
	var b strings.Builder
	b.WriteString("```go\n")
	for i := 0; b.Len() < markdownRowsBytes+1<<10; i++ {
		fmt.Fprintf(&b, "func f%d() {}\n", i)
	}
	b.WriteString("```\n")
	plan := planMarkdownRows(b.String(), false)
	if plan == nil || len(plan.rows) < 2 {
		t.Fatalf("a document of one block but %d KiB of code shows as rows of it: %v", markdownRowsBytes>>10, plan)
	}
	if got := planMarkdownRows("```go\nfunc f() {}\n```\n", false); got != nil {
		t.Fatal("a short code block is one view")
	}
}

func TestPlanMarkdownRowsCutBigTablesKeepingTheHeader(t *testing.T) {
	var b strings.Builder
	b.WriteString("Before\n\n| a | b | c |\n|:--|--:|---|\n")
	for i := range 1700 { // 5100 cells
		fmt.Fprintf(&b, "| row%d | %d | text |\n", i, i)
	}
	b.WriteString("\nAfter\n")
	doc := b.String()
	plan := planMarkdownRows(doc, true)
	var tables []mdRow
	for _, row := range plan.rows {
		if row.kind == parser.Table {
			tables = append(tables, row)
		}
	}
	if want := (1700 + 332) / 333; len(tables) != want {
		t.Fatalf("1700 rows by %d is %d tables, got %d", mdTableChunk/3, want, len(tables))
	}
	next := 0
	for i, row := range tables {
		if !strings.HasPrefix(row.src, "| a | b | c |\n|:--|--:|---|\n| row") {
			t.Fatalf("table %d must carry the header and its alignment: %.60q", i, row.src)
		}
		for _, line := range strings.Split(row.src, "\n")[2:] {
			if want := fmt.Sprintf("| row%d | %d | text |", next, next); line != want {
				t.Fatalf("table %d: row %d reads %q, want %q", i, next, line, want)
			}
			next++
		}
		if row.table == 0 || row.table != tables[0].table {
			t.Errorf("the tables of one table share its number: %d", row.table)
		}
		if i > 0 && plan.top(indexOfRow(plan, row), markdownSpacing(markdown.DefaultTheme(ui.LightTheme()), 13)) != 0 {
			t.Errorf("the tables cut from one follow each other without room")
		}
	}
	if next != 1700 {
		t.Fatalf("every row once: %d", next)
	}
	// A hit in a late row is in a late table.
	if a, z := plan.rowAt(strings.Index(doc, "| row10 |")), plan.rowAt(strings.Index(doc, "| row1690 |")); a >= z {
		t.Errorf("rowAt must tell the first table (%d) from the last (%d)", a, z)
	}
	if got := planMarkdownRows("| a | b |\n|---|---|\n| 1 | 2 |\n", false); got != nil {
		t.Fatal("a small table is one view")
	}
}

func indexOfRow(plan *mdRows, row mdRow) int {
	for i := range plan.rows {
		if plan.rows[i].start == row.start {
			return i
		}
	}
	return -1
}

func TestPlanMarkdownRowsKeepIndentedCodeIndented(t *testing.T) {
	var b strings.Builder
	b.WriteString("Intro\n\n")
	for i := range 300 {
		fmt.Fprintf(&b, "    code%d\n", i)
	}
	b.WriteString("\nOutro\n")
	plan := planMarkdownRows(b.String(), true)
	n := 0
	for _, row := range plan.rows {
		if row.kind == parser.CodeBlock {
			n++
			for _, line := range strings.Split(row.src, "\n") {
				if !strings.HasPrefix(line, "    code") {
					t.Fatalf("every line of an indented block stays indented: %q", line)
				}
			}
		}
	}
	if n != 4 {
		t.Fatalf("300 lines by %d is 4 rows, got %d", mdCodeChunk, n)
	}
}

func TestPlanMarkdownRowsFootnotesStayAtTheEnd(t *testing.T) {
	doc := "# T\n\nA claim[^1] and another[^second].\n\n[^1]: First note.\n\nMiddle.\n\n[^second]: Second note.\n\nEnd.\n"
	plan := planMarkdownRows(doc, true)
	last := plan.rows[len(plan.rows)-1]
	if plan.notes != len(plan.rows)-1 || last.kind != parser.FootnoteDefinition ||
		!strings.Contains(last.src, "First note.") || !strings.Contains(last.src, "Second note.") {
		t.Fatalf("the footnotes are one last row: notes=%d row=%q", plan.notes, last.src)
	}
	for i, row := range plan.rows[:len(plan.rows)-1] {
		if strings.Contains(row.src, "note.") {
			t.Errorf("row %d must not hold a footnote: %q", i, row.src)
		}
	}
	if got := plan.rowAt(strings.Index(doc, "Second note")); got != plan.notes {
		t.Errorf("a byte in a footnote is in the footnotes' row, got %d", got)
	}
	if got := plan.rowAt(strings.Index(doc, "Middle")); got == plan.notes {
		t.Error("a byte between footnotes is not in the footnotes' row")
	}
}

func TestPlanMarkdownRowsAnchorsAndRowAt(t *testing.T) {
	doc, _ := rowsFixture()
	plan := planMarkdownRows(doc, true)
	for slug, token := range map[string]string{"heading-hdone-one": "Heading hdone one", "last-hlast-heading": "Last hlast heading"} {
		row, ok := plan.anchorRow("#" + slug)
		if !ok || !strings.Contains(plan.rows[row].src, token) {
			t.Errorf("#%s must name the row of %q, got %d %v", slug, token, row, ok)
		}
	}
	if _, ok := plan.anchorRow("#no-such-heading"); ok {
		t.Error("an unknown heading names no row")
	}
	if row, ok := plan.anchorRow("#HEADING-hdone-ONE"); !ok || row != 0 {
		t.Errorf("a link's case does not matter: %d %v", row, ok)
	}
	for i, row := range plan.rows {
		if got := plan.rowAt(row.start); got != i {
			t.Errorf("rowAt(start of row %d) = %d", i, got)
		}
		if got := plan.rowAt(row.end - 1); got != i {
			t.Errorf("rowAt(end of row %d) = %d", i, got)
		}
	}
}

// TestMarkdownTwoMiBShowsAsRowsAndFramesBuildOnlyWhatIsInView: the user's
// ceiling. A dense document of the cap's size is rows, and a frame costs what
// is on screen — the one view took ~250 ms a frame here.
func TestMarkdownTwoMiBShowsAsRowsAndFramesBuildOnlyWhatIsInView(t *testing.T) {
	var b strings.Builder
	for i := 0; b.Len() < markdownRenderCap-200; i++ {
		fmt.Fprintf(&b, "## Entry %d\n\nA **decision** about `thing%d` with a [link](https://example.com/%d) and more words to fill the line.\n\n- first point %d\n- second point\n\n", i, i, i, i)
	}
	doc := b.String()
	if len(doc) > markdownRenderCap || planPreviewTier("big.md", int64(len(doc))) != tierMarkdown {
		t.Fatalf("the fixture must be Markdown under the cap: %d bytes", len(doc))
	}
	s, _, root := filesOpsTestShell(t)
	file := writePreviewFile(t, root, "big.md", []byte(doc))
	planned := time.Now()
	mp := newMarkdownPreview(file, root, doc, nil)
	t.Logf("cutting %d KiB into rows took %v", len(doc)>>10, time.Since(planned))
	if mp.rows == nil || len(mp.rows.rows) < 10_000 {
		t.Fatalf("a 2 MiB document of many blocks shows as rows, got %v", mp.rows)
	}
	tester := ui.NewTester(func(c *ui.Context) { s.markdownBody(c, mp) }, 900, 800)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	tester.Frame()
	var worst time.Duration
	for range 5 {
		start := time.Now()
		tester.Frame()
		worst = max(worst, time.Since(start))
	}
	if !tester.HasText("Entry 0") {
		t.Fatalf("the top of the document must be on screen: %q", tester.Texts())
	}
	if tester.HasText("Entry 5000") {
		t.Fatal("a row far below must not be built")
	}
	t.Logf("worst of 5 frames: %v", worst)
	if worst > 60*time.Millisecond { // the one view: ~250 ms
		t.Fatalf("a frame of the 2 MiB document took %v: the rows are not saving the work", worst)
	}
}
