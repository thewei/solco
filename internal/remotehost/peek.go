package remotehost

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

/**
 * [INPUT]: 依赖 Backend.ReadPane、Server（resolveInstance/currentTree/guard）
 * [OUTPUT]: 对外提供 PeekLine（纯函数）与 GET /api/v3/panes/{paneId}/peek
 * [POS]: 手机侧栏/窥视条用的一行「这个窗格此刻在显示什么」：Herdr pane.read(visible) 取最后一行有字的行，≤200 字节纯文本；每 pane 1 s 节流，所以多手机/多轮询只打一次 Herdr
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	peekMaxBytes = 200
	peekTTL      = time.Second
	peekCacheMax = 256
)

// PeekLine returns the last line of a visible screen that carries a letter or
// digit (a TUI's bottom border or blank padding says nothing), trimmed,
// stripped of control characters and cut to peekMaxBytes on a rune boundary.
func PeekLine(screen string) string {
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.Map(func(r rune) rune {
			if r == '\t' {
				return ' '
			}
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, lines[i])
		line = strings.TrimSpace(line)
		if !strings.ContainsFunc(line, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
			continue
		}
		return truncateUTF8(line, peekMaxBytes)
	}
	return ""
}

func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

type peekEntry struct {
	at   time.Time
	line string
}

type peekCache struct {
	mu      sync.Mutex
	entries map[string]peekEntry
}

func (c *peekCache) get(key string, now time.Time) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || now.Sub(e.at) >= peekTTL {
		return "", false
	}
	return e.line, true
}

func (c *peekCache) put(key, line string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]peekEntry{}
	}
	if len(c.entries) >= peekCacheMax {
		for k, e := range c.entries {
			if now.Sub(e.at) >= peekTTL {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= peekCacheMax {
			c.entries = map[string]peekEntry{}
		}
	}
	c.entries[key] = peekEntry{at: now, line: line}
}

func (s *Server) handlePeek(w http.ResponseWriter, r *http.Request, _ Device) {
	instance, ok := s.resolveAnyInstance(w, r)
	if !ok {
		return
	}
	paneID := r.PathValue("paneID")
	tree, err := s.currentTree(instance)
	if err != nil {
		slog.Warn("remote host peek: tree failed", "instance", instance, "error", err)
		writeError(w, http.StatusBadGateway, "backend_unavailable", "")
		return
	}
	if !tree.hasPane(paneID) {
		writeError(w, http.StatusNotFound, "unknown_pane", "")
		return
	}
	key := instance + "\x00" + paneID
	now := s.opts.Now()
	line, cached := s.peeks.get(key, now)
	if !cached {
		screen, err := s.opts.Backend.ReadPane(r.Context(), instance, paneID)
		if err != nil {
			slog.Warn("remote host peek: pane read failed", "instance", instance, "pane", paneID, "error", err)
			writeError(w, http.StatusBadGateway, "backend_unavailable", "")
			return
		}
		line = PeekLine(screen)
		s.peeks.put(key, line, now)
	}
	writeJSON(w, http.StatusOK, map[string]string{"paneId": paneID, "line": line})
}
