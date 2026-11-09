package remotehost

import (
	"context"
	"log/slog"
	"time"

	"github.com/thewei/solco/internal/feedback"
)

/**
 * [INPUT]: Options.Feedback（若还实现 Stream——*feedback.Client 实现）、feedback.Event
 * [OUTPUT]: Server.startFeedbackWatch/stopFeedbackWatch（随 serve/Close）、Server.NotifyFeedback（events 帧 `feedback`）
 * [POS]: 手机收到“有 Agent 在等你”的唯一来源（2026-11-09）：Host 跟随 broker 的 SSE，把每个新 Ask / 已解决的 Ask 变成一帧 `{"t":"feedback","kind":"ask"|"resolved","threadId","askId","waiting"}` 推给所有已打开的 events 套接字；帧只带不透明 id 与等待数，永远没有项目名、问题或答案——手机的本地通知也只用固定文案；桌面与常驻 hostd 都经此路径，不另接线；broker 不在时每 5 s 重连，不报错
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const feedbackReconnect = 5 * time.Second

type feedbackStreamer interface {
	Stream(ctx context.Context, fn func(feedback.Event)) error
}

// startFeedbackWatch follows the broker's event stream for as long as the Host serves. A source that cannot stream
// (a test stand-in) simply never produces frames.
func (s *Server) startFeedbackWatch() {
	src, ok := s.opts.Feedback.(feedbackStreamer)
	if !ok || s.opts.Feedback == nil {
		return
	}
	s.mu.Lock()
	if s.feedbackCancel != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.feedbackCancel, s.feedbackDone = cancel, done
	s.mu.Unlock()
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			_ = src.Stream(ctx, func(e feedback.Event) { s.feedbackEvent(ctx, e) })
			select {
			case <-ctx.Done():
				return
			case <-time.After(feedbackReconnect):
			}
		}
	}()
}

func (s *Server) stopFeedbackWatch() {
	s.mu.Lock()
	cancel, done := s.feedbackCancel, s.feedbackDone
	s.feedbackCancel, s.feedbackDone = nil, nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (s *Server) feedbackEvent(ctx context.Context, e feedback.Event) {
	kind := ""
	switch e.Type {
	case "ask_created":
		kind = "ask"
	case "ask_resolved":
		kind = "resolved"
	default:
		return
	}
	turn, cancel := context.WithTimeout(ctx, feedbackCallTimeout)
	defer cancel()
	snap, err := feedback.ReadSnapshot(turn, s.opts.Feedback, "")
	if err != nil {
		slog.Debug("remote host feedback watch read failed", "error", err)
		return
	}
	s.NotifyFeedback(kind, e.ThreadID, e.AskID, snap.Waiting)
}

// NotifyFeedback tells every open events socket that an Ask appeared (kind "ask") or was settled ("resolved"). The
// frame holds opaque ids and the count of waiting Asks and nothing else; the phone re-reads GET /feedback for the
// rest. A socket whose queue is full drops the frame rather than stall the Mac.
func (s *Server) NotifyFeedback(kind, threadID, askID string, waiting int) {
	frame := map[string]any{"t": "feedback", "kind": kind, "threadId": threadID, "askId": askID, "waiting": waiting}
	s.mu.Lock()
	defer s.mu.Unlock()
	for q := range s.notices {
		select {
		case q <- frame:
		default:
		}
	}
}
