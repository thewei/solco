package remotehost

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

/**
 * [INPUT]: 依赖 HistoryBackend（可选的 Backend 扩展）、Server（resolveAnyInstance/currentTree/guard）
 * [OUTPUT]: 对外提供 PaneHistory、HistoryBackend、GET /api/v3/terminals/{terminalID}/history（能力位 terminalHistory）
 * [POS]: 手机终端的「完整历史」：observe 流只有当前屏，滚出去的行靠这个只读快照取回；Herdr 保留文本（纯 UTF-8，无 ANSI）取尾部 512 KiB（整行边界），每 pane 5 s 缓存，Herdr 正在写入时有限重试，仍写不稳则 409 history_busy；日志只记 pane/错误，从不记内容
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	historyMaxBytes = 512 << 10
	historyTTL      = 5 * time.Second
	historyCacheMax = 64
	historyAttempts = 5
	historyRetryGap = 40 * time.Millisecond
)

// PaneHistory is one pane's retained text (scrollback + screen) as Herdr
// reports it, with the content revision the read was pinned to.
type PaneHistory struct {
	Text     string
	Rows     uint64
	Revision uint64
}

// HistoryBackend is an optional Backend extension: a Backend that implements
// it makes the Host advertise `terminalHistory` and serve the history route.
type HistoryBackend interface {
	ReadPaneHistory(ctx context.Context, instance, paneID string) (PaneHistory, error)
}

type terminalHistory struct {
	Revision     uint64 `json:"revision"`
	RetainedRows uint64 `json:"retainedRows"`
	Truncated    bool   `json:"truncated"`
	Text         string `json:"text"`
}

// tailHistory keeps the end of text within max bytes, starting on a line
// boundary (or, for one endless line, a rune boundary).
func tailHistory(text string, max int) (string, bool) {
	if len(text) <= max {
		return text, false
	}
	cut := len(text) - max
	if nl := strings.IndexByte(text[cut:], '\n'); nl >= 0 && cut+nl+1 < len(text) {
		return text[cut+nl+1:], true
	}
	for cut < len(text) && !utf8.RuneStart(text[cut]) {
		cut++
	}
	return text[cut:], true
}

// historyChanging reports a read Herdr refused because the pane kept
// printing (the retained-text read is pinned to one content revision).
func historyChanging(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "is changing") || strings.Contains(msg, "changed during read") ||
		strings.Contains(msg, "stale_content")
}

type historyEntry struct {
	at   time.Time
	body terminalHistory
}

type historyCache struct {
	mu      sync.Mutex
	entries map[string]historyEntry
}

func (c *historyCache) get(key string, now time.Time) (terminalHistory, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || now.Sub(e.at) >= historyTTL {
		return terminalHistory{}, false
	}
	return e.body, true
}

func (c *historyCache) put(key string, body terminalHistory, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]historyEntry{}
	}
	if len(c.entries) >= historyCacheMax {
		for k, e := range c.entries {
			if now.Sub(e.at) >= historyTTL {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= historyCacheMax {
			c.entries = map[string]historyEntry{}
		}
	}
	c.entries[key] = historyEntry{at: now, body: body}
}

func (s *Server) handleTerminalHistory(w http.ResponseWriter, r *http.Request, _ Device) {
	hb, ok := s.opts.Backend.(HistoryBackend)
	if !ok {
		writeError(w, http.StatusNotFound, "unsupported", "")
		return
	}
	instance, ok := s.resolveAnyInstance(w, r)
	if !ok {
		return
	}
	tree, err := s.currentTree(instance)
	if err != nil {
		slog.Warn("remote host history: tree failed", "instance", instance, "error", err)
		writeError(w, http.StatusBadGateway, "backend_unavailable", "")
		return
	}
	paneID, ok := tree.paneForTerminal(r.PathValue("terminalID"))
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_terminal", "")
		return
	}
	key := instance + "\x00" + paneID
	now := s.opts.Now()
	if body, hit := s.history.get(key, now); hit {
		writeJSON(w, http.StatusOK, body)
		return
	}
	var (
		h       PaneHistory
		readErr error
	)
	for attempt := 0; attempt < historyAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(historyRetryGap):
			}
		}
		h, readErr = hb.ReadPaneHistory(r.Context(), instance, paneID)
		if readErr == nil || !historyChanging(readErr) {
			break
		}
	}
	switch {
	case readErr == nil:
	case errors.Is(readErr, context.Canceled):
		return
	case historyChanging(readErr):
		slog.Info("remote host history: pane kept changing", "instance", instance, "pane", paneID)
		writeError(w, http.StatusConflict, "history_busy", "The terminal is still printing; try again in a moment.")
		return
	default:
		slog.Warn("remote host history: read failed", "instance", instance, "pane", paneID, "error", readErr)
		writeError(w, http.StatusBadGateway, "backend_unavailable", "")
		return
	}
	text, truncated := tailHistory(h.Text, historyMaxBytes)
	body := terminalHistory{Revision: h.Revision, RetainedRows: h.Rows, Truncated: truncated, Text: text}
	s.history.put(key, body, now)
	slog.Debug("remote host history: served", "instance", instance, "pane", paneID, "bytes", len(text), "rows", h.Rows, "truncated", truncated)
	writeJSON(w, http.StatusOK, body)
}
