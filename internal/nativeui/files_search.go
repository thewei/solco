package nativeui

/**
 * [INPUT]: rightPanel.currentRoot/showIgnored、sidebar 头部 headerSearch（搜索钮 + 输入框）、filesview.ListFiles/MatchFiles/SearchContent（git ls-files、ripgrep → git grep 回退）、ui/editor.State.GoTo/Select
 * [OUTPUT]: Files 侧栏的统一搜索（2026-10-30）：一个输入框 + 「Files | Content」分段开关，一次只显示一种结果。Files = 内存里对异步文件清单的子序列/前缀排序；Content = 去抖后的 ripgrep/git grep 全文搜索（Aa/全词/正则三个开关，「含忽略文件」跟随树工具条的 Show ignored files 眼睛），结果按文件分组并高亮命中；点命中 → 打开文件并把代码视图滚到命中行、选中命中文本（Markdown 切到源码）
 * [POS]: 只做呈现与调度：枚举与搜索全在 solco-gitkit/filesview（渲染零 IO、全部后台 + 代际围栏）；不新增 Herdr 方法、不建第二份索引存储；无快捷键，只有搜索钮（产品决策 2026-10-30）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/thewei/solco-gitkit/filesview"
)

const (
	// filesSearchRows caps the file-name rows drawn; the status line says how many matched.
	filesSearchRows = 300
	// filesSearchDebounce waits out typing before a content search spawns a process.
	filesSearchDebounce = 220 * time.Millisecond
)

// Search modes: the segmented switch under the field.
const (
	filesSearchNames = iota
	filesSearchText
)

// filesSearchState is the Files search: the field's text, which kind of
// result it asks for, and the latest answer of each kind. gen fences content
// lookups, idxGen the file index; a bump drops whatever is in flight.
type filesSearchState struct {
	open                            bool
	query                           string
	mode                            int
	caseSensitive, wholeWord, regex bool

	gen    atomic.Uint64
	cancel context.CancelFunc
	idxGen atomic.Uint64

	// File names: the index is read once per root and ignored-toggle, then ranked in memory.
	index          []string
	indexRoot      string
	indexIgnored   bool
	indexReady     bool
	indexLoading   bool
	indexTruncated bool
	indexErr       string
	hits           []string
	hitTotal       int

	// File content: the latest answer stays on screen while the next one runs.
	content        filesview.ContentResult
	contentLoading bool
	contentErr     string
}

// filesReveal is the place a content hit asks the code view to show.
type filesReveal struct {
	path              string
	line, col, length int
}

// filesSearchOn reports whether results replace the tree.
func (s *Shell) filesSearchOn() bool {
	st := &s.filesSearch
	return st.open && strings.TrimSpace(st.query) != ""
}

// filesSearchReset drops the search with its root (a new project's files
// share nothing with the old one's).
func (s *Shell) filesSearchReset() {
	st := &s.filesSearch
	st.open, st.query = false, ""
	s.filesSearchInvalidate()
	s.filesSearchChanged()
}

// filesSearchInvalidate forgets the file index (a refresh, a created, renamed
// or deleted file) and, while a name search is on screen, reads it again.
func (s *Shell) filesSearchInvalidate() {
	st := &s.filesSearch
	st.idxGen.Add(1)
	st.index, st.indexReady, st.indexLoading, st.indexErr, st.indexRoot = nil, false, false, "", ""
	if s.filesSearchOn() {
		s.filesSearchChanged()
	}
}

// filesSearchChanged runs whenever what to look for changed: the text, the
// mode, an option, or the ignored toggle.
func (s *Shell) filesSearchChanged() {
	st := &s.filesSearch
	st.gen.Add(1)
	if st.cancel != nil {
		st.cancel()
		st.cancel = nil
	}
	st.contentLoading = false
	st.hits, st.hitTotal = nil, 0
	if !st.open || strings.TrimSpace(st.query) == "" {
		st.content, st.contentErr = filesview.ContentResult{}, ""
		return
	}
	switch st.mode {
	case filesSearchNames:
		s.filesSearchEnsureIndex()
		s.filesSearchRank()
	default:
		s.filesSearchRunContent()
	}
}

// filesSearchRank orders the indexed paths for the current query.
func (s *Shell) filesSearchRank() {
	st := &s.filesSearch
	if !st.indexReady {
		return
	}
	st.hits, st.hitTotal = filesview.MatchFiles(st.index, st.query, filesSearchRows)
}

// filesSearchEnsureIndex starts the one background read of the root's files
// the name search ranks; render never lists a directory.
func (s *Shell) filesSearchEnsureIndex() {
	st := &s.filesSearch
	root, ignored := s.rightPanel.currentRoot, s.rightPanel.showIgnored
	if root == "" {
		return
	}
	if (st.indexReady || st.indexLoading) && st.indexRoot == root && st.indexIgnored == ignored {
		return
	}
	gen := st.idxGen.Add(1)
	st.index, st.indexReady, st.indexLoading, st.indexErr, st.indexTruncated = nil, false, true, "", false
	st.indexRoot, st.indexIgnored = root, ignored
	load := func() (filesview.FileIndex, error) {
		ctx, cancel := context.WithTimeout(context.Background(), filesview.SearchDeadline)
		defer cancel()
		return filesview.ListFiles(ctx, root, ignored)
	}
	apply := func(idx filesview.FileIndex, err error) {
		st.indexLoading = false
		if err != nil {
			st.indexErr = filesSearchError(err)
			return
		}
		st.index, st.indexTruncated, st.indexReady = idx.Paths, idx.Truncated, true
		if st.mode == filesSearchNames {
			s.filesSearchRank()
		}
	}
	if s.win == nil {
		idx, err := load()
		apply(idx, err)
		return
	}
	go func() {
		idx, err := load()
		s.applyGuarded(st.idxGen.Load, gen, func() { apply(idx, err) })
	}()
}

// filesSearchRunContent searches file text in the background after a short
// pause; the previous answer stays up until the new one lands.
func (s *Shell) filesSearchRunContent() {
	st := &s.filesSearch
	if len([]rune(strings.TrimSpace(st.query))) < filesview.MinContentQuery {
		st.content, st.contentErr = filesview.ContentResult{}, ""
		return
	}
	root := s.rightPanel.currentRoot
	if root == "" {
		return
	}
	q := filesview.ContentQuery{
		Pattern: st.query, CaseSensitive: st.caseSensitive, WholeWord: st.wholeWord,
		Regex: st.regex, IncludeIgnored: s.rightPanel.showIgnored,
	}
	gen := st.gen.Load()
	ctx, cancel := context.WithTimeout(context.Background(), filesview.SearchDeadline)
	st.cancel = cancel
	st.contentLoading = true
	apply := func(res filesview.ContentResult, err error) {
		st.contentLoading = false
		if err != nil {
			st.content, st.contentErr = filesview.ContentResult{}, filesSearchError(err)
			return
		}
		st.content, st.contentErr = res, ""
	}
	if s.win == nil {
		res, err := filesview.SearchContent(ctx, root, q)
		cancel()
		apply(res, err)
		return
	}
	go func() {
		defer cancel()
		select {
		case <-time.After(filesSearchDebounce):
		case <-ctx.Done():
			return
		}
		res, err := filesview.SearchContent(ctx, root, q)
		s.applyGuarded(st.gen.Load, gen, func() { apply(res, err) })
	}()
}

// pluralMatches writes "1 match" / "N matches".
func pluralMatches(n int) string {
	if n == 1 {
		return "1 match"
	}
	return itoa(n) + " matches"
}

// filesSearchError words an engine failure for the results area.
func filesSearchError(err error) string {
	if errors.Is(err, filesview.ErrNoContentEngine) {
		return "Needs ripgrep or a Git repository. Install ripgrep with “brew install ripgrep”."
	}
	return err.Error()
}

// filesSearchOpenFirst is Enter in the field: the best hit opens.
func (s *Shell) filesSearchOpenFirst() {
	st := &s.filesSearch
	root := s.rightPanel.currentRoot
	switch {
	case !s.filesSearchOn():
	case st.mode == filesSearchNames && len(st.hits) > 0:
		s.openFileDetail(filepath.Join(root, st.hits[0]))
	case st.mode == filesSearchText && len(st.content.Files) > 0 && len(st.content.Files[0].Matches) > 0:
		f := st.content.Files[0]
		s.openFileHit(filepath.Join(root, f.Path), f.Matches[0])
	}
}

// openFileHit opens a content hit and shows its line.
func (s *Shell) openFileHit(abs string, m filesview.ContentMatch) {
	length := m.End - m.Start
	if m.Start < 0 || length < 0 {
		length = 0
	}
	s.openFileAt(abs, m.Line-1, m.Col, length)
}

// openFileAt opens path with the code view showing line (from 0) and
// selecting length bytes from byte column col. Markdown opens as source, the
// only form with lines to point at.
func (s *Shell) openFileAt(path string, line, col, length int) {
	st := &s.rightPanel
	st.reveal = &filesReveal{path: path, line: line, col: col, length: length}
	s.openDetail(DetailFile) // the hit's file is a card in the center, whichever branch below shows it
	if filesview.PreviewFlavor(path) == filesview.FlavorMarkdown && !st.markdownSource {
		if st.selectedFilePath == path {
			s.setMarkdownSource(true) // flips to the editor and lands the reveal on its text
			return
		}
		st.markdownSource = true
	}
	if st.selectedFilePath == path {
		s.applyFilesReveal() // already showing, or the text lands and applies it
		return
	}
	s.openFilePreview(path)
}

// applyFilesReveal puts the code view on the requested line once the file's
// text is there.
func (s *Shell) applyFilesReveal() {
	st := &s.rightPanel
	r := st.reveal
	if r == nil || st.nativeCode == nil || r.path != st.selectedFilePath {
		return
	}
	st.reveal = nil
	buf := st.nativeCode.Buffer
	line := max(0, min(r.line, buf.LineCount()-1))
	start := buf.Offset(editor.Pos{Line: line, Col: r.col})
	end := min(start+r.length, buf.Len())
	// GoTo centers the line; Select only widens to the hit, keeping the center.
	st.nativeCode.GoTo(editor.Pos{Line: line, Col: r.col})
	st.nativeCode.Select(start, end)
}

// ─── Rendering ─────────────────────────────────────────────────────────

// filesSearchOptions is the strip under the field: the Files | Content
// switch and, for content, the three matching toggles.
func (s *Shell) filesSearchOptions(c *ui.Context) {
	st := &s.filesSearch
	ui.Column(c).FillWidth().Padding(0, sidebarGutter, 6).Gap(6).Children(func() {
		if segmentTabs(c, "Search in", &st.mode, []segmentTab{{"Files", iconFile}, {"Content", iconFileText}}, nil) {
			s.filesSearchChanged()
		}
		if st.mode != filesSearchText {
			return
		}
		ui.Row(c).FillWidth().Gap(2).AlignItems(ui.Center).Children(func() {
			s.filesSearchToggle(c, "Aa", "Match case", &st.caseSensitive)
			s.filesSearchToggle(c, "ab", "Match whole word", &st.wholeWord)
			s.filesSearchToggle(c, ".*", "Use regular expression", &st.regex)
		})
	})
}

// filesSearchToggle is one matching option: a text chip that wears the same
// on face every toggle does.
func (s *Shell) filesSearchToggle(c *ui.Context, face, label string, on *bool) {
	t := c.Theme()
	k := gorexColorsOf(t.Dark)
	b := tipped(c, ui.ButtonBase(c.Key("find-opt:"+face)).Height(gorexPaneBtn).Padding(0, 8).
		Radius(iconButtonRadius(gorexPaneBtn)).AlignItems(ui.Center).Justify(ui.Center).FocusRing(false).Label(label), label)
	gorexToggleFace(b, k, *on)
	col := k.iconMuted
	if b.Hovered() || *on {
		col = k.text
	}
	b.Children(func() {
		ui.Text(c, face).Font(gdCodeFont()).FontSize(Typography().BodySmall).FontWeight(WeightSemibold).TextColor(col).PassThrough()
	})
	if b.Clicked() {
		*on = !*on
		s.filesSearchChanged()
	}
}

// filesSearchResults replaces the tree while the field holds a query.
func (s *Shell) filesSearchResults(c *ui.Context, root string) {
	st := &s.filesSearch
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(4, sidebarGutter, 12).Children(func() {
			if st.mode == filesSearchNames {
				s.filesSearchNameRows(c, root)
			} else {
				s.filesSearchContentRows(c, root)
			}
		})
	})
}

// filesSearchNote is a quiet line of the results area.
func filesSearchNote(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(Typography().Micro).TextColor(c.Theme().TextMuted).Padding(4, treeRowPad)
}

func filesSearchWait(c *ui.Context) {
	ui.Row(c).FillWidth().Padding(6, treeRowPad).Children(func() { ui.Spinner(c).Size(12, 12) })
}

// filesSearchDir is the folder part of a relative path, or "".
func filesSearchDir(rel string) string {
	if dir := filepath.Dir(rel); dir != "." {
		return dir
	}
	return ""
}

func filesSearchDirText(c *ui.Context, dir string) {
	if dir == "" {
		return
	}
	ui.Text(c, dir).Font(gdCodeFont()).FontSize(Typography().Micro).TextColor(c.Theme().TextMuted).
		SingleLine().Shrink(3).MinWidth(0)
}

// filesSearchMenu is a hit's right-click menu: the tree row's own.
func (s *Shell) filesSearchMenu(root, rel string) func(*ui.Menu) {
	abs := filepath.Join(root, rel)
	e := filesview.Entry{Name: filepath.Base(rel), Path: abs, RelPath: rel}
	return func(m *ui.Menu) { s.filesRowMenu(m, e) }
}

func (s *Shell) filesSearchNameRows(c *ui.Context, root string) {
	st := &s.filesSearch
	switch {
	case st.indexErr != "":
		filesSearchNote(c, st.indexErr)
		return
	case !st.indexReady:
		filesSearchWait(c)
		return
	case len(st.hits) == 0:
		filesSearchNote(c, "No file names match")
		return
	}
	count := pluralFiles(st.hitTotal)
	if st.hitTotal > len(st.hits) {
		count = "Showing the first " + itoa(len(st.hits)) + " of " + pluralFiles(st.hitTotal)
	}
	filesSearchNote(c, count)
	for _, rel := range st.hits {
		abs := filepath.Join(root, rel)
		dir := filesSearchDir(rel)
		_, clicked := s.sidebarTreeRow(c, sidebarTreeSpec{
			Key: "find-file:" + rel, Title: filepath.Base(rel), Label: "File " + rel, Tooltip: rel,
			Glyph: iconFile, Current: s.rightPanel.selectedFilePath == abs,
			Menu:     s.filesSearchMenu(root, rel),
			Trailing: func() { filesSearchDirText(c, dir) },
		})
		if clicked {
			s.openFileDetail(abs)
		}
	}
	if st.indexTruncated {
		filesSearchNote(c, "Large project: only the first "+pluralFiles(filesview.MaxIndexedFiles)+" are listed.")
	}
}

func (s *Shell) filesSearchContentRows(c *ui.Context, root string) {
	st := &s.filesSearch
	switch {
	case len([]rune(strings.TrimSpace(st.query))) < filesview.MinContentQuery:
		filesSearchNote(c, "Type at least "+itoa(filesview.MinContentQuery)+" characters")
		return
	case st.contentErr != "":
		filesSearchNote(c, st.contentErr)
		return
	case st.content.Matches == 0 && st.contentLoading:
		filesSearchWait(c)
		return
	case st.content.Matches == 0:
		filesSearchNote(c, "No matches")
		return
	}
	summary := pluralMatches(st.content.Matches) + " in " + pluralFiles(len(st.content.Files))
	if st.content.Truncated {
		summary = "First " + pluralMatches(st.content.Matches) + " — narrow the search"
	}
	ui.Row(c).FillWidth().Padding(4, treeRowPad).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, summary).FontSize(Typography().Micro).TextColor(c.Theme().TextMuted).SingleLine().Grow(1).MinWidth(0)
		if st.contentLoading {
			ui.Spinner(c).Size(10, 10)
		}
	})
	for _, f := range st.content.Files {
		abs := filepath.Join(root, f.Path)
		dir := filesSearchDir(f.Path)
		n := len(f.Matches)
		_, clicked := s.sidebarTreeRow(c, sidebarTreeSpec{
			Key: "find-group:" + f.Path, Title: filepath.Base(f.Path), Label: "File " + f.Path, Tooltip: f.Path,
			Glyph: iconFile, Current: s.rightPanel.selectedFilePath == abs,
			Menu: s.filesSearchMenu(root, f.Path),
			Trailing: func() {
				filesSearchDirText(c, dir)
				ui.Text(c, itoa(n)).FontSize(Typography().Micro).TextColor(c.Theme().TextMuted).Shrink(0).Margin(0, 0, 0, 6)
			},
		})
		if clicked && n > 0 {
			s.openFileHit(abs, f.Matches[0])
		}
		for _, m := range f.Matches {
			if s.filesSearchMatchRow(c, abs, m) {
				s.openFileHit(abs, m)
			}
		}
		if f.More {
			filesSearchNote(c, "More matches in this file")
		}
	}
}

// filesSearchMatchRow is one hit: its line number and the line with the
// matched text marked. It reports a click.
func (s *Shell) filesSearchMatchRow(c *ui.Context, abs string, m filesview.ContentMatch) bool {
	t := c.Theme()
	row := ui.ButtonBase(c.Key("find-hit:"+abs+":"+itoa(m.Line)+":"+itoa(m.Col))).FillWidth().MinWidth(0).
		Height(s.treeRowHeight()).Padding(0, treeRowPad, 0, treeGlyphX(0)).Radius(treeRowRadius).
		FocusRing(false).AlignItems(ui.Center).Gap(8).Label("Line " + itoa(m.Line) + ": " + m.Text)
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		ui.Text(c, itoa(m.Line)).Font(gdCodeFont()).FontSize(Typography().Micro).TextColor(t.TextMuted).
			Width(28).Shrink(0).SingleLine().PassThrough()
		spans := []ui.Span{{Text: m.Text, Color: t.Text}}
		if m.Start >= 0 && m.End <= len(m.Text) && m.Start < m.End {
			mark := gdPaletteFor(t).match
			spans = []ui.Span{
				{Text: m.Text[:m.Start], Color: t.Text},
				{Text: m.Text[m.Start:m.End], Color: t.Text, Background: mark},
				{Text: m.Text[m.End:], Color: t.Text},
			}
		}
		ui.Box(c).Grow(1).MinWidth(0).ClipX().PassThrough().Children(func() {
			ui.RichText(c, spans...).Font(gdCodeFont()).FontSize(Typography().BodySmall).NoWrap().PassThrough()
		})
	})
	return row.Clicked()
}
