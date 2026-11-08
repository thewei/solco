package nativeui

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/remotehost"
	"github.com/thewei/solco/internal/sessiontrash"
)

/**
 * [INPUT]: 桌面的 SessionTrashView（背后是 internal/sessiontrash，自带锁）、sessionLookup 与 HistoryListView（目录元数据）、remoteLiveSessions 的身份快照、history_live.go 的 matchLiveSession
 * [OUTPUT]: Shell.hostTrasher（remotehost.Trasher）；失败翻译成 remotehost.TrashError 的稳定错误码
 * [POS]: 手机 Sessions 的回收站与桌面同一个 Store、同一组拒绝规则（运行中=精确身份匹配、刚写入=ErrBusy、路径必须在 Provider 根内、放回不覆盖），只开放可逆的“移入/放回”，永久删除与清空不在手机上；
 *        Host goroutine 只碰构造时捕获的服务与线程安全快照，窗口侧的列表对账（afterSessionsTrashed / 重新读取）经 applyOnWindow 回到窗口车道；
 *        日志只记操作名与数量，会话 id 与标题永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	remoteTrashMax      = 200
	remoteTrashScanWait = 20 * time.Second
)

type remoteTrasher struct {
	store     SessionTrashView
	lookup    sessionLookup
	service   HistoryListView
	live      *remoteLiveSessions
	onTrashed func(keys []string) // window lane: reconcile the open Sessions page
	onRestore func(ids []string)  // window lane: reload both lists
}

// hostTrasher builds the Host's Trash seam; nil when the pieces it needs are
// missing (no Trash wired, or a catalog without lookup), so the phone is told
// there is no Trash.
func (s *Shell) hostTrasher(service HistoryListView) remotehost.Trasher {
	lookup, ok := service.(sessionLookup)
	if !ok || s.hist.trash == nil {
		return nil
	}
	return &remoteTrasher{
		store: s.hist.trash, lookup: lookup, service: service, live: &s.remoteLive,
		onTrashed: func(keys []string) { s.applyOnWindow(func() { s.afterSessionsTrashed(keys) }) },
		onRestore: func(ids []string) {
			s.applyOnWindow(func() {
				s.dropTrashItems(ids)
				s.requestHistoryList()
				s.requestTrashList()
			})
		},
	}
}

func (t *remoteTrasher) Trash(_ context.Context, sessionID string) (string, error) {
	meta, ok := t.lookup.Session(sessionID)
	if !ok {
		return "", &remotehost.TrashError{Code: "unknown_session", Status: http.StatusNotFound}
	}
	t.live.mu.RLock()
	ids := t.live.ids
	t.live.mu.RUnlock()
	if _, running := matchLiveSession(meta, ids); running {
		return "", inUseError()
	}
	item, err := t.store.Trash(meta)
	switch {
	case err == nil:
	case errors.Is(err, sessiontrash.ErrBusy):
		return "", inUseError()
	default:
		slog.Warn("remote trash: move failed", "error", err)
		return "", &remotehost.TrashError{Code: "trash_failed", Status: http.StatusBadGateway, Detail: "Couldn't move the session to the Trash."}
	}
	if forgetter, ok := t.service.(historyForgetter); ok {
		forgetter.Forget(meta.Key) // the phone's next list must not show it
	}
	t.onTrashed([]string{meta.Key})
	return item.ID, nil
}

func inUseError() *remotehost.TrashError {
	return &remotehost.TrashError{Code: "session_in_use", Status: http.StatusConflict, Detail: "An Agent is still using this session. Try again when it's idle."}
}

func (t *remoteTrasher) List(context.Context) ([]remotehost.TrashEntry, error) {
	items, err := t.store.List()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].DeletedAt.After(items[j].DeletedAt) })
	if len(items) > remoteTrashMax {
		items = items[:remoteTrashMax]
	}
	entries := make([]remotehost.TrashEntry, 0, len(items))
	for _, item := range items {
		meta := trashSummary(item).Meta
		entries = append(entries, remotehost.TrashEntry{
			ID:        item.ID,
			Agent:     string(item.Agent),
			Title:     fallbackText(strings.TrimSpace(meta.Title), history.Untitled),
			Project:   fallbackText(meta.ProjectName, shortDir(meta.ProjectPath)),
			DeletedMs: item.DeletedAt.UnixMilli(),
			Messages:  meta.MessageCount,
		})
	}
	return entries, nil
}

func (t *remoteTrasher) Restore(ctx context.Context, entryID string) error {
	_, err := t.store.Restore(entryID)
	switch {
	case err == nil:
	case errors.Is(err, sessiontrash.ErrNotFound):
		return &remotehost.TrashError{Code: "unknown_entry", Status: http.StatusNotFound}
	case errors.Is(err, sessiontrash.ErrExists):
		return &remotehost.TrashError{Code: "session_exists", Status: http.StatusConflict, Detail: "A session file already exists at the original location."}
	default:
		slog.Warn("remote trash: put back failed", "error", err)
		return &remotehost.TrashError{Code: "trash_failed", Status: http.StatusBadGateway, Detail: "Couldn't put the session back."}
	}
	// The restored file comes back through the same scan that found it the
	// first time; bounded, so a slow disk never holds the phone's answer long.
	scanCtx, cancel := context.WithTimeout(ctx, remoteTrashScanWait)
	if _, err := t.service.Scan(scanCtx); err != nil {
		slog.Warn("remote trash: history scan after put back failed", "error", err)
	}
	cancel()
	t.onRestore([]string{entryID})
	return nil
}
