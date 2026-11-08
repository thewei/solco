package remotehost

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/thewei/solco-gitkit/filesview"
)

/**
 * [INPUT]: 依赖 Options.Files（开关）、files_browse.go 的 containedPath（与 /files/content 同一道越界检查）、git.go 的 workspaceFolder（Workspace → 目录）、solco-gitkit/filesview 的 ListFiles/MatchFiles（桌面 Files 侧栏「文件名」搜索的同一份索引与排序）、server.go 的 guard/writeError
 * [OUTPUT]: 对外提供 GET /api/v3/workspaces/{id}/files/search?q=（scope view）——按文件名/路径搜 Workspace 目录：最好的 maxSearchHits 条相对路径（最好的在前）、命中总数 total、索引是否被截断 truncated；capability filesSearch 随 Options.Files 一起出现
 * [POS]: 手机 Files 页的搜索（2026-11-07，用户：「Files 增加搜索API」）：只读、只搜文件名与路径（不搜正文、不含被忽略文件——与树同一套忽略规则）；返回的每一条都是 /files/content 打得开的普通文件（Git 还列着但磁盘上已删除的、指向目录外的符号链接、目录一律不返回）；查询词与命中路径永不进日志，只记设备、workspace、条数
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	// maxSearchHits is how many ranked paths one answer carries; the phone says how many more matched.
	maxSearchHits = 50
	// maxSearchQuery bounds the query in bytes.
	maxSearchQuery = 256
)

// searchIndex lists a folder's files; tests swap it to reach the index failures a real checkout does not have.
var searchIndex = func(ctx context.Context, root string) (filesview.FileIndex, error) {
	return filesview.ListFiles(ctx, root, false)
}

// FilesSearch is one file-name search of a Workspace folder.
type FilesSearch struct {
	WorkspaceID string `json:"workspaceId"`
	// Hits are folder-relative slash paths, best first.
	Hits []string `json:"hits"`
	// Total counts the files that matched: exact while every match fit in Hits, otherwise the count before the cap
	// (which may include a few that would not open).
	Total int `json:"total"`
	// Truncated: the folder holds more files than one search indexes, so Total may undercount.
	Truncated bool `json:"truncated,omitempty"`
}

// servable reports whether /files/content would serve rel: it resolves inside the folder (a link out, or a file Git still
// lists after it was deleted, does not) and is a regular file.
func servable(root, rel string) bool {
	full, err := containedPath(root, rel)
	if err != nil {
		return false
	}
	info, err := os.Stat(full)
	return err == nil && info.Mode().IsRegular()
}

func (s *Server) handleFilesSearch(w http.ResponseWriter, r *http.Request, dev Device) {
	if !s.opts.Files {
		writeError(w, http.StatusNotFound, "files_unavailable", "")
		return
	}
	root, id, ok := s.workspaceFolder(w, r)
	if !ok {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || len(query) > maxSearchQuery || strings.ContainsRune(query, 0) {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	index, err := searchIndex(r.Context(), root)
	switch {
	case errors.Is(err, filesview.ErrNoContentEngine):
		writeError(w, http.StatusBadGateway, "search_unavailable", "")
		return
	case err != nil:
		slog.Warn("remote host files: search failed", "device", dev.ID, "workspace", id)
		writeError(w, http.StatusBadGateway, "files_failed", "")
		return
	}
	ranked, total := filesview.MatchFiles(index.Paths, query, 0)
	out := FilesSearch{WorkspaceID: id, Hits: []string{}, Total: total, Truncated: index.Truncated}
	for _, rel := range ranked {
		if len(out.Hits) == maxSearchHits {
			break
		}
		if servable(root, rel) {
			out.Hits = append(out.Hits, rel)
		}
	}
	if len(out.Hits) < maxSearchHits {
		out.Total = len(out.Hits) // every match was checked, so the count leaves out the ones that would not open
	}
	slog.Info("remote host files search", "device", dev.ID, "workspace", id, "hits", len(out.Hits), "total", out.Total)
	writeJSON(w, http.StatusOK, out)
}
