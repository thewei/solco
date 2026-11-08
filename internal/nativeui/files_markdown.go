package nativeui

/**
 * [INPUT]: filesview.PreviewFlavor/ImagePreviewCap/MaxNativeImagePixels、rightPanel.currentRoot、codeEditorTheme/gdCodeFont/Typography、safeExternalURL、MyGo ui/markdown
 * [OUTPUT]: previewTier/planPreviewTier（每种文件归哪一级）、markdownPreview（一份已读的 Markdown + 渲染选项，Rendered 视图的全部状态）、markdownTheme、classifyMarkdownLink/markdownPreview.follow（链接只放行外链/本页标题/项目内文件）、markdownImageLoader（文档里的图片：只读项目内、字节与像素有预算）、Shell.markdownBody
 * [POS]: 产品决策 2026-11-08「Files 预览原生优先」：Markdown 用 MyGo 官方 ui/markdown 原生渲染（≤ markdownRenderCap，更大的读源码），不再经 WebView；文档里写的链接与图片路径都是不可信输入，所以全部限定在项目根内，远程图片不加载
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
	"github.com/thewei/solco-gitkit/filesview"
)

// markdownRenderCap is the largest Markdown file the preview formats as
// text. The view builds every block of the document in a frame (it does not
// virtualize), so the cost of opening grows with the file: 176 KB of dense
// decision log takes ~0.2 s the first frame and ~2 ms after, a 4 MB file
// takes seconds. Past the cap the file reads as source in the editor, which
// virtualizes and opens at any size (2026-11-08).
const markdownRenderCap = 256 << 10

// markdownImageCap bounds one image a document shows, in bytes.
const markdownImageCap = 8 << 20

// previewTier is who shows a selected file.
type previewTier int

const (
	tierText          previewTier = iota // the code view: text, code, SVG source, Markdown past the cap
	tierMarkdown                         // the code view and the formatted view, one click apart
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
			// SVG is XML source: it reads natively as text.
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
	dark       bool // the appearance opts.Theme was made for
	// openFile shows another project file; openExternal hands a web address to
	// the system. Both are fields so a test can watch them.
	openFile     func(path string)
	openExternal func(url string)
}

// newMarkdownPreview prepares file's text for the formatted view. openFile is
// what a link to another project file does.
func newMarkdownPreview(file, root, src string, openFile func(string)) *markdownPreview {
	mp := &markdownPreview{file: file, root: root, src: src, openFile: openFile}
	mp.openExternal = func(u string) { go func() { _ = mygo.Shell.OpenExternal(u) }() }
	mp.opts = &markdown.Options{
		LoadImage: markdownImageLoader(root, file),
		OnLink:    mp.follow,
		LinkMenu:  mp.linkMenu,
	}
	return mp
}

// options is the view's Options for this frame: the same value every frame,
// its Theme swapped only when the appearance flips (the view lays every
// paragraph out again whenever its theme value changes).
func (mp *markdownPreview) options(t *ui.Theme) *markdown.Options {
	if mp.opts.Theme == nil || mp.dark != t.Dark {
		mp.opts.Theme, mp.dark = markdownTheme(t), t.Dark
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

// markdownBody is the formatted view: the document on the center's paper,
// scrolling, selectable (⌘C copies text, HTML and Markdown).
func (s *Shell) markdownBody(c *ui.Context, mp *markdownPreview) {
	sp := Spacing()
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(sp.XL, sp.XL+sp.L, sp.XL*2).Children(func() {
			markdown.View(c, mp.src, mp.options(c.Theme()))
		})
	})
}
