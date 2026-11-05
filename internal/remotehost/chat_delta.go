package remotehost

import (
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/chatstream"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: read-only OMP/Pi chat.stream events, Host's Herdr-resolved Pane/Session target and canonical chatsession.State.
 * [OUTPUT]: bounded, agent-keyed in-memory companion overlay + wake signal; seq-fenced Chat suffix wire patches.
 * [POS]: SC-06 phone Chat transport; no new OMP/Herdr process and no alternate transcript authority.
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	chatStreamMaxEntries = 32
	chatStreamTTL        = 2 * time.Minute
	chatStreamCoalesce   = 80 * time.Millisecond
	chatPatchTailLimit   = 3
)

type streamSlot struct {
	buffer chatstream.Buffer
	seen   time.Time
}

type companionStreamFeed struct {
	mu      sync.Mutex
	streams map[string]*streamSlot
	wake    chan struct{}
}

func streamKey(agent, pane, path string) string {
	return agent + "\x00" + pane + "\x00" + filepath.Clean(path)
}

// IngestOMPChatStream receives the *existing* apphook event on the desktop's
// UI lane. It never creates/replaces an agent or trusts a remote path: the WS
// consumer must match an independently resolved exact OMP Pane and transcript.
func (s *Server) IngestOMPChatStream(e chatstream.Event) {
	if e.Validate() != nil {
		return
	}
	feed := &s.chatStreamFeed
	feed.mu.Lock()
	defer feed.mu.Unlock()
	if feed.streams == nil {
		feed.streams = make(map[string]*streamSlot)
	}
	key := streamKey(e.Agent, e.PaneID, e.SessionPath)
	slot := feed.streams[key]
	// No durable subscription is assumed: after the first event, a new
	// process's first assistant message is the next safe bootstrap.
	if slot == nil {
		if e.Kind != "message_start" {
			return
		}
		slot = &streamSlot{}
		feed.streams[key] = slot
	}
	if !slot.buffer.Apply(e) {
		return
	}
	now := time.Now()
	slot.seen = now
	for name, old := range feed.streams {
		if now.Sub(old.seen) > chatStreamTTL {
			delete(feed.streams, name)
		}
	}
	if len(feed.streams) > chatStreamMaxEntries {
		var oldest string
		var at time.Time
		for name, item := range feed.streams {
			if oldest == "" || item.seen.Before(at) {
				oldest, at = name, item.seen
			}
		}
		delete(feed.streams, oldest)
	}
	if feed.wake == nil {
		feed.wake = make(chan struct{})
	}
	close(feed.wake)
	feed.wake = make(chan struct{})
}

func (s *Server) chatStreamWake() <-chan struct{} {
	feed := &s.chatStreamFeed
	feed.mu.Lock()
	defer feed.mu.Unlock()
	if feed.wake == nil {
		feed.wake = make(chan struct{})
	}
	return feed.wake
}

func (s *Server) chatStreamOverlay(pane string, target chatTarget, canonical chatsession.State) chatsession.State {
	if target.Provider != history.AgentOMP && target.Provider != history.AgentPi {
		return canonical
	}
	feed := &s.chatStreamFeed
	feed.mu.Lock()
	defer feed.mu.Unlock()
	slot := feed.streams[streamKey(string(target.Provider), pane, target.Path)]
	if slot == nil || time.Since(slot.seen) > chatStreamTTL {
		return canonical
	}
	canonical.Turns = slot.buffer.Overlay(canonical.Turns)
	if slot.buffer.Completed && len(canonical.Turns) > 0 && !canonical.Turns[len(canonical.Turns)-1].User {
		// The canonical decoder might still hold a provisional tail. The
		// provider has confirmed the entire run ended; don't keep its caret.
		canonical.Turns = append([]chatsession.ChatTurn(nil), canonical.Turns...)
		canonical.Turns[len(canonical.Turns)-1].Live = false
	}
	return canonical
}

// chatStreamStatus retires a stale Herdr working status only after the
// exact Provider/Pane/session's agent_end, never after message_end.
// This remains a presentation/sendability hint: Herdr PromptAgent is the
// final arbiter and can still reject any new prompt.
func (s *Server) chatStreamStatus(pane string, target chatTarget, status string) string {
	if status != "working" || (target.Provider != history.AgentOMP && target.Provider != history.AgentPi) {
		return status
	}
	feed := &s.chatStreamFeed
	feed.mu.Lock()
	defer feed.mu.Unlock()
	slot := feed.streams[streamKey(string(target.Provider), pane, target.Path)]
	if slot != nil && slot.buffer.Completed && time.Since(slot.seen) <= chatStreamTTL {
		return "idle"
	}
	return status
}

// A newly accepted prompt must immediately retire the preceding run's
// completed flag, even if the next message_start has not reached the Socket.
func (s *Server) resetChatStreamCompletion(pane string, target chatTarget) {
	feed := &s.chatStreamFeed
	feed.mu.Lock()
	defer feed.mu.Unlock()
	key := streamKey(string(target.Provider), pane, target.Path)
	slot := feed.streams[key]
	if slot == nil || !slot.buffer.Completed {
		return
	}
	slot.buffer.Reset()
	if feed.wake == nil {
		feed.wake = make(chan struct{})
	}
	close(feed.wake)
	feed.wake = make(chan struct{})
}

type chatWireFrame struct {
	T           string            `json:"t"`
	PaneID      string            `json:"paneId"`
	AgentStatus string            `json:"agentStatus,omitempty"`
	Interaction *chatInteraction  `json:"interaction,omitempty"`
	Queued      *queuedFrame      `json:"queued,omitempty"`
	State       chatsession.State `json:"state"`
	Seq         uint64            `json:"seq"`
}

type chatWireDelta struct {
	T       string                 `json:"t"`
	PaneID  string                 `json:"paneId"`
	BaseSeq uint64                 `json:"baseSeq"`
	Seq     uint64                 `json:"seq"`
	Start   int                    `json:"start"`
	Turns   []chatsession.ChatTurn `json:"turns"`
}

// chatSuffixDelta fails closed when metadata, session, or the untouched
// prefix changed. A rolling-window removal is *not* safe to patch as a suffix.
func chatSuffixDelta(base, next chatWireFrame) (chatWireDelta, bool) {
	start := max(0, len(next.State.Turns)-chatPatchTailLimit)
	// A sudden shrink cannot be represented as an ordinary tail update.
	// In particular, replacing an entire 40-turn window with two turns
	// must send a full transcript frame, not a misleading start=0 patch.
	if start < max(0, len(base.State.Turns)-chatPatchTailLimit) {
		return chatWireDelta{}, false
	}
	if base.PaneID != next.PaneID || base.AgentStatus != next.AgentStatus ||
		!reflect.DeepEqual(base.Interaction, next.Interaction) ||
		!reflect.DeepEqual(base.Queued, next.Queued) ||
		start > len(base.State.Turns) {
		return chatWireDelta{}, false
	}
	left, right := base.State, next.State
	left.Turns, right.Turns = nil, nil
	if !reflect.DeepEqual(left, right) {
		return chatWireDelta{}, false
	}
	for i := 0; i < start; i++ {
		if !reflect.DeepEqual(base.State.Turns[i], next.State.Turns[i]) {
			return chatWireDelta{}, false
		}
	}
	return chatWireDelta{
		T: "chat_delta", PaneID: next.PaneID, BaseSeq: base.Seq, Seq: next.Seq,
		Start: start, Turns: next.State.Turns[start:],
	}, true
}
