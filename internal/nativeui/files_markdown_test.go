package nativeui

/**
 * [INPUT]: files_markdown.go（previewTier 路由、markdownPreview、链接分类、图片读取）、files_webview.go 的 AVIF/ICO 页、headless 右侧面板 Files 页
 * [OUTPUT]: 2026-11-08 预览原生优先回归：每种文件的分级归属、Markdown 原生渲染⇄源码切换且不重读、链接只放行该放行的、图片只读项目里且有像素预算、主题随外观只换一次、AVIF/ICO 页只有几百字节
 * [POS]: 只验证 nativeui 的分级接线与状态；读取/分类语义由 solco-gitkit/filesview 测试覆盖，渲染本身由 MyGo ui/markdown 的测试覆盖
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/thewei/solco-gitkit/filesview"
	"github.com/thewei/solco/internal/codehl"
)

// realTempDir is t.TempDir() with its symlinks resolved (macOS' /var is one),
// the form the project root takes in the shell.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeAt(t *testing.T, dir, rel string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestPlanPreviewTierRoutesEachFileToItsOwner pins the routing table: only
// what the native tier cannot show (AVIF, ICO) ever reaches a page, and
// Markdown renders only while formatting it stays cheap.
func TestPlanPreviewTierRoutesEachFileToItsOwner(t *testing.T) {
	const mib = 1 << 20
	for _, tc := range []struct {
		name string
		size int64
		want previewTier
	}{
		{"main.go", 100, tierText},
		{"notes.txt", 10 * mib, tierText},
		{"README.md", 100, tierMarkdown},
		{"README.markdown", markdownRenderCap, tierMarkdown},
		{"README.md", markdownRenderCap + 1, tierText}, // too big to format: it reads as source
		{"shot.png", 100, tierImage},
		{"shot.JPG", 100, tierImage},
		{"shot.jpeg", 100, tierImage},
		{"anim.gif", 100, tierImage},
		{"photo.webp", 100, tierImage},
		{"scan.bmp", 100, tierImage},
		{"logo.svg", 100, tierText}, // XML source, however large
		{"logo.svg", 30 * mib, tierText},
		{"icon.ico", 100, tierWebImage},
		{"photo.avif", 100, tierWebImage},
		{"photo.avif", filesview.ImagePreviewCap, tierWebImage},
		{"photo.avif", filesview.ImagePreviewCap + 1, tierImageTooLarge},
	} {
		if got := planPreviewTier(tc.name, tc.size); got != tc.want {
			t.Errorf("%s (%d bytes): tier %v, want %v", tc.name, tc.size, got, tc.want)
		}
	}
}

// TestClassifyMarkdownLink: a link opens only what it names — a web address
// outside, a heading inside the page, a file inside the project — and
// everything else (other schemes, paths that leave the project, symlinks that
// do, files that are not there) does nothing.
func TestClassifyMarkdownLink(t *testing.T) {
	root := realTempDir(t)
	guide := writeAt(t, root, "docs/guide.md", []byte("# guide\n"))
	other := writeAt(t, root, "docs/other.md", []byte("# other\n"))
	spaced := writeAt(t, root, "docs/my doc.md", []byte("# spaced\n"))
	top := writeAt(t, root, "README.md", []byte("# top\n"))
	outside := writeAt(t, filepath.Dir(root), filepath.Base(root)+"-outside.md", []byte("secret\n"))
	t.Cleanup(func() { _ = os.Remove(outside) })
	if err := os.Symlink(outside, filepath.Join(root, "docs", "escape.md")); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		dest   string
		kind   markdownLinkKind
		target string
	}{
		{"https://example.com/a?b=1#c", linkExternal, "https://example.com/a?b=1#c"},
		{"http://example.com", linkExternal, "http://example.com"},
		{"mailto:dev@example.com", linkExternal, "mailto:dev@example.com"},
		{"http://", linkNone, ""},
		{"javascript:alert(1)", linkNone, ""},
		{"file:///etc/hosts", linkNone, ""},
		{"data:text/html,hi", linkNone, ""},
		{"solco://reveal?tab=1", linkNone, ""},
		{"", linkNone, ""},
		{"#install", linkAnchor, "#install"},
		{"other.md", linkFile, other},
		{"./other.md#part", linkFile, other},
		{"other.md?raw=1", linkFile, other},
		{"my%20doc.md", linkFile, spaced},
		{"../README.md", linkFile, top},
		{"/README.md", linkFile, top}, // root-relative, as a repository's links read
		{"missing.md", linkNone, ""},
		{"../docs", linkNone, ""}, // a folder is not a file
		{"../../" + filepath.Base(root) + "-outside.md", linkNone, ""},
		{"escape.md", linkNone, ""}, // a symlink that leaves the project
		{"/etc/hosts", linkNone, ""},
	} {
		kind, target := classifyMarkdownLink(root, guide, tc.dest)
		if kind != tc.kind || target != tc.target {
			t.Errorf("%q: got (%v, %q), want (%v, %q)", tc.dest, kind, target, tc.kind, tc.target)
		}
	}
}

// TestWithinRootRefusesWhatItCannotResolve: the guard answers for the real
// location — symlinks followed — and a path it cannot resolve is not inside.
func TestWithinRootRefusesWhatItCannotResolve(t *testing.T) {
	root := realTempDir(t)
	inside := writeAt(t, root, "a/b.txt", []byte("x"))
	outside := writeAt(t, filepath.Dir(root), filepath.Base(root)+"-out.txt", []byte("x"))
	t.Cleanup(func() { _ = os.Remove(outside) })
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		root:                                 true,
		inside:                               true,
		filepath.Join(root, "a"):             true,
		filepath.Join(root, "missing.txt"):   false,
		filepath.Join(root, "link.txt"):      false,
		outside:                              false,
		filepath.Join(root, "a", "..", ".."): false,
		filepath.Join(root, "a", "..", "a"):  true,
		filepath.Join(root+"-sibling", "x"):  false,
	} {
		if got := withinRoot(root, path); got != want {
			t.Errorf("withinRoot(root, %q) = %v, want %v", strings.TrimPrefix(path, root), got, want)
		}
	}
	if withinRoot("", inside) || withinRoot(filepath.Join(root, "nope"), inside) {
		t.Error("an unresolvable root contains nothing")
	}
	// Neither side resolves: both read as "" and would "contain" each other.
	if withinRoot(filepath.Join(root, "nope"), filepath.Join(root, "nope", "deeper")) {
		t.Error("two paths that do not exist are not within one another")
	}
}

// bombPNG is a valid 1-bit grey PNG of w×h pixels whose all-zero rows
// compress to a few KB: cheap to store, expensive to decode — a decoder that
// trusts the header allocates w*h*4 bytes.
func bombPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	chunk := func(kind string, data []byte) {
		_ = binary.Write(&out, binary.BigEndian, uint32(len(data)))
		body := append([]byte(kind), data...)
		out.Write(body)
		_ = binary.Write(&out, binary.BigEndian, crc32.ChecksumIEEE(body))
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(h))
	ihdr[8] = 1 // bit depth 1, colour type 0 (grey)
	chunk("IHDR", ihdr)
	var idat bytes.Buffer
	zw := zlib.NewWriter(&idat)
	row := make([]byte, 1+(w+7)/8) // filter byte 0 + zeros
	for i := 0; i < h; i++ {
		if _, err := zw.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	chunk("IDAT", idat.Bytes())
	chunk("IEND", nil)
	return out.Bytes()
}

// TestMarkdownImageLoaderReadsOnlyProjectImagesWithinBudget: what a document
// says to show is untrusted, so the loader stays inside the project, refuses
// the network and anything past the byte and pixel budgets.
func TestMarkdownImageLoaderReadsOnlyProjectImagesWithinBudget(t *testing.T) {
	root := realTempDir(t)
	guide := writeAt(t, root, "docs/guide.md", []byte("# guide\n"))
	writeAt(t, root, "docs/pic.png", tinyPNG(t))
	writeAt(t, root, "shot.png", tinyPNG(t))
	writeAt(t, root, "docs/logo.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8"><rect width="8" height="8"/></svg>`))
	writeAt(t, root, "docs/bomb.png", bombPNG(t, 8192, 8192))                                  // 67 Mpx over a few KB: past MaxNativeImagePixels
	writeAt(t, root, "docs/huge.png", append(tinyPNG(t), make([]byte, markdownImageCap+1)...)) // a valid PNG past the byte cap
	writeAt(t, root, "docs/notes.txt", []byte("not an image"))
	outside := writeAt(t, filepath.Dir(root), filepath.Base(root)+"-outside.png", tinyPNG(t))
	t.Cleanup(func() { _ = os.Remove(outside) })
	if err := os.Symlink(outside, filepath.Join(root, "docs", "escape.png")); err != nil {
		t.Fatal(err)
	}
	load := markdownImageLoader(root, guide)

	for _, ok := range []string{"pic.png", "./pic.png", "../shot.png", "/shot.png", "logo.svg"} {
		src, err := load(ok)
		if err != nil || src == nil {
			t.Errorf("%q must load: %v", ok, err)
		}
	}
	if src, _ := load("logo.svg"); src != nil {
		if _, isSVG := src.(*ui.SVG); !isSVG {
			t.Errorf("an SVG loads as a vector, got %T", src)
		}
	}
	for _, bad := range []string{
		"https://example.com/pic.png", "http://example.com/pic.png", "data:image/png;base64,AAAA", "file:///etc/hosts",
		"../../" + filepath.Base(root) + "-outside.png", "escape.png", "/etc/hosts",
		"bomb.png", "huge.png", "notes.txt", "missing.png", "..", "",
	} {
		if src, err := load(bad); err == nil || src != nil {
			t.Errorf("%q must not load (got %v, %v)", bad, src, err)
		}
	}
}

// TestMarkdownThemeFollowsTheReviewPalette: a code block colours a character
// as the review and the Files code view do, in both appearances, and the
// document reads at the section size with GitHub's rule under its H1/H2.
func TestMarkdownThemeFollowsTheReviewPalette(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := ui.LightTheme()
		if dark {
			theme = ui.DarkTheme()
		}
		th, p := markdownTheme(theme), gdPaletteFor(theme)
		if th.CodeColors == nil {
			t.Fatalf("dark=%v: code blocks need the review's colors", dark)
		}
		for kind, class := range map[editor.TokenKind]codehl.Class{
			editor.TokenKeyword: codehl.Keyword, editor.TokenString: codehl.String,
			editor.TokenComment: codehl.Comment, editor.TokenType: codehl.Type,
			editor.TokenNumber: codehl.Number,
		} {
			if got := th.CodeColors.Tokens[kind].Color; got != p.syntax[class] {
				t.Errorf("dark=%v kind %v = %v, want the review's %v", dark, kind, got, p.syntax[class])
			}
		}
		if th.FontSize != Typography().Section || th.CodeFont != gdCodeFont() {
			t.Errorf("dark=%v: type %v/%q, want the section size and the review's code font", dark, th.FontSize, th.CodeFont)
		}
		if !th.Headings[0].Rule || !th.Headings[1].Rule || th.Headings[2].Rule {
			t.Errorf("dark=%v: only H1 and H2 carry the rule: %+v", dark, th.Headings[:3])
		}
		if th.Text != (ui.Color{}) || th.Link != (ui.Color{}) {
			t.Errorf("dark=%v: text and link colors must stay the window theme's, which follows contrast and appearance", dark)
		}
	}
}

// TestMarkdownOptionsKeepOneThemePerAppearance: the view re-lays-out every
// paragraph when its theme value changes, so the theme must be the same
// pointer frame after frame and change only with the appearance.
func TestMarkdownOptionsKeepOneThemePerAppearance(t *testing.T) {
	mp := newMarkdownPreview("/p/a.md", "/p", "# a\n", func(string) {})
	light, dark := ui.LightTheme(), ui.DarkTheme()
	first := mp.options(light)
	lightTheme := first.Theme
	if lightTheme == nil {
		t.Fatal("the options must carry a theme")
	}
	if mp.options(light) != first || first.Theme != lightTheme {
		t.Fatal("a steady appearance must hand the view the same Options and the same Theme")
	}
	flipped := mp.options(dark)
	if flipped != first {
		t.Fatal("an appearance change must keep the Options: the image cache lives in them")
	}
	if flipped.Theme == nil || flipped.Theme == lightTheme {
		t.Fatal("an appearance change must swap the Theme")
	}
	if darkTheme := flipped.Theme; mp.options(dark).Theme != darkTheme {
		t.Fatal("the dark Theme must be as steady as the light one")
	}
}

func tableDoc() string {
	return "# Release notes\n\nSome **bold** words and a [link](https://example.com).\n\n" +
		"- first item\n- second item\n\n```go\nfunc main() {}\n```\n\n| name | size |\n|---|---|\n| alpha | 1 |\n"
}

// TestMarkdownRendersAsFormattedTextAndSourceIsOneClickAway: a small Markdown
// file shows formatted, the Source choice swaps to the editor without reading
// the file again, and a file past the cap offers no Rendered at all.
func TestMarkdownRendersAsFormattedTextAndSourceIsOneClickAway(t *testing.T) {
	s, tester, root := filesOpsTestShell(t)
	md := writePreviewFile(t, root, "notes.md", []byte(tableDoc()))
	big := writePreviewFile(t, root, "big.md", bytes.Repeat([]byte("# x\n"), markdownRenderCap/4+1))
	st := &s.rightPanel

	s.openFileDetail(md)
	if st.nativePreviewPath != md || st.nativeMarkdown == nil || st.nativeCode == nil {
		t.Fatalf("a small Markdown file holds both views: native=%q md=%v code=%v", st.nativePreviewPath, st.nativeMarkdown != nil, st.nativeCode != nil)
	}
	tester.Frame()
	for _, want := range []string{"Rendered", "Source", "Release notes", "first item", "second item", "func main() {}", "alpha"} {
		if !tester.HasText(want) {
			t.Errorf("rendered view must show %q: %q", want, tester.Texts())
		}
	}
	for _, raw := range []string{"# Release notes", "**bold**", "```go", "|---|"} {
		if tester.HasText(raw) {
			t.Errorf("rendered view must not show the markup %q", raw)
		}
	}

	code, mdState := st.nativeCode, st.nativeMarkdown
	s.setMarkdownSource(true)
	if st.nativeCode != code || st.nativeMarkdown != mdState {
		t.Fatal("switching to Source must keep both views, not read the file again")
	}
	tester.Frame()
	if tester.HasText("first item") && !tester.HasText("- first item") {
		t.Fatalf("Source must show the markup, not the formatted text: %q", tester.Texts())
	}
	s.setMarkdownSource(false)
	tester.Frame()
	if !tester.HasText("first item") || tester.HasText("- first item") {
		t.Fatalf("Rendered must bring the formatted text back: %q", tester.Texts())
	}

	s.openFileDetail(big)
	if st.nativePreviewPath != big || st.nativeMarkdown != nil || st.nativeCode == nil {
		t.Fatalf("an oversized Markdown file reads as source only: md=%v code=%v", st.nativeMarkdown != nil, st.nativeCode != nil)
	}
	tester.Frame()
	if tester.HasText("Rendered") {
		t.Fatal("an oversized file cannot render, so it must not offer Rendered")
	}
}

// TestSearchHitInTheOpenRenderedFileLandsInSource: a content hit has a line to
// point at, which only the source has — so a hit in the Markdown file that is
// already open (formatted) flips it to Source and selects the match at once.
func TestSearchHitInTheOpenRenderedFileLandsInSource(t *testing.T) {
	s, tester, root := filesOpsTestShell(t)
	md := writePreviewFile(t, root, "notes.md", []byte(tableDoc()))
	st := &s.rightPanel
	s.openFileDetail(md)
	tester.Frame()
	if st.markdownSource || st.nativeMarkdown == nil {
		t.Fatal("the file must be open formatted first")
	}
	s.openFileAt(md, 4, 2, 5) // "- first item": the word "first"
	tester.Frame()
	if !st.markdownSource || st.reveal != nil {
		t.Fatalf("the hit must flip to Source and be consumed: source=%v reveal=%v", st.markdownSource, st.reveal)
	}
	if got := st.nativeCode.SelectedText(); got != "first" {
		t.Fatalf("selected %q, want the match", got)
	}
}

// TestMarkdownLinksOpenWhatTheyNameAndNothingElse follows links the way a
// click does: web addresses go to the browser, a heading scrolls the page,
// a project file opens in the preview, and the rest does nothing.
func TestMarkdownLinksOpenWhatTheyNameAndNothingElse(t *testing.T) {
	s, _, root := filesOpsTestShell(t)
	guide := writePreviewFile(t, root, "guide.md", []byte("# guide\n\n## Install\n"))
	other := writePreviewFile(t, root, "other.md", []byte("# other\n"))
	st := &s.rightPanel
	s.openFileDetail(guide)
	mp := st.nativeMarkdown
	if mp == nil {
		t.Fatal("the guide must hold a rendered view")
	}
	var browser []string
	mp.openExternal = func(u string) { browser = append(browser, u) }

	mp.follow("https://example.com/docs")
	mp.follow("mailto:dev@example.com")
	mp.follow("javascript:alert(1)")
	mp.follow("file:///etc/hosts")
	if got := strings.Join(browser, " "); got != "https://example.com/docs mailto:dev@example.com" {
		t.Fatalf("browser opens = %q", got)
	}
	if st.selectedFilePath != guide {
		t.Fatalf("a refused link must not move the selection: %q", st.selectedFilePath)
	}

	version := mp.opts.AnchorVersion
	mp.follow("#install")
	mp.follow("#install")
	if mp.opts.Anchor != "#install" || mp.opts.AnchorVersion != version+2 {
		t.Fatalf("an anchor click scrolls, every click: %q v%d (was v%d)", mp.opts.Anchor, mp.opts.AnchorVersion, version)
	}

	mp.follow("other.md")
	if st.selectedFilePath != other || st.nativePreviewPath != other {
		t.Fatalf("a project link opens its file: selection %q", st.selectedFilePath)
	}
	if len(st.expandedDirs) != 0 {
		t.Fatalf("a file in the root has no folder to open: %v", st.expandedDirs)
	}

	// A file deep in the project: the tree opens the folders above it, and only those.
	deep := writeAt(t, root, "docs/deep/more.md", []byte("# more\n"))
	s.openFileDetail(guide)
	st.nativeMarkdown.follow("docs/deep/more.md")
	if st.selectedFilePath != deep {
		t.Fatalf("the deep link opens its file: selection %q", st.selectedFilePath)
	}
	for _, dir := range []string{filepath.Join(root, "docs"), filepath.Join(root, "docs", "deep")} {
		if !st.expandedDirs[dir] {
			t.Errorf("the tree must open %s so the linked file's row is on screen", dir)
		}
	}
	if st.expandedDirs[root] || len(st.expandedDirs) != 2 {
		t.Errorf("only the folders above the file open: %v", st.expandedDirs)
	}
}

// TestFilesImagePageIsAFewHundredBytes: the one page the preview ever loads
// is the image and a few hundred bytes around it — no script bundle, no
// stylesheet file, no network, no bridge.
func TestFilesImagePageIsAFewHundredBytes(t *testing.T) {
	url := "data:image/x-icon;base64,AAABAAEAEBAAAAEAIABoBAAAFgAAACgAAAAQ"
	page := filesImagePage(url)
	if !strings.Contains(page, `src="`+url+`"`) {
		t.Fatalf("the page must show the image it was given: %q", page)
	}
	if overhead := len(page) - len(url); overhead > 1024 {
		t.Fatalf("the page is %d bytes around the image; it must stay under a KB", overhead)
	}
	for _, banned := range []string{"<script src", "<link", "http://", "https://", "fetch(", "XMLHttpRequest", "mygo"} {
		if strings.Contains(page, banned) {
			t.Errorf("the page must not reach out: found %q", banned)
		}
	}
	if !strings.Contains(page, "onerror") {
		t.Fatal("an image the engine cannot decode must say so instead of showing a broken glyph")
	}
}

// TestFilesWebviewFillIsTheDetailCardPaper: the AVIF/ICO web view sits in the
// Detail card, so it is filled with the card's paper — the paper the code view
// takes — in both appearances. The window canvas (webviewBackground) read as a
// band a shade off the card on the first real run (2026-11-08).
func TestFilesWebviewFillIsTheDetailCardPaper(t *testing.T) {
	m := regexp.MustCompile(`^light-dark\((#[0-9a-f]{6}), (#[0-9a-f]{6})\)$`).FindStringSubmatch(filesWebviewFill())
	if m == nil {
		t.Fatalf("filesWebviewFill %q is not light-dark(#rrggbb, #rrggbb)", filesWebviewFill())
	}
	for i, dark := range []bool{false, true} {
		card := paneCardFaceOf(dark, false, false).paper
		if got := ui.Hex(m[i+1]); got != card {
			t.Errorf("dark=%v: web view filled %s, the card's paper is %v", dark, m[i+1], card)
		}
		theme := ui.LightTheme()
		if dark {
			theme = ui.DarkTheme()
		}
		if code := codeEditorTheme(theme).Background; code != card {
			t.Errorf("dark=%v: the code view's ground %v is not the card's paper %v", dark, code, card)
		}
	}
	if filesWebviewFill() == webviewBackground {
		t.Error("the card's paper and the window canvas are different roles; the web view must not take the canvas")
	}
	// The web view is made with that fill (it cannot be read back from a live view).
	src, err := os.ReadFile("files_webview.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "BackgroundColor: filesWebviewFill()") {
		t.Error("filesWebviewFor must fill its web view with filesWebviewFill()")
	}
}

// TestSettingsOffersNoRichFilePreviewSwitch: the Files preview is native first
// and has nothing to turn off, so the retired switch is gone from Settings —
// from the Compatibility group and from Settings search, which would otherwise
// still lead there (it did, for one build).
func TestSettingsOffersNoRichFilePreviewSwitch(t *testing.T) {
	s := NewShell()
	s.router.Replace("/settings/diagnostics")
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	if !tester.HasText("Rich chat view") {
		t.Fatalf("the Compatibility group did not render; texts=%q", tester.Texts())
	}
	if tester.HasText("Rich file preview") {
		t.Errorf("Settings still offers the retired switch; texts=%q", tester.Texts())
	}
	if hits := matchSettings(s.settingsEntries(), "file preview"); len(hits) != 0 {
		t.Errorf("Settings search still finds the retired switch: %+v", hits)
	}
}

// TestPreviewImagesGoNativeAndOnlyAVIFAndICOWantAPage: decodable formats are
// native bitmaps, an SVG is source, an oversized AVIF says so, and a page
// that cannot start says that instead of leaving the center blank.
func TestPreviewImagesGoNativeAndOnlyAVIFAndICOWantAPage(t *testing.T) {
	s, tester, root := filesOpsTestShell(t)
	st := &s.rightPanel
	dot := writePreviewFile(t, root, "dot.png", tinyPNG(t))
	svg := writePreviewFile(t, root, "logo.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`))
	icon := writePreviewFile(t, root, "icon.ico", []byte("not decoded natively"))
	bigAVIF := writePreviewFile(t, root, "big.avif", make([]byte, filesview.ImagePreviewCap+1))
	bigSVG := writePreviewFile(t, root, "big.svg", make([]byte, filesview.ImagePreviewCap+1))

	s.openFileDetail(dot)
	if st.nativePreviewPath != dot || st.nativeImageFailed != "" || st.webImage {
		t.Fatalf("a PNG is a native bitmap: native=%q failed=%q page=%v", st.nativePreviewPath, st.nativeImageFailed, st.webImage)
	}
	s.openFileDetail(svg)
	if st.nativeCode == nil || st.webImage {
		t.Fatal("an SVG reads as XML source in the code view")
	}
	s.openFileDetail(bigSVG)
	if st.nativePreviewPath != bigSVG || st.nativeText == nil {
		t.Fatalf("an oversized SVG still reads as source: native=%q", st.nativePreviewPath)
	}
	s.openFileDetail(bigAVIF)
	if !strings.Contains(st.nativeImageFailed, "too large") {
		t.Fatalf("an oversized AVIF must say it is too large: %q", st.nativeImageFailed)
	}
	s.openFileDetail(icon)
	tester.Frame()
	if st.webImage || !strings.Contains(st.nativeImageFailed, "could not start") || !tester.HasText("Can't preview this image") {
		t.Fatalf("with no window to host the page the ICO must say so: page=%v failed=%q", st.webImage, st.nativeImageFailed)
	}
}
