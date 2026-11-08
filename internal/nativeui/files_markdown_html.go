package nativeui

/**
 * [INPUT]: golang.org/x/net/html（只用来解析：不执行、不取资源、不渲染）、markdown/parser 的 HTML 行内节点、MyGo markdown.Options.HTML、markdownPreview（files_markdown.go）
 * [OUTPUT]: translateHTML（一个原始 HTML 块 → 一串 Markdown 块 + 居中标记；字节/节点/深度/输出都有上限，超了就让调用方按原文显示）、markdownPreview.htmlBlock（Options.HTML：每个 Markdown 块嵌一个 markdown.View，居中的块居中）、normalizeInlineHTML（段落/标题/单元格里的行内 HTML：<img>、<a> 变成 Markdown，注释与布局标签去掉，脚本类连内容丢弃）
 * [POS]: 产品决策 2026-11-08「Files 预览原生优先」补充（用户：Markdown 里的原始 HTML 要支持）：文档里的 HTML 是不可信输入，只取它的结构与文字再交给 Markdown 视图画；脚本/样式/框架/表单/媒体整块丢弃，链接只留 http(s)/mailto/锚点/项目内相对路径，图片地址再经 markdownImageLoader（只读项目内）；不支持的版式（颜色、定宽、折叠状态）按普通流排，不为它们造渲染引擎
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// What one HTML block may cost. A README's header is a few hundred bytes; a
// block past these bounds is shown as the text it is.
const (
	htmlBlockCap = 256 << 10 // bytes of HTML translated at once
	htmlNodeCap  = 20_000    // nodes visited
	htmlDepthCap = 48        // nesting followed
	htmlOutCap   = 512 << 10 // bytes of Markdown made
)

// htmlChunk is one block of a translated HTML block: Markdown for a view of its
// own, and whether the HTML centered it.
type htmlChunk struct {
	md     string
	center bool
	list   bool // a list: what follows an item's text without a blank line
}

// htmlDropped are the elements that are never drawn, with all that is inside
// them: code that runs, styles, frames, forms, media and what only describes
// the page.
var htmlDropped = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Iframe: true, atom.Object: true, atom.Embed: true, atom.Applet: true,
	atom.Frame: true, atom.Frameset: true, atom.Form: true, atom.Input: true, atom.Button: true, atom.Textarea: true,
	atom.Select: true, atom.Option: true, atom.Video: true, atom.Audio: true, atom.Canvas: true, atom.Svg: true,
	atom.Math: true, atom.Link: true, atom.Meta: true, atom.Base: true, atom.Template: true, atom.Noscript: true,
	atom.Head: true, atom.Title: true, atom.Source: true, atom.Track: true,
}

// htmlBlocks are the elements that start a block of their own.
var htmlBlocks = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true,
	atom.Ul: true, atom.Ol: true, atom.Li: true, atom.Table: true, atom.Thead: true, atom.Tbody: true, atom.Tfoot: true,
	atom.Tr: true, atom.Td: true, atom.Th: true, atom.Caption: true, atom.Blockquote: true, atom.Pre: true, atom.Hr: true,
	atom.Details: true, atom.Summary: true, atom.Section: true, atom.Article: true, atom.Header: true, atom.Footer: true,
	atom.Nav: true, atom.Aside: true, atom.Main: true, atom.Center: true, atom.Figure: true, atom.Figcaption: true,
	atom.Dl: true, atom.Dt: true, atom.Dd: true, atom.Address: true, atom.Fieldset: true, atom.Legend: true,
	atom.Body: true, atom.Html: true, atom.Menu: true,
}

// htmlFormat maps the elements of inline formatting to the tag the Markdown
// view draws for them (it applies <b>, <i>, <u>, <s>, <code>, <kbd>, <samp>,
// <mark>, <sub>, <sup> and <small> itself); the others lose their tags.
var htmlFormat = map[atom.Atom]string{
	atom.B: "b", atom.Strong: "b", atom.I: "i", atom.Em: "i", atom.Cite: "i", atom.Var: "i", atom.Dfn: "i",
	atom.U: "u", atom.Ins: "u", atom.S: "s", atom.Del: "s", atom.Strike: "s",
	atom.Code: "code", atom.Tt: "code", atom.Kbd: "kbd", atom.Samp: "samp",
	atom.Mark: "mark", atom.Sub: "sub", atom.Sup: "sup", atom.Small: "small",
}

var (
	textAlignCenter = regexp.MustCompile(`(?i)text-align\s*:\s*center`)
	mdOrderedStart  = regexp.MustCompile(`^(\d{1,9})([.)])(\s|$)`)
	mdBlockStart    = regexp.MustCompile(`^(#{1,6}|[-+])(\s|$)`)
	htmlHint        = regexp.MustCompile(`<[A-Za-z!/]`)
	htmlLanguage    = regexp.MustCompile(`^[A-Za-z0-9_+#.-]{1,24}$`)
)

// htmlWalker translates one HTML block, counting what it spends.
type htmlWalker struct {
	nodes, out int
	fail       bool
}

// step counts a node at depth and reports whether the walk must stop.
func (w *htmlWalker) step(depth int) bool {
	w.nodes++
	if w.nodes > htmlNodeCap || depth > htmlDepthCap {
		w.fail = true
	}
	return w.fail
}

// translateHTML translates raw, an HTML block, to blocks of Markdown, in
// order. ok is false when the block is past what it will spend; no chunks with
// ok means the HTML draws nothing (a comment, an empty element).
func translateHTML(raw string) (chunks []htmlChunk, ok bool) {
	if len(raw) > htmlBlockCap {
		return nil, false
	}
	nodes, err := html.ParseFragment(strings.NewReader(raw), &html.Node{Type: html.ElementNode, DataAtom: atom.Body, Data: "body"})
	if err != nil {
		return nil, false
	}
	root := &html.Node{Type: html.ElementNode, DataAtom: atom.Body, Data: "body"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	w := &htmlWalker{}
	chunks = w.flow(root, 0, false)
	if w.fail {
		return nil, false
	}
	return chunks, true
}

// flow translates the children of parent as blocks: consecutive text and inline
// elements make a paragraph, block elements make their own.
func (w *htmlWalker) flow(parent *html.Node, depth int, center bool) []htmlChunk {
	var out []htmlChunk
	var run []*html.Node
	endRun := func() {
		if len(run) == 0 {
			return
		}
		text := strings.TrimSpace(w.inline(run, depth))
		run = run[:0]
		if strings.TrimSpace(strings.ReplaceAll(text, "<br>", "")) != "" { // breaks alone draw nothing (and would read as an HTML block)
			out = w.emit(out, htmlChunk{md: guardLineStart(text), center: center})
		}
	}
	for c := parent.FirstChild; c != nil && !w.fail; c = c.NextSibling {
		switch {
		case c.Type == html.CommentNode || c.Type == html.DoctypeNode:
		case c.Type == html.TextNode:
			run = append(run, c)
		case c.Type == html.ElementNode && htmlBlocks[c.DataAtom]:
			endRun()
			for _, ch := range w.block(c, depth+1, center) {
				out = w.emit(out, ch)
			}
		default:
			run = append(run, c)
		}
	}
	endRun()
	return out
}

// emit adds a chunk, counting its size.
func (w *htmlWalker) emit(out []htmlChunk, ch htmlChunk) []htmlChunk {
	w.out += len(ch.md)
	if w.out > htmlOutCap {
		w.fail = true
	}
	return append(out, ch)
}

// block translates one block element.
func (w *htmlWalker) block(n *html.Node, depth int, center bool) []htmlChunk {
	if w.step(depth) || htmlDropped[n.DataAtom] {
		return nil
	}
	centered := center || htmlCentered(n)
	para := func(text string) []htmlChunk {
		if text = strings.TrimSpace(text); text == "" {
			return nil
		}
		return []htmlChunk{{md: guardLineStart(text), center: centered}}
	}
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		text := strings.TrimSpace(w.inlineChildren(n, depth+1))
		if text == "" {
			return nil
		}
		return []htmlChunk{{md: strings.Repeat("#", int(n.Data[1]-'0')) + " " + text, center: centered}}
	case atom.P, atom.Legend, atom.Caption, atom.Address, atom.Dd:
		return para(w.inlineChildren(n, depth+1))
	case atom.Dt:
		if text := strings.TrimSpace(w.inlineChildren(n, depth+1)); text != "" {
			return para("<b>" + text + "</b>")
		}
		return nil
	case atom.Figcaption:
		if text := strings.TrimSpace(w.inlineChildren(n, depth+1)); text != "" {
			return para("<i>" + text + "</i>")
		}
		return nil
	case atom.Summary:
		if text := strings.TrimSpace(w.inlineChildren(n, depth+1)); text != "" {
			return para("▾ <b>" + text + "</b>")
		}
		return nil
	case atom.Blockquote:
		inner := w.flow(n, depth+1, centered)
		if len(inner) == 0 {
			return nil
		}
		lines := strings.Split(joinChunks(inner), "\n")
		for i, l := range lines {
			if l == "" {
				lines[i] = ">"
			} else {
				lines[i] = "> " + l
			}
		}
		return []htmlChunk{{md: strings.Join(lines, "\n")}}
	case atom.Pre:
		return w.pre(n)
	case atom.Hr:
		return []htmlChunk{{md: "---"}}
	case atom.Ul, atom.Ol:
		if md := w.list(n, depth+1); md != "" {
			return []htmlChunk{{md: md, list: true}}
		}
		return nil
	case atom.Table:
		if md := w.table(n, depth+1); md != "" {
			return []htmlChunk{{md: md}}
		}
		return nil
	}
	// Layout containers (div, section, center, details, figure, a stray li, …)
	// add nothing of their own: what is inside flows on.
	return w.flow(n, depth+1, centered)
}

// pre translates <pre> to a fenced code block, naming its language when a
// code element inside says so.
func (w *htmlWalker) pre(n *html.Node) []htmlChunk {
	var b strings.Builder
	var lang string
	var walk func(*html.Node, int)
	walk = func(p *html.Node, depth int) {
		for c := p.FirstChild; c != nil && !w.step(depth); c = c.NextSibling {
			switch {
			case c.Type == html.TextNode:
				b.WriteString(c.Data)
			case c.Type == html.ElementNode && c.DataAtom == atom.Br:
				b.WriteByte('\n')
			case c.Type == html.ElementNode && !htmlDropped[c.DataAtom]:
				if c.DataAtom == atom.Code && lang == "" {
					lang = htmlCodeLanguage(attr(c, "class"))
				}
				walk(c, depth+1)
			}
		}
	}
	walk(n, 1)
	text := strings.TrimRight(b.String(), "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	fence := strings.Repeat("`", max(3, longestRun(text, '`')+1))
	return []htmlChunk{{md: fence + lang + "\n" + text + "\n" + fence}}
}

// list translates <ul> or <ol>: each li's blocks under its marker.
func (w *htmlWalker) list(n *html.Node, depth int) string {
	ordered := n.DataAtom == atom.Ol
	number := 1
	if v, err := strconv.Atoi(strings.TrimSpace(attr(n, "start"))); err == nil && v >= 0 && v < 1_000_000 {
		number = v
	}
	var items []string
	for c := n.FirstChild; c != nil && !w.fail; c = c.NextSibling {
		if c.Type != html.ElementNode || c.DataAtom != atom.Li {
			continue
		}
		marker := "- "
		if ordered {
			marker = strconv.Itoa(number) + ". "
			number++
		}
		body := joinItem(w.flow(c, depth+1, false))
		pad := strings.Repeat(" ", len(marker))
		lines := strings.Split(body, "\n")
		for i := range lines {
			switch {
			case i == 0:
				lines[i] = marker + lines[i]
			case lines[i] != "":
				lines[i] = pad + lines[i]
			}
		}
		items = append(items, strings.Join(lines, "\n"))
	}
	return strings.Join(items, "\n")
}

// table translates <table> to a pipe table, its first row the header.
func (w *htmlWalker) table(n *html.Node, depth int) string {
	var rows [][]string
	var walk func(*html.Node, int)
	walk = func(p *html.Node, d int) {
		for c := p.FirstChild; c != nil && !w.step(d); c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.DataAtom {
			case atom.Thead, atom.Tbody, atom.Tfoot:
				walk(c, d+1)
			case atom.Tr:
				var row []string
				for cell := c.FirstChild; cell != nil && !w.fail; cell = cell.NextSibling {
					if cell.Type == html.ElementNode && (cell.DataAtom == atom.Td || cell.DataAtom == atom.Th) {
						row = append(row, strings.TrimSpace(w.inlineChildren(cell, d+2)))
						if span, err := strconv.Atoi(attr(cell, "colspan")); err == nil && span > 1 && span <= 16 {
							for range span - 1 {
								row = append(row, "")
							}
						}
					}
				}
				if len(row) > 0 {
					rows = append(rows, row)
				}
			}
		}
	}
	walk(n, depth)
	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	if cols == 0 || w.fail {
		return ""
	}
	line := func(r []string) string {
		cells := make([]string, cols)
		for i := range cells {
			if i < len(r) && r[i] != "" {
				cells[i] = r[i]
			}
		}
		return "| " + strings.Join(cells, " | ") + " |"
	}
	var b strings.Builder
	b.WriteString(line(rows[0]) + "\n|" + strings.Repeat(" --- |", cols))
	for _, r := range rows[1:] {
		b.WriteString("\n" + line(r))
	}
	return b.String()
}

// inline translates nodes as the inline Markdown of one paragraph: a single
// line, whitespace collapsed as HTML collapses it.
func (w *htmlWalker) inline(nodes []*html.Node, depth int) string {
	var b strings.Builder
	for _, n := range nodes {
		w.inlineNode(&b, n, depth)
	}
	return collapseSpace(b.String())
}

func (w *htmlWalker) inlineChildren(n *html.Node, depth int) string {
	var kids []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, c)
	}
	return w.inline(kids, depth)
}

func (w *htmlWalker) inlineNode(b *strings.Builder, n *html.Node, depth int) {
	if w.step(depth) {
		return
	}
	switch n.Type {
	case html.TextNode:
		b.WriteString(escapeMD(collapseSpace(n.Data)))
		return
	case html.ElementNode:
	default:
		return
	}
	if htmlDropped[n.DataAtom] {
		return
	}
	switch n.DataAtom {
	case atom.Br:
		b.WriteString("<br>")
	case atom.A:
		inner := strings.TrimSpace(w.inlineChildren(n, depth+1))
		if dest := htmlLinkTarget(attr(n, "href"), true); dest != "" && inner != "" {
			fmt.Fprintf(b, "[%s](<%s>)", inner, dest)
		} else {
			b.WriteString(inner)
		}
	case atom.Img:
		alt := strings.TrimSpace(collapseSpace(attr(n, "alt")))
		if src := htmlLinkTarget(attr(n, "src"), false); src != "" {
			fmt.Fprintf(b, "![%s](<%s>)", escapeMD(alt), src)
		} else {
			b.WriteString(escapeMD(alt))
		}
	default:
		inner := w.inlineChildren(n, depth+1)
		switch tag := htmlFormat[n.DataAtom]; {
		case tag != "" && strings.TrimSpace(inner) != "":
			b.WriteString("<" + tag + ">" + inner + "</" + tag + ">")
		case htmlBlocks[n.DataAtom]:
			b.WriteString(" " + inner + " ") // a block inside inline text (a cell's paragraphs): a word boundary
		default:
			b.WriteString(inner)
		}
	}
}

// htmlBlock builds an HTML block of the document: each block of its
// translation as a Markdown view of its own, the centered ones centered. HTML
// that draws nothing (a comment) draws nothing; HTML past the translator's
// bounds shows as its text, as the view shows HTML it has no hook for.
func (mp *markdownPreview) htmlBlock(c *ui.Context, raw string) {
	th := mp.fullTheme(c.Theme())
	res, hit := mp.htmlMemo[raw]
	if !hit {
		chunks, ok := translateHTML(raw)
		res = htmlResult{chunks, ok}
		if mp.htmlMemo == nil || len(mp.htmlMemo) >= 256 {
			mp.htmlMemo = map[string]htmlResult{}
		}
		mp.htmlMemo[raw] = res
	}
	if !res.ok || mp.htmlDepth >= 2 {
		ui.Text(c, raw).Font(th.CodeFont).FontSize(th.FontSize * th.CodeSize).TextColor(th.Muted)
		return
	}
	if len(res.chunks) == 0 {
		return
	}
	ui.Column(c).AlignSelf(ui.Stretch).Gap(th.FontSize * th.BlockSpacing).Children(func() {
		for _, ch := range res.chunks {
			mp.htmlDepth++
			el := markdown.View(c, ch.md, mp.opts)
			mp.htmlDepth--
			if ch.center {
				el.AlignSelf(ui.Center)
			}
		}
	})
}

// htmlResult is a translated HTML block, kept by its text.
type htmlResult struct {
	chunks []htmlChunk
	ok     bool
}

// normalizeInlineHTML rewrites the raw HTML inside paragraphs, headings and
// table cells of src to what the view draws: <img> and <a href> become
// Markdown images and links, comments and layout tags (span, font, abbr, …) go
// with their content kept, script-like elements go with their content, and
// the tags of formatting stay for the view. Code spans, fences and HTML blocks
// are not touched. The view shows other inline HTML as its text.
func normalizeInlineHTML(src string) string {
	if !htmlHint.MatchString(src) {
		return src
	}
	d := parser.New(markdownExtensions)
	d.Set(src)
	type edit struct {
		from, to int
		text     string
	}
	var edits []edit
	for i := 1; i < d.Len(); i++ {
		switch d.Block(i).Kind {
		case parser.Paragraph, parser.Heading, parser.TableCell:
		default:
			continue
		}
		in := d.Inlines(i)
		type openLink struct {
			at   int // its edit
			href string
		}
		var links []openLink
		skipTo := -1
		for k := range in {
			n := &in[k]
			if n.Kind != parser.HTML || n.Start < skipTo || n.End > len(src) {
				continue
			}
			tag := src[n.Start:n.End]
			if strings.HasPrefix(tag, "<!--") {
				edits = append(edits, edit{n.Start, n.End, ""})
				continue
			}
			name, closing, attrs := parseHTMLTag(tag)
			a := atom.Lookup([]byte(name))
			switch {
			case name == "br" || htmlFormat[a] != "":
				// drawn by the view
			case name == "a" && closing:
				if len(links) > 0 {
					top := links[len(links)-1]
					links = links[:len(links)-1]
					if top.href != "" {
						edits[top.at].text = "["
						edits = append(edits, edit{n.Start, n.End, "](<" + top.href + ">)"})
						continue
					}
				}
				edits = append(edits, edit{n.Start, n.End, ""})
			case name == "a":
				edits = append(edits, edit{n.Start, n.End, ""}) // a "[" once its end tag is found
				links = append(links, openLink{len(edits) - 1, htmlLinkTarget(attrs["href"], true)})
			case name == "img" && !closing:
				alt := strings.TrimSpace(collapseSpace(attrs["alt"]))
				text := escapeMD(alt)
				if s := htmlLinkTarget(attrs["src"], false); s != "" {
					text = "![" + escapeMD(alt) + "](<" + s + ">)"
				}
				edits = append(edits, edit{n.Start, n.End, text})
			case htmlDropped[a] && !closing:
				end := n.End
				for j := k + 1; j < len(in); j++ {
					if in[j].Kind != parser.HTML || in[j].End > len(src) {
						continue
					}
					if nm, cl, _ := parseHTMLTag(src[in[j].Start:in[j].End]); cl && nm == name {
						end = in[j].End
						break
					}
				}
				edits = append(edits, edit{n.Start, end, ""})
				skipTo = end
			default:
				edits = append(edits, edit{n.Start, n.End, ""})
			}
		}
	}
	if len(edits) == 0 {
		return src
	}
	sort.SliceStable(edits, func(a, b int) bool { return edits[a].from < edits[b].from })
	var out strings.Builder
	last := 0
	for _, e := range edits {
		if e.from < last {
			continue // inside what an earlier edit replaced
		}
		out.WriteString(src[last:e.from])
		out.WriteString(e.text)
		last = e.to
	}
	out.WriteString(src[last:])
	return out.String()
}

// parseHTMLTag reads one raw tag: its lower-cased name, whether it closes, and
// its attributes by lower-cased name. Anything that is not a tag has no name.
func parseHTMLTag(raw string) (name string, closing bool, attrs map[string]string) {
	z := html.NewTokenizer(strings.NewReader(raw))
	switch z.Next() {
	case html.StartTagToken, html.SelfClosingTagToken:
	case html.EndTagToken:
		closing = true
	default:
		return "", false, nil
	}
	tok := z.Token()
	attrs = make(map[string]string, len(tok.Attr))
	for _, a := range tok.Attr {
		attrs[strings.ToLower(a.Key)] = a.Val
	}
	return strings.ToLower(tok.Data), closing, attrs
}

// htmlLinkTarget returns dest, ready to stand between < and > in Markdown,
// when a link (or an image) may carry it: a web address, for a link a mail
// address or anchor too, or a path without a scheme (the app decides which of
// those open or load); "" otherwise.
func htmlLinkTarget(dest string, link bool) string {
	dest = strings.TrimSpace(dest)
	if dest == "" || len(dest) > 2048 {
		return ""
	}
	for _, r := range dest {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	u, err := url.Parse(dest)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "":
	case "mailto":
		if !link {
			return ""
		}
	default:
		return ""
	}
	return strings.NewReplacer("<", "%3C", ">", "%3E", `\`, "%5C", " ", "%20").Replace(dest)
}

// htmlCentered reports whether n centers its content: <center>, align=center
// or a style that says text-align: center.
func htmlCentered(n *html.Node) bool {
	if n.DataAtom == atom.Center {
		return true
	}
	if v := strings.ToLower(strings.TrimSpace(attr(n, "align"))); v == "center" || v == "middle" {
		return true
	}
	return textAlignCenter.MatchString(attr(n, "style"))
}

// htmlCodeLanguage reads the language of a code element's class, as
// language-go or lang-go, and nothing that is not a plain name.
func htmlCodeLanguage(class string) string {
	for _, f := range strings.Fields(class) {
		for _, p := range []string{"language-", "lang-"} {
			if l, ok := strings.CutPrefix(f, p); ok && htmlLanguage.MatchString(l) {
				return l
			}
		}
	}
	return ""
}

// attr returns the value of attribute key of n.
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

// escapeMD escapes what Markdown reads as syntax in text.
func escapeMD(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\', '`', '*', '_', '[', ']', '<', '>', '|', '~', '$', '&', '!':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// guardLineStart keeps the start of a paragraph from reading as a list item,
// a heading or a rule.
func guardLineStart(s string) string {
	switch {
	case mdOrderedStart.MatchString(s):
		m := mdOrderedStart.FindStringSubmatchIndex(s) // 1. → 1\. : the escape goes before the dot
		return s[:m[4]] + `\` + s[m[4]:]
	case mdBlockStart.MatchString(s), strings.Trim(s, "-= ") == "":
		return `\` + s
	}
	return s
}

// collapseSpace turns each run of HTML whitespace into one space.
func collapseSpace(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r', '\f':
			if !space {
				b.WriteByte(' ')
				space = true
			}
		default:
			b.WriteRune(r)
			space = false
		}
	}
	return b.String()
}

// longestRun is the length of the longest run of c in s.
func longestRun(s string, c byte) int {
	best, run := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			run++
			best = max(best, run)
		} else {
			run = 0
		}
	}
	return best
}

// joinChunks joins blocks of Markdown with blank lines.
func joinChunks(chunks []htmlChunk) string {
	parts := make([]string, len(chunks))
	for i, ch := range chunks {
		parts[i] = ch.md
	}
	return strings.Join(parts, "\n\n")
}

// joinItem joins the blocks of a list item: a list straight under the text it
// follows, other blocks after a blank line.
func joinItem(chunks []htmlChunk) string {
	var b strings.Builder
	for i, ch := range chunks {
		switch {
		case i == 0:
		case ch.list:
			b.WriteString("\n")
		default:
			b.WriteString("\n\n")
		}
		b.WriteString(ch.md)
	}
	return b.String()
}
