package remotehost

import (
	"context"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/thewei/solco/internal/feedback"
	"github.com/thewei/solco/internal/notifyhook"
)

/**
 * [INPUT]: Options.Channels（用户在 Settings 里加的 webhook 渠道与宽限秒数，每次事件现读）、s.notices（每个已打开的 events 套接字一个队列 = 手机在线）、feedback.Source
 * [OUTPUT]: Server.NotifyChannels（只看手机是否在线）、Server.notifyChannelsAsk（在线也要给手机一个宽限期去打开该 Ask）、Server.markFeedbackSeen / askResolvedForChannels
 * [POS]: 「手机告知不到时调用 webhook」（2026-11-09 决策）的 Host 一半：手机没有存活的 events 连接 → 立刻调用；有连接的 Feedback Ask → 等 GraceSeconds（默认 30 s），期间手机打开了该线程或 Ask 已被回答/结束就不调用，否则调用。后台被系统切网的手机，连接随之断开，Host 看到的就是“不在线”。载荷只含通知用的词（项目 · Agent、问题首行、描述摘要 ≤500 字、solco:// 链接），绝无答案/图片/路径
 * [PROTOCOL]: URL 与令牌是秘密，只在 notifyhook 内使用；日志只记渠道名与状态；变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	channelSummaryMax = 500 // runes of an Agent's description sent to a channel (the user's choice, 2026-11-09)
	channelTitleMax   = 120
)

// ChannelSource returns the enabled webhook targets and the phone's grace period. nil, or no targets, means no
// channel is configured and the Host never calls out.
type ChannelSource func() (targets []notifyhook.Target, grace time.Duration)

// channelState is the Host's memory for the grace gate.
type channelState struct {
	mu      sync.Mutex
	seen    map[string]time.Time   // thread → last time a phone read it
	waiting map[string]*time.Timer // ask → its pending grace timer
	sender  notifyhook.Sender
}

func (s *Server) channelTargets() ([]notifyhook.Target, time.Duration) {
	if s.opts.Channels == nil {
		return nil, 0
	}
	return s.opts.Channels()
}

// phoneConnected reports whether any phone holds an open events socket.
func (s *Server) phoneConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.notices) > 0
}

// NotifyChannels calls the user's channels with ev when no phone is connected to be told. A connected phone
// raises its own notification, so nothing is sent.
func (s *Server) NotifyChannels(ev notifyhook.Event) {
	if s.phoneConnected() {
		return
	}
	targets, _ := s.channelTargets()
	if len(targets) == 0 {
		return
	}
	go s.channels.sender.Send(context.Background(), targets, ev)
}

// notifyChannelsAsk is the Feedback rule: no phone connected → call now; a phone connected → give it grace to
// open the thread (or for the Ask to be settled), then call.
func (s *Server) notifyChannelsAsk(ev notifyhook.Event, since time.Time) {
	targets, grace := s.channelTargets()
	if len(targets) == 0 {
		return
	}
	send := func() { s.channels.sender.Send(context.Background(), targets, ev) }
	if !s.phoneConnected() {
		go send()
		return
	}
	st := &s.channels
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.waiting == nil {
		st.waiting = map[string]*time.Timer{}
	}
	if old := st.waiting[ev.Item]; old != nil {
		old.Stop()
	}
	st.waiting[ev.Item] = time.AfterFunc(grace, func() {
		st.mu.Lock()
		_, pending := st.waiting[ev.Item]
		delete(st.waiting, ev.Item)
		opened := st.seen[ev.Thread].After(since)
		st.mu.Unlock()
		if pending && !opened {
			send()
		}
	})
}

// markFeedbackSeen records that a phone opened a thread.
func (s *Server) markFeedbackSeen(thread string) {
	if thread == "" {
		return
	}
	st := &s.channels
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.seen == nil {
		st.seen = map[string]time.Time{}
	}
	st.seen[thread] = time.Now()
}

// askResolvedForChannels drops an Ask's pending call: it was answered or ended before the grace ran out.
func (s *Server) askResolvedForChannels(askID string) {
	st := &s.channels
	st.mu.Lock()
	defer st.mu.Unlock()
	if t := st.waiting[askID]; t != nil {
		t.Stop()
		delete(st.waiting, askID)
	}
}

// stopChannels cancels every pending grace timer (Close).
func (s *Server) stopChannels() {
	st := &s.channels
	st.mu.Lock()
	defer st.mu.Unlock()
	for id, t := range st.waiting {
		t.Stop()
		delete(st.waiting, id)
	}
}

// channelEventForAsk words a new Ask for the channels: the phone notification's own words, a bounded excerpt
// of the description, and the deep link. The description is read only when a channel will actually be called.
func (s *Server) channelEventForAsk(ctx context.Context, e feedback.Event) notifyhook.Event {
	project, client := notifyhook.Words(e.Project, feedbackNameMax), notifyhook.Words(e.Client, feedbackNameMax)
	title := project
	if client != "" {
		if title != "" {
			title += " · "
		}
		title += client
	}
	if title == "" {
		title = "An Agent is waiting for your reply"
	}
	ev := notifyhook.Event{
		Kind: notifyhook.KindQuestion, Title: notifyhook.Words(title, channelTitleMax),
		Text: notifyhook.Words(e.Preview, feedbackPreviewMax), Project: project, Agent: client,
		Thread: e.ThreadID, Item: e.AskID, Link: feedbackLink(e.ThreadID, e.AskID),
	}
	if snap, err := feedback.ReadSnapshot(ctx, s.opts.Feedback, e.ThreadID); err == nil && snap.Detail != nil {
		for _, a := range snap.Detail.Asks {
			if a.ID == e.AskID {
				ev.Summary = notifyhook.Excerpt(a.Description, channelSummaryMax)
				break
			}
		}
	}
	return ev
}

// feedbackLink is the solco:// link the phone's Feedback screen opens on.
func feedbackLink(thread, ask string) string {
	q := url.Values{}
	q.Set("thread", thread)
	if ask != "" {
		q.Set("ask", ask)
	}
	return "solco://feedback?" + q.Encode()
}

// automationChannelWords words one automation step the way the desktop's system notification does.
func automationChannelWords(kind, name string) (title, text string) {
	switch kind {
	case "draft":
		return "Draft needs approval", name
	case "done":
		return name, "Finished"
	default:
		return name, "Failed"
	}
}

func assistantNeedsWords(n int) string {
	if n == 1 {
		return "1 Assistant needs you"
	}
	return strconv.Itoa(n) + " Assistants need you"
}

// NotifyAgent hands an Agent's attention notification (the desktop's own title and body) to the channels.
func (s *Server) NotifyAgent(title, body string) {
	s.NotifyChannels(notifyhook.Event{Kind: notifyhook.KindAgent, Title: notifyhook.Words(title, channelTitleMax), Text: notifyhook.Words(body, feedbackPreviewMax)})
}
