package nativeui

/**
 * [INPUT]: 依赖 AssistantsBridge 的客户端（Coordinator 读）、history.HistoryService 目录（OMP 会话按 Coordinator 文件夹名定位）、bindChatWindow 的只读绑定
 * [OUTPUT]: AssistantsBridge.PreviewCoordinator（Coordinator 没在线时把它上一段对话只读显示在共享 Chat 里）、Shell.releaseAssistantPreview、Shell.assistantPreviewActive、Shell.rescanForPreview（目录里还没有它的转录时节流地要一轮扫描）、assistantPreviewKey
 * [POS]: 2026-11-07——一个 Assistant 一个 Coordinator 一段对话：Agent 停了/没启动时仍能读到它上次说了什么，点输入框启动后（`--resume` 同一份转录）无缝切到实时 Chat；只读绑定走 Sessions 详情同一条管线，没有第二套转录读取，也不写任何文件
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"strings"
	"time"

	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/personalagent"
)

// previewScanGap is the least time between two catalog rounds asked for by a
// preview that found nothing: the page asks every 5 s and must never turn that
// into a scan loop.
const previewScanGap = 30 * time.Second

// CoordinatorPreview says whether the shared Chat now shows the Assistant's
// previous Coordinator conversation (read-only). The page keeps asking while
// no live Coordinator is bound.
type CoordinatorPreview struct {
	AssistantID string `json:"assistantId"`
	Previewing  bool   `json:"previewing"`
}

// assistantPreviewPick picks, among OMP sessions the catalog found for this
// Assistant, the newest one. A Coordinator runs in its own folder
// (`…/coordinators/<assistantId>`), and that folder is the session's working
// directory — the only fact used; no title or text is read.
func assistantPreviewPick(list []history.SessionSummary, assistantID string) (history.SessionMeta, bool) {
	suffix := "/coordinators/" + assistantID
	var best history.SessionMeta
	found := false
	for _, summary := range list {
		meta := summary.Meta
		if meta.Archived || meta.Agent != history.AgentOMP {
			continue
		}
		if !strings.HasSuffix(strings.TrimRight(strings.ReplaceAll(meta.ProjectPath, "\\", "/"), "/"), suffix) {
			continue
		}
		if !found || meta.UpdatedAt > best.UpdatedAt {
			best, found = meta, true
		}
	}
	return best, found
}

// assistantPreviewKey is the catalog key of that newest session ("" = none).
func assistantPreviewKey(list []history.SessionSummary, assistantID string) string {
	meta, _ := assistantPreviewPick(list, assistantID)
	return meta.Key
}

// PreviewCoordinator shows the Assistant's previous Coordinator conversation,
// read-only, until a live Coordinator is bound. It is a no-op (and reports
// false) while the Chat already sits on the Coordinator's own pane, so a late
// answer can never pull the live Chat back to the file.
func (b *AssistantsBridge) PreviewCoordinator(ctx context.Context, assistantID string) (CoordinatorPreview, error) {
	out := CoordinatorPreview{AssistantID: assistantID}
	// The 2026-11-07 storm: a hidden /assistants WebView kept polling this
	// after the user left the route and re-bound the shared Chat read-only
	// over the pane's live tail every cycle. Off-route asks are void.
	if b.shell.router.Path() != routeAssistants {
		return out, nil
	}
	c, err := b.client()
	if err != nil {
		return out, err
	}
	rctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	co, err := c.Coordinator(rctx, assistantID)
	if err != nil {
		return out, err
	}
	var service HistoryListView
	b.shell.callOnWindow(func() { service = b.shell.hist.service })
	if service == nil {
		return out, nil
	}
	list, err := service.List(rctx, history.HistoryQuery{Provider: history.AgentOMP, Search: assistantID, Limit: 20})
	if err != nil {
		return out, err
	}
	key := assistantPreviewKey(list, assistantID)
	b.shell.callOnWindow(func() {
		if key == "" && co.TaskID != "" && co.State != personalagent.CoordinatorNone {
			b.shell.rescanForPreview()
		}
		out.Previewing = b.shell.previewAssistantConversation(co, key)
	})
	return out, nil
}

// rescanForPreview asks for one catalog round (window lane) when a Coordinator
// that has run before has no conversation in the catalog: its transcript was
// written after the last scan — OMP's identity reaches Solco through Herdr and
// only a Solco status hook (not OMP's) starts a scan — so without this a
// Coordinator stopped in the same app run shows an empty page until a restart
// (seen on the isolated sandbox 2026-11-07). Throttled; the page's next ask
// finds the key.
func (s *Shell) rescanForPreview() {
	if s.hist.service == nil || time.Since(s.assistants.previewScanAt) < previewScanGap {
		return
	}
	s.assistants.previewScanAt = time.Now()
	s.startHistoryScan()
}

// previewAssistantConversation binds the read-only window (window lane).
func (s *Shell) previewAssistantConversation(co personalagent.Coordinator, key string) bool {
	if co.State == personalagent.CoordinatorReady && co.PaneID != "" && s.chatPaneID == co.PaneID && !s.chatReadOnly {
		return false // the live Chat is already on the Coordinator
	}
	if key == "" {
		s.releaseAssistantPreview()
		return false
	}
	s.assistants.previewKey = key
	if !s.chatReadOnly || s.chatConversationID != key {
		s.bindChatWindow(key, true)
	}
	// The binding happens between frames, before assistantsPage has had a
	// build to claim it: stamp it as this frame's, or the next frame's
	// releaseIdleHistoryChat drops it at once (seen on the real app 2026-11-07).
	s.hist.detailFrame = s.hist.viewFrame
	return s.chatReadOnly && s.chatConversationID == key
}

// assistantPreviewActive reports the shared Chat showing a Coordinator's
// previous conversation (its standing brief rows are hidden like a live one's).
func (s *Shell) assistantPreviewActive() bool {
	return s.chatReadOnly && s.assistants.previewKey != "" && s.assistants.previewKey == s.chatConversationID
}

// releaseAssistantPreview drops the preview binding (window lane): the live
// Coordinator took over, or the Assistant has no conversation to show.
func (s *Shell) releaseAssistantPreview() {
	if s.assistants.previewKey == "" {
		return
	}
	if s.assistantPreviewActive() {
		s.releaseHistoryChat()
	}
	s.assistants.previewKey = ""
}
