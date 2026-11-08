package nativeui

/**
 * [INPUT]: filesview.PreviewFlavor/ImagePreviewCap/MaxNativeImagePixels、rightPanel.currentRoot、codeEditorTheme/gdCodeFont/Typography、safeExternalURL、MyGo ui/markdown、files_markdown_rows.go（大文档分行）、files_markdown_find.go（查找）、files_markdown_rich.go / files_markdown_html.go（数学、Mermaid、原始 HTML）
 * [OUTPUT]: previewTier/planPreviewTier（每种文件归哪一级）、markdownPreview（一份已读的 Markdown + 渲染选项 + 分行/查找状态，Rendered 视图的全部状态）、markdownTheme、classifyMarkdownLink/markdownPreview.follow（链接只放行外链/本页标题/项目内文件）、markdownImageLoader（文档里的图片：只读项目内、字节与像素有预算）、Shell.markdownBody（⌘F 查找条 + 一个可选择的容器：右键有 Copy / Select All 与路径助手）
 * [POS]: 产品决策 2026-11-08「Files 预览原生优先」：Markdown 用 MyGo 官方 ui/markdown 原生渲染（≤ markdownRenderCap = 2 MiB，更大的读源码），不再经 WebView；块多的文档按块分行只构建可见行；文档里写的链接与图片路径都是不可信输入，所以全部限定在项目根内，远程图片不加载
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"bytes"
	"errors"
	"image"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
	"github.com/thewei/solco-gitkit/filesview"
)

// markdownRenderCap is the largest Markdown file the preview formats as
// text (2026-11-08, the user: "上限调大一点，2m 大一些都没关系"). The view
// builds every block of a document in each frame, so a document of many
// blocks shows as rows of a virtual list (files_markdown_rows.go) and costs
// what is in view; past the cap the file reads as source in the editor, which
// virtualizes too and opens at any size.
const markdownRenderCap = 2 << 20

// svgRenderCap is the largest SVG the preview draws (2026-11-08, "Image |
// Source"): MyGo draws an SVG's shapes every frame, so a drawing of this size
// is already a heavy one; past it the file reads as source.
const svgRenderCap = 1 << 20

// markdownImageCap bounds one image a document shows, in bytes.
const markdownImageCap = 8 << 20

// previewTier is who shows a selected file.
type previewTier int

const (
	tierText          previewTier = iota // the code view: text, code, Markdown and SVG past their caps
	tierMarkdown                         // the code view and the formatted view, one click apart
	tierSVG                              // the code view and the drawing, one click apart
	tierImage                            // a native bitmap
	tierWebImage                         // AVIF/ICO: the only formats the engine decodes and Go does not
	tierImageTooLarge                    // an AVIF/ICO past ImagePreviewCap
)

// planPreviewTier routes a file by name and size. Only what no native view
// can show ever reaches a page.
func planPreviewTier(name string, size int64) previewTier {
	ext := strings.ToLower(filepath.Ext(name))
	switch filesview.PreviewFlavor(name) {
	case filesview.FlavorMarkdown:
		if size <= markdownRenderCap {
			return tierMarkdown
		}
	case filesview.FlavorImage:
		switch {
		case ext == ".svg":
			if size <= svgRenderCap {
				return tierSVG
			}
			// too heavy to draw each frame: XML source
		case nativeImageDecodable(ext):
			return tierImage
		case size <= filesview.ImagePreviewCap:
			return tierWebImage
		default:
			return tierImageTooLarge
		}
	}
	return tierText
}

// hasRenderedView reports whether the name says the file has a formatted or
// drawn view beside its source (whether it is shown depends on its size).
func hasRenderedView(path string) bool {
	return filesview.PreviewFlavor(path) == filesview.FlavorMarkdown || strings.EqualFold(filepath.Ext(path), ".svg")
}

// nativeImageDecodable reports whether Go can decode the format (the decoder
// set ui's bitmap support registers: PNG, JPEG, GIF, WebP, BMP).
func nativeImageDecodable(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		return true
	}
	return false
}

// markdownPreview is one open Markdown file's formatted view: its text, and
// the Options the view keeps between frames (its image cache and the anchor
// to scroll to), which must outlive the frame and never be copied.
type markdownPreview struct {
	file, root string
	src        string
	opts       *markdown.Options
	dark       bool      // the appearance opts.Theme was made for
	spacing    mdSpacing // the block spacing of that theme, for the rows
	// rows is the document cut into the rows of a virtual list, when it has
	// many blocks (or once the find bar needed to scroll it); nil is one view.
	rows    *mdRows
	list    ui.ListState
	find    mdFind
	marking *rowMark // the row being built, for its code blocks
	// htmlMemo keeps the translation of each HTML block by its text; htmlDepth
	// is how many HTML blocks the one being built sits in.
	htmlMemo  map[string]htmlResult
	htmlDepth int
	// openFile shows another project file; openExternal hands a web address to
	// the system. Both are fields so a test can watch them.
	openFile     func(path string)
	openExternal func(url string)
}

// newMarkdownPreview prepares file's text for the formatted view. openFile is
// what a link to another project file does.
func newMarkdownPreview(file, root, src string, openFile func(string)) *markdownPreview {
	src = normalizeInlineHTML(src)
	mp := &markdownPreview{file: file, root: root, src: src, openFile: openFile}
	mp.openExternal = func(u string) { go func() { _ = mygo.Shell.OpenExternal(u) }() }
	mp.opts = mp.newOptions()
	mp.rows = planMarkdownRows(src, false)
	if mp.rows != nil && mp.rows.heavy {
		return nil // more than a frame can carry: the file reads as source
	}
	return mp
}

// newOptions is the Options of one view of the document. The selection is left
// to the container around the view (markdownBody), which is one across all
// the rows of a long document.
func (mp *markdownPreview) newOptions() *markdown.Options {
	return &markdown.Options{
		Extensions:     markdownExtensions,
		ShareSelection: true,
		LoadImage:      markdownImageLoader(mp.root, mp.file),
		OnLink:         mp.follow,
		LinkMenu:       mp.linkMenu,
		CodeBlock:      mp.codeBlock,
		Code: map[string]func(*ui.Context, *markdown.CodeBlockView){
			"mermaid": mp.mermaidBlock,
			"math":    mp.mathFence,
		},
		Math:       mp.mathBlock,
		InlineMath: mp.inlineMath,
		HTML:       mp.htmlBlock,
	}
}

// options is the view's Options for this frame: the same value every frame,
// its Theme swapped only when the appearance flips (the view lays every
// paragraph out again whenever its theme value changes).
func (mp *markdownPreview) options(t *ui.Theme) *markdown.Options {
	if mp.opts.Theme == nil || mp.dark != t.Dark {
		mp.opts.Theme, mp.dark = markdownTheme(t), t.Dark
		mp.spacing = markdownSpacing(markdown.DefaultTheme(t), mp.opts.Theme.FontSize)
	}
	return mp.opts
}

// markdownTheme is the document's look: the window theme's fonts and colors
// (the view fills what is left zero from it, so contrast and appearance
// follow on their own), the section size to read at, GitHub's rule under
// the two big headings, and the review's colors in code blocks.
func markdownTheme(t *ui.Theme) *markdown.Theme {
	th := &markdown.Theme{
		FontSize:   Typography().Section,
		CodeFont:   gdCodeFont(),
		CodeColors: codeEditorTheme(t),
	}
	th.Headings[0].Rule, th.Headings[1].Rule = true, true
	return th
}

// markdownLinkKind is what a link in a document opens.
type markdownLinkKind int

const (
	linkNone     markdownLinkKind = iota // refused: another scheme, outside the project, not there
	linkExternal                         // a web or mail address, for the system
	linkAnchor                           // a heading of this page
	linkFile                             // a file of the project
)

// classifyMarkdownLink decides what dest, a link of the document file, opens.
// The text of a document is untrusted: only web and mail addresses leave the
// app, only files under root open inside it.
func classifyMarkdownLink(root, file, dest string) (markdownLinkKind, string) {
	dest = strings.TrimSpace(dest)
	switch {
	case dest == "":
		return linkNone, ""
	case strings.HasPrefix(dest, "#"):
		return linkAnchor, dest
	case safeExternalURL(dest):
		return linkExternal, dest
	}
	path, ok := resolveMarkdownTarget(root, filepath.Dir(file), dest)
	if !ok {
		return linkNone, ""
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return linkNone, ""
	}
	return linkFile, path
}

// follow is a click on a link of the document.
func (mp *markdownPreview) follow(dest string) {
	switch kind, target := classifyMarkdownLink(mp.root, mp.file, dest); kind {
	case linkExternal:
		mp.openExternal(target)
	case linkAnchor:
		if mp.rows != nil {
			if row, ok := mp.rows.anchorRow(target); ok {
				mp.list.ScrollTo(row, ui.Start)
			}
			return
		}
		mp.opts.Anchor = target
		mp.opts.AnchorVersion++ // the same heading asked for twice scrolls twice
	case linkFile:
		mp.openFile(target)
	}
}

// linkMenu is the context menu of a link: Open and Copy for what the system
// opens, then the text's own Copy and Select All.
func (mp *markdownPreview) linkMenu(m *ui.Menu, dest string) {
	if kind, target := classifyMarkdownLink(mp.root, mp.file, dest); kind == linkExternal {
		if m.Item("Open Link").Chosen() {
			mp.openExternal(target)
		}
		if m.Item("Copy Link").Chosen() {
			mygo.Clipboard.WriteText(target)
		}
		m.Separator()
	}
	m.EditItems()
}

// resolveMarkdownTarget turns a link or image address of the document in dir
// into a path under root: relative to dir, or to root when it starts with a
// slash (how a repository's own links read). Any scheme, host, path that
// leaves the project, symlink that does, or missing file is refused.
func resolveMarkdownTarget(root, dir, dest string) (string, bool) {
	if root == "" || strings.ContainsRune(dest, 0) {
		return "", false
	}
	u, err := url.Parse(strings.TrimSpace(dest))
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" {
		return "", false
	}
	rel := filepath.FromSlash(u.Path)
	path := filepath.Join(dir, rel)
	if filepath.IsAbs(rel) {
		path = filepath.Join(root, rel)
	}
	if !withinRoot(root, path) {
		return "", false
	}
	return path, true
}

// withinRoot reports whether path, with its symlinks resolved, is root or
// under it; a path that does not exist is not.
func withinRoot(root, path string) bool {
	realRoot, err1 := filepath.EvalSymlinks(root)
	realPath, err2 := filepath.EvalSymlinks(path)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(realRoot, realPath)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// markdownImageLoader loads the images of the document file for the view,
// on the view's own goroutines. It reads project files only, up to
// markdownImageCap bytes and filesview.MaxNativeImagePixels pixels; the
// network is never touched, so a remote image shows as its description.
func markdownImageLoader(root, file string) func(string) (ui.ImageSource, error) {
	dir := filepath.Dir(file)
	return func(dest string) (ui.ImageSource, error) {
		path, ok := resolveMarkdownTarget(root, dir, dest)
		if !ok {
			return nil, os.ErrNotExist
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > markdownImageCap {
			return nil, errors.New("not a project image within the preview budget")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		// A failed decode must return a nil interface, not a typed nil
		// pointer inside one: the view draws whatever non-nil source it gets.
		if strings.EqualFold(filepath.Ext(path), ".svg") {
			svg, err := ui.ParseSVG(raw)
			if err != nil {
				return nil, err
			}
			return svg, nil
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		if int64(cfg.Width)*int64(cfg.Height) > filesview.MaxNativeImagePixels {
			return nil, filesview.ErrDimensionsTooLarge
		}
		bitmap, err := ui.DecodeBitmap(raw)
		if err != nil {
			return nil, err
		}
		return bitmap, nil
	}
}

// markdownBody is the formatted view: the find bar over the document on the
// center's paper, scrolling, selectable (⌘C copies the text) with the path
// helpers in its right-click menu. A document of many blocks is a virtual list
// of its rows, one selection across the rows in view.
func (s *Shell) markdownBody(c *ui.Context, mp *markdownPreview) {
	sp := Spacing()
	mp.findFrame(c)
	t := c.Theme()
	opts := mp.options(t)
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Selectable().ContextMenu(func(m *ui.Menu) {
		m.EditItems()
		m.Separator()
		s.filesPathItems(m, mp.file, true)
	}).Children(func() {
		if mp.rows != nil {
			rows := mp.rows
			ui.List(c, &mp.list, len(rows.rows), func(i int) {
				top := rows.top(i, mp.spacing)
				if top > 0 && rows.rows[i].kind == parser.ThematicBreak {
					// The view's own rule has margins of its own, which the room
					// it is given among its siblings replaces: so does this one.
					ui.Box(c).Height(1).Background(opts.Theme.Rule).AlignSelf(ui.Stretch).Margin(top, 0, 0, 0)
					return
				}
				el := mp.buildRow(c, i, opts)
				if top > 0 {
					el.Margin(top, 0, 0, 0)
				}
			}).FillWidth().Grow(1).MinHeight(0).Padding(sp.XL, sp.XL+sp.L, sp.XL*2)
			return
		}
		ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().Padding(sp.XL, sp.XL+sp.L, sp.XL*2).Children(func() {
				markdown.View(c, mp.src, opts)
			})
		})
	})
}
