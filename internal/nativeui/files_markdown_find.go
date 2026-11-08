package nativeui

/**
 * [INPUT]: markdownPreview（src、rows、list）、MyGo ui.FindBar / ui.ListState、parser（块与行内偏移）
 * [OUTPUT]: mdFind（⌘F 的查找条状态）、markdownPreview.findFrame（每帧：⌘F 开条、查询变了重算命中、步进/滚动到当前命中行）、markdownPreview.rowSource（命中行的源码里把命中包进 <mark>，当前命中再加 <u>）、markdownPreview.codeBlock（含命中的代码块自己画：高亮底色，与视图的默认代码块同版式）、buildFindIndex/mdIndex.search（只数看得见的文字：段落、标题、表格单元格、代码行；Mermaid 图、数学、原始 HTML 不是文字）
 * [POS]: 产品决策 2026-11-08（用户：「渲染视图要支持查找」）：ui/markdown 没有高亮或滚动到某处的接口，所以用它的两个现成机制——命中所在的行用 ListState.ScrollTo 带到眼前，命中的字在那一行的源码里包 <mark>（视图自己画底色）。第一次有命中时，一个小文档也改按行显示（同一份行表，外观一致），关查找条后不退回，视图不跳
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// mdFindCap bounds the hits kept: a query of one letter in a long document
// would otherwise hold a hit for every one of them.
const mdFindCap = 100_000

// mdFind is the find bar of a formatted document.
type mdFind struct {
	open    bool
	query   string
	current int
	// hitsFor is the (lower-cased) query hits answers; "" while none is.
	hitsFor string
	hits    []mdHit
	rowOf   []int // the row of each hit, once the document shows as rows
	index   *mdIndex
	marked  map[int]markedRow
	scroll  bool // bring the current hit into view in this frame
}

// mdHit is one occurrence of the query: at is where its text starts in the
// document, in the text beginning there.
type mdHit struct{ at int }

// markedRow is a row's source with its hits marked, for the hit that was
// current when it was made; codeBase is, for each code block of the row, the
// number among the row's hits of its first (the blocks draw their own).
type markedRow struct {
	src      string
	emph     int
	codeBase []int
}

// rowMark is what the row being built tells its code blocks, which the view
// builds one after another: the hit that is current (its number among the
// row's) and where each block's own hits start.
type rowMark struct {
	emph     int
	codeBase []int
	next     int
}

// mdLeaf is a piece of text the document shows: a paragraph, a heading, a
// table cell, or a line of code.
type mdLeaf struct {
	at   int    // where it starts in the document
	text string // lower-cased
}

// mdIndex is the text of a document, leaf by leaf.
type mdIndex struct{ leaves []mdLeaf }

// buildFindIndex reads src as the view does and lists what shows as text.
func buildFindIndex(src string) *mdIndex {
	d := parser.New(markdownExtensions)
	d.Set(src)
	idx := &mdIndex{}
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		switch b.Kind {
		case parser.Paragraph, parser.Heading, parser.TableCell:
			if text := visibleText(d.Inlines(i)); text != "" {
				idx.leaves = append(idx.leaves, mdLeaf{at: b.Start, text: strings.ToLower(text)})
			}
		case parser.CodeBlock:
			if drawnCode(b.Language()) {
				continue // a diagram: its source is not what shows
			}
			for k := range d.LineCount(i) {
				line, at := d.Line(i, k)
				if line != "" {
					idx.leaves = append(idx.leaves, mdLeaf{at: at, text: strings.ToLower(line)})
				}
			}
		}
	}
	return idx
}

// visibleText is the text of inlines as it reads on the page: text and code,
// a space for a line break; not an image's description, raw HTML or math,
// which show as pictures and tags.
func visibleText(in []parser.Inline) string {
	var b strings.Builder
	for k := 0; k < len(in); {
		switch n := &in[k]; n.Kind {
		case parser.Image:
			k = n.Next
			continue
		case parser.Text, parser.Code:
			b.WriteString(n.Text)
		case parser.SoftBreak, parser.LineBreak:
			b.WriteByte(' ')
		}
		k++
	}
	return b.String()
}

// search lists the occurrences of lq, a lower-cased query, in document
// order, capped at mdFindCap.
func (x *mdIndex) search(lq string) []mdHit {
	var hits []mdHit
	if lq == "" {
		return nil
	}
	for _, leaf := range x.leaves {
		for pos := 0; len(hits) < mdFindCap; {
			j := strings.Index(leaf.text[pos:], lq)
			if j < 0 {
				break
			}
			hits = append(hits, mdHit{at: leaf.at})
			pos += j + len(lq)
		}
		if len(hits) >= mdFindCap {
			break
		}
	}
	return hits
}

// findFrame is the find bar's frame: ⌘F opens it, a new query is searched
// before the bar is built (so its count is right in this frame), a step
// brings the hit's row into view, and closing it clears every mark.
func (mp *markdownPreview) findFrame(c *ui.Context) {
	f := &mp.find
	if c.Shortcut(ui.Cmd, ui.KeyF) {
		f.open = true
	}
	switch lq := strings.ToLower(f.query); {
	case !f.open || lq == "":
		if f.hitsFor != "" {
			mp.clearFind()
		}
	case lq != f.hitsFor:
		mp.search(lq)
	}
	n := 0
	if f.open && f.hitsFor != "" {
		n = len(f.hits)
	}
	bar := ui.FindBar(c, &f.open, &f.query, n, &f.current).Label("Find in document")
	if bar.Changed() {
		f.scroll = true
	}
	if f.open && f.scroll && n > 0 {
		f.scroll = false
		mp.revealHit(f.current)
	}
}

// search looks for lq (lower-cased) and makes the first hit current.
func (mp *markdownPreview) search(lq string) {
	f := &mp.find
	if f.index == nil {
		f.index = buildFindIndex(mp.src)
	}
	f.hitsFor, f.hits, f.rowOf, f.current = lq, f.index.search(lq), nil, 0
	f.marked = map[int]markedRow{}
	if len(f.hits) == 0 {
		return
	}
	// A small document shows as one view, which cannot be scrolled to a hit:
	// from here on it shows as rows, which look the same and can.
	if mp.rows == nil {
		mp.rows = planMarkdownRows(mp.src, true)
	}
	f.rowOf = make([]int, len(f.hits))
	for i, h := range f.hits {
		f.rowOf[i] = mp.rows.rowAt(h.at)
	}
	f.scroll = true
}

// clearFind forgets the hits and the marks (the query stays for the next ⌘F).
func (mp *markdownPreview) clearFind() {
	f := &mp.find
	f.hitsFor, f.hits, f.rowOf, f.marked, f.current = "", nil, nil, nil, 0
}

// revealHit scrolls the rows so that the row of hit i is in view: not at all
// when it already is, so stepping through hits of one screen leaves the page
// still.
func (mp *markdownPreview) revealHit(i int) {
	f := &mp.find
	if i < 0 || i >= len(f.rowOf) {
		return
	}
	row := f.rowOf[i]
	if first, last := mp.list.Visible(); first >= 0 && row >= first+1 && row <= last-1 {
		return
	}
	mp.list.ScrollTo(row, ui.Center)
}

// buildRow builds row i's view: its source with the hits of the find bar
// marked while it is open, and its code blocks told which hit is current.
func (mp *markdownPreview) buildRow(c *ui.Context, i int, opts *markdown.Options) ui.Element {
	m := mp.rowMarks(i)
	mp.marking = &rowMark{emph: m.emph, codeBase: m.codeBase}
	el := markdown.View(c, m.src, opts)
	mp.marking = nil
	return el
}

// rowSource is what row i's view shows: its source, with the hits marked.
func (mp *markdownPreview) rowSource(i int) string { return mp.rowMarks(i).src }

func (mp *markdownPreview) rowMarks(i int) markedRow {
	f := &mp.find
	row := &mp.rows.rows[i]
	if !f.open || f.hitsFor == "" || len(f.hits) == 0 {
		return markedRow{src: row.src, emph: -1}
	}
	emph := -1 // the ordinal, among the row's hits, of the current one
	if f.current >= 0 && f.current < len(f.rowOf) && f.rowOf[f.current] == i {
		first := sort.SearchInts(f.rowOf, i) // rowOf is in document order
		emph = f.current - first
	}
	if m, ok := f.marked[i]; ok && m.emph == emph {
		return m
	}
	src, bases := markMatches(row.src, f.hitsFor, emph)
	m := markedRow{src: src, emph: emph, codeBase: bases}
	f.marked[i] = m
	return m
}

// markMatches wraps the occurrences of lq (lower-cased) in src in <mark>, the
// emph-th also in <u> (-1: none), for the view to draw its own highlight
// behind them. It marks what it can place exactly: text as the source spells
// it, and a code span as a whole; text inside an image, an autolink, raw HTML
// or math, and a match across two inlines (the words of "a *b*"), are found
// by the bar but left unmarked. The lines of a code block cannot carry the
// tags: the block draws its own marks (markedCode), and markMatches only says
// where its hits begin among the row's, in the second result.
func markMatches(src, lq string, emph int) (string, []int) {
	if lq == "" || !strings.Contains(strings.ToLower(src), lq) {
		return src, nil
	}
	d := parser.New(markdownExtensions)
	d.Set(src)
	type edit struct {
		at   int
		text string
	}
	var edits []edit
	var bases []int
	ord := 0
	wrap := func(from, to int) {
		open, closing := "<mark>", "</mark>"
		if ord == emph {
			open, closing = "<mark><u>", "</u></mark>"
		}
		edits = append(edits, edit{from, open}, edit{to, closing})
		ord++
	}
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		switch b.Kind {
		case parser.CodeBlock:
			if drawnCode(b.Language()) {
				continue // a diagram, built by its own hook
			}
			bases = append(bases, ord)
			for k := range d.LineCount(i) {
				line, _ := d.Line(i, k)
				ord += strings.Count(strings.ToLower(line), lq)
			}
			continue
		case parser.Paragraph, parser.Heading, parser.TableCell:
		default:
			continue
		}
		in := d.Inlines(i)
		for k := 0; k < len(in); {
			n := &in[k]
			switch {
			case n.Kind == parser.Image, n.Kind == parser.Link && n.Auto:
				k = n.Next
				continue
			case n.Kind == parser.Text && src[n.Start:n.End] == n.Text:
				lt := strings.ToLower(n.Text)
				if len(lt) != len(n.Text) {
					break // case folding changed the length: offsets would not line up
				}
				for pos := 0; ; {
					j := strings.Index(lt[pos:], lq)
					if j < 0 {
						break
					}
					wrap(n.Start+pos+j, n.Start+pos+j+len(lq))
					pos += j + len(lq)
				}
			case n.Kind == parser.Code && strings.Contains(strings.ToLower(n.Text), lq):
				wrap(n.Start, n.End)
			}
			k++
		}
	}
	if len(edits) == 0 {
		return src, bases
	}
	sort.SliceStable(edits, func(a, b int) bool { return edits[a].at < edits[b].at })
	var out strings.Builder
	last := 0
	for _, e := range edits {
		out.WriteString(src[last:e.at])
		out.WriteString(e.text)
		last = e.at
	}
	out.WriteString(src[last:])
	return out.String(), bases
}

// codeBlock is the view's hook for code blocks: one that holds a hit of the
// open find bar is drawn by markedCode, which marks the hits; every other is
// the view's own.
func (mp *markdownPreview) codeBlock(c *ui.Context, cb *markdown.CodeBlockView) {
	f := &mp.find
	if mp.marking == nil || !f.open || f.hitsFor == "" || !strings.Contains(strings.ToLower(cb.Code()), f.hitsFor) {
		if mp.marking != nil {
			mp.marking.next++
		}
		markdown.DefaultCodeBlock(c, cb)
		return
	}
	rm := mp.marking
	base := 0
	if rm.next < len(rm.codeBase) {
		base = rm.codeBase[rm.next]
	}
	rm.next++
	mp.markedCode(c, cb, rm.emph-base) // the current hit's number within this block, if it is in it
}

// markedCode draws a code block as the view's DefaultCodeBlock does — the same
// box, header, copy button and highlighted lines — with the hits of the query
// on a background, and the current one underlined; current is its number
// among this block's hits (outside 0..n when the current hit is elsewhere).
// The view offers no way to mark inside its code, and its layout is pinned
// against this one by a test, so a change in MyGo shows there.
func (mp *markdownPreview) markedCode(c *ui.Context, cb *markdown.CodeBlockView, current int) {
	th := mp.fullTheme(c.Theme())
	em := func(v float32) float32 { return v * th.FontSize }
	label := "Code"
	if cb.Language != "" {
		label = "Code, " + cb.Language
	}
	lq := mp.find.hitsFor
	var tok editor.Tokenizer
	if l := editor.LanguageNamed(cb.Language); l != nil && cb.Language != "" {
		tok = l.Tokenizer
	}
	ct := th.CodeColors
	lines := strings.Split(strings.TrimSuffix(cb.Code(), "\n"), "\n")
	hit := 0 // the number of the next hit in this block
	var chunks [][]ui.Span
	state := uint64(0)
	for first := 0; first < len(lines); first += 32 { // the view lays code out 32 lines at a time
		var spans []ui.Span
		for l := first; l < min(first+32, len(lines)); l++ {
			if l > first {
				spans = append(spans, ui.Span{Text: "\n"})
			}
			var toks []editor.Token
			if tok != nil {
				toks, state = tok.Tokenize(lines[l], state, nil)
			}
			spans = append(spans, markLine(lines[l], toks, ct, lq, th, &hit, current)...)
		}
		if len(spans) == 0 {
			spans = append(spans, ui.Span{Text: " "})
		}
		chunks = append(chunks, spans)
	}
	box := ui.Column(c).Direction(ui.LeftToRight).Background(th.CodeBlockBackground).Border(1, th.CodeBlockBorder).Radius(th.CodeBlockRadius).AlignSelf(ui.Stretch).Clip()
	box.Role(ui.RoleGroup).Label(label)
	box.Children(func() {
		head := ui.Row(c).AlignItems(ui.Center).Padding(em(0.35), em(0.4), 0, em(th.CodeBlockPadding))
		head.Children(func() {
			ui.Text(c, cb.Language).FontSize(em(0.8)).TextColor(th.Muted).Unselectable()
			ui.Spacer(c)
			cb.CopyButton(c)
		})
		pad := em(th.CodeBlockPadding)
		ui.ScrollHorizontal(c).Direction(ui.LeftToRight).AlignSelf(ui.Stretch).Font(th.CodeFont).FontSize(em(th.CodeSize)).LineHeight(th.CodeLineHeight).Children(func() {
			ui.Column(c).Padding(em(0.25), pad, pad, pad).Shrink(0).Children(func() {
				for _, spans := range chunks {
					ui.RichText(c, spans...).NoWrap()
				}
			})
		})
	})
}

// markLine is the spans of one line of code: its tokens colored as the view's
// own code block colors them, cut where the query matches, which get the
// theme's mark background (the current hit underlined as well). hit numbers
// the matches of the block as they come.
func markLine(line string, toks []editor.Token, ct *editor.Theme, lq string, th *markdown.Theme, hit *int, current int) []ui.Span {
	type piece struct {
		from, to int
		sp       ui.Span
	}
	var pieces []piece
	start := 0
	for _, t := range toks {
		end := min(t.End, len(line))
		if end > start {
			sp := ui.Span{Color: ct.TokenColor(t.Kind)}
			if s := ct.Tokens[t.Kind]; s.Bold {
				sp.Weight = 600
			} else if s.Italic {
				sp.Italic = true
			}
			pieces = append(pieces, piece{start, end, sp})
		}
		start = max(start, end)
	}
	if start < len(line) {
		pieces = append(pieces, piece{start, len(line), ui.Span{Color: ct.Text}})
	}
	type match struct{ from, to, n int }
	var matches []match
	lower := strings.ToLower(line)
	if len(lower) != len(line) { // case folding moved the offsets: count, do not place
		*hit += strings.Count(lower, lq)
	} else {
		for pos := 0; ; {
			j := strings.Index(lower[pos:], lq)
			if j < 0 {
				break
			}
			matches = append(matches, match{pos + j, pos + j + len(lq), *hit})
			*hit++
			pos += j + len(lq)
		}
	}
	var out []ui.Span
	for _, p := range pieces {
		at := p.from
		for _, m := range matches {
			if m.to <= at || m.from >= p.to {
				continue
			}
			from, to := max(m.from, at), min(m.to, p.to)
			if from > at {
				sp := p.sp
				sp.Text = line[at:from]
				out = append(out, sp)
			}
			sp := p.sp
			sp.Text, sp.Background, sp.Underline = line[from:to], th.Mark, m.n == current
			out = append(out, sp)
			at = to
		}
		if at < p.to {
			sp := p.sp
			sp.Text = line[at:p.to]
			out = append(out, sp)
		}
	}
	return out
}

// fullTheme is the theme the view resolves for this preview: the window's
// defaults with the preview's own type and code colors.
func (mp *markdownPreview) fullTheme(t *ui.Theme) *markdown.Theme {
	th := markdown.DefaultTheme(t)
	if ov := mp.opts.Theme; ov != nil {
		th.FontSize, th.CodeFont, th.CodeColors = ov.FontSize, ov.CodeFont, ov.CodeColors
	}
	return th
}
