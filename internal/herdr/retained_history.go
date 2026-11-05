package herdr

/**
 * [INPUT]: 依赖 Herdr Protocol 22 的 pane.copy_motion、pane.get、pane.selection.read（只读）；历史行坐标归 Herdr/Ghostty 管理
 * [OUTPUT]: 提供 Manager.ReadPaneRetainedText，读取当前仍在缓冲区内的完整纯文本历史与修订号
 * [POS]: Herdr 的历史输出查询边界；不滚动共享视口、不附着终端、不维护第二份 scrollback
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

// PaneRetainedText is a text extraction from the server-owned scrollback,
// not a raw PTY log or a coding agent's own alternate-screen conversation.
type PaneRetainedText struct {
	Text            string
	RetainedRows    uint64
	ContentRevision uint64
}

// ReadPaneRetainedText reads the same retained Ghostty screen rows used by
// Herdr's TUI copy mode. pane.read has a hard 1,000-row ceiling on Herdr
// 0.9.5; pane.selection.read has absolute screen-row coordinates instead.
//
// Explicit export/history actions may call this function. Do not invoke it
// on every render: the response includes the entire retained text in memory.
// A growing/overwritten buffer returns stale_content rather than silently
// stitching together inconsistent history.
func (m *Manager) ReadPaneRetainedText(ctx context.Context, session, paneID string) (*PaneRetainedText, error) {
	if ctx == nil {
		return nil, errors.New("context is required")
	}
	if strings.TrimSpace(paneID) == "" {
		return nil, errors.New("pane id is required")
	}
	socket, err := m.reachableSocket(session)
	if err != nil {
		return nil, err
	}
	call := func(method string, params any, kind string, dest any) error {
		var raw json.RawMessage
		if err := callRPCWithContext(ctx, socket, method, params, false, &raw); err != nil {
			return err
		}
		return decodeTypedResult(raw, kind, dest)
	}

	// First pin the Ghostty content sequence. pane.get's pane.revision is a
	// different counter and cannot be used as content_revision.
	var first struct {
		Cursor          PaneTextPoint `json:"cursor"`
		ContentRevision uint64        `json:"content_revision"`
	}
	if err := call("pane.copy_motion", map[string]any{
		"pane_id": paneID, "cursor": PaneTextPoint{Row: 0, Col: 0},
		"motion": "line_end",
	}, "pane_copy_motion", &first); err != nil {
		return nil, fmt.Errorf("pin Herdr pane content: %w", err)
	}
	if first.ContentRevision%2 != 0 {
		return nil, errors.New("Herdr pane content is changing")
	}

	var pane struct {
		Pane struct {
			Scroll *PaneScroll `json:"scroll"`
		} `json:"pane"`
	}
	if err := call("pane.get", map[string]any{"pane_id": paneID}, "pane_info", &pane); err != nil {
		return nil, fmt.Errorf("read Herdr pane scroll bounds: %w", err)
	}
	if pane.Pane.Scroll == nil {
		return nil, errors.New("Herdr pane scroll bounds are unavailable")
	}
	metrics := pane.Pane.Scroll
	total := metrics.MaxOffsetFromBottom + metrics.ViewportRows
	if total < metrics.MaxOffsetFromBottom || total > math.MaxUint32 {
		return nil, errors.New("Herdr pane history exceeds supported row coordinates")
	}
	if total == 0 {
		return &PaneRetainedText{ContentRevision: first.ContentRevision}, nil
	}

	// line_end resolves the actual final column without guessing the pane's
	// width. Passing the pinned revision rejects a changed/rerolled buffer.
	var last struct {
		Cursor          PaneTextPoint `json:"cursor"`
		ContentRevision uint64        `json:"content_revision"`
	}
	if err := call("pane.copy_motion", map[string]any{
		"pane_id":          paneID,
		"cursor":           PaneTextPoint{Row: uint32(total - 1), Col: 0},
		"motion":           "line_end",
		"content_revision": first.ContentRevision,
	}, "pane_copy_motion", &last); err != nil {
		return nil, fmt.Errorf("resolve Herdr history endpoint: %w", err)
	}
	if last.ContentRevision != first.ContentRevision || last.Cursor.Row != uint32(total-1) {
		return nil, errors.New("Herdr pane history changed during read")
	}

	var selection struct {
		PaneID string `json:"pane_id"`
		Text   string `json:"text"`
	}
	if err := call("pane.selection.read", map[string]any{
		"pane_id":          paneID,
		"anchor":           PaneTextPoint{Row: 0, Col: 0},
		"cursor":           last.Cursor,
		"content_revision": first.ContentRevision,
	}, "pane_selection", &selection); err != nil {
		return nil, fmt.Errorf("read Herdr retained history: %w", err)
	}
	if selection.PaneID != paneID {
		return nil, errors.New("Herdr pane history response targeted a different pane")
	}
	return &PaneRetainedText{
		Text: selection.Text, RetainedRows: total, ContentRevision: first.ContentRevision,
	}, nil
}
