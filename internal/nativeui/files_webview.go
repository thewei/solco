package nativeui

/**
 * [INPUT]: filesview.ReadImageDataURL（ImagePreviewCap 上限）、主窗口 mygo.WebView（ui.WebView 元素）、rightPanel.webImage、filesWebviewFill（files_native_preview.go：本应用的预览坐在什么纸上）
 * [OUTPUT]: Shell.beginWebImage（AVIF/ICO 的读取与交接）、filesWebviewFor/dropFilesWebview/filesWebviewBody（WebView 的惰性创建、销毁与挂载）、filesImagePage（整页：一个 <img> 加一行失败提示）
 * [POS]: 产品决策 2026-11-08「Files 预览原生优先」：WebView 只剩 AVIF/ICO——Go 解不了、WKWebView 解得了的两种格式。页面是 Go 现拼的静态 HTML（不画底，由 WebView 的 filesWebviewFill 铺——预览所在那张卡的纸色，不是窗口画布，否则在白卡里露出一条色差；没有脚本包、样式文件、网络或桥），图片字节经 LoadHTML 一次交给引擎；选中别的文件即销毁 WebView，WebContent 进程不常驻。创建失败时给出真实空态
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"log/slog"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/filesview"
)

// filesImagePage is the whole page the AVIF/ICO tier loads: the image, centered
// and contained on the paper the web view is filled with (filesWebviewFill; the
// page itself paints nothing), and one line of truth when the engine cannot
// decode it. No script file, no stylesheet file, no request: the bytes are
// already inside the document.
func filesImagePage(dataURL string) string {
	return `<!doctype html><meta charset="utf-8"><meta name="color-scheme" content="light dark">` +
		`<style>html,body{height:100%;margin:0;background:transparent;-webkit-user-select:none;cursor:default}` +
		`body{display:flex;align-items:center;justify-content:center;font:13px -apple-system,system-ui,sans-serif}` +
		`img{max-width:calc(100% - 32px);max-height:calc(100% - 32px);object-fit:contain;-webkit-user-drag:none}` +
		`p{margin:0 24px;text-align:center;opacity:.65}</style>` +
		`<body oncontextmenu="return false">` +
		`<img alt="" src="` + dataURL + `" ` +
		`onerror="document.body.innerHTML='<p>Can\u2019t show this image. Reveal it in Finder to open it.</p>'">`
}

// beginWebImage hands an AVIF or ICO to the one tier that decodes it. The read
// (bounded by ImagePreviewCap) runs off the UI lane; the page is made and
// loaded on it.
func (s *Shell) beginWebImage(path string) {
	st := &s.rightPanel
	s.claimNativePreview(path)
	st.nativeImageLoading = true
	gen := st.filesGen.Load()
	root := st.currentRoot
	apply := func(dataURL string, err error) {
		if gen != st.filesGen.Load() || st.currentRoot != root || st.selectedFilePath != path {
			return
		}
		st.nativeImageLoading = false
		if err != nil {
			st.nativeImageFailed = err.Error()
			return
		}
		view := s.filesWebviewFor()
		if view == nil {
			st.nativeImageFailed = "The embedded preview could not start. Reveal it in Finder to open it."
			return
		}
		view.Page().LoadHTML(filesImagePage(dataURL), "")
		st.webImage = true
	}
	if s.win == nil {
		dataURL, err := filesview.ReadImageDataURL(path)
		apply(dataURL, err)
		return
	}
	go func() {
		dataURL, err := filesview.ReadImageDataURL(path)
		s.applyOnWindow(func() { apply(dataURL, err) })
	}()
}

// filesWebviewFor returns the files preview WebView, created on first use, or
// nil when no window exists or creation failed once (latched, so a truthful
// state shows instead of retrying every frame). Like the chat page's view it
// shows only while a frame builds its ui.WebView element. Call on the UI lane.
func (s *Shell) filesWebviewFor() *mygo.WebView {
	if s.win == nil || s.filesWebviewBroken {
		return nil
	}
	if s.filesWebview == nil {
		view, err := s.win.NewWebView(mygo.WebViewOptions{BackgroundColor: filesWebviewFill()})
		if err != nil {
			s.filesWebviewBroken = true
			slog.Warn("create files preview webview failed; AVIF and ICO say so", "error", err)
			return nil
		}
		// Dropping a file onto the page must not replace the image with it.
		// The app's own LoadHTML is not asked about.
		view.Page().OnWillNavigate(func(e *mygo.NavigateEvent) { e.PreventDefault() })
		s.filesWebview = view
	}
	return s.filesWebview
}

// dropFilesWebview destroys the preview WebView, if there is one: it exists
// only while an AVIF or ICO is the open file. Call on the UI lane.
func (s *Shell) dropFilesWebview() {
	if s.filesWebview != nil {
		s.filesWebview.Destroy()
		s.filesWebview = nil
	}
}

// filesWebviewBody places the page in the center. The caller checked
// rightPanel.webImage, which is set only once the view exists.
func (s *Shell) filesWebviewBody(c *ui.Context) {
	if view := s.filesWebview; view != nil {
		ui.WebView(c, view).Grow(1)
	}
}
