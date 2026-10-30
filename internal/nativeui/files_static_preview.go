package nativeui

/**
 * [INPUT]: rightPanel.currentRoot/ignoredSet/showIgnored、选中的 .html/.htm/.xhtml 文件、internal/preview（StaticServer 静态服务 + Local Preview 窗口）
 * [OUTPUT]: Files 查看条里 HTML 文件的「Open Preview」与「Open in Browser」（产品决策 2026-10-30）：点了才在项目根上起只读 127.0.0.1 静态服务（同根复用一个端口、忽略集合与「显示忽略项」眼睛同步），页面在既有 Local Preview 窗口里打开（已开则跳到该页），或交给系统浏览器；换项目即关服务
 * [POS]: 仍是 Files 的呈现层：服务与安全口径在 internal/preview/static.go；不新增 Herdr 方法、不动 filesPreview 桥（WebView 页仍只认自己的桥）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/thewei/solco/internal/preview"
)

// filesStaticWindow keys the one Local Preview window the static server's
// pages share.
const filesStaticWindow = "files-static"

// The desktop calls are vars so tests, which run no application loop for
// them to wait on, never reach the real window or browser.
var (
	openInBrowser     = mygo.Shell.OpenExternal
	showStaticPreview = func(s *Shell, url string) error {
		_, err := s.previews.controllerFor(filesStaticWindow).ShowPreviewURL(url)
		return err
	}
)

// isHTMLPage reports whether the file is a page a browser renders.
func isHTMLPage(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html", ".htm", ".xhtml":
		return true
	}
	return false
}

// filesStaticURL serves the root (once) and returns the address of path.
func (s *Shell) filesStaticURL(path string) (string, bool) {
	st := &s.rightPanel
	root := st.currentRoot
	rel, err := filepath.Rel(root, path)
	if root == "" || err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		s.status = "The file is outside the project folder"
		return "", false
	}
	if s.staticPreview == nil || s.staticPreviewRoot != root {
		s.closeStaticPreview()
		srv, err := preview.StartStatic(root)
		if err != nil {
			s.status = "Could not start the preview server"
			return "", false
		}
		s.staticPreview, s.staticPreviewRoot = srv, root
	}
	s.syncStaticPreview()
	return s.staticPreview.URLFor(rel), true
}

// syncStaticPreview hands the server the tree's ignore state: what the
// tree hides, the server does not serve.
func (s *Shell) syncStaticPreview() {
	if s.staticPreview != nil {
		s.staticPreview.SetIgnore(s.rightPanel.ignoredSet, s.rightPanel.showIgnored)
	}
}

// closeStaticPreview stops the server (a new project, or none).
func (s *Shell) closeStaticPreview() {
	if s.staticPreview != nil {
		_ = s.staticPreview.Close()
		s.staticPreview, s.staticPreviewRoot = nil, ""
	}
}

// openStaticPreview shows the page in the Local Preview window.
func (s *Shell) openStaticPreview(path string) {
	url, ok := s.filesStaticURL(path)
	if !ok {
		return
	}
	if err := showStaticPreview(s, url); err != nil {
		s.status = "Preview window unavailable"
		return
	}
	s.status = "Serving " + filepath.Base(s.staticPreviewRoot) + " at " + s.staticPreview.Origin()
}

// openStaticInBrowser hands the served page to the system browser.
func (s *Shell) openStaticInBrowser(path string) {
	url, ok := s.filesStaticURL(path)
	if !ok {
		return
	}
	if err := openInBrowser(url); err != nil {
		s.status = "Could not open the browser"
	}
}
