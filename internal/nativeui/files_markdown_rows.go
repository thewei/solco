package nativeui

/**
 * [INPUT]: MyGo ui/markdown/parser（块与偏移）、markdown.Slug、markdownExtensions
 * [OUTPUT]: planMarkdownRows（把大文档按顶层块切成行：mdRows/mdRow；列表过长再按条目切段、过长的代码块按行数切段）、mdRows.top（相邻两行的间距，与 ui/markdown 自己排版块间距的规则一致）、mdRows.rowAt（源码偏移 → 行号，查找用）、markdownSpacing（间距所需的主题数值）
 * [POS]: 产品决策 2026-11-08「Files 预览原生优先」之后续（用户：Markdown 渲染上限放到 2 MiB）：ui/markdown 的一帧要构建全部块（官方文档：长文档请一次显示一节），2 MiB 的稠密文档整体渲染每帧约 250 ms；按顶层块切成 ui.List 的行，只构建可见的行，每帧不到 2 ms。链接引用定义（落在块与块之间的空隙里）按行附上被引用的那几条；锚点用「标题 slug → 行」表
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// markdownExtensions is what the preview's parser reads: GitHub's Markdown
// with footnotes and math (the view's own default) and TeX between single
// dollars, which the parser keeps off amounts of money ("$5 and $10").
const markdownExtensions = parser.Default | parser.ExtDollarMath

const (
	// A document shows as rows from markdownRowsThreshold blocks or
	// markdownRowsBytes of text. The view builds every block each frame
	// (about 2.7 ms per thousand blocks, 34 ms for 2 MiB of code), so a
	// document under both is one view — selectable whole, as the view keeps
	// it — and a larger one is a virtual list of its blocks.
	markdownRowsThreshold = 1500
	markdownRowsBytes     = 128 << 10

	// A list longer than mdListSplit items is cut into rows of mdListChunk
	// items, a code block longer than mdCodeSplit lines into rows of
	// mdCodeChunk lines, a table of more than mdTableSplit cells into tables
	// of mdTableChunk cells' rows (each with the header again): a block is the
	// smallest thing a row can skip, so a whole 2 MiB log in one fence would
	// cost what the whole document did. A fence drawn as a picture (a Mermaid
	// diagram, math) is one thing and is not cut.
	mdListSplit, mdListChunk   = 80, 40
	mdCodeSplit, mdCodeChunk   = 160, 80
	mdTableSplit, mdTableChunk = 4000, 1000

	// Past these a row is more than a frame can carry, and the document is not
	// formatted at all (it reads as source): a block holding more than
	// mdBlockCap blocks of its own (a quote of 30,000 paragraphs: ~0.4 s a
	// frame), a text of more than mdLeafCap bytes in one paragraph (above 1 MiB
	// the system's text engine does not return in minutes).
	mdBlockCap = 6000
	mdLeafCap  = 512 << 10
)

// mdRow is one row of a document shown as rows: a top-level block, or a run
// of a long list's items or of a long code block's lines.
type mdRow struct {
	src        string // what the row's view shows: its text, plus the link definitions it uses
	start, end int    // the row's text in the document, definitions not included
	kind       parser.Kind
	level      int  // a heading's
	list       int  // the rows cut from one list share its number (1-based); 0 for the others
	tight      bool // that list's
	table      int  // likewise for the rows cut from one table
}

// mdRows is a document cut into rows.
type mdRows struct {
	rows    []mdRow
	anchors map[string]int // heading slug → the row of the first heading of it
	// The view puts the footnotes of a document at its end, below a rule: so
	// do the rows, in one last row, whose spans in the text are noteSpans.
	notes     int      // that row; -1 when there is none
	noteSpans [][2]int // in document order
	// heavy: a block of the document is more than a frame can carry (mdBlockCap,
	// mdLeafCap), so the formatted view is not offered.
	heavy bool
}

// defLine is the first line of a link reference definition: [label]: …
var defLine = regexp.MustCompile(`^ {0,3}\[([^\]\n]+)\]:`)

// planMarkdownRows cuts src into rows, or returns nil when it is small enough
// (blocks and bytes) to show as one view and force is not set.
func planMarkdownRows(src string, force bool) *mdRows {
	d := parser.New(markdownExtensions)
	d.Set(src)
	if !force && d.Len() <= markdownRowsThreshold && len(src) <= markdownRowsBytes {
		return nil
	}
	out := &mdRows{anchors: map[string]int{}, notes: -1}
	defs := map[string]string{}
	var notes []string
	prev := 0 // the end of the text taken so far
	gap := func(to int) {
		if to > prev {
			collectDefinitions(src[prev:to], defs)
		}
	}
	lists, tables := 0, 0
	for i := 1; i < d.Len(); i++ { // a text no engine lays out in a frame
		switch b := d.Block(i); b.Kind {
		case parser.Paragraph, parser.Heading, parser.TableCell:
			if b.End-b.Start > mdLeafCap {
				out.heavy = true
			}
		}
	}
	for j := range d.Children(0) {
		b := d.Block(j)
		start, end := lineStart(src, b.Start), lineEnd(src, b.End)
		gap(start) // link definitions are consumed by the parser and leave no block: they sit between blocks
		prev = end
		switch {
		case b.Kind == parser.FootnoteDefinition:
			notes = append(notes, src[start:end])
			out.noteSpans = append(out.noteSpans, [2]int{start, end})
		case b.Kind == parser.List && countChildren(d, j) > mdListSplit:
			lists++
			out.cutList(d, src, j, lists)
		case b.Kind == parser.CodeBlock && d.LineCount(j) > mdCodeSplit && !drawnCode(b.Language()):
			out.cutCode(d, src, j, start, end)
		case b.Kind == parser.Table && b.Next-j > mdTableSplit:
			tables++
			out.cutTable(d, src, j, start, end, tables)
		default:
			if b.Next-j > mdBlockCap {
				out.heavy = true
			}
			out.rows = append(out.rows, mdRow{src: src[start:end], start: start, end: end, kind: b.Kind, level: b.Level})
		}
		if b.Kind == parser.Heading {
			if slug := markdown.Slug(parser.PlainText(d.Inlines(j))); slug != "" {
				if _, seen := out.anchors[slug]; !seen {
					out.anchors[slug] = len(out.rows) - 1
				}
			}
		}
	}
	gap(len(src))
	if len(notes) > 0 {
		out.notes = len(out.rows)
		out.rows = append(out.rows, mdRow{src: strings.Join(notes, "\n\n"), start: len(src) + 1, end: len(src) + 1, kind: parser.FootnoteDefinition})
	}
	if len(defs) > 0 {
		for i := range out.rows {
			out.rows[i].src = withDefinitions(&out.rows[i], defs)
		}
	}
	return out
}

// cutList adds the rows of list j, mdListChunk items each. A row starts at
// the line of its first item, so indentation keeps its meaning; an ordered
// list goes on numbering where the row begins, as the view numbers it (from
// the first number, one more for each item, whatever the others say).
func (r *mdRows) cutList(d *parser.Document, src string, j, number int) {
	list := d.Block(j)
	var first, last, firstIdx, cost int
	n, idx := 0, 0
	flush := func() {
		if n == 0 {
			return
		}
		if cost > mdBlockCap {
			r.heavy = true
		}
		cost = 0
		fb := d.Block(first)
		start, end := lineStart(src, fb.Start), lineEnd(src, d.Block(last).End)
		text := src[start:end]
		if list.Ordered {
			text = renumberFirstItem(text, fb.Start-start, fb.End-start, strconv.Itoa(list.Number+firstIdx))
		}
		r.rows = append(r.rows, mdRow{src: text, start: start, end: end, kind: parser.List, list: number, tight: list.Tight})
		n = 0
	}
	for item := range d.Children(j) {
		if n == 0 {
			first, firstIdx = item, idx
		}
		last = item
		idx++
		cost += d.Block(item).Next - item
		if n++; n == mdListChunk {
			flush()
		}
	}
	flush()
}

// renumberFirstItem gives the first item of text, whose marker starts at
// marker and which ends at itemEnd, the number want. Where the number grows
// wider the item's own lines move right with it, to stay under their item.
func renumberFirstItem(text string, marker, itemEnd int, want string) string {
	digits := 0
	for marker+digits < len(text) && text[marker+digits] >= '0' && text[marker+digits] <= '9' {
		digits++
	}
	if digits == 0 || text[marker:marker+digits] == want {
		return text
	}
	itemEnd = min(max(itemEnd, marker+digits), len(text))
	item := text[:marker] + want + text[marker+digits:itemEnd]
	if delta := len(want) - digits; delta > 0 {
		lines := strings.Split(item, "\n")
		for k := 1; k < len(lines); k++ {
			if strings.TrimSpace(lines[k]) != "" {
				lines[k] = strings.Repeat(" ", delta) + lines[k]
			}
		}
		item = strings.Join(lines, "\n")
	}
	return item + text[itemEnd:]
}

// cutCode adds the rows of code block j, mdCodeChunk lines each, each its
// own block: fenced as the original was, or indented as it was.
func (r *mdRows) cutCode(d *parser.Document, src string, j, start, end int) {
	b := d.Block(j)
	open, fence := "", ""
	if b.Fenced {
		first := src[start:lineEnd(src, start)]
		open = strings.TrimRight(first, "\r")
		fence = strings.TrimLeft(open, " ")
		fence = fence[:len(fence)-len(strings.TrimLeft(fence, fence[:1]))] // the run of ` or ~
	}
	lines := strings.SplitAfter(d.Lines(j), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for from := 0; from < len(lines); from += mdCodeChunk {
		to := min(from+mdCodeChunk, len(lines))
		var text strings.Builder
		if b.Fenced {
			text.WriteString(open + "\n")
		}
		for _, line := range lines[from:to] {
			if !b.Fenced {
				text.WriteString("    ")
			}
			text.WriteString(line)
		}
		if b.Fenced {
			text.WriteString(fence + "\n")
		}
		// A row's span is its own lines', so that a hit finds the row it is in.
		from0, to0 := start, end
		if from > 0 {
			_, at := d.Line(j, from)
			from0 = lineStart(src, at)
		}
		if to < len(lines) {
			_, at := d.Line(j, to-1)
			to0 = lineEnd(src, at)
		}
		r.rows = append(r.rows, mdRow{src: strings.TrimRight(text.String(), "\n"), start: from0, end: to0, kind: parser.CodeBlock})
	}
}

// cutTable adds the rows of table j: tables of about mdTableChunk cells, each
// with the header again so that it reads on its own. The rows follow each
// other without room between, as the lines of one table do; their columns are
// sized by their own cells, which a single table's are not.
func (r *mdRows) cutTable(d *parser.Document, src string, j, start, end, number int) {
	lines := strings.Split(src[start:end], "\n")
	cols := 1
	for row := range d.Children(j) {
		cols = max(countChildren(d, row), 1)
		break
	}
	if len(lines) < 3 { // a header alone: nothing to cut
		r.rows = append(r.rows, mdRow{src: src[start:end], start: start, end: end, kind: parser.Table})
		return
	}
	header := lines[0] + "\n" + lines[1] + "\n"
	offsets := make([]int, len(lines)) // where each line starts in the document
	for i, at := 0, start; i < len(lines); i++ {
		offsets[i] = at
		at += len(lines[i]) + 1
	}
	per := max(mdTableChunk/cols, 1)
	for from := 2; from < len(lines); from += per {
		to := min(from+per, len(lines))
		row := mdRow{src: header + strings.Join(lines[from:to], "\n"), start: offsets[from], end: offsets[to-1] + len(lines[to-1]), kind: parser.Table, table: number}
		if from == 2 {
			row.start = start
		}
		r.rows = append(r.rows, row)
	}
}

// countChildren is how many blocks block i holds directly.
func countChildren(d *parser.Document, i int) int {
	n := 0
	for range d.Children(i) {
		n++
	}
	return n
}

func lineStart(s string, i int) int { return strings.LastIndexByte(s[:i], '\n') + 1 }

func lineEnd(s string, i int) int {
	if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
		return i + j
	}
	return len(s)
}

// collectDefinitions notes the link reference definitions among the lines of
// text, the first of a label winning as it does in CommonMark.
func collectDefinitions(text string, defs map[string]string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	for _, line := range strings.Split(text, "\n") {
		if m := defLine.FindStringSubmatch(line); m != nil {
			key := parser.NormalizeLabel(m[1])
			if _, seen := defs[key]; !seen {
				defs[key] = strings.TrimRight(line, "\r")
			}
		}
	}
}

// withDefinitions is the row's text with the definitions of the references
// it makes ([text][label], [label], ![alt][label]) after it: the view of one
// row knows no other. Rows that show no links stay as they are.
func withDefinitions(row *mdRow, defs map[string]string) string {
	switch row.kind {
	case parser.CodeBlock, parser.HTMLBlock, parser.MathBlock, parser.ThematicBreak:
		return row.src
	}
	var used []string
	seen := map[string]bool{}
	s := row.src
	for i := 0; i < len(s); i++ {
		if s[i] != '[' {
			continue
		}
		end := strings.IndexByte(s[i+1:], ']')
		if end < 0 {
			break
		}
		if label := s[i+1 : i+1+end]; end > 0 && end <= 200 {
			if line, ok := defs[parser.NormalizeLabel(label)]; ok && !seen[line] {
				seen[line] = true
				used = append(used, line)
			}
		}
		i += end
	}
	if len(used) == 0 {
		return row.src
	}
	return row.src + "\n\n" + strings.Join(used, "\n")
}

// rowAt is the row holding the byte at offset of the document: the last one
// that starts at or before it, or the footnotes' for a byte in one.
func (r *mdRows) rowAt(offset int) int {
	if r.notes >= 0 {
		i := sort.Search(len(r.noteSpans), func(i int) bool { return r.noteSpans[i][1] > offset })
		if i < len(r.noteSpans) && r.noteSpans[i][0] <= offset {
			return r.notes
		}
	}
	lo, hi := 0, len(r.rows)
	if r.notes >= 0 {
		hi-- // the footnotes' row is past the end of the text
	}
	for lo < hi {
		m := (lo + hi) / 2
		if r.rows[m].start <= offset {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return max(lo-1, 0)
}

// anchorRow is the row of the heading a link to #slug names, if any: as the
// view reads it, in lower case, and also with %-escapes undone.
func (r *mdRows) anchorRow(dest string) (int, bool) {
	name := strings.TrimPrefix(strings.TrimSpace(dest), "#")
	for _, try := range []string{name, unescapeAnchor(name)} {
		if row, ok := r.anchors[strings.ToLower(try)]; ok {
			return row, true
		}
	}
	return 0, false
}

func unescapeAnchor(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

// mdSpacing is the room between blocks in DIPs, as the theme gives it.
type mdSpacing struct {
	block, item float32
	head        [6]struct{ top, bottom float32 }
}

// markdownSpacing reads the spacing of a view's theme: the window theme's
// defaults (markdownTheme sets none) at the document's font size.
func markdownSpacing(window *markdown.Theme, fontSize float32) mdSpacing {
	sp := mdSpacing{block: window.BlockSpacing * fontSize, item: window.ItemSpacing * fontSize}
	for i, h := range window.Headings {
		sp.head[i].top, sp.head[i].bottom = h.Top*fontSize, h.Bottom*fontSize
	}
	return sp
}

// top is the room above row i: what the view puts between top-level blocks —
// the block spacing, a heading's own room under it and above the next block —
// and, between the rows of one list, the spacing of its items.
func (r *mdRows) top(i int, sp mdSpacing) float32 {
	if i <= 0 || i >= len(r.rows) {
		return 0
	}
	prev, cur := &r.rows[i-1], &r.rows[i]
	if cur.kind == parser.FootnoteDefinition {
		return 0 // the view puts a rule, with room of its own, above the footnotes
	}
	top := sp.block
	if cur.table != 0 && cur.table == prev.table {
		return 0
	}
	if cur.list != 0 && cur.list == prev.list && cur.tight {
		top = sp.item
	}
	if prev.kind == parser.Heading {
		top = sp.head[prev.level-1].bottom
	}
	if cur.kind == parser.Heading {
		top = max(top, sp.head[cur.level-1].top)
	}
	return top
}
