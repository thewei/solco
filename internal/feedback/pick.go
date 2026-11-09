/**
 * [INPUT]: 依赖 agentcatalog.ListSkills（已装 Skill 的 SKILL.md）、filesview.ListFiles/MatchFiles（git ls-files / ripgrep 的项目文件索引）
 * [OUTPUT]: 对外提供 ContextRef、PickItem、Pick（给「选文件 / 选 Skill」按钮的候选；文件先列 git 改动过的）、FilterSuggest（Agent 建议的上下文，丢弃无效项）、ResolveContext（把页面选的引用校验并解析成 Agent 能读的绝对路径）、ErrBadContext
 * [POS]: 回答附带的「上下文引用」——用户在回答里指给 Agent 的文件、文件夹、Skill；只传路径，不传内容，不写任何文件
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package feedback

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/thewei/solco-gitkit/filesview"
	"github.com/thewei/solco-gitkit/gitworkbench"
	"github.com/thewei/solco/internal/agentcatalog"
)

// ErrBadContext is a context reference that names nothing readable inside the thread's project or the installed Skills.
var ErrBadContext = errors.New("feedback: bad context reference")

const (
	maxContextRefs = 20
	maxPickItems   = 40
	maxSuggest     = 8
	maxChanged     = 12
	indexTTL       = 15 * time.Second
)

// Context reference kinds.
const (
	RefFile   = "file"
	RefFolder = "folder"
	RefSkill  = "skill"
)

// ContextRef is one thing the user points the Agent at with an answer. The page sends Kind + Name (a project-relative
// path, or a Skill name); the broker resolves it and fills Path with the absolute path the Agent reads (a Skill's is
// its SKILL.md). Hosts strip Path before it reaches a phone.
type ContextRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}

// PickItem is one row of the file / Skill picker.
type PickItem struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

type cachedIndex struct {
	at    time.Time
	files []string
	dirs  []string
}

var (
	indexMu sync.Mutex
	indexes = map[string]cachedIndex{}
	// listFiles is replaced by tests.
	listFiles = func(ctx context.Context, root string) (filesview.FileIndex, error) {
		return filesview.ListFiles(ctx, root, false)
	}
	// listChanged is the working tree's changed files (git status, newest work first for the picker); replaced by tests.
	listChanged = func(ctx context.Context, root string) []string {
		out, err := (&gitworkbench.Runner{}).Read(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
		if err != nil {
			return nil
		}
		return parseChanged(out)
	}
)

// ChangedDetail marks a picker row that is a changed file of the working tree.
const ChangedDetail = "changed"

// parseChanged reads `git status --porcelain=v1 -z`: "XY path\0", a rename or copy is followed by its source path.
func parseChanged(out []byte) []string {
	var paths []string
	parts := strings.Split(string(out), "\x00")
	for i := 0; i < len(parts); i++ {
		e := parts[i]
		if len(e) < 4 {
			continue
		}
		xy, p := e[:2], e[3:]
		if strings.ContainsAny(xy, "RC") {
			i++ // the source path of the rename
		}
		if strings.Contains(xy, "D") {
			continue // nothing left to read
		}
		paths = append(paths, p)
	}
	return paths
}

func projectIndex(ctx context.Context, root string) ([]string, []string) {
	indexMu.Lock()
	if c, ok := indexes[root]; ok && time.Since(c.at) < indexTTL {
		indexMu.Unlock()
		return c.files, c.dirs
	}
	indexMu.Unlock()
	idx, err := listFiles(ctx, root)
	if err != nil {
		return nil, nil
	}
	seen := map[string]bool{}
	var dirs []string
	for _, p := range idx.Paths {
		for d := path.Dir(p); d != "." && d != "/" && !seen[d]; d = path.Dir(d) {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	indexMu.Lock()
	indexes[root] = cachedIndex{at: time.Now(), files: idx.Paths, dirs: dirs}
	indexMu.Unlock()
	return idx.Paths, dirs
}

// Pick lists what the picker offers: kind "files" = files and folders of root ranked for query (an empty query lists the
// top level), kind "skills" = installed Skills whose name or description holds the query. Read-only.
func Pick(ctx context.Context, root, kind, query string) []PickItem {
	query = strings.TrimSpace(query)
	switch kind {
	case "skills":
		var out []PickItem
		q := strings.ToLower(query)
		for _, sk := range agentcatalog.ListSkills(root) {
			if q == "" || strings.Contains(strings.ToLower(sk.Name), q) || strings.Contains(strings.ToLower(sk.Detail), q) {
				out = append(out, PickItem{Kind: RefSkill, Name: sk.Name, Detail: clipRunes(sk.Detail, 140)})
			}
		}
		sort.SliceStable(out, func(i, j int) bool {
			return strings.HasPrefix(strings.ToLower(out[i].Name), q) && !strings.HasPrefix(strings.ToLower(out[j].Name), q)
		})
		if len(out) > maxPickItems {
			out = out[:maxPickItems]
		}
		return out
	case "files":
		if root == "" {
			return nil
		}
		files, dirs := projectIndex(ctx, root)
		var out []PickItem
		changed := map[string]bool{}
		if listChanged != nil {
			for _, p := range listChanged(ctx, root) {
				changed[p] = true
			}
		}
		if query == "" {
			n := 0
			for _, p := range sortedKeys(changed) {
				if n == maxChanged {
					break
				}
				out = append(out, PickItem{Kind: RefFile, Name: p, Detail: ChangedDetail})
				n++
			}
			for _, d := range dirs {
				if !strings.Contains(d, "/") {
					out = append(out, PickItem{Kind: RefFolder, Name: d})
				}
			}
			for _, f := range files {
				if !strings.Contains(f, "/") && !changed[f] {
					out = append(out, PickItem{Kind: RefFile, Name: f})
				}
			}
		} else {
			dh, _ := filesview.MatchFiles(dirs, query, 12)
			for _, d := range dh {
				out = append(out, PickItem{Kind: RefFolder, Name: d})
			}
			fh, _ := filesview.MatchFiles(files, query, maxPickItems)
			var hot, rest []PickItem
			for _, f := range fh {
				if changed[f] {
					hot = append(hot, PickItem{Kind: RefFile, Name: f, Detail: ChangedDetail})
				} else {
					rest = append(rest, PickItem{Kind: RefFile, Name: f})
				}
			}
			out = append(append(out, hot...), rest...)
		}
		if len(out) > maxPickItems {
			out = out[:maxPickItems]
		}
		return out
	}
	return nil
}

// ResolveContext validates the page's references against root and returns them with absolute paths. A file or folder
// must be a relative path that stays inside root and exists (its Kind is taken from the disk, not the page); a Skill
// must be installed. Anything else fails the whole answer, so the Agent never reads a path the user did not pick.
func ResolveContext(root string, refs []ContextRef) ([]ContextRef, error) {
	if len(refs) > maxContextRefs {
		return nil, ErrBadContext
	}
	var out []ContextRef
	var skills []agentcatalog.Skill
	seen := map[string]bool{}
	for _, r := range refs {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			return nil, ErrBadContext
		}
		var ref ContextRef
		switch r.Kind {
		case RefFile, RefFolder:
			rel := path.Clean(filepath.ToSlash(name))
			if root == "" || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
				return nil, ErrBadContext
			}
			abs := filepath.Join(root, filepath.FromSlash(rel))
			info, err := os.Stat(abs)
			if err != nil {
				return nil, ErrBadContext
			}
			kind := RefFile
			if info.IsDir() {
				kind = RefFolder
			}
			ref = ContextRef{Kind: kind, Name: rel, Path: abs}
		case RefSkill:
			if skills == nil {
				skills = agentcatalog.ListSkills(root)
			}
			found := false
			for _, sk := range skills {
				if sk.Name == name {
					ref = ContextRef{Kind: RefSkill, Name: sk.Name, Path: sk.Path}
					found = true
					break
				}
			}
			if !found {
				return nil, ErrBadContext
			}
		default:
			return nil, ErrBadContext
		}
		if key := ref.Kind + "\x00" + ref.Name; !seen[key] {
			seen[key] = true
			out = append(out, ref)
		}
	}
	return out, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// FilterSuggest keeps the references an Agent suggested that name something real in root (or an installed Skill),
// as path-free Kind + Name pairs; a bad suggestion is dropped, never an error, because the Agent is not the user.
func FilterSuggest(root string, refs []ContextRef) []ContextRef {
	var out []ContextRef
	seen := map[string]bool{}
	for _, r := range refs {
		if len(out) == maxSuggest {
			break
		}
		ok, err := ResolveContext(root, []ContextRef{{Kind: r.Kind, Name: r.Name}})
		if err != nil || len(ok) != 1 {
			continue
		}
		if key := ok[0].Kind + "\x00" + ok[0].Name; !seen[key] {
			seen[key] = true
			out = append(out, ContextRef{Kind: ok[0].Kind, Name: ok[0].Name})
		}
	}
	return out
}
