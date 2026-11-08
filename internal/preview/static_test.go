package preview

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thewei/solco-gitkit/filesview"
)

func staticFixture(t *testing.T) (*StaticServer, string) {
	t.Helper()
	root := t.TempDir()
	outside := t.TempDir()
	put := func(base, rel, body string) {
		p := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put(root, "index.html", "<h1>home</h1>")
	put(root, "docs/page.html", "<p>page</p>")
	put(root, "docs/style.css", "p{}")
	put(root, "empty/note.txt", "x")
	put(root, ".git/config", "[core]")
	put(root, "node_modules/dep/index.js", "dep")
	put(root, "dist/app.js", "built")
	put(outside, "secret.txt", "outside")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "leak.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".git"), filepath.Join(root, "gitlink")); err != nil {
		t.Fatal(err)
	}
	s, err := StartStatic(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.SetIgnore(filesview.NewIgnoreSet([]string{"dist"}), false)
	return s, root
}

func get(t *testing.T, url string, mutate ...func(*http.Request)) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mutate {
		m(req)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), resp.Header
}

func TestStaticServerServesTheRootReadOnly(t *testing.T) {
	s, _ := staticFixture(t)
	if !IsLoopbackURL(s.Origin()) {
		t.Fatalf("origin %q must be an approved loopback target", s.Origin())
	}
	if code, body, h := get(t, s.URLFor("docs/page.html")); code != 200 || body != "<p>page</p>" || !strings.HasPrefix(h.Get("Content-Type"), "text/html") {
		t.Fatalf("page: %d %q %q", code, body, h.Get("Content-Type"))
	}
	if code, _, h := get(t, s.URLFor("docs/style.css")); code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "text/css") {
		t.Fatalf("css: %d %q", code, h.Get("Content-Type"))
	}
	if code, body, _ := get(t, s.URLFor("")); code != 200 || body != "<h1>home</h1>" {
		t.Fatalf("a folder with index.html serves it: %d %q", code, body)
	}
	if code, _, h := get(t, s.Origin()+"/docs"); code != http.StatusMovedPermanently || h.Get("Location") != "/docs/" {
		t.Fatalf("a folder without its slash redirects: %d %q", code, h.Get("Location"))
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req, _ := http.NewRequest(method, s.URLFor("docs/page.html"), strings.NewReader("x"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s must be refused, got %d", method, resp.StatusCode)
		}
	}
}

func TestStaticServerListsFoldersWithoutHiddenEntries(t *testing.T) {
	s, _ := staticFixture(t)
	if err := os.Remove(filepath.Join(filepath.Dir(filepath.Join(s.root, "index.html")), "index.html")); err != nil {
		t.Fatal(err)
	}
	code, body, _ := get(t, s.URLFor("")+"/")
	if code != 200 || !strings.Contains(body, `href="docs/"`) || !strings.Contains(body, `href="empty/"`) {
		t.Fatalf("listing: %d %q", code, body)
	}
	for _, hidden := range []string{".git", "node_modules", "dist", "gitlink", "leak.txt"} {
		if strings.Contains(body, hidden) {
			t.Fatalf("listing shows hidden %q: %q", hidden, body)
		}
	}
	if code, body, _ := get(t, s.Origin()+"/docs/"); code != 200 || !strings.Contains(body, `href="../"`) || !strings.Contains(body, "page.html") {
		t.Fatalf("a sub-folder lists with a way back: %d %q", code, body)
	}
}

func TestStaticServerRefusesWhatStaysPrivate(t *testing.T) {
	s, _ := staticFixture(t)
	for _, p := range []string{".git/config", "gitlink/config", "leak.txt", "node_modules/dep/index.js", "dist/app.js", "../etc/passwd", "%2e%2e/secret.txt", "missing.html"} {
		if code, body, _ := get(t, s.Origin()+"/"+p); code == 200 {
			t.Fatalf("%q must not be served: %q", p, body)
		}
	}
	if code, _, _ := get(t, s.URLFor("docs/page.html"), func(r *http.Request) { r.Host = "evil.example:80" }); code != http.StatusForbidden {
		t.Fatalf("a foreign Host header (DNS rebinding) must be refused, got %d", code)
	}
}

func TestStaticServerFollowsTheIgnoredToggle(t *testing.T) {
	s, _ := staticFixture(t)
	if code, _, _ := get(t, s.URLFor("dist/app.js")); code != http.StatusNotFound {
		t.Fatalf("ignored files stay hidden by default: %d", code)
	}
	s.SetIgnore(filesview.NewIgnoreSet([]string{"dist"}), true)
	if code, body, _ := get(t, s.URLFor("dist/app.js")); code != 200 || body != "built" {
		t.Fatalf("the toggle serves ignored files: %d %q", code, body)
	}
	if code, _, _ := get(t, s.URLFor(".git/config")); code == 200 {
		t.Fatal(".git is never served, toggle or not")
	}
}

func TestStaticServerURLForEscapes(t *testing.T) {
	s, _ := staticFixture(t)
	if got := s.URLFor("a b/文档#1.html"); !strings.HasSuffix(got, "/a%20b/%E6%96%87%E6%A1%A3%231.html") {
		t.Fatalf("url = %q", got)
	}
}
