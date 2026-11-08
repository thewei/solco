package nativeui

/**
 * [INPUT]: rightPanel.selectedFilePath/currentRoot/markdownSource、planPreviewTier/newMarkdownPreview（files_markdown.go）、filesview.ReadText/DecodeImageBounded、ui/editor（只读代码视图）、stdlib image 解码 + ui.NewBitmap
 * [OUTPUT]: Files 预览的分级计划（文件名×大小 → previewTier）、原生代码视图（editor.View，Buffer 离 UI lane 构建）、Markdown 渲染视图的挂载（与代码视图同一次读取、开关不重读；文档里指向项目内文件的链接走 openLinkedFile：开成 Detail 卡并展开树里的上层文件夹）、原生 ui.Image 视图（像素预算）、查看条（大小/截断/Markdown 渲染⇄源码；文件名由 Detail 卡头部给出，条里不再重复）与失败真实态、filesWebviewFill（AVIF/ICO 那页的 WebView 底色 = Detail 卡纸色）、clearNativeFilesPreview 清理点
 * [POS]: 产品决策 2026-11-08「Files 预览原生优先」：文本/代码/Markdown/常见图片全在原生视图里，只有 AVIF/ICO 这两种 Go 解不了的格式才交给一页几百字节的 WebView（files_webview.go）；读取与解码全部离 UI lane，ui.NewBitmap 与 editor.State 回 UI lane；不新增 Herdr 方法
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/thewei/solco-gitkit/filesview"
)

// openFilePreview records the tree selection and routes it through the
// preview plan.
func (s *Shell) openFilePreview(path string) {
	st := &s.rightPanel
	if st.reveal != nil && st.reveal.path != path {
		st.reveal = nil // a hit's line belongs to its own file only
	}
	if st.selectedFilePath == path {
		return
	}
	st.selectedFilePath = path
	s.planFilePreview(path)
}

// planFilePreview picks the selection's tier (planPreviewTier). The size
// probe runs off the render closure; the plan lands on the UI lane.
func (s *Shell) planFilePreview(path string) {
	st := &s.rightPanel
	s.clearNativeFilesPreview()
	root := st.currentRoot
	if s.win == nil {
		// Headless (tests): probe and decide inline like requestDirectory.
		size, flavor := probePreview(path)
		s.applyPreviewPlan(path, root, size, flavor)
		return
	}
	go func() {
		size, flavor := probePreview(path)
		s.callOnWindow(func() { s.applyPreviewPlan(path, root, size, flavor) })
	}()
}

// setMarkdownSource switches the sticky Rendered/Source choice. A small
// Markdown file holds both views from one read, so the switch only changes
// which one the body builds; a line a search hit asked for lands now if the
// editor is the view the switch brought up.
func (s *Shell) setMarkdownSource(source bool) {
	st := &s.rightPanel
	if st.markdownSource == source {
		return
	}
	st.markdownSource = source
	s.applyFilesReveal()
}

// probePreview stats the file and reads its flavor off the UI lane.
func probePreview(path string) (int64, filesview.Flavor) {
	var size int64
	if info, err := os.Stat(path); err == nil {
		size = info.Size()
	}
	return size, filesview.PreviewFlavor(path)
}

// applyPreviewPlan routes the selection to its tier (UI lane). A failed
// stat (file just removed) still plans on the name alone; the tiers surface
// their own read errors.
func (s *Shell) applyPreviewPlan(path, root string, size int64, flavor filesview.Flavor) {
	st := &s.rightPanel
	if st.currentRoot != root || st.selectedFilePath != path {
		return // stale: the user moved on while the probe ran
	}
	st.previewSize = size
	st.previewPlanned = path
	switch planPreviewTier(path, size) {
	case tierImage:
		s.beginNativeImage(path)
	case tierWebImage:
		s.beginWebImage(path)
	case tierImageTooLarge:
		s.claimNativePreview(path)
		st.nativeImageFailed = "This image is too large to preview. Reveal it in Finder to open it."
	default: // tierText, tierMarkdown
		s.beginNativeText(path, flavor)
	}
}

// claimNativePreview hands the center to the native tier for path with
// every native slot empty.
func (s *Shell) claimNativePreview(path string) {
	st := &s.rightPanel
	if st.webImage {
		st.webImage = false
		s.dropFilesWebview() // the page and its engine process go with the image
	}
	st.nativePreviewPath = path
	st.nativeText = nil
	st.nativeCode = nil
	st.nativeMarkdown = nil
	st.nativeTextLoading = false
	st.nativeTextFailed = ""
	st.nativeImage = nil
	st.nativeImageLoading = false
	st.nativeImageFailed = ""
}

// beginNativeText loads a text file into the code view; a Markdown file
// under markdownRenderCap also gets its formatted view from the same read.
// The read and the editor Buffer (a rope, safe from any goroutine) build off
// the UI lane; the State that shows it is made on it.
func (s *Shell) beginNativeText(path string, flavor filesview.Flavor) {
	st := &s.rightPanel
	s.claimNativePreview(path)
	st.nativeTextLoading = true
	gen := st.filesGen.Load()
	root := st.currentRoot
	name := filepath.Base(path)
	render := planPreviewTier(path, st.previewSize) == tierMarkdown
	load := func() (filesview.Preview, *editor.Buffer, error) {
		preview, err := filesview.ReadText(path, root)
		if err != nil || preview.IsBinary {
			return preview, nil, err
		}
		buf := editor.NewBuffer(preview.Content)
		buf.SetLanguage(editor.LanguageFor(name))
		if !render {
			preview.Content = "" // the Buffer holds the text now
		}
		return preview, buf, nil
	}
	apply := func(preview filesview.Preview, buf *editor.Buffer, err error) {
		if gen != st.filesGen.Load() || st.currentRoot != root || st.selectedFilePath != path {
			return
		}
		st.nativeTextLoading = false
		if err != nil {
			st.nativeTextFailed = err.Error()
			return
		}
		st.nativeText = &preview
		if render && buf != nil && !preview.Truncated {
			st.nativeMarkdown = newMarkdownPreview(path, root, preview.Content, s.openLinkedFile)
			preview.Content = "" // the formatted view holds the text now
		}
		if buf != nil {
			st.nativeCode = &editor.State{
				Buffer:       buf,
				ReadOnly:     true,
				NoActiveLine: true, // a viewer has no line being worked on
				Label:        name,
				Placeholder:  "Empty file",
				// Prose wraps; code scrolls sideways.
				SoftWrap: flavor == filesview.FlavorMarkdown || buf.Language() == nil,
			}
		}
		s.applyFilesReveal()
	}
	if s.win == nil {
		preview, buf, err := load()
		apply(preview, buf, err)
		return
	}
	go func() {
		preview, buf, err := load()
		s.applyOnWindow(func() { apply(preview, buf, err) })
	}()
}

// beginNativeImage decodes an image for the Native tier: the bounded
// decode runs off the UI lane, the Bitmap wraps on it (texture upload
// wants the main thread).
func (s *Shell) beginNativeImage(path string) {
	st := &s.rightPanel
	s.claimNativePreview(path)
	st.nativeImageLoading = true
	gen := st.filesGen.Load()
	root := st.currentRoot
	if s.win == nil {
		// Headless (tests): decode inline; the Bitmap wrap needs the GPU,
		// so headless state carries only the outcome, never the bitmap.
		_, err := filesview.DecodeImageBounded(path)
		if gen != st.filesGen.Load() || st.currentRoot != root || st.selectedFilePath != path {
			return
		}
		st.nativeImageLoading = false
		if err != nil {
			st.nativeImageFailed = err.Error()
		}
		return
	}
	go func() {
		img, err := filesview.DecodeImageBounded(path)
		s.callOnWindow(func() {
			if gen != st.filesGen.Load() || st.currentRoot != root || st.selectedFilePath != path {
				return
			}
			st.nativeImageLoading = false
			if err != nil {
				st.nativeImageFailed = err.Error()
				return
			}
			st.nativeImage = ui.NewBitmap(img)
		})
	}()
}

// clearNativeFilesPreview drops the per-selection preview state (selection
// change, root change, removal of the selected file); the sticky
// markdownSource choice stays.
func (s *Shell) clearNativeFilesPreview() {
	st := &s.rightPanel
	st.previewSize = 0
	st.previewPlanned = ""
	s.claimNativePreview("")
}

// filesPreviewBar is the open file's strip over every tier: size, the limit
// that cut it, and the Rendered/Source choice of a Markdown file small enough
// to format. The Detail card's header names the file.
func (s *Shell) filesPreviewBar(c *ui.Context, selected string) {
	st := &s.rightPanel
	t := c.Theme()
	typ := Typography()
	sp := Spacing()
	// The segmented choice sets the bar's height; every file keeps it, so
	// the code never jumps between a Markdown file and the rest. The Detail
	// card's header above already names the file, so the bar only carries
	// what is about it: size, the truncation note, the page actions and the
	// view choice, pushed right by a spacer (2026-11-08). No rule under it: the
	// header, this bar and the code are one surface.
	ui.Row(c).FillWidth().MinHeight(Controls().RegularHeight+2*sp.S).Padding(sp.S, sp.XL).Gap(sp.M).AlignItems(ui.Center).Children(func() {
		ui.Spacer(c)
		if st.previewSize > 0 {
			ui.Text(c, filesview.FormatFileSize(st.previewSize)).
				FontSize(typ.Caption).TextColor(t.TextMuted)
		}
		if st.nativeText != nil && st.nativeText.Truncated {
			statusPill(c, "Showing the first "+filesview.FormatFileSize(filesview.TextPreviewCap), ToneAttention)
		}
		if isHTMLPage(selected) {
			s.syncStaticPreview()
			k := gorexColorsOf(t.Dark)
			if gorexIconButton(c, k, iconGlobe, "Open in Browser", "Open in the default browser", false, gorexPaneBtn, gorexPaneGlyph).Clicked() {
				s.openStaticInBrowser(selected)
			}
			if tipped(c, regularButton(c, "Open Preview"), "Serve the project folder on localhost and preview the page").Clicked() {
				s.openStaticPreview(selected)
			}
		}
		if st.previewPlanned == selected && planPreviewTier(selected, st.previewSize) == tierMarkdown {
			choice := 0
			if st.markdownSource {
				choice = 1
			}
			if segmentedControl(c, &choice, "Rendered", "Source").Label("Markdown view").Changed() {
				s.setMarkdownSource(choice == 1)
			}
		}
	})
}

// openLinkedFile is what a link to another project file does: the file opens
// as a Detail card, as a tree click opens it, and the tree opens the folders
// above it so its row shows where the file is (Solco Git's revealFile does the
// same for its own tree).
func (s *Shell) openLinkedFile(path string) {
	root := s.rightPanel.currentRoot
	for dir := filepath.Dir(path); len(dir) > len(root) && strings.HasPrefix(dir, root); dir = filepath.Dir(dir) {
		s.rightPanel.expandedDirs[dir] = true
	}
	s.openFileDetail(path)
}

// filesWebviewFill is what the AVIF/ICO web view is filled with — before its
// page paints and behind the image: the paper of the Detail card the preview
// sits in (the surface role, the ground the code view takes too through
// codeEditorTheme), in both appearances. webviewBackground is the window
// canvas, the right fill for the Chat page and, here, a band a shade off the
// card.
func filesWebviewFill() string {
	hex := func(c ui.Color) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }
	return "light-dark(" + hex(paneCardFaceOf(false, false, false).paper) + ", " + hex(paneCardFaceOf(true, false, false).paper) + ")"
}

// nativePreviewBody renders what the plan chose for the center: the formatted
// Markdown, the code view, the decoded image, the AVIF/ICO page, or the
// truthful state.
func (s *Shell) nativePreviewBody(c *ui.Context) {
	st := &s.rightPanel
	switch {
	case st.nativeTextLoading || st.nativeImageLoading:
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Spinner(c).Size(16, 16)
		})
	case st.nativeImageFailed != "":
		emptyState(c, "Can't preview this image", st.nativeImageFailed)
	case st.nativeTextFailed != "":
		emptyState(c, "Can't read this file", st.nativeTextFailed)
	case st.nativeImage != nil:
		ui.Box(c).Grow(1).FillWidth().Padding(Spacing().L).Children(func() {
			ui.Image(c, st.nativeImage).Fit(ui.ScaleDown).Grow(1).FillWidth()
		})
	case st.webImage:
		s.filesWebviewBody(c)
	case st.nativeText != nil && st.nativeText.IsBinary:
		emptyState(c, "Binary file", "Reveal it in Finder to open it.")
	case st.nativeMarkdown != nil && !st.markdownSource:
		s.markdownBody(c, st.nativeMarkdown)
	case st.nativeCode != nil:
		// The theme follows the window each frame: an appearance change
		// repaints an open file without reloading it.
		st.nativeCode.Theme = codeEditorTheme(c.Theme())
		editor.View(c, st.nativeCode).FillWidth().Grow(1).MinHeight(0)
	default:
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Spinner(c).Size(16, 16)
		})
	}
}
