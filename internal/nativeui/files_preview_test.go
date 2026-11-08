package nativeui

/**
 * [INPUT]: headless 右侧面板 Files 页、filesview 读取域、原生 editor 代码视图
 * [OUTPUT]: 预览分级回归（2026-11-08 起整条链原生优先）：文本/代码原生、Markdown 的 Rendered⇄Source 开关、截断/二进制/空文件/CJK、语法配色单一来源；Markdown 渲染/链接/图片/AVIF·ICO 页在 files_markdown_test.go
 * [POS]: 只验证 nativeui 的分级接线与状态；读取/分类语义由 solco-gitkit/filesview 测试覆盖
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/thewei/solco-gitkit/filesview"
	"github.com/thewei/solco/internal/codehl"
)

func writePreviewFile(t *testing.T, root, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestPreviewPlanSendsTextAndCodeToTheNativeView: text and code never go to
// a page — even past the old 1 MiB web cap — and the view is a read-only
// editor holding the file with its language.
func TestPreviewPlanSendsTextAndCodeToTheNativeView(t *testing.T) {
	s, tester, root := filesOpsTestShell(t)
	goFile := writePreviewFile(t, root, "main.go", []byte("package main\n\nfunc main() {}\n"))
	big := writePreviewFile(t, root, "big.log", bytes.Repeat([]byte("line of a log\n"), 120_000)) // ~1.7 MiB

	s.openFileDetail(goFile)
	st := &s.rightPanel
	if st.nativePreviewPath != goFile || st.nativeCode == nil || st.webImage {
		t.Fatalf("go file: native=%q code=%v page=%v", st.nativePreviewPath, st.nativeCode != nil, st.webImage)
	}
	if !st.nativeCode.ReadOnly || st.nativeCode.Buffer.String() != "package main\n\nfunc main() {}\n" {
		t.Fatalf("code state = readonly:%v text:%q", st.nativeCode.ReadOnly, st.nativeCode.Buffer.String())
	}
	if st.nativeCode.Buffer.Language() == nil || st.nativeCode.SoftWrap {
		t.Fatalf("code must highlight by extension and scroll sideways: lang=%v wrap=%v",
			st.nativeCode.Buffer.Language(), st.nativeCode.SoftWrap)
	}
	tester.Frame()
	if !tester.HasText("main.go") || !tester.HasText("29 B") {
		t.Fatalf("viewer bar must carry the name and size: %q", tester.Texts())
	}
	if tester.HasText("Rendered") {
		t.Fatal("only Markdown offers the Rendered/Source choice")
	}

	s.openFileDetail(big)
	if st.nativePreviewPath != big || st.nativeCode == nil || st.nativeText.Truncated {
		t.Fatalf("a file past the old web cap must show whole: native=%q code=%v", st.nativePreviewPath, st.nativeCode != nil)
	}
	if st.nativeCode.SoftWrap != (st.nativeCode.Buffer.Language() == nil) {
		t.Fatal("plain text wraps, a known language does not")
	}
}

// TestPreviewPlanTruncatesPastTheTextCapTruthfully: an 8 MiB head with the
// pill that says so, never silence.
func TestPreviewPlanTruncatesPastTheTextCapTruthfully(t *testing.T) {
	s, tester, root := filesOpsTestShell(t)
	huge := writePreviewFile(t, root, "huge.txt", bytes.Repeat([]byte("a"), filesview.TextPreviewCap+1))
	s.openFileDetail(huge)
	st := &s.rightPanel
	if st.nativeText == nil || !st.nativeText.Truncated || st.nativeCode == nil {
		t.Fatalf("huge text = %+v code=%v", st.nativeText, st.nativeCode != nil)
	}
	tester.Frame()
	if !tester.HasText("Showing the first 8.0 MB") {
		t.Fatalf("truncation pill missing: %q", tester.Texts())
	}
}

// TestPreviewShowsBinaryEmptyAndCJKFilesHonestly: binary has no code view,
// an empty file is an empty view, and CJK prose past the classification
// probe stays text (the cut-rune regression).
func TestPreviewShowsBinaryEmptyAndCJKFilesHonestly(t *testing.T) {
	s, tester, root := filesOpsTestShell(t)
	st := &s.rightPanel

	bin := writePreviewFile(t, root, "blob.dat", append([]byte("head"), make([]byte, 64)...))
	s.openFileDetail(bin)
	if st.nativeText == nil || !st.nativeText.IsBinary || st.nativeCode != nil {
		t.Fatalf("binary = %+v code=%v", st.nativeText, st.nativeCode != nil)
	}
	tester.Frame()
	if !tester.HasText("Binary file") {
		t.Fatalf("binary state missing: %q", tester.Texts())
	}

	empty := writePreviewFile(t, root, "empty.txt", nil)
	s.openFileDetail(empty)
	if st.nativeCode == nil || st.nativeCode.Buffer.Len() != 0 || st.nativeText.IsBinary {
		t.Fatalf("empty file must be an empty code view: code=%v", st.nativeCode != nil)
	}

	cjk := writePreviewFile(t, root, "notes.txt", []byte(strings.Repeat("中", 4000)))
	s.openFileDetail(cjk)
	if st.nativeText == nil || st.nativeText.IsBinary || st.nativeCode == nil || st.nativeCode.Buffer.Len() != 12000 {
		t.Fatalf("CJK text must stay text: %+v", st.nativeText)
	}
}

// TestPreviewSourceChoiceDrivesTheBarToggle clicks the real toggle.
func TestPreviewSourceChoiceDrivesTheBarToggle(t *testing.T) {
	s, tester, root := filesOpsTestShell(t)
	small := writePreviewFile(t, root, "small.md", []byte("# hello\n"))
	s.openFileDetail(small)
	tester.Frame()
	tester.Click("Source")
	tester.Frame()
	if !s.rightPanel.markdownSource || s.rightPanel.nativePreviewPath != small {
		t.Fatalf("clicking Source: markdownSource=%v native=%q", s.rightPanel.markdownSource, s.rightPanel.nativePreviewPath)
	}
	tester.Click("Rendered")
	tester.Frame()
	if s.rightPanel.markdownSource || s.rightPanel.nativeMarkdown == nil {
		t.Fatalf("clicking Rendered: markdownSource=%v rendered=%v", s.rightPanel.markdownSource, s.rightPanel.nativeMarkdown != nil)
	}
}

// TestCodeEditorThemeFollowsTheReviewPalette: Files and the Git review color
// a file the same way, because the editor's tokens come from gd_palette.
func TestCodeEditorThemeFollowsTheReviewPalette(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := ui.LightTheme()
		if dark {
			theme = ui.DarkTheme()
		}
		th := codeEditorTheme(theme)
		p := gdPaletteFor(theme)
		for kind, class := range map[editor.TokenKind]codehl.Class{
			editor.TokenKeyword: codehl.Keyword, editor.TokenString: codehl.String,
			editor.TokenComment: codehl.Comment, editor.TokenType: codehl.Type,
			editor.TokenNumber: codehl.Number, editor.TokenOperator: codehl.Plain,
		} {
			if got := th.Tokens[kind].Color; got != p.syntax[class] {
				t.Errorf("dark=%v kind %v = %v, want the review's %v", dark, kind, got, p.syntax[class])
			}
		}
		if th.Text != p.code || th.LineNumber != p.lineNumber {
			t.Errorf("dark=%v code/line-number colors diverge from the review", dark)
		}
		if th.Selection != editor.DefaultTheme(theme).Selection {
			t.Errorf("dark=%v structural colors must stay the framework's", dark)
		}
	}
}

// TestReviewAndFilesPaintACharacterTheSameColor: the review's lines and the
// Files code view run one tokenizer through one palette, so a character has
// one color on both surfaces — the promise of codehl.KindClasses, checked
// character by character instead of table against table.
func TestReviewAndFilesPaintACharacterTheSameColor(t *testing.T) {
	lines := []string{
		"package main",
		`import "fmt"`,
		"/* a block",
		"still comment */ var n int = 42",
		`func main() { fmt.Println("x\n", n) } // done`,
	}
	for _, dark := range []bool{false, true} {
		theme := ui.LightTheme()
		if dark {
			theme = ui.DarkTheme()
		}
		files, review := codeEditorTheme(theme), gdPaletteFor(theme)
		segs := codehl.Lines("main.go", lines)
		var (
			state  uint64
			tokens []editor.Token
		)
		for row, line := range lines {
			tokens, state = editor.LanguageFor("main.go").Tokenizer.Tokenize(line, state, tokens[:0])
			want := make([]ui.Color, len(line))
			for i := range want {
				want[i] = files.Text
			}
			start := 0
			for _, tok := range tokens {
				for i := start; i < min(tok.End, len(line)); i++ {
					want[i] = files.Tokens[tok.Kind].Color
				}
				start = max(start, tok.End)
			}
			var got []ui.Color
			for _, span := range gdCodeSpans(line, segs[row], nil, review) {
				for range len(span.Text) {
					got = append(got, span.Color)
				}
			}
			if len(got) != len(want) {
				t.Fatalf("dark=%v line %d: review painted %d characters, Files %d", dark, row, len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("dark=%v line %d col %d (%q of %q): review %v, Files %v", dark, row, i, line[i], line, got[i], want[i])
					break
				}
			}
		}
	}
}
