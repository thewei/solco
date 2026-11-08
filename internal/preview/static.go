package preview

import (
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/thewei/solco-gitkit/filesview"
)

/**
 * [INPUT]: 一个项目根目录、filesview 忽略集合（Files 树同一份）与「显示忽略项」开关
 * [OUTPUT]: StaticServer——只绑 127.0.0.1 随机端口的只读静态服务：根 = 项目根，GET/HEAD、Host 校验、.git 永不提供、符号链接逃逸与忽略项（开关关闭时）一律 404、目录有 index.html 则出它、否则出目录列表页；URLFor 拼出某相对路径的预览地址
 * [POS]: Files 的 HTML 预览（产品决策 2026-10-30）：只有用户点「Open Preview」才起，换项目/退出即关；预览仍在既有 Local Preview 窗口（loopback 限定）里看，不新增 Herdr 方法、不动 Files 桥
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// StaticServer serves one folder, read-only, to the loopback preview window.
type StaticServer struct {
	root string // symlink-resolved absolute root
	ln   net.Listener
	srv  *http.Server

	ignore      atomic.Pointer[filesview.IgnoreSet]
	showIgnored atomic.Bool
}

// StartStatic serves root on a free 127.0.0.1 port.
func StartStatic(root string) (*StaticServer, error) {
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(real); err != nil || !info.IsDir() {
		return nil, errors.New("not a folder: " + root)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &StaticServer{root: real, ln: ln}
	s.ignore.Store(filesview.NewIgnoreSet(nil))
	s.srv = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// Root is the folder being served, as given to StartStatic (resolved).
func (s *StaticServer) Root() string { return s.root }

// Origin is the server's address, without a trailing slash.
func (s *StaticServer) Origin() string { return "http://" + s.ln.Addr().String() }

// Close stops the server.
func (s *StaticServer) Close() error { return s.srv.Close() }

// SetIgnore follows the Files tree: with showIgnored off, what its ignore set
// hides is not served either.
func (s *StaticServer) SetIgnore(set *filesview.IgnoreSet, showIgnored bool) {
	if set == nil {
		set = filesview.NewIgnoreSet(nil)
	}
	s.ignore.Store(set)
	s.showIgnored.Store(showIgnored)
}

// URLFor is the address of a path relative to the root ("" for the root).
func (s *StaticServer) URLFor(rel string) string {
	rel = filepath.ToSlash(rel)
	parts := strings.Split(strings.Trim(rel, "/"), "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return s.Origin() + "/" + strings.Join(parts, "/")
}

// hidden reports whether a path (slash-separated segments from the root) is
// off limits: .git always, ignored entries while the toggle is off.
func (s *StaticServer) hidden(segs []string) bool {
	set, show := s.ignore.Load(), s.showIgnored.Load()
	for i, name := range segs {
		if name == ".git" {
			return true
		}
		if !show && set.Ignored(name, strings.Join(segs[:i+1], "/")) {
			return true
		}
	}
	return false
}

func splitRel(rel string) []string {
	if rel == "" || rel == "." {
		return nil
	}
	return strings.Split(filepath.ToSlash(rel), "/")
}

func (s *StaticServer) hostOK(host string) bool {
	port := fmt.Sprint(s.ln.Addr().(*net.TCPAddr).Port)
	return host == "127.0.0.1:"+port || host == "localhost:"+port
}

func (s *StaticServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.hostOK(r.Host) { // a page on another origin must not read these files by rebinding DNS
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	rel := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	segs := splitRel(rel)
	if strings.ContainsRune(rel, 0) || s.hidden(segs) {
		http.NotFound(w, r)
		return
	}
	real, err := filepath.EvalSymlinks(filepath.Join(s.root, filepath.FromSlash(rel)))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !s.allowed(real) {
		http.NotFound(w, r) // a link out of the root, or into what stays hidden
		return
	}
	info, err := os.Stat(real)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !info.IsDir() {
		s.serveFile(w, r, real)
		return
	}
	if !strings.HasSuffix(r.URL.Path, "/") {
		target := r.URL.Path + "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}
	if index := filepath.Join(real, "index.html"); !s.hidden(append(segs[:len(segs):len(segs)], "index.html")) {
		if st, err := os.Stat(index); err == nil && st.Mode().IsRegular() {
			s.serveFile(w, r, index)
			return
		}
	}
	s.serveListing(w, rel, segs, real)
}

// allowed reports whether a symlink-resolved path lies inside the root and
// outside what stays hidden.
func (s *StaticServer) allowed(real string) bool {
	back, err := filepath.Rel(s.root, real)
	return err == nil && back != ".." && !strings.HasPrefix(back, ".."+string(filepath.Separator)) && !s.hidden(splitRel(back))
}

func (s *StaticServer) serveFile(w http.ResponseWriter, r *http.Request, real string) {
	f, err := os.Open(real)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, filepath.Base(real), info.ModTime(), f)
}

// serveListing is the folder page: its visible entries, folders first.
func (s *StaticServer) serveListing(w http.ResponseWriter, rel string, segs []string, real string) {
	entries, err := os.ReadDir(real)
	if err != nil {
		http.Error(w, "cannot read folder", http.StatusInternalServerError)
		return
	}
	type row struct {
		name string
		dir  bool
	}
	var rows []row
	for _, e := range entries {
		if s.hidden(append(segs[:len(segs):len(segs)], e.Name())) {
			continue
		}
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(filepath.Join(real, e.Name()))
			if err != nil || !s.allowed(target) {
				continue // broken, or leading out: it would 404 when followed
			}
			if st, err := os.Stat(target); err == nil {
				isDir = st.IsDir()
			}
		}
		rows = append(rows, row{e.Name(), isDir})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].dir != rows[j].dir {
			return rows[i].dir
		}
		return strings.ToLower(rows[i].name) < strings.ToLower(rows[j].name)
	})
	title := "/" + rel
	if rel != "" {
		title += "/"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	b.WriteString("<!doctype html><meta charset=utf-8><meta name=color-scheme content=\"light dark\"><title>Index of ")
	b.WriteString(html.EscapeString(title))
	b.WriteString("</title><style>body{font:14px -apple-system,system-ui,sans-serif;margin:24px}a{text-decoration:none}li{list-style:none;padding:2px 0}ul{padding:0}</style><h3>Index of ")
	b.WriteString(html.EscapeString(title))
	b.WriteString("</h3><ul>")
	if rel != "" {
		b.WriteString(`<li><a href="../">../</a></li>`)
	}
	for _, r := range rows {
		name := r.name
		href := url.PathEscape(name)
		if r.dir {
			name += "/"
			href += "/"
		}
		b.WriteString(`<li><a href="` + html.EscapeString(href) + `">` + html.EscapeString(name) + "</a></li>")
	}
	b.WriteString("</ul>")
	_, _ = w.Write([]byte(b.String()))
}
