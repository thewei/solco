package nativeui

/**
 * [INPUT]: files_markdown_html.go（translateHTML、htmlBlock、normalizeInlineHTML）、files_markdown.go
 * [OUTPUT]: 原始 HTML 的回归：块级 HTML 译成 Markdown 块（标题/段落/列表/表格/代码/折叠/引用）、居中、注释不画、脚本/框架/表单整块丢弃、javascript: 与 data: 地址不放行、文本里的 Markdown 语法被转义、字节/节点/深度/输出上限、行内 <img>/<a> 变 Markdown、代码里的 HTML 不动
 * [POS]: 用户 2026-11-08「Markdown 里的原始 HTML 要支持」；文档里的 HTML 是不可信输入，这些测试守"只取结构与文字，不执行、不取资源"
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestTranslateHTMLMakesMarkdownBlocks(t *testing.T) {
	type chunk struct {
		md     string
		center bool
	}
	for _, c := range []struct {
		name, html string
		want       []chunk
	}{
		{"comment draws nothing", `<!-- hidden -->`, nil},
		{"break alone draws nothing", `<br>`, nil},
		{"centered image", `<p align="center"><img src="a.png" width="10" alt="Logo"></p>`, []chunk{{"![Logo](<a.png>)", true}}},
		{"centered heading and badge", "<h1 align=\"center\">Title &amp; more</h1>\n<p align=\"center\">\n  <a href=\"https://x.dev\"><img src=\"https://img.shields.io/b.svg\" alt=\"badge\"></a>\n</p>",
			[]chunk{{`# Title \& more`, true}, {"[![badge](<https://img.shields.io/b.svg>)](<https://x.dev>)", true}}},
		{"center element and style", `<center>a</center><div style="text-align: center">b</div><div>c</div>`, []chunk{{"a", true}, {"b", true}, {"c", false}}},
		{"formatting stays as the view's tags", `<p>hi <b>there</b> <i>x</i> <kbd>K</kbd> <strike>s</strike> <span style="color:red">r</span></p>`, []chunk{{"hi <b>there</b> <i>x</i> <kbd>K</kbd> <s>s</s> r", false}}},
		{"text is escaped", `<p>*not em* [x] &lt;y&gt; a_b | $5 # !</p>`, []chunk{{`\*not em\* \[x\] \<y\> a\_b \| \$5 # \!`, false}}},
		{"line start cannot become a block", `<p>1. one</p><p># not a heading</p><p>- item</p><p>---</p>`, []chunk{{`1\. one`, false}, {`\# not a heading`, false}, {`\- item`, false}, {`\---`, false}}},
		{"scripts and frames vanish with their content", `<script>alert(1)</script><style>p{}</style><iframe src="x">frame</iframe><form><input value="v"><button>go</button></form><p>kept</p><object>o</object><svg><text>t</text></svg>`, []chunk{{"kept", false}}},
		{"javascript and data addresses are not links or images", `<p><a href="javascript:alert(1)">x</a> <a href=" JavaScript:alert(1)">y</a> <img src="data:image/png;base64,AAAA" alt="d"> <img src="javascript:x" alt="j"> <a href="vbscript:x">v</a></p>`, []chunk{{"x y d j v", false}}},
		{"safe addresses stay", `<p><a href="https://a.dev/p?q=1">w</a> <a href="mailto:me@a.dev">m</a> <a href="docs/x.md">r</a> <a href="#top">t</a> <img src="img/a.png"></p>`, []chunk{{"[w](<https://a.dev/p?q=1>) [m](<mailto:me@a.dev>) [r](<docs/x.md>) [t](<#top>) ![](<img/a.png>)", false}}},
		{"link destinations cannot break out", "<p><a href=\"a b>c<d\\e\">x</a></p>", []chunk{{"[x](<a%20b%3Ec%3Cd%5Ce>)", false}}},
		{"nested list", `<ul><li>a</li><li>b<ul><li>c</li></ul></li></ul>`, []chunk{{"- a\n- b\n  - c", false}}},
		{"ordered list with start", `<ol start="3"><li>x</li><li><p>y</p><p>z</p></li></ol>`, []chunk{{"3. x\n4. y\n\n   z", false}}},
		{"table", `<table><thead><tr><th>A</th><th>B</th></tr></thead><tbody><tr><td>1</td><td>2|3<br>x</td></tr><tr><td colspan="2">w</td></tr></tbody></table>`, []chunk{{"| A | B |\n| --- | --- |\n| 1 | 2\\|3<br>x |\n| w |  |", false}}},
		{"code with language", "<pre><code class=\"language-go\">x := 1\nfmt.Println(x)\n</code></pre>", []chunk{{"```go\nx := 1\nfmt.Println(x)\n```", false}}},
		{"code fence outgrows the code", "<pre>a ``` b</pre>", []chunk{{"````\na ``` b\n````", false}}},
		{"unsafe code language is dropped", "<pre><code class=\"language-go;evil\">x</code></pre>", []chunk{{"```\nx\n```", false}}},
		{"details shows its summary and content", `<details><summary>More</summary>text</details>`, []chunk{{"▾ <b>More</b>", false}, {"text", false}}},
		{"quote", `<blockquote><p>a</p><p>b</p></blockquote>`, []chunk{{"> a\n>\n> b", false}}},
		{"rule and definition list", `<hr><dl><dt>T</dt><dd>D</dd></dl>`, []chunk{{"---", false}, {"<b>T</b>", false}, {"D", false}}},
		{"figure", `<figure><img src="a.png" alt="A"><figcaption>cap</figcaption></figure>`, []chunk{{"![A](<a.png>)", false}, {"<i>cap</i>", false}}},
		{"picture falls back to its image", `<picture><source srcset="x.webp"><img src="x.png" alt="X"></picture>`, []chunk{{"![X](<x.png>)", false}}},
	} {
		got, ok := translateHTML(c.html)
		if !ok {
			t.Errorf("%s: refused", c.name)
			continue
		}
		var have []chunk
		for _, ch := range got {
			have = append(have, chunk{ch.md, ch.center})
		}
		if fmt.Sprint(have) != fmt.Sprint(c.want) {
			t.Errorf("%s:\n got  %#v\n want %#v", c.name, have, c.want)
		}
	}
}

// TestTranslateHTMLEmitsNoHTMLBlocks: what the translator makes is Markdown for
// a nested view, which must never itself read as an HTML block (that is how
// translation would recurse): block tags are escaped, and the tags it keeps
// are inline ones that never stand alone on a line.
func TestTranslateHTMLEmitsNoHTMLBlocks(t *testing.T) {
	for _, h := range []string{
		`<div>&lt;div&gt;x&lt;/div&gt;</div>`,
		`<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>`,
		`<b></b><i> </i><br><br> <br>`,
		`<kbd>a</kbd><kbd>b</kbd>`,
		`<p><b>bold</b></p><p><br>after a break</p><p><kbd>K</kbd></p>`,
		`<ul><li><kbd>K</kbd></li></ul><table><tr><td><b>x</b></td></tr></table>`,
	} {
		chunks, ok := translateHTML(h)
		if !ok {
			t.Fatalf("%q refused", h)
		}
		for _, ch := range chunks {
			d := parser.New(markdownExtensions)
			d.Set(ch.md)
			for i := 1; i < d.Len(); i++ {
				if d.Block(i).Kind == parser.HTMLBlock {
					t.Errorf("%q made Markdown that reads as an HTML block: %q", h, ch.md)
				}
			}
		}
	}
}

func TestTranslateHTMLIsBounded(t *testing.T) {
	cases := map[string]string{
		"past the byte cap":   "<p>" + strings.Repeat("x", htmlBlockCap) + "</p>",
		"nesting":             strings.Repeat("<div>", 5000) + "x" + strings.Repeat("</div>", 5000),
		"nodes":               strings.Repeat("<b>x</b>", htmlNodeCap),
		"deep inline nesting": strings.Repeat("<b>", 3000) + "x",
		"output":              strings.Repeat("<p>"+strings.Repeat("y", 1000)+"</p>", htmlOutCap/1000+50),
	}
	for name, html := range cases {
		start := time.Now()
		chunks, ok := translateHTML(html)
		if ok {
			t.Errorf("%s: translated (%d chunks); it is past what a block may cost", name, len(chunks))
		}
		if took := time.Since(start); took > time.Second {
			t.Errorf("%s: took %v to refuse", name, took)
		}
	}
	// A big but honest block is within bounds.
	if _, ok := translateHTML("<ul>" + strings.Repeat("<li>item</li>", 2000) + "</ul>"); !ok {
		t.Error("2000 list items are a normal block")
	}
}

// TestTranslateHTMLNodeBound: 21,000 paragraphs are 42,000 nodes in 170 KB of
// HTML, under the byte, depth and output bounds, so only the node bound can
// refuse them; 9,000 are 18,000 nodes and pass.
func TestTranslateHTMLNodeBound(t *testing.T) {
	if _, ok := translateHTML(strings.Repeat("<p>a</p>", 9_000)); !ok {
		t.Error("18,000 nodes are within the bound")
	}
	if _, ok := translateHTML(strings.Repeat("<p>a</p>", 21_000)); ok {
		t.Error("42,000 nodes are past the bound")
	}
}

// TestHTMLLinkTargetGates: what a link or an image of an HTML block may carry.
// The address ends up in a Markdown link or image the view draws; the app's own
// gates decide what opens or loads, and these are the ones before them.
func TestHTMLLinkTargetGates(t *testing.T) {
	for _, c := range []struct {
		dest string
		link bool
		want string
	}{
		{"https://example.com/a b", true, "https://example.com/a%20b"},
		{"http://example.com", false, "http://example.com"},
		{"mailto:a@b.c", true, "mailto:a@b.c"},
		{"mailto:a@b.c", false, ""}, // an image is never a mail address
		{"#top", true, "#top"},
		{"docs/guide.md", true, "docs/guide.md"},
		{"a<b>c", true, "a%3Cb%3Ec"},
		{"javascript:alert(1)", true, ""},
		{"data:text/html;base64,AAAA", false, ""},
		{"file:///etc/passwd", true, ""},
		{"ftp://example.com/x", true, ""},
		{"", true, ""},
		{"  ", true, ""},
		{"https://example.com/\nx", true, ""},
		{"https://example.com/" + strings.Repeat("a", 2100), true, ""},
		{"https://example.com/" + strings.Repeat("a", 2000), true, "https://example.com/" + strings.Repeat("a", 2000)},
	} {
		if got := htmlLinkTarget(c.dest, c.link); got != c.want {
			t.Errorf("htmlLinkTarget(%.40q, link=%v) = %.60q, want %.60q", c.dest, c.link, got, c.want)
		}
	}
}

func TestHTMLBlockDrawsAsFormattedTextAndCentersWhatItSays(t *testing.T) {
	s, _, root := filesOpsTestShell(t)
	doc := "<p align=\"center\">Hello <b>World</b></p>\n\n<p>left text</p>\n\n<!-- hidden note -->\n\nplain\n"
	mp := newMarkdownPreview(root+"/h.md", root, doc, nil)
	tester := renderRows(t, s, mp, 400)
	texts := strings.Join(tester.Texts(), "\n")
	for _, raw := range []string{"<p", "<b>", "</p>", "align=", "hidden note", "<!--"} {
		if strings.Contains(texts, raw) {
			t.Errorf("raw HTML %q shows: %q", raw, texts)
		}
	}
	centered, ok1 := findContaining(tester, "Hello")
	left, ok2 := findContaining(tester, "left text")
	if !ok1 || !ok2 {
		t.Fatalf("HTML text not drawn: %q", tester.Texts())
	}
	if mid, view := centered.X+centered.W/2, float32(tester.Image().Bounds().Dx())/2; mid < view-6 || mid > view+6 {
		t.Errorf("align=center text sits at %.0f..%.0f, its middle is not the view's (%.0f)", centered.X, centered.X+centered.W, view)
	}
	if left.X > 60 {
		t.Errorf("uncentered HTML text starts at %.0f, as a paragraph does", left.X)
	}
	// What follows the HTML (and the comment) is placed as after any block.
	if _, ok := findContaining(tester, "plain"); !ok {
		t.Errorf("the paragraph after the HTML is missing: %q", tester.Texts())
	}
}

func TestHTMLBlockPastTheBoundsShowsAsText(t *testing.T) {
	s, _, root := filesOpsTestShell(t)
	html := strings.Repeat("<div>", 200) + "deep" + strings.Repeat("</div>", 200)
	mp := newMarkdownPreview(root+"/h.md", root, html+"\n", nil)
	tester := renderRows(t, s, mp, 300)
	if !tester.HasText(html) {
		t.Errorf("HTML past the depth bound shows as its text, as it does without a hook: %q", tester.Texts())
	}
}

func TestHTMLBlockDoesNotRecurse(t *testing.T) {
	s, _, root := filesOpsTestShell(t)
	mp := newMarkdownPreview(root+"/h.md", root, "<p>x</p>\n", nil)
	mp.htmlDepth = 2 // as if inside two nested translations
	tester := renderRows(t, s, mp, 200)
	if !tester.HasText("<p>x</p>") {
		t.Errorf("past the nesting bound an HTML block shows as text: %q", tester.Texts())
	}
}

func TestNormalizeInlineHTMLReadsWhatTheViewDraws(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"plain text untouched", "just text with a < b and `<span>` code\n", "just text with a < b and `<span>` code\n"},
		{"layout tags go, content stays", "a <span style=\"color:red\">b</span> <font size=3>c</font> <abbr title=\"x\">d</abbr>\n", "a b c d\n"},
		{"image becomes Markdown", "see <img src=\"p.png\" alt=\"i\"> here\n", "see ![i](<p.png>) here\n"},
		{"unsafe image keeps its alt", "<img src=\"javascript:x\" alt=\"j[1]\"> <img src=\"data:image/png;base64,AA\" alt=\"d\">\n", "j\\[1\\] d\n"},
		{"anchor becomes a link", "go <a href=\"https://x.dev\">there</a>.\n", "go [there](<https://x.dev>).\n"},
		{"anchor around an image", "<a href=\"https://x.dev\"><img src=\"b.svg\" alt=\"b\"></a>\n", "[![b](<b.svg>)](<https://x.dev>)\n"},
		{"unsafe or missing href leaves the text", "<a href=\"javascript:alert(1)\">x</a> <a name=\"top\">y</a> <a href=\"\">z</a>\n", "x y z\n"},
		{"unclosed anchor leaves the text", "<a href=\"https://x.dev\">open\n", "open\n"},
		{"comments go", "a <!-- note --> b\n", "a  b\n"},
		{"scripts go with their content", "a <script>alert(1)</script>b <style>x{}</style>c <iframe src=x>f</iframe>d\n", "a b c d\n"},
		{"unclosed script drops its tag only", "a <script>b\n", "a b\n"},
		{"formatting stays for the view", "<b>b</b> <kbd>k</kbd> <sub>2</sub> a<br>b\n", "<b>b</b> <kbd>k</kbd> <sub>2</sub> a<br>b\n"},
		{"headings and cells too", "# T <span>x</span>\n\n| a |\n| --- |\n| <img src=\"i.png\" alt=\"I\"> |\n", "# T x\n\n| a |\n| --- |\n| ![I](<i.png>) |\n"},
		{"code is not touched", "```html\n<span>x</span>\n<img src=\"a.png\">\n```\n\n    <b>indented</b>\n", "```html\n<span>x</span>\n<img src=\"a.png\">\n```\n\n    <b>indented</b>\n"},
		{"HTML blocks are left to their hook", "<div>\n<img src=\"a.png\">\n</div>\n\ntext <span>s</span>\n", "<div>\n<img src=\"a.png\">\n</div>\n\ntext s\n"},
	} {
		if got := normalizeInlineHTML(c.in); got != c.want {
			t.Errorf("%s:\n in   %q\n got  %q\n want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestPreviewShowsNormalizedInlineHTML(t *testing.T) {
	s, _, root := filesOpsTestShell(t)
	mp := newMarkdownPreview(root+"/n.md", root, "A <span style=\"x\">span</span> and <a href=\"https://x.dev\">a link</a> <!-- no -->end.\n", nil)
	tester := renderRows(t, s, mp, 200)
	texts := strings.Join(tester.Texts(), "\n")
	if !strings.Contains(texts, "A span and a link end.") || strings.Contains(texts, "<span") || strings.Contains(texts, "<!--") {
		t.Errorf("inline HTML reads as the text it means: %q", texts)
	}
}

// TestHTMLBlocksWorkAsRowsOfALongDocument: in a document cut into rows, an HTML
// block is a row like any other and draws from its translation.
func TestHTMLBlocksWorkAsRowsOfALongDocument(t *testing.T) {
	s, _, root := filesOpsTestShell(t)
	var b strings.Builder
	for i := range 2000 {
		fmt.Fprintf(&b, "paragraph %d\n\n", i)
		if i == 1000 {
			b.WriteString("<h2 align=\"center\">Middle <i>title</i></h2>\n\n")
		}
	}
	mp := newMarkdownPreview(root+"/long.md", root, b.String(), nil)
	if mp.rows == nil {
		t.Fatal("the document must show as rows")
	}
	tester := ui.NewTester(func(c *ui.Context) { s.markdownBody(c, mp) }, 760, 400)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	for range 2 {
		tester.Frame()
	}
	mp.list.ScrollTo(1001, ui.Start)
	for range 3 {
		tester.Frame()
	}
	if _, ok := findContaining(tester, "Middle"); !ok {
		t.Errorf("the HTML row did not draw: %q", tester.Texts())
	}
	if tester.HasText("<h2") {
		t.Errorf("raw HTML shows in a row: %q", tester.Texts())
	}
}
