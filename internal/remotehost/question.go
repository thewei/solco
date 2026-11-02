package remotehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/conversation"
)

/**
 * [INPUT]: 依赖 chatsession.Reader.Ask（omp/pi 转录里的未答 `ask`）、chatsession.DetectScreenPrompt（Claude 屏幕提问，经 Backend.ReadPane）、conversation.PendingInteraction.AnswerKeys/MultiSelectKeys（与桌面同一套按键规则）、Backend.SendAgentKeys/PromptAgent/SendPaneInput
 * [OUTPUT]: 对外提供聊天帧里的 `interaction`（chatInteraction）与 POST /api/v3/panes/{paneID}/chat/answer
 * [POS]: 手机 Chat 的 Question 卡（P3）：只在 agent 为 blocked 时派生，状态全在 Host 的每 pane 光标里（omp 多问题的步数、刚答过的卡），不碰桌面的 InteractionBroker；
 *        id 由 pane+来源+步数+问题+选项哈希而来（无状态可重派生），答题时重新派生并比对（过期/已答 → 409 interaction_gone），投递只走 Herdr agent.send_keys / agent.prompt（被阻塞的输入框走 pane.send_input），Herdr 拒绝原样回传；
 *        picker（/model 等）在 picker.go 共用本文件的 derive/answer；不含 Queue；提示正文、选项与答案永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	screenReadEvery  = 1200 * time.Millisecond
	screenReadBudget = 3 * time.Second
	// resolvedSuppress hides a just-answered card while the TUI still shows it.
	resolvedSuppress = 2500 * time.Millisecond
	maxAnswerCursors = 256
)

// answerTextSettle is the pause between selecting a free-text row and typing
// the answer (the TUI must land in its text field first); a var for tests.
var answerTextSettle = 500 * time.Millisecond

// textOption matches the rows that take typed text ("Other (type your own)",
// "Type something.", "Chat about this") — the same rule the desktop page uses.
var textOption = regexp.MustCompile(`(?i)^(type something|chat about|other|write-in)\b`)

// chatInteraction is the card the phone renders for a blocked agent.
type chatInteraction struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"` // question | permission
	Header       string   `json:"header,omitempty"`
	Prompt       string   `json:"prompt"`
	Options      []string `json:"options"`
	Descriptions []string `json:"descriptions,omitempty"`
	Multi        bool     `json:"multi,omitempty"`
	// TextOptions are the options that take a typed answer.
	TextOptions []string `json:"textOptions,omitempty"`
	// Step/Steps place one question of a multi-question ask (0-based).
	Step  int `json:"step,omitempty"`
	Steps int `json:"steps,omitempty"`
}

// askCursor is the Host's memory for one pane's Question card. mu serializes
// derivation and answering, so the first answer wins.
type askCursor struct {
	mu sync.Mutex
	// omp's ask presents its questions one after another; the transcript does
	// not say which is on screen, so the Host counts the answers it delivered.
	toolID string
	step   int
	done   bool
	// The last delivered answer, hidden while the TUI still draws it.
	resolvedID string
	resolvedAt time.Time
	// Screen-scrape cache.
	screenAt    time.Time
	screenInter conversation.PendingInteraction
	screenOK    bool
	// A picker command (/model, /permissions) the Host just sent: the screen is
	// read until the picker shows (pickerUntil) and then while it stays (pickerLive).
	pickerUntil time.Time
	pickerSince time.Time
	pickerAt    time.Time
	pickerInter conversation.PendingInteraction
	pickerLive  bool
}

func (s *Server) askFor(instance, paneID string) *askCursor {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.asks == nil {
		s.asks = map[string]*askCursor{}
	}
	key := instance + "\x00" + paneID
	cur := s.asks[key]
	if cur == nil {
		if len(s.asks) >= maxAnswerCursors {
			clear(s.asks)
		}
		cur = &askCursor{}
		s.asks[key] = cur
	}
	return cur
}

func interactionID(paneID, toolID string, step int, p conversation.PendingInteraction) string {
	h := sha256.New()
	for _, part := range []string{paneID, toolID, string(p.Kind), p.Prompt, strings.Join(p.Options, "\x1f")} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	h.Write([]byte{byte(step)})
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// derive reads the card the blocked agent is showing, or false. The caller
// holds cur.mu and has already seen status == "blocked".
func (s *Server) derive(ctx context.Context, instance, paneID string, cur *askCursor, reader *chatsession.Reader, blocked bool) (conversation.PendingInteraction, bool) {
	now := s.opts.Now()
	if p, handled := s.derivePicker(ctx, instance, paneID, cur, now); handled {
		return p, p.ID != ""
	}
	var found conversation.PendingInteraction
	var toolID string
	step := 0
	ok := false
	parked := false // Pi's ask_user_question dialog: open while the host says `working`
	if reader != nil {
		if ask, has := reader.Ask(); has {
			parked = ask.ParksWorking
		}
	}
	if !blocked && !parked {
		return conversation.PendingInteraction{}, false
	}
	if reader != nil {
		if ask, has := reader.Ask(); has {
			if cur.toolID != ask.ToolID {
				cur.toolID, cur.step, cur.done = ask.ToolID, 0, false
			}
			if !cur.done && cur.step < len(ask.Questions) {
				q := ask.Questions[cur.step]
				found = conversation.PendingInteraction{
					Kind: conversation.InteractionQuestion, Prompt: q.Prompt, Options: q.Options,
					Header: q.Header, Descriptions: q.Descriptions, Start: q.Start, Multi: q.Multi,
					Step: cur.step, Steps: len(ask.Questions), CommitRow: q.CommitRow, ParkedWorking: ask.ParksWorking,
				}
				toolID, step, ok = ask.ToolID, cur.step, true
			}
		}
	}
	if !ok {
		if now.Sub(cur.screenAt) >= screenReadEvery {
			cur.screenAt, cur.screenOK = now, false
			rctx, cancel := context.WithTimeout(ctx, screenReadBudget)
			screen, err := s.opts.Backend.ReadPane(rctx, instance, paneID)
			cancel()
			if err != nil {
				slog.Warn("remote host chat: screen read failed", "instance", instance, "pane", paneID)
			} else if agentPrompt, hit := chatsession.DetectAgentPrompt(screen); hit {
				cur.screenInter, cur.screenOK = agentPrompt, true
			} else if kind, prompt, options, hit := chatsession.DetectScreenPrompt(screen); hit {
				cur.screenInter = conversation.PendingInteraction{Kind: kind, Prompt: prompt, Options: options, ParkedWorking: parked}
				if chatsession.IsOmpReviewSheet(screen) {
					cur.screenInter.EscapeOption = "Cancel"
				}
				cur.screenOK = true
			}
		}
		if !cur.screenOK {
			return conversation.PendingInteraction{}, false
		}
		found = cur.screenInter
	}
	found.ID = interactionID(paneID, toolID, step, found)
	if found.ID == cur.resolvedID && now.Sub(cur.resolvedAt) < resolvedSuppress {
		return conversation.PendingInteraction{}, false
	}
	return found, true
}

func toWire(p conversation.PendingInteraction) *chatInteraction {
	w := &chatInteraction{
		ID: p.ID, Kind: string(p.Kind), Header: p.Header, Prompt: p.Prompt, Options: p.Options,
		Multi: p.Multi, Step: p.Step, Steps: p.Steps,
	}
	if slices.ContainsFunc(p.Descriptions, func(d string) bool { return d != "" }) {
		w.Descriptions = p.Descriptions
	}
	for _, o := range p.Options {
		if !p.Multi && textOption.MatchString(strings.TrimSpace(o)) {
			w.TextOptions = append(w.TextOptions, o)
		}
	}
	return w
}

// interactionFrame is the card to ship with a chat state, "" status = none.
func (s *Server) interactionFrame(ctx context.Context, instance, paneID, status string, reader *chatsession.Reader) *chatInteraction {
	if status != "blocked" && !(status == "working" && askParked(reader)) && !s.pickerWatching(instance, paneID) {
		return nil
	}
	cur := s.askFor(instance, paneID)
	cur.mu.Lock()
	defer cur.mu.Unlock()
	p, ok := s.derive(ctx, instance, paneID, cur, reader, status == "blocked")
	if !ok {
		return nil
	}
	return toWire(p)
}

type chatAnswerRequest struct {
	InteractionID string   `json:"interactionId"`
	Option        string   `json:"option,omitempty"`
	Options       []string `json:"options,omitempty"`
	Text          string   `json:"text,omitempty"`
}

func (s *Server) handleChatAnswer(w http.ResponseWriter, r *http.Request, dev Device) {
	var req chatAnswerRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxChatText+1<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.InteractionID == "" || len(req.Text) > maxChatText || !utf8.ValidString(req.Text) {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	instance, paneID, target, ok := s.sendTarget(w, r)
	if !ok {
		return
	}
	var reader *chatsession.Reader
	if target.Path != "" {
		if rd, err := chatsession.Open(target.Provider, target.Path); err == nil {
			if _, err := rd.Sync(); err == nil {
				reader = rd
			}
		}
	}
	if target.Status != "blocked" && !(target.Status == "working" && askParked(reader)) && !s.pickerWatching(instance, paneID) {
		writeError(w, http.StatusConflict, "interaction_gone", "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), chatSendTimeout)
	defer cancel()

	cur := s.askFor(instance, paneID)
	cur.mu.Lock()
	defer cur.mu.Unlock()
	cur.screenAt, cur.pickerAt = time.Time{}, time.Time{} // the answer must match the screen as it is now
	p, found := s.derive(ctx, instance, paneID, cur, reader, target.Status == "blocked")
	if !found || p.ID != req.InteractionID {
		writeError(w, http.StatusConflict, "interaction_gone", "")
		return
	}
	var keys []string
	if p.Multi {
		if req.Option != "" || req.Text != "" {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
		for _, pick := range req.Options {
			if !slices.Contains(p.Options, pick) {
				writeError(w, http.StatusBadRequest, "unknown_option", "")
				return
			}
		}
		keys = p.MultiKeys(req.Options)
	} else {
		typed := slices.ContainsFunc(toWire(p).TextOptions, func(o string) bool { return o == req.Option })
		if len(req.Options) > 0 || (typed && req.Text == "") || (!typed && req.Text != "") {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
		var err error
		if keys, err = p.AnswerKeys(req.Option, req.Text != ""); err != nil {
			writeError(w, http.StatusBadRequest, "unknown_option", "")
			return
		}
	}
	backend := s.opts.Backend
	if err := backend.SendAgentKeys(ctx, instance, paneID, keys); err != nil {
		s.sendFailed(w, dev, paneID, "answer", err)
		return
	}
	cur.resolvedID, cur.resolvedAt = p.ID, s.opts.Now()
	if p.Picker {
		s.armPicker(cur) // a picker may continue with a second list; re-read until it is gone
	}
	if p.Steps > 0 {
		if cur.step++; cur.step >= p.Steps {
			cur.done = true
		}
	}
	if req.Text != "" {
		settle := answerTextSettle
		if len(keys) == 1 && keys[0] == "esc" {
			settle = interruptSettle
		}
		if !sleepCtx(ctx, settle) {
			writeError(w, http.StatusGatewayTimeout, "delivery_uncertain", "")
			return
		}
		var err error
		if p.ParkedWorking {
			// Pi's dialog reads `working` to its host: agent.prompt would
			// refuse as busy, so type into the focused input directly.
			err = backend.SendPaneInput(ctx, instance, paneID, req.Text, []string{"enter"})
		} else {
			err = backend.PromptAgent(ctx, instance, paneID, req.Text)
		}
		if err != nil && !p.ParkedWorking && strings.Contains(err.Error(), "blocked and requires interactive input") {
			// The answer goes into the very prompt the agent is blocked on
			// (omp's custom-answer editor): agent.prompt refuses that by
			// design, so type it into the pane directly.
			err = backend.SendPaneInput(ctx, instance, paneID, req.Text, []string{"enter"})
		}
		if err != nil {
			s.sendFailed(w, dev, paneID, "answer-text", err)
			return
		}
	}
	slog.Info("remote host chat answer sent", "device", dev.ID, "pane", paneID, "kind", string(p.Kind), "keys", len(keys))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// askParked reports a transcript that ends on Pi's unanswered
// ask_user_question call.
func askParked(reader *chatsession.Reader) bool {
	if reader == nil {
		return false
	}
	ask, has := reader.Ask()
	return has && ask.ParksWorking
}
