package nativeui

/**
 * [INPUT]: github.com/go-tex/math（TeX 数学 → SVG，内嵌 STIX Two Math）、tuios.dev/sketch + sketch/raster（Mermaid → SVG → 位图，纯 Go）、MyGo ui.ParseSVG / Painter.Icon / ui.NewBitmap / markdown.Options.{Math,InlineMath,Code}、markdownPreview.fullTheme（files_markdown_find.go）
 * [OUTPUT]: typesetTeX（有界、带缓存：长度/\def 拒绝、400 ms 预算、结果 SVG 与尺寸上限）、markdownPreview.mathBlock/mathFence/inlineMath（块级 $$…$$ 与 ```math、行内 $…$）、markdownPreview.mermaidBlock（后台线程画、位图按主题与源码缓存、没画出来时给原因并留源码）、drawnCode（Mermaid/math 围栏画成图，不是可搜索的文字）
 * [POS]: 产品决策 2026-11-08「Files 预览原生优先」补充（用户：Markdown 里的原始 HTML / 数学公式 / Mermaid 要支持）：数学与图都在本机用纯 Go 画，不经 WebView、不联网；文档里的 TeX 与 Mermaid 是不可信输入，所以每一步都有上限，画不出来就退回源码
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"errors"
	"fmt"
	"image/color"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	texmath "github.com/go-tex/math"
	"tuios.dev/sketch"
	"tuios.dev/sketch/raster"
)

// What one formula may cost. The TeX of a document is untrusted: the engine
// expands \def macros (a few bytes of nested macros ask for exponentially many
// tokens), so a formula that defines any is refused, and the rest is bounded in
// size and, when something unforeseen is slow, in time.
const (
	texCap      = 4 << 10   // bytes of TeX in one formula
	texSVGCap   = 256 << 10 // bytes of the SVG it typesets to
	texExtent   = 4096      // DIPs, either side of the drawn formula
	texBudget   = 400 * time.Millisecond
	texMemoCap  = 512 // formulas kept
	inlineMathK = 1.1 // inline formulas are set a little larger than the text: STIX's x-height is small beside the sans text around it
	blockMathK  = 1.3 // display formulas, larger still
)

// texUnsafe matches what a formula may not contain: macro definitions and the
// primitives around them (only \def is implemented by the engine; refusing the
// family keeps it so when the engine grows).
var texUnsafe = regexp.MustCompile(`\\(?:def|edef|gdef|xdef|let|futurelet|csname|expandafter|newcommand|renewcommand|providecommand|newenvironment|renewenvironment|catcode|input|include|write|read|openin|openout)(?:[^a-zA-Z]|$)`)

var texEngine = sync.OnceValues(func() (*texmath.Renderer, error) {
	return texmath.New(texmath.DefaultFont())
})

type texKey struct {
	tex     string
	px      int
	display bool
}

// texFormula is a typeset formula: the SVG of its glyphs and the box it takes,
// in DIPs, with its depth below the baseline.
type texFormula struct {
	svg                  *ui.SVG
	width, height, depth float32
}

// texMemo remembers formulas, and the ones that cannot be drawn (nil).
var texMemo = struct {
	sync.Mutex
	items map[texKey]*texFormula
}{items: map[texKey]*texFormula{}}

// typesetTeX typesets tex at px DIPs of text size, in display style when
// display; nil when the formula is refused, takes too long or is not TeX the
// engine reads. It is cheap for what it has seen.
func typesetTeX(tex string, px int, display bool) *texFormula {
	tex = strings.TrimSpace(tex)
	key := texKey{tex, px, display}
	texMemo.Lock()
	f, ok := texMemo.items[key]
	texMemo.Unlock()
	if ok {
		return f
	}
	f = typesetTeXNow(tex, px, display)
	texMemo.Lock()
	if len(texMemo.items) >= texMemoCap {
		clear(texMemo.items)
	}
	texMemo.items[key] = f
	texMemo.Unlock()
	return f
}

func typesetTeXNow(tex string, px int, display bool) *texFormula {
	if tex == "" || len(tex) > texCap || px < 4 || px > 256 || texUnsafe.MatchString(tex) {
		return nil
	}
	r, err := texEngine()
	if err != nil {
		return nil
	}
	type result struct {
		svg string
		m   texmath.Metrics
		err error
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if recover() != nil {
				done <- result{err: errors.New("typesetting failed")}
			}
		}()
		var res result
		if display {
			res.svg, res.m, res.err = r.RenderDisplaySVGMetrics(tex, px)
		} else {
			res.svg, res.m, res.err = r.RenderSVGMetrics(tex, px)
		}
		done <- res
	}()
	var res result
	select {
	case res = <-done:
	case <-time.After(texBudget):
		return nil
	}
	m := res.m
	if res.err != nil || len(res.svg) > texSVGCap || !finitePositive(m.Width) || !finitePositive(m.Height+m.Depth) ||
		m.Width > texExtent || m.Height+m.Depth > texExtent || m.Depth < 0 || m.Height < 0 {
		return nil
	}
	svg, err := ui.ParseSVG([]byte(res.svg))
	if err != nil {
		return nil
	}
	return &texFormula{svg: svg, width: float32(m.Width), height: float32(m.Height), depth: float32(m.Depth)}
}

func finitePositive(v float64) bool { return v > 0 && !math.IsInf(v, 0) && !math.IsNaN(v) }

// mathPx is the text size a formula is set at: that of the text around it,
// scaled.
func mathPx(size, k float32) int { return int(math.Round(float64(size * k))) }

// inlineMath makes math within text an object of it, on the baseline: the
// view asks again whenever its paragraph changes, which the memo makes cheap.
// A formula the engine will not typeset shows as its TeX.
func (mp *markdownPreview) inlineMath(tex string, size float32) (markdown.InlineObject, bool) {
	f := typesetTeX(tex, mathPx(size, inlineMathK), false)
	if f == nil {
		return markdown.InlineObject{}, false
	}
	return markdown.InlineObject{Width: f.width, Height: f.height, Depth: f.depth, Draw: func(p *ui.Painter, r ui.Rect) {
		p.Icon(f.svg, r, p.Theme().Text)
	}}, true
}

// mathBlock builds a block of display math: its typeset formula, centered, in a
// box that scrolls sideways when the formula is wider than the view. A formula
// it cannot typeset shows as its TeX, as the view shows math it has no hook for.
func (mp *markdownPreview) mathBlock(c *ui.Context, tex string) {
	th := mp.fullTheme(c.Theme())
	f := typesetTeX(tex, mathPx(th.FontSize, blockMathK), true)
	if f == nil {
		ui.ScrollHorizontal(c).AlignSelf(ui.Stretch).Children(func() {
			ui.Text(c, strings.TrimSpace(tex)).Font(th.CodeFont).FontSize(th.FontSize*th.CodeSize).TextColor(th.Muted).NoWrap().Margin(0, ui.Auto)
		})
		return
	}
	ui.Row(c).AlignSelf(ui.Stretch).Justify(ui.Center).Padding(th.FontSize*0.25, 0).Children(func() {
		// As large as it is, no wider than the view (a formula wider than the
		// view shrinks to it: a box that scrolls cannot center what is narrower).
		ui.Icon(c, f.svg).Width(f.width).MaxWidthPercent(100).AspectRatio(f.width / (f.height + f.depth))
	})
}

// mathFence is a ```math fence, which GitHub draws as display math.
func (mp *markdownPreview) mathFence(c *ui.Context, cb *markdown.CodeBlockView) {
	mp.mathBlock(c, cb.Code())
}

// drawnCode reports whether a code block of this language is drawn as a
// picture rather than shown as lines of code: what a search cannot find in.
func drawnCode(lang string) bool {
	return strings.EqualFold(lang, "mermaid") || strings.EqualFold(lang, "math")
}

// What one diagram may cost: the source is untrusted. sketch bounds its own
// layout work (these limits are tighter than its defaults) and recovers from
// panics; the picture is bounded in pixels.
const (
	mermaidSource = 20_000    // characters of diagram source
	mermaidSVG    = 1_500_000 // characters of the SVG it draws
	mermaidEdges  = 600       // edges of a flowchart
	mermaidPixels = 12_000_000
	mermaidScale  = 2 // the picture is drawn at twice its size: crisp on a Retina display
	mermaidMemo   = 48
)

// mermaidDiagram is a diagram being drawn, or drawn.
type mermaidDiagram struct {
	done bool
	bmp  *ui.Bitmap
	w, h float32 // DIPs
	err  string  // why it could not be drawn
}

var mermaidCache = struct {
	sync.Mutex
	items map[string]*mermaidDiagram
}{items: map[string]*mermaidDiagram{}}

// diagramOf returns the diagram of src in t's colors, starting to draw it on a
// goroutine the first time; the window is asked for a frame once it is drawn.
func diagramOf(src string, t *ui.Theme, services ui.Services) mermaidDiagram {
	key := fmt.Sprintf("%t|%v|%v|%v|%v|%v|%s", t.Dark, t.Text, t.TextMuted, t.Surface, t.Border, t.Accent, src)
	mermaidCache.Lock()
	d, ok := mermaidCache.items[key]
	if !ok {
		if len(mermaidCache.items) >= mermaidMemo {
			clear(mermaidCache.items)
		}
		d = &mermaidDiagram{}
		mermaidCache.items[key] = d
		theme := *t
		go func() {
			bmp, w, h, err := drawMermaid(src, &theme)
			mermaidCache.Lock()
			d.bmp, d.w, d.h, d.err, d.done = bmp, w, h, err, true
			mermaidCache.Unlock()
			services.Invalidate()
		}()
	}
	snapshot := *d
	mermaidCache.Unlock()
	return snapshot
}

// drawMermaid draws src as a bitmap of Mermaid's diagram, or says why not.
func drawMermaid(src string, t *ui.Theme) (bmp *ui.Bitmap, w, h float32, why string) {
	defer func() {
		if recover() != nil {
			bmp, w, h, why = nil, 0, 0, "the diagram could not be drawn"
		}
	}()
	off := false
	d, err := sketch.Render(src, sketch.RenderOptions{
		Limit: mermaidSource, OutputLimit: mermaidSVG, MaxEdges: mermaidEdges,
		Links: &off, Images: &off,
		Theme: mermaidTheme(t),
	})
	if err != nil {
		return nil, 0, 0, mermaidReason(err)
	}
	img, err := raster.Draw(d.Data, raster.Options{Scale: mermaidScale, Readable: true, MaxPixels: mermaidPixels})
	if err != nil {
		return nil, 0, 0, "the diagram is too large to draw"
	}
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		return nil, 0, 0, "the diagram is empty"
	}
	return ui.NewBitmap(img), float32(b.Dx()) / mermaidScale, float32(b.Dy()) / mermaidScale, ""
}

// mermaidReason is the part of a sketch error to show the reader: its message.
func mermaidReason(err error) string {
	var se *sketch.Error
	if errors.As(err, &se) && se.Message != "" {
		msg := se.Message
		if len(msg) > 200 {
			msg = msg[:200] + "…"
		}
		return msg
	}
	return "the diagram could not be drawn"
}

// mermaidTheme is the diagram's palette: the window theme's colors, with the
// series colors of Mermaid's light or dark set.
func mermaidTheme(t *ui.Theme) *sketch.Theme {
	name := "pele"
	if t.Dark {
		name = "flexoki-dark"
	}
	base, _ := sketch.ThemeNamed(name)
	th := *base
	std := func(c ui.Color) color.Color { return color.NRGBA{R: c.R, G: c.G, B: c.B, A: c.A} }
	th.Bg, th.Fg, th.Muted = std(t.Background), std(t.Text), std(t.TextMuted)
	th.Line = std(t.Text.Alpha(0.7).Over(t.Background))
	th.Surface, th.SurfaceAlt, th.Border = std(t.Surface), std(t.SurfaceHover), std(t.Border)
	th.Accent, th.Critical = std(t.Accent), std(t.Danger)
	if t.Radius > 0 {
		th.Radius = float64(t.Radius)
	}
	return &th
}

// mermaidBlock builds a ```mermaid fence: a placeholder while the diagram is
// drawn on another goroutine, then the diagram, as wide as it is and no wider
// than the view; a diagram that cannot be drawn shows why above its source.
func (mp *markdownPreview) mermaidBlock(c *ui.Context, cb *markdown.CodeBlockView) {
	th := mp.fullTheme(c.Theme())
	d := diagramOf(cb.Code(), c.Theme(), c.Services())
	switch {
	case !d.done:
		ui.Row(c).AlignSelf(ui.Stretch).Height(th.FontSize * 6).AlignItems(ui.Center).Justify(ui.Center).Radius(th.CodeBlockRadius).Background(th.CodeBlockBackground).Children(func() {
			ui.Text(c, "Drawing diagram…").TextColor(th.Muted).FontSize(th.FontSize * 0.875)
		})
	case d.err != "":
		ui.Column(c).AlignSelf(ui.Stretch).Gap(th.FontSize * 0.35).Children(func() {
			ui.Text(c, "Mermaid: "+d.err).TextColor(th.Muted).FontSize(th.FontSize * 0.8125)
			markdown.DefaultCodeBlock(c, cb)
		})
	default:
		ui.Row(c).AlignSelf(ui.Stretch).Justify(ui.Center).Children(func() {
			ui.Image(c, d.bmp).Width(d.w).MaxWidthPercent(100).AspectRatio(d.w / d.h).Label("Mermaid diagram")
		})
	}
}
