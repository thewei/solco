package chatsession

import (
	"errors"
	"sync"
	"time"

	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 history.LiveDecoder/LiveWatcher/FileLiveSource（Provider 转录的增量尾读）、Fold、Turns、PlanOf、Read/AttachSubagents
 * [OUTPUT]: 对外提供 State（一个会话的只读 Chat 线格式）、Reader（Open/Sync/State）
 * [POS]: 与 Shell/MyGo 无关的单会话读取器（2026-10-31，Remote Host P2）：一个 Reader 持有一个转录文件的增量解码器，Sync 只在文件变化时解码，State 用与桌面相同的 Fold/Turns/PlanOf 投影；不写 Provider 文件、不记录对话内容
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// State is the read-only conversation a phone renders (no composer, no
// interactions: those are the desktop bridge's and P3's).
type State struct {
	Provider      string `json:"provider"`
	Model         string `json:"model,omitempty"`
	ContextTokens *int64 `json:"contextTokens,omitempty"`
	// ContextWindow is the window the agent states, in tokens (set by the Host, which reads it once a minute; 0 = none).
	ContextWindow int64 `json:"contextWindow,omitempty"`
	HasOlder      bool  `json:"hasOlder,omitempty"`
	// Working reports a streaming (uncommitted) tail: the agent is writing now.
	Working   bool           `json:"working,omitempty"`
	Plan      *ChatPlan      `json:"plan,omitempty"`
	Subagents []SubagentCard `json:"subagents,omitempty"`
	Turns     []ChatTurn     `json:"turns"`
}

// subagentsEvery is the minimum spacing between sub-agent directory reads.
const subagentsEvery = 4 * time.Second

// Reader follows one provider transcript file. Safe for concurrent use.
type Reader struct {
	mu       sync.Mutex
	provider history.AgentID
	path     string
	decoder  *history.LiveDecoder
	watcher  *history.LiveWatcher
	start    int
	views    []View
	viewsAt  time.Time
	hydrated bool
}

// Open builds a Reader for provider's transcript at path. It fails closed for
// providers without a decoder.
func Open(provider history.AgentID, path string) (*Reader, error) {
	if path == "" {
		return nil, errors.New("chatsession: empty transcript path")
	}
	decoder, err := history.NewLiveDecoder(provider)
	if err != nil {
		return nil, err
	}
	return &Reader{
		provider: provider,
		path:     path,
		decoder:  decoder,
		watcher:  history.NewLiveWatcher(history.NewLiveSource(provider, path), decoder),
		start:    -1,
	}, nil
}

// Path is the followed transcript file.
func (r *Reader) Path() string { return r.path }

// Sync decodes whatever the file gained since the last call and reports
// whether the rendered state changed (the first successful call always does).
// Sub-agent changes are picked up by State; Sync only watches the transcript.
func (r *Reader) Sync() (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sync, err := r.watcher.Poll()
	if err != nil {
		return false, err
	}
	first := !r.hydrated
	r.hydrated = true
	return first || sync.HasUpdates(), nil
}

// Ask returns the transcript's pending `ask` / `ask_question` call (omp/pi, agy), if the agent is
// parked on one. It reads the same window State renders without moving it.
func (r *Reader) Ask() (TranscriptAsk, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return TranscriptQuestion(Fold(r.decoder.Projection(), r.decoder.PendingTail(), r.start).Turns)
}

// Older moves the window one page up the decoder's own projection (it already
// holds the whole transcript, so nothing is re-read) and reports whether it
// moved; false at the top or once the render bound is reached.
func (r *Reader) Older() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	window := Fold(r.decoder.Projection(), r.decoder.PendingTail(), r.start)
	if !window.HasOlder {
		return false
	}
	r.start = max(0, window.Start-history.WindowLimit)
	return true
}

// State projects the current window. now stamps sub-agent durations.
func (r *Reader) State(now time.Time) State {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending := r.decoder.PendingTail()
	window := Fold(r.decoder.Projection(), pending, r.start)
	r.start = window.Start
	state := State{
		Provider:      string(r.provider),
		Model:         window.Model,
		ContextTokens: window.ContextTokens,
		HasOlder:      window.HasOlder,
		Working:       pending != nil,
		Plan:          PlanOf(window.Turns),
		Turns:         Turns(window.Turns, "", nil),
	}
	if now.Sub(r.viewsAt) >= subagentsEvery {
		r.views, r.viewsAt = Read(r.provider, r.path, now), now
	}
	state.Subagents = AttachSubagents(state.Turns, window.Turns, r.views, now)
	return state
}

// Subagent opens one of the conversation's sub-agents (the drawer). id must be
// one State listed; the sub-agent's own file is read outside the Reader's lock.
func (r *Reader) Subagent(id string, now time.Time) (SubagentView, error) {
	r.mu.Lock()
	window := Fold(r.decoder.Projection(), r.decoder.PendingTail(), r.start)
	if r.viewsAt.IsZero() || now.Sub(r.viewsAt) >= subagentsEvery {
		r.views, r.viewsAt = Read(r.provider, r.path, now), now
	}
	views := r.views
	r.mu.Unlock()
	return OpenSubagent(views, window.Turns, id, now)
}
