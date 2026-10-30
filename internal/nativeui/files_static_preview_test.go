package nativeui

/**
 * [INPUT]: headless Files 页面、preview.StaticServer、可替换的预览窗口/浏览器调用
 * [OUTPUT]: HTML 预览回归（2026-10-30）：只有 HTML 文件的查看条有 Open Preview / Open in Browser、点了才起服务且同根复用一个端口、服务出的页面与相对资源可取、忽略项随眼睛开关、换项目服务即关
 * [POS]: 只验证 nativeui 接线；服务安全口径由 internal/preview 测试覆盖
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fetch(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestHTMLFilesOfferAStaticPreview(t *testing.T) {
	s, tester, root := filesOpsTestShell(t)
	for rel, body := range map[string]string{
		"site/index.html": "<link rel=stylesheet href=style.css><h1>hi</h1>",
		"site/style.css":  "h1{}",
		"dist/app.js":     "built",
		"notes.txt":       "plain",
	} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var shown []string
	showStaticPreview = func(_ *Shell, url string) error { shown = append(shown, url); return nil }
	var browsed []string
	openInBrowser = func(url string) error { browsed = append(browsed, url); return nil }
	t.Cleanup(func() {
		openInBrowser = func(string) error { return nil }
		showStaticPreview = func(*Shell, string) error { return nil }
		s.closeStaticPreview()
	})

	s.openFilePreview(filepath.Join(root, "notes.txt"))
	tester.Frame()
	if tester.HasText("Open Preview") {
		t.Fatal("only HTML pages offer a preview")
	}
	if s.staticPreview != nil {
		t.Fatal("nothing is served until Open Preview is pressed")
	}

	s.openFilePreview(filepath.Join(root, "site", "index.html"))
	tester.Frame()
	tester.Frame()
	if !tester.HasText("Open Preview") {
		t.Fatalf("an HTML page needs Open Preview; texts=%q", tester.Texts())
	}
	if err := tester.Click("Open Preview"); err != nil {
		t.Fatal(err)
	}
	if len(shown) != 1 || !strings.HasSuffix(shown[0], "/site/index.html") || s.staticPreview == nil {
		t.Fatalf("preview window got %v (server %v)", shown, s.staticPreview != nil)
	}
	if code, body := fetch(t, shown[0]); code != 200 || !strings.Contains(body, "<h1>hi</h1>") {
		t.Fatalf("page: %d %q", code, body)
	}
	if code, body := fetch(t, strings.Replace(shown[0], "index.html", "style.css", 1)); code != 200 || body != "h1{}" {
		t.Fatalf("a relative asset must load: %d %q", code, body)
	}

	if err := tester.Click("Open in Browser"); err != nil {
		t.Fatal(err)
	}
	if len(browsed) != 1 || browsed[0] != shown[0] {
		t.Fatalf("the browser got %v, want the preview address %v (same server)", browsed, shown)
	}

	// Ignored files follow the tree's toggle.
	distURL := strings.Replace(shown[0], "site/index.html", "dist/app.js", 1)
	if code, _ := fetch(t, distURL); code != http.StatusNotFound {
		t.Fatalf("dist/ is a fixed ignored name: %d", code)
	}
	s.rightPanel.showIgnored = true
	tester.Frame()
	if code, body := fetch(t, distURL); code != 200 || body != "built" {
		t.Fatalf("with Show ignored files the server serves it: %d %q", code, body)
	}

	s.rightPanel.currentRoot = "elsewhere"
	s.syncRightPanelRoot()
	if s.staticPreview != nil {
		t.Fatal("a new project closes the server")
	}
	if code, _ := fetch(t, shown[0]); code != 0 {
		t.Fatalf("the old address must stop answering, got %d", code)
	}
}
