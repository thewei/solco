package solcogit

/**
 * [INPUT]: solcogit.go, fake file system / clock / environment, and the literal links Solco Git's parser tests (../solco-git, window_test.go TestOpenURLParsing) accept
 * [OUTPUT]: the contract of the one-way link — the exact link shape, what is refused before a link is made, where the app is looked for and how long the answer is kept, and that Open reaches the chosen bundle with the right link
 * [POS]: 2026-11-07 决定「Solco Git and the right panel」第 2、5 条的回归防线；链接形状在两个仓库各钉一份字面量
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"errors"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// The same literal is in Solco Git's TestOpenURLParsing: a change to the shape
// of the link must change both.
func TestURLShapeMatchesWhatSolcoGitParses(t *testing.T) {
	link, ok := URL("/Users/me/src/app", Files, "src/main.go")
	const want = "solcogit://open?file=src%2Fmain.go&repo=%2FUsers%2Fme%2Fsrc%2Fapp&view=files"
	if !ok || link != want {
		t.Fatalf("URL = %q, %v; want %q", link, ok, want)
	}
	link, ok = URL("/Users/me/src/app", Changes, "")
	if !ok || link != "solcogit://open?repo=%2FUsers%2Fme%2Fsrc%2Fapp&view=changes" {
		t.Fatalf("no file: %q, %v", link, ok)
	}
}

func TestURLRoundTripsAwkwardPaths(t *testing.T) {
	repo := "/Users/me/My Repos/ünï côde & more#1"
	file := "dir with space/a+b?c.go"
	link, ok := URL(repo, Commits, file)
	if !ok {
		t.Fatal("refused a valid target")
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme != Scheme || u.Host != "open" {
		t.Fatalf("parse %q: %v %+v", link, err, u)
	}
	q := u.Query()
	if q.Get("repo") != repo || q.Get("file") != file || q.Get("view") != "commits" {
		t.Errorf("round trip lost something: %v", q)
	}
}

func TestURLRefusesWhatCannotBeLinked(t *testing.T) {
	for name, tc := range map[string]struct {
		repo string
		view View
		file string
	}{
		"relative repo": {"src/app", Changes, ""},
		"empty repo":    {"", Changes, ""},
		"NUL in repo":   {"/a\x00b", Changes, ""},
		"unknown view":  {"/a", "stash", ""},
		"absolute file": {"/a", Files, "/etc/passwd"},
		"escaping file": {"/a", Files, "../secret"},
		"deep escape":   {"/a", Files, "x/../../secret"},
		"NUL in file":   {"/a", Files, "a\x00b"},
		"too long":      {"/" + strings.Repeat("a", MaxLinkLen), Changes, ""},
	} {
		if link, ok := URL(tc.repo, tc.view, tc.file); ok {
			t.Errorf("%s: made %q", name, link)
		}
	}
	// A path that merely contains dots is fine.
	if _, ok := URL("/a", Files, "..hidden/x"); !ok {
		t.Error("a file named ..hidden is inside the repository")
	}
	// The file is cleaned: no ./ or doubled slashes reach the link.
	link, _ := URL("/a", Files, "./x//y.go")
	if !strings.Contains(link, "file=x%2Fy.go") {
		t.Errorf("file not cleaned: %q", link)
	}
}

// ---- Locator -----------------------------------------------------------------

type fakeInfo struct{ dir bool }

func (f fakeInfo) Name() string       { return "x" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return 0 }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.dir }
func (f fakeInfo) Sys() any           { return nil }

type fakeWorld struct {
	now     time.Time
	present map[string]bool // path → is a directory
	env     map[string]string
	stats   int
}

func (w *fakeWorld) locator() *Locator {
	return &Locator{
		goos: "darwin",
		now:  func() time.Time { return w.now },
		stat: func(p string) (os.FileInfo, error) {
			w.stats++
			dir, ok := w.present[p]
			if !ok {
				return nil, os.ErrNotExist
			}
			return fakeInfo{dir: dir}, nil
		},
		getenv: func(k string) string { return w.env[k] },
		home:   func() (string, error) { return "/Users/me", nil },
	}
}

func newWorld() *fakeWorld {
	return &fakeWorld{now: time.Unix(1_000_000, 0), present: map[string]bool{}, env: map[string]string{}}
}

func TestLocatorLooksInTheStandardPlacesInOrder(t *testing.T) {
	w := newWorld()
	l := w.locator()
	if got := l.Path(); got != "" {
		t.Fatalf("nothing installed: %q", got)
	}
	w.now = w.now.Add(time.Minute)
	w.present["/Users/me/Applications/Solco Git.app"] = true
	if got := l.Path(); got != "/Users/me/Applications/Solco Git.app" {
		t.Fatalf("user Applications: %q", got)
	}
	w.now = w.now.Add(time.Minute)
	w.present["/Applications/Solco Git.app"] = true
	if got := l.Path(); got != "/Applications/Solco Git.app" {
		t.Fatalf("/Applications wins over the user's: %q", got)
	}
}

func TestLocatorOverrideWinsAndMustBeABundleDirectory(t *testing.T) {
	w := newWorld()
	w.present["/Applications/Solco Git.app"] = true
	w.present["/tmp/dev/Solco Git.app"] = true
	w.present["/tmp/file.app"] = false // a file, not a bundle
	w.present["/tmp/dev/tool"] = true  // a directory, but not an .app

	w.env[EnvApp] = "/tmp/dev/Solco Git.app"
	if got := w.locator().Path(); got != "/tmp/dev/Solco Git.app" {
		t.Errorf("override ignored: %q", got)
	}
	for _, bad := range []string{"/tmp/file.app", "/tmp/dev/tool", "/tmp/missing.app"} {
		w.env[EnvApp] = bad
		if got := w.locator().Path(); got != "/Applications/Solco Git.app" {
			t.Errorf("override %q should fall through to the standard place, got %q", bad, got)
		}
	}
}

func TestLocatorAnswerIsCachedForAFewSeconds(t *testing.T) {
	w := newWorld()
	l := w.locator()
	l.Path()
	after := w.stats
	for i := 0; i < 50; i++ { // every frame of a second
		l.Path()
	}
	if w.stats != after {
		t.Errorf("Path hit the file system %d more times inside the cache window", w.stats-after)
	}
	w.present["/Applications/Solco Git.app"] = true
	if l.Path() != "" {
		t.Error("the cached answer should still stand")
	}
	w.now = w.now.Add(recheckEvery)
	if l.Path() == "" {
		t.Error("after the window the app that was installed meanwhile must be found")
	}
	w.present = map[string]bool{}
	w.now = w.now.Add(recheckEvery)
	if l.Path() != "" {
		t.Error("an app removed meanwhile must stop being offered")
	}
}

func TestLocatorFindsNothingOffMacOS(t *testing.T) {
	w := newWorld()
	w.present["/Applications/Solco Git.app"] = true
	l := w.locator()
	l.goos = "linux"
	if l.Path() != "" || w.stats != 0 {
		t.Errorf("only macOS has the app (stats=%d)", w.stats)
	}
}

// ---- Link --------------------------------------------------------------------

func TestLinkOpensTheChosenBundleWithTheLink(t *testing.T) {
	w := newWorld()
	w.present["/Applications/Solco Git.app"] = true
	var gotApp, gotLink string
	k := &Link{locator: w.locator(), run: func(app, link string) error { gotApp, gotLink = app, link; return nil }}
	if !k.Available() {
		t.Fatal("not available although installed")
	}
	if err := k.Open("/Users/me/src/app", Changes, "main.go"); err != nil {
		t.Fatal(err)
	}
	if gotApp != "/Applications/Solco Git.app" || gotLink != "solcogit://open?file=main.go&repo=%2FUsers%2Fme%2Fsrc%2Fapp&view=changes" {
		t.Errorf("run(%q, %q)", gotApp, gotLink)
	}
}

func TestLinkRefusesBeforeAskingTheSystem(t *testing.T) {
	w := newWorld()
	calls := 0
	k := &Link{locator: w.locator(), run: func(string, string) error { calls++; return nil }}
	if k.Available() {
		t.Fatal("available although nothing is installed")
	}
	if err := k.Open("/a", Changes, ""); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("not installed: %v", err)
	}
	w.present["/Applications/Solco Git.app"] = true
	w.now = w.now.Add(time.Minute)
	if err := k.Open("relative", Changes, ""); !errors.Is(err, ErrBadTarget) {
		t.Errorf("bad target: %v", err)
	}
	if calls != 0 {
		t.Errorf("the system was asked %d times for something refused", calls)
	}
	want := errors.New("exit status 1")
	k.run = func(string, string) error { return want }
	if err := k.Open("/a", Changes, ""); !errors.Is(err, want) {
		t.Errorf("the system's refusal must reach the caller: %v", err)
	}
}
