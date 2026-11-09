package remotehost

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"reflect"
	"time"

	"encoding/json"
	"strconv"
	"github.com/coder/websocket"
	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 Backend.Projection（agent_session 身份）、internal/chatsession（Reader/State）、history.ResolveLiveSource、server.go 的 currentTree/trackConn/guard
 * [OUTPUT]: 对外提供 GET /api/v3/panes/{paneID}/chat（一次性只读 State）与 GET /api/v3/panes/{paneID}/chat/stream（WebSocket：初次/会话切换/状态差异用 `chat` 全帧，OMP/Pi Token 优先 `chat_delta` 尾 3 回合/80ms 合并；手机发 `{t:snapshot}` 精确全量复位）
 * [POS]: 手机 Chat 只读（P2/SC-06）：窗格 → Herdr 的 agent_session 身份 → Provider 转录文件；OMP/Pi 额外使用同进程 Solco apphook 的只读临时叠加，不启动第二 Agent；原始 JSONL 仍是真值，跨 Pane/路径拒绝；会话换了流里重绑并发全量帧；没有可读会话 = 409 no_conversation
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	chatPollEvery                             = time.Second
	chatRebindEvery                           = 4 // polls between identity re-resolutions / sub-agent refreshes
	maxChatStreamsPerDev                      = 4
	statusNoConversation websocket.StatusCode = 4409
)

// chatTarget is a pane's agent as the chat endpoints see it. Provider/Path are
// empty when the agent has no readable conversation yet (Code "no_conversation").
type chatTarget struct {
	Provider history.AgentID
	Path     string
	// Status is Herdr's agent_status for the pane ("" when the pane runs no agent).
	Status string
	// ConversationID ("live:<provider>:<terminal>", the desktop's own key) and
	// Fingerprint (Herdr's agent_session only, never a hook identity) address the
	// follow-up queue; empty when the conversation is not resolved.
	ConversationID string
	Fingerprint    string
	// Code is "" on success, else the wire error code.
	Code string
}

// resolveChat maps a pane to its agent's status and provider transcript from
// one projection read. Unknown pane IDs never reach a path.
func (s *Server) resolveChat(instance, paneID string) chatTarget {
	proj, err := s.opts.Backend.Projection(instance)
	if err != nil {
		slog.Warn("remote host chat: projection failed", "instance", instance, "error", err)
		return chatTarget{Code: "backend_unavailable"}
	}
	tree := BuildTree(instance, proj)
	if !tree.hasPane(paneID) {
		return chatTarget{Code: "unknown_pane"}
	}
	target := chatTarget{Code: "no_conversation"}
	for _, ws := range tree.Workspaces {
		for _, tab := range ws.Tabs {
			for _, pane := range tab.Panes {
				if pane.PaneID == paneID && pane.Agent != nil {
					target.Status = pane.Agent.Status
				}
			}
		}
	}
	for _, a := range proj.Agents {
		if a.PaneID != paneID {
			continue
		}
		identity := a.AgentSession
		if identity == nil && s.opts.Identity != nil {
			identity = s.opts.Identity(instance, a.PaneID, a.TerminalID, a.Kind)
		}
		if identity == nil {
			continue
		}
		provider, ok := history.ParseAgentID(identity.Agent)
		if !ok {
			break
		}
		home := s.opts.Home
		if home == "" {
			if home, err = os.UserHomeDir(); err != nil {
				break
			}
		}
		path, err := history.ResolveLiveSource(home, provider, identity.Kind, identity.Source, identity.Value)
		if err != nil {
			break
		}
		if _, err := os.Stat(path); err != nil {
			break
		}
		target.Provider, target.Path, target.Code = provider, path, ""
		target.ConversationID = "live:" + string(provider) + ":" + a.TerminalID
		if a.AgentSession != nil {
			target.Fingerprint = conversation.SessionIdentity{
				Provider: string(provider), Kind: a.AgentSession.Kind, Source: a.AgentSession.Source, Value: a.AgentSession.Value,
			}.Fingerprint()
		}
		break
	}
	return target
}

var chatErrorStatus = map[string]int{
	"unknown_pane":        http.StatusNotFound,
	"no_conversation":     http.StatusConflict,
	"backend_unavailable": http.StatusBadGateway,
}

type chatBody struct {
	PaneID string `json:"paneId"`
	// AgentStatus is Herdr's agent_status (idle/working/blocked/…): what the
	// composer needs to know whether a send can land.
	AgentStatus string `json:"agentStatus,omitempty"`
	// Interaction is the Question/permission card of a blocked agent.
	Interaction *chatInteraction `json:"interaction,omitempty"`
	// Queued is the follow-up waiting behind the running turn.
	Queued *queuedFrame `json:"queued,omitempty"`
	chatsession.State
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request, _ Device) {
	instance, ok := s.resolveInstance(w, r)
	if !ok {
		return
	}
	paneID := r.PathValue("paneID")
	target := s.resolveChat(instance, paneID)
	if target.Code != "" {
		writeError(w, chatErrorStatus[target.Code], target.Code, "")
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	reader, err := chatsession.Open(target.Provider, target.Path)
	if err == nil {
		if limit > 0 {
			reader.SetLimit(limit)
		}
		_, err = reader.Sync()
	}
	if err != nil {
		slog.Warn("remote host chat: read failed", "instance", instance, "pane", paneID, "error", err)
		writeError(w, http.StatusConflict, "no_conversation", "")
		return
	}
	writeJSON(w, http.StatusOK, chatBody{
		PaneID: paneID, AgentStatus: s.chatStreamStatus(paneID, target, target.Status),
		Interaction: s.interactionFrame(r.Context(), instance, paneID, target.Status, reader),
		Queued:      s.queuedFor(target),
		State:       s.chatState(r.Context(), target, reader),
	})
}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request, dev Device) {
	instance, ok := s.resolveInstance(w, r)
	if !ok {
		return
	}
	paneID := r.PathValue("paneID")
	target := s.resolveChat(instance, paneID)
	if target.Code != "" {
		writeError(w, chatErrorStatus[target.Code], target.Code, "")
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	status := target.Status
	reader, err := chatsession.Open(target.Provider, target.Path)
	if err != nil {
		writeError(w, http.StatusConflict, "no_conversation", "")
		return
	}
	if limit > 0 {
		reader.SetLimit(limit)
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{subprotocolBearer}})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer s.acquireChat(dev.ID, cancel)()
	defer s.trackConn(dev.ID, func() { _ = conn.Close(websocket.StatusPolicyViolation, "revoked"); cancel() })()
	// The phone may ask for older transcript turns or for a full snapshot
	// after a missing/invalid suffix frame. Neither request mutates the runtime.
	olderReq := make(chan struct{}, 1)
	snapshotReq := make(chan struct{}, 1)
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				cancel()
				return
			}
			var msg struct {
				T string `json:"t"`
			}
			if json.Unmarshal(data, &msg) == nil {
				switch msg.T {
				case "older":
					select {
					case olderReq <- struct{}{}:
					default:
					}
				case "snapshot":
					select {
					case snapshotReq <- struct{}{}:
					default:
					}
				}
			}
		}
	}()

	if _, err := reader.Sync(); err != nil {
		return
	}
	canonical := s.chatState(ctx, target, reader)
	interaction := s.interactionFrame(ctx, instance, paneID, status, reader)
	queued := s.queuedFor(target)
	var last chatWireFrame
	sent := false
	push := func(force bool) bool {
		frame := chatWireFrame{
			T: "chat", PaneID: paneID, AgentStatus: s.chatStreamStatus(paneID, target, status),
			Interaction: interaction, Queued: queued,
			State: s.chatStreamOverlay(paneID, target, canonical),
			Seq:   last.Seq + 1,
		}
		if sent && !force && reflect.DeepEqual(frame.State, last.State) &&
			frame.AgentStatus == last.AgentStatus && reflect.DeepEqual(frame.Interaction, last.Interaction) &&
			reflect.DeepEqual(frame.Queued, last.Queued) {
			return true
		}
		full, err := json.Marshal(frame)
		if err != nil {
			return false
		}
		raw := full
		if sent && !force && (target.Provider == history.AgentOMP || target.Provider == history.AgentPi) {
			if delta, ok := chatSuffixDelta(last, frame); ok {
				if compact, err := json.Marshal(delta); err == nil && len(compact) < len(full) {
					raw = compact
				}
			}
		}
		wctx, wcancel := context.WithTimeout(ctx, wsWriteTimeout)
		defer wcancel()
		if err := conn.Write(wctx, websocket.MessageText, raw); err != nil {
			return false
		}
		last, sent = frame, true
		return true
	}
	if !push(true) {
		return
	}
	tick := time.NewTicker(chatPollEvery)
	defer tick.Stop()
	var timer *time.Timer
	var deadline <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	wake := s.chatStreamWake()
	for n := 1; ; {
		select {
		case <-ctx.Done():
			return
		case <-olderReq:
			if reader.Older() {
				canonical = s.chatState(ctx, target, reader)
				if !push(true) {
					return
				}
			}
			continue
		case <-snapshotReq:
			canonical = s.chatState(ctx, target, reader)
			if !push(true) {
				return
			}
			continue
		case <-wake:
			wake = s.chatStreamWake()
			if timer == nil {
				timer = time.NewTimer(chatStreamCoalesce)
				deadline = timer.C
			}
			continue
		case <-deadline:
			deadline = nil
			timer = nil
			if !push(false) {
				return
			}
			continue
		case <-tick.C:
			n++
		}
		watching := status == "blocked" || s.pickerWatching(instance, paneID) || s.queuedFor(target) != nil
		forceFull := false
		if n%chatRebindEvery == 0 || watching {
			next := s.resolveChat(instance, paneID)
			switch {
			case next.Code == "unknown_pane":
				_ = conn.Close(statusTerminalGone, "pane_gone")
				return
			case next.Code == "no_conversation":
				_ = conn.Close(statusNoConversation, "no_conversation")
				return
			case next.Code == "":
				status, target = next.Status, next
				if next.Path != reader.Path() {
					fresh, err := chatsession.Open(next.Provider, next.Path)
					if err != nil {
						_ = conn.Close(statusNoConversation, "no_conversation")
						return
					}
					reader, sent, forceFull = fresh, false, true
				}
			}
		}
		changed, err := reader.Sync()
		if err != nil {
			continue
		}
		// Periodic canonical refresh retires reconciled overlays, refreshes
		// queue/interaction metadata, and remains the non-OMP fallback.
		if changed || n%chatRebindEvery == 0 || watching || (target.Provider == history.AgentOMP || target.Provider == history.AgentPi) {
			canonical = s.chatState(ctx, target, reader)
			interaction = s.interactionFrame(ctx, instance, paneID, status, reader)
			queued = s.queuedFor(target)
			if !push(forceFull) {
				return
			}
		}
	}
}

// acquireChat registers one open chat stream of a device and returns its release. A phone that changed network
// leaves its old sockets half-open on this side (a stream writes only on change, so nothing ever fails), and those
// would hold the device's slots until TCP gives up: a full device evicts its oldest stream instead of refusing the
// new one, so the chat the user just opened always gets through.
func (s *Server) acquireChat(deviceID string, evict func()) (release func()) {
	slot := &chatSlot{evict: evict}
	s.mu.Lock()
	if s.chats == nil {
		s.chats = map[string][]*chatSlot{}
	}
	slots := append(s.chats[deviceID], slot)
	var dropped []*chatSlot
	for len(slots) > maxChatStreamsPerDev {
		dropped = append(dropped, slots[0])
		slots = slots[1:]
	}
	s.chats[deviceID] = slots
	s.mu.Unlock()
	for _, old := range dropped {
		old.evict()
	}
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		kept := s.chats[deviceID][:0:0]
		for _, other := range s.chats[deviceID] {
			if other != slot {
				kept = append(kept, other)
			}
		}
		if len(kept) == 0 {
			delete(s.chats, deviceID)
			return
		}
		s.chats[deviceID] = kept
	}
}

// chatSlot is one open chat stream; evict ends it.
type chatSlot struct{ evict func() }
