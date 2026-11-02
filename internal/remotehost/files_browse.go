package remotehost

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/thewei/solco/internal/filesview"
)

/**
 * [INPUT]: 依赖 Options.Files（开关）、git.go 的 workspaceFolder（Workspace → 目录）、internal/filesview 的 ListDirect/IgnoreSet/GitIgnorePaths/PreviewFlavor/ReadPreview/ReadImageDataURL（与桌面 /files 同一份忽略规则与分类）、server.go 的 guard/writeError
 * [OUTPUT]: 对外提供 GET /api/v3/workspaces/{id}/files?dir=（一层目录，目录在前、忽略项已滤掉、最多 maxFileEntries 条）与 GET /api/v3/workspaces/{id}/files/content?path=（text/markdown 正文最多 1 MiB、image 为 data URL 最多 4 MiB、其余 binary 无正文），scope view
 * [POS]: 手机「Files」只读视图：路径一律是 Workspace 目录内的相对路径（拒绝绝对路径、`..`、NUL，符号链接解析后必须仍在目录内，越界一律 400 bad_request）；不写、不改名、不删；目录名/文件名/正文永不进日志，只记设备、workspace、条目数与字节数
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	maxFileEntries = 1000
	// maxPhoneImage is the largest image the phone takes as a data URL (the desktop's WebView tier takes 24 MiB).
	maxPhoneImage = 4 << 20
)

// FileEntry is one child of a listed folder. Kind is dir | file | symlink (a symlink is shown, never walked into).
type FileEntry struct {
	Name string `json:"name"`
	Path string `json:"path"` // slash-separated, relative to the Workspace folder
	Kind string `json:"kind"`
	Size int64  `json:"size"` // bytes; 0 for a folder
}

// FilesList is one folder's direct children.
type FilesList struct {
	WorkspaceID string      `json:"workspaceId"`
	Dir         string      `json:"dir"` // "" = the Workspace folder itself
	Entries     []FileEntry `json:"entries"`
	// Truncated: more entries exist than the list carries.
	Truncated bool `json:"truncated,omitempty"`
}

// FileContent is one file's read-only preview. Flavor is text | markdown | image | binary.
type FileContent struct {
	WorkspaceID string `json:"workspaceId"`
	Path        string `json:"path"`
	Name        string `json:"name"`
	Flavor      string `json:"flavor"`
	Size        int64  `json:"size"`
	// Truncated: the text was cut to the wire budget (1 MiB).
	Truncated bool `json:"truncated,omitempty"`
	// TooLarge: an image past what the phone takes; no data.
	TooLarge bool   `json:"tooLarge,omitempty"`
	Content  string `json:"content,omitempty"`
	DataURL  string `json:"dataUrl,omitempty"`
}

var errOutsideFolder = errors.New("path is outside the folder")

// containedPath joins root and a relative path and proves the symlink-resolved target still lies inside root.
// A path that does not exist passes the escape test as os.ErrNotExist so the caller can answer 404.
func containedPath(root, rel string) (string, error) {
	if strings.ContainsRune(rel, 0) || len(rel) > maxLaunchPath {
		return "", errOutsideFolder
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errOutsideFolder
	}
	full := filepath.Join(root, clean)
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	inside, err := filepath.Rel(canonicalRoot, resolved)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", errOutsideFolder
	}
	return full, nil
}

// filesFolder answers the shared refusals (Files off, unknown Workspace) and resolves the path query to a folder entry.
func (s *Server) filesFolder(w http.ResponseWriter, r *http.Request, query string) (root, full, rel, workspaceID string, ok bool) {
	if !s.opts.Files {
		writeError(w, http.StatusNotFound, "files_unavailable", "")
		return "", "", "", "", false
	}
	root, workspaceID, ok = s.workspaceFolder(w, r)
	if !ok {
		return "", "", "", "", false
	}
	rel = filepath.ToSlash(filepath.Clean(filepath.FromSlash(r.URL.Query().Get(query))))
	if rel == "." {
		rel = ""
	}
	full, err := containedPath(root, rel)
	switch {
	case errors.Is(err, errOutsideFolder):
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return "", "", "", "", false
	case err != nil:
		writeError(w, http.StatusNotFound, "unknown_file", "")
		return "", "", "", "", false
	}
	return root, full, rel, workspaceID, true
}

func (s *Server) handleFilesList(w http.ResponseWriter, r *http.Request, dev Device) {
	root, full, rel, id, ok := s.filesFolder(w, r, "dir")
	if !ok {
		return
	}
	children, err := filesview.ListDirect(full, root)
	if err != nil {
		if errors.Is(err, filesview.ErrNotADirectory) {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
		slog.Warn("remote host files: list failed", "device", dev.ID, "workspace", id)
		writeError(w, http.StatusBadGateway, "files_failed", "")
		return
	}
	gitPaths, _ := filesview.GitIgnorePaths(r.Context(), root) // not a repository: the fixed names still apply
	ignore := filesview.NewIgnoreSet(gitPaths)
	out := FilesList{WorkspaceID: id, Dir: rel, Entries: []FileEntry{}}
	for _, child := range children {
		if ignore.Ignored(child.Name, child.RelPath) {
			continue
		}
		if len(out.Entries) == maxFileEntries {
			out.Truncated = true
			break
		}
		entry := FileEntry{Name: child.Name, Path: filepath.ToSlash(child.RelPath), Kind: string(child.Kind), Size: child.SizeBytes}
		if child.IsDir {
			entry.Size = 0 // a folder's own size is the filesystem's business, not information
		}
		out.Entries = append(out.Entries, entry)
	}
	slog.Info("remote host files list", "device", dev.ID, "workspace", id, "entries", len(out.Entries))
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFileContent(w http.ResponseWriter, r *http.Request, dev Device) {
	root, full, rel, id, ok := s.filesFolder(w, r, "path")
	if !ok {
		return
	}
	info, err := os.Stat(full)
	if err != nil || rel == "" || info.IsDir() {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	name := filepath.Base(full)
	out := FileContent{WorkspaceID: id, Path: rel, Name: name, Size: info.Size()}
	switch filesview.PreviewFlavor(name) {
	case filesview.FlavorImage:
		out.Flavor = "image"
		if info.Size() > maxPhoneImage {
			out.TooLarge = true
			break
		}
		url, err := filesview.ReadImageDataURL(full)
		if err != nil {
			slog.Warn("remote host files: image failed", "device", dev.ID, "workspace", id)
			writeError(w, http.StatusBadGateway, "files_failed", "")
			return
		}
		out.DataURL = url
	default:
		preview, err := filesview.ReadPreview(full, root)
		if err != nil {
			slog.Warn("remote host files: read failed", "device", dev.ID, "workspace", id)
			writeError(w, http.StatusBadGateway, "files_failed", "")
			return
		}
		if preview.IsBinary {
			out.Flavor = "binary"
			break
		}
		out.Flavor = string(filesview.PreviewFlavor(name))
		out.Content, out.Truncated = preview.Content, preview.Truncated
	}
	slog.Info("remote host files content", "device", dev.ID, "workspace", id, "bytes", info.Size(), "flavor", out.Flavor)
	writeJSON(w, http.StatusOK, out)
}
