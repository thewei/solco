package nativeui

/**
 * [INPUT]: 依赖 history.ReadUsage 的只读用量读取、Sessions 列表行与 Shell 后台车道
 * [OUTPUT]: 提供行内会话详情的状态机（点击展开/收起、代际隔离的懒加载用量）与展开后的详情表面板
 * [POS]: Sessions 列表的就地详情层；每次展开现读原始会话文件的用量，不缓存、不落盘、不跳详情页；表本身是 history_info.go
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"log/slog"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/history"
)

// usageReadTimeout bounds one lazy usage read.
const usageReadTimeout = 20 * time.Second

// historyPreview is the single open inline detail: which session, and the
// usage read from its original file (nil while loading or when the Provider
// reports none). The zero value is closed.
type historyPreview struct {
	key     string
	loading bool
	usage   *history.SessionUsage
}

// toggleHistoryPreview opens the detail of one session, or closes it when it
// is the open one. Only one is open at a time.
func (s *Shell) toggleHistoryPreview(key string) {
	if s.hist.preview.key == key {
		s.closeHistoryPreview()
		return
	}
	s.openHistoryPreview(key)
}

// openHistoryPreview unfolds the session at once and reads its usage on the
// background lane. A newer open (or close) bumps the generation, so a late
// result can never fill another row's detail.
func (s *Shell) openHistoryPreview(key string) {
	if key == "" {
		return
	}
	if s.hist.previewCancel != nil {
		s.hist.previewCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.hist.previewCancel = cancel
	generation := s.hist.previewGen.Add(1)
	meta := s.sessionMetaByKey(key)
	s.hist.preview = historyPreview{key: key, loading: meta != nil}
	if meta == nil {
		return
	}
	s.readSessionUsage(ctx, *meta, func(usage *history.SessionUsage) {
		if generation != s.hist.previewGen.Load() {
			return
		}
		s.hist.preview = historyPreview{key: key, usage: usage}
	})
}

// closeHistoryPreview drops the open detail and any read still in flight.
func (s *Shell) closeHistoryPreview() {
	if s.hist.previewCancel != nil {
		s.hist.previewCancel()
		s.hist.previewCancel = nil
	}
	s.hist.previewGen.Add(1)
	s.hist.preview = historyPreview{}
}

// readSessionUsage reads one session's usage off the UI lane and hands the
// result (nil when none) to apply on the window lane.
func (s *Shell) readSessionUsage(ctx context.Context, meta history.SessionMeta, apply func(*history.SessionUsage)) {
	s.dispatch(func() {
		readCtx, cancel := context.WithTimeout(ctx, usageReadTimeout)
		defer cancel()
		usage, err := history.ReadUsage(readCtx, meta.Agent, meta.FilePath)
		var result *history.SessionUsage
		switch {
		case err == nil:
			result = &usage
		case ctx.Err() == nil:
			slog.Debug("history usage unavailable", "agent", meta.Agent, "error", err)
		}
		s.applyOnWindow(func() { apply(result) })
	})
}

// sessionMetaByKey finds a session in the open list, or the open detail page.
func (s *Shell) sessionMetaByKey(key string) *history.SessionMeta {
	for _, summary := range s.hist.sessions {
		if summary.Meta.Key == key {
			meta := summary.Meta
			return &meta
		}
	}
	return nil
}

// sessionPreview is the panel under an open row: the detail table, indented
// to the title column so it reads as part of the row above.
func (s *Shell) sessionPreview(c *ui.Context, summary history.SessionSummary) {
	sp := Spacing()
	tokens := designTokens(c.Theme().Dark)
	ui.Column(c).FillWidth().Padding(sp.S, rowInset, sp.M, rowInset+24+sp.L).Children(func() {
		ui.Column(c).FillWidth().Padding(sp.M, sp.L).Radius(Radius().Row).Background(tokens.Content).Children(func() {
			s.sessionInfoTable(c, summary.Meta, s.hist.preview, true)
		})
	})
}
