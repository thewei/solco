package remotehost

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"net/http"
	"os"
	"time"

	"encoding/json"
	"github.com/coder/websocket"
	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 Backend.Projection（agent_session 身份）、internal/chatsession（Reader/State）、history.ResolveLiveSource、server.go 的 currentTree/trackConn/guard
 * [OUTPUT]: 对外提供 GET /api/v3/panes/{paneID}/chat（一次性只读 State）与 GET /api/v3/panes/{paneID}/chat/stream（WebSocket，`chat` 帧 = 完整 State，内容变才推）
 * [POS]: 手机 Chat 只读（P2）：窗格 → Herdr 的 agent_session 身份 → Provider 自己的转录文件 → 与桌面同一份 chatsession 投影；Host 只读文件，不 attach、不写 Provider、不记录对话内容；会话换了（新 session 文件）流里重绑；没有可读会话 = 409 no_conversation，手机退回 Terminal
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
	reader, err := chatsession.Open(target.Provider, target.Path)
	if err == nil {
		_, err = reader.Sync()
	}
	if err != nil {
		slog.Warn("remote host chat: read failed", "instance", instance, "pane", paneID, "error", err)
		writeError(w, http.StatusConflict, "no_conversation", "")
		return
	}
	writeJSON(w, http.StatusOK, chatBody{
		PaneID: paneID, AgentStatus: target.Status,
		Interaction: s.interactionFrame(r.Context(), instance, paneID, target.Status, reader),
		State:       reader.State(s.opts.Now()),
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
	status := target.Status
	reader, err := chatsession.Open(target.Provider, target.Path)
	if err != nil {
		writeError(w, http.StatusConflict, "no_conversation", "")
		return
	}
	if !s.acquireChat(dev.ID) {
		writeError(w, http.StatusTooManyRequests, "too_many_streams", "")
		return
	}
	defer s.releaseChat(dev.ID)
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{subprotocolBearer}})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer s.trackConn(dev.ID, func() { _ = conn.Close(websocket.StatusPolicyViolation, "revoked"); cancel() })()
	go func() { // the phone sends nothing; reading detects a close
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()

	var last [sha256.Size]byte
	sent := false
	push := func() bool {
		frame := map[string]any{"t": "chat", "paneId": paneID, "agentStatus": status, "state": reader.State(s.opts.Now())}
		if card := s.interactionFrame(ctx, instance, paneID, status, reader); card != nil {
			frame["interaction"] = card
		}
		raw, err := json.Marshal(frame)
		if err != nil {
			return false
		}
		sum := sha256.Sum256(raw)
		if sent && sum == last {
			return true
		}
		last, sent = sum, true
		wctx, wcancel := context.WithTimeout(ctx, wsWriteTimeout)
		defer wcancel()
		return conn.Write(wctx, websocket.MessageText, raw) == nil
	}
	if _, err := reader.Sync(); err != nil || !push() {
		return
	}
	tick := time.NewTicker(chatPollEvery)
	defer tick.Stop()
	for n := 1; ; n++ {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if n%chatRebindEvery == 0 || status == "blocked" { // a blocked agent retires its card the moment it moves on
			next := s.resolveChat(instance, paneID)
			switch {
			case next.Code == "unknown_pane":
				_ = conn.Close(statusTerminalGone, "pane_gone")
				return
			case next.Code == "no_conversation":
				_ = conn.Close(statusNoConversation, "no_conversation")
				return
			case next.Code == "":
				status = next.Status // a status change alone changes the frame
				if next.Path != reader.Path() {
					fresh, err := chatsession.Open(next.Provider, next.Path)
					if err != nil {
						_ = conn.Close(statusNoConversation, "no_conversation")
						return
					}
					reader, sent = fresh, false
				}
			}
		}
		changed, err := reader.Sync()
		if err != nil {
			continue // a momentarily unreadable file is retried, not fatal
		}
		// A blocked agent re-derives its card every poll (the frame is hashed, so only a change goes out).
		if (changed || n%chatRebindEvery == 0 || status == "blocked") && !push() {
			return
		}
	}
}

func (s *Server) acquireChat(deviceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chats == nil {
		s.chats = map[string]int{}
	}
	if s.chats[deviceID] >= maxChatStreamsPerDev {
		return false
	}
	s.chats[deviceID]++
	return true
}

func (s *Server) releaseChat(deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chats[deviceID]--; s.chats[deviceID] <= 0 {
		delete(s.chats, deviceID)
	}
}
