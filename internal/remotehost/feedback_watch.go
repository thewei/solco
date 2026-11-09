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
 * [POS]: 手机收到“有 Agent 在等你”的唯一来源（2026-11-09）：Host 跟随 broker 的 SSE，把每个新 Ask / 已解决的 Ask 变成一帧 `{"t":"feedback","kind":"ask"|"resolved","threadId","askId","waiting"[,"project","client","preview"]}` 推给所有已打开的 events 套接字；新 Ask 的帧带项目名、Agent 名和问题首行（用户 2026-11-09 要求：通知要看得出是哪个项目发生什么事，与桌面系统通知同字，修订了“手机推送不带文字”；帧只走已鉴权的固定指纹连接，通知由手机本地发出，不经 Expo/FCM 等第三方），永远没有答案、描述、图片或路径；桌面与常驻 hostd 都经此路径，不另接线；broker 不在时每 5 s 重连，不报错
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
	if kind == "resolved" {
		s.askResolvedForChannels(e.AskID)
	} else if targets, _ := s.channelTargets(); len(targets) > 0 {
		s.notifyChannelsAsk(s.channelEventForAsk(turn, e), time.Now())
	}
	n := FeedbackNotice{Kind: kind, ThreadID: e.ThreadID, AskID: e.AskID, Waiting: snap.Waiting}
	if kind == "ask" {
		n.Project, n.Client, n.Preview = clip(e.Project, feedbackNameMax), clip(e.Client, feedbackNameMax), clip(e.Preview, feedbackPreviewMax)
	}
	s.NotifyFeedback(n)
}

const (
	feedbackNameMax    = 80  // runes of a project or Agent name in a frame
	feedbackPreviewMax = 200 // runes of the question's first line in a frame
)

func clip(text string, max int) string {
	if r := []rune(text); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return text
}

// FeedbackNotice is one `feedback` events frame: which Ask moved and, for a new one, the words the phone's
// notification shows (the same project, Agent and question first line as the desktop's system notification).
type FeedbackNotice struct {
	Kind     string // "ask" | "resolved"
	ThreadID string
	AskID    string
	Waiting  int
	Project  string
	Client   string
	Preview  string
}

// NotifyFeedback tells every open events socket that an Ask appeared (kind "ask") or was settled ("resolved"). The
// frame holds opaque ids, the count of waiting Asks and, for a new Ask, the short words of the notification; the
// phone re-reads GET /feedback for the rest. A socket whose queue is full drops the frame rather than stall the Mac.
func (s *Server) NotifyFeedback(n FeedbackNotice) {
	frame := map[string]any{"t": "feedback", "kind": n.Kind, "threadId": n.ThreadID, "askId": n.AskID, "waiting": n.Waiting}
	for k, v := range map[string]string{"project": n.Project, "client": n.Client, "preview": n.Preview} {
		if v != "" {
			frame[k] = v
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for q := range s.notices {
		select {
		case q <- frame:
		default:
		}
	}
}
