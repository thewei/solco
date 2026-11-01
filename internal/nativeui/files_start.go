package nativeui

/**
 * [INPUT]: rightPanel.currentRoot、Shell.recentFiles（会话里最近打开的文件，MRU）、gitSnapshot().Files、根目录下的 README
 * [OUTPUT]: filesStart（/files 未选文件时的起始页：「Pick up where you left off」最近文件、「Changed in git」改动文件、README 卡片；三者都没有时退回一句话空态）、noteRecentFile（记录打开的文件）、filesStartData（目录内的最近文件/README，按根目录缓存，渲染不做 IO）
 * [POS]: 2026-10-30 UX audit 议题 3（空态 B，用户选定；design: designs/ux-audit）：把整页空白变成可点的入口；只读呈现，不新建索引，最近文件随 settings.Session 持久，不写别处
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
)

const (
	// recentFilesKept bounds the remembered files; filesStartShown how many
	// the start page lists per section.
	recentFilesKept = 12
	filesStartShown = 5
)

// filesStartCache is what the start page knows about the current root. It is
// rebuilt when the root changes or a file was opened, never per frame.
type filesStartCache struct {
	root   string
	stale  bool
	recent []string // absolute paths under root that still exist, newest first
	readme string   // absolute path of the root's README, or ""
}

// noteRecentFile moves path to the front of the remembered files.
func (s *Shell) noteRecentFile(path string) {
	kept := make([]string, 0, recentFilesKept)
	kept = append(kept, path)
	for _, p := range s.recentFiles {
		if p != path && len(kept) < recentFilesKept {
			kept = append(kept, p)
		}
	}
	s.recentFiles = kept
	s.filesStartInfo.stale = true
}

// filesStartData returns the root's recent files and README, stat'ing only
// when the cache is stale.
func (s *Shell) filesStartData(root string) *filesStartCache {
	cache := &s.filesStartInfo
	if cache.root == root && !cache.stale {
		return cache
	}
	cache.root, cache.stale = root, false
	cache.recent, cache.readme = nil, ""
	prefix := strings.TrimSuffix(root, string(filepath.Separator)) + string(filepath.Separator)
	for _, p := range s.recentFiles {
		if strings.HasPrefix(p, prefix) && len(cache.recent) < filesStartShown {
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				cache.recent = append(cache.recent, p)
			}
		}
	}
	for _, name := range []string{"README.md", "README.markdown", "README", "readme.md"} {
		if info, err := os.Stat(filepath.Join(root, name)); err == nil && !info.IsDir() {
			cache.readme = filepath.Join(root, name)
			break
		}
	}
	return cache
}

// filesStart is the /files center while no file is open.
func (s *Shell) filesStart(c *ui.Context, root string) {
	sp := Spacing()
	data := s.filesStartData(root)
	var changed []gitworkbench.ChangeFile
	if snap := s.gitSnapshot(); snap != nil && s.git != nil && s.git.root == root {
		for _, f := range snap.Files {
			if len(changed) < filesStartShown {
				changed = append(changed, f)
			}
		}
	}
	if len(data.recent) == 0 && len(changed) == 0 && data.readme == "" {
		emptyState(c, "Select a file to preview", "")
		return
	}
	// The launcher sits in the middle of the page, as the bare empty state does;
	// a long list shrinks the scroll to the page and scrolls.
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Justify(ui.Center).Children(func() {
		ui.Scroll(c).FillWidth().MinHeight(0).Children(func() {
			contentColumn(c, 460, func() { // the list is as wide as its longest row (<=420): the status letter stays near the names
				ui.Column(c).AlignSelf(ui.Center).MaxWidth(420).Padding(sp.XL, sp.L).Gap(sp.L).Children(func() {
					ui.Text(c, "Select a file to preview").FontWeight(WeightSemibold)
					if len(data.recent) > 0 {
						s.filesStartSection(c, "Pick up where you left off", func() {
							for _, p := range data.recent {
								rel, _ := filepath.Rel(root, p)
								s.filesStartRow(c, "recent:"+p, p, filepath.Base(p), filesSearchDir(rel), "")
							}
						})
					}
					if len(changed) > 0 {
						s.filesStartSection(c, "Changed in git", func() {
							for i := range changed {
								f := &changed[i]
								s.filesStartRow(c, "changed:"+f.Path, filepath.Join(root, f.Path),
									filepath.Base(f.Path), filesSearchDir(f.Path), gdStatusLetter(f))
							}
						})
					}
					if data.readme != "" {
						s.filesStartSection(c, "Start here", func() {
							s.filesStartRow(c, "readme", data.readme, filepath.Base(data.readme), "Project overview", "")
						})
					}
				})
			})
		})
	})
}

func (s *Shell) filesStartSection(c *ui.Context, title string, rows func()) {
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		ui.Text(c, strings.ToUpper(title)).FontSize(Typography().Micro).TextColor(c.Theme().TextMuted).Padding(0, 0, 4, 0)
		rows()
	})
}

// filesStartRow is one clickable file: glyph, name, quiet detail, and an
// optional git status letter at the end.
func (s *Shell) filesStartRow(c *ui.Context, key, abs, name, detail, status string) {
	t := c.Theme()
	sp := Spacing()
	row := ui.ButtonBase(c.Key(key)).FillWidth().Height(30).Radius(Radius().Control).
		Padding(0, sp.M).Label("Open " + name)
	if tint, ok := rowTint(designTokens(t.Dark), false, row.Hovered()); ok {
		row.Background(tint)
	}
	row.Transition(rowFade)
	row.Children(func() {
		ui.Row(c).FillWidth().Gap(sp.S).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconFile).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
			ui.Text(c, name).SingleLine().Shrink(0)
			ui.Text(c, detail).FontSize(Typography().BodySmall).TextColor(t.TextMuted).SingleLine().Grow(1).MinWidth(0)
			if status != "" {
				ui.Text(c, status).Font(gdCodeFont()).FontSize(Typography().BodySmall).TextColor(t.TextMuted).Shrink(0)
			}
		})
	})
	if row.Clicked() {
		s.openFilePreview(abs)
	}
}
