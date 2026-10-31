package chatsession

import (
	"errors"
	"time"

	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 View/Linked/Card（子代理投影）、history.ReadSubagent（Provider 自己的子代理文件，只读）、conversation.ItemsFromTranscript/DeriveTimeline、Turns
 * [OUTPUT]: 对外提供 SubagentView 与 OpenSubagent——抽屉里一个子代理自己的只读对话
 * [POS]: 桌面 ChatBridge.OpenSubagent 与手机 Remote Host 共用；id 必须是当前会话已列出的子代理（页面永远不传路径）；读不了转录时回 Error 文案而不是失败；不写文件、不记录对话内容
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// SubagentView is the drawer's read-only conversation.
type SubagentView struct {
	Subagent SubagentCard `json:"subagent"`
	Turns    []ChatTurn   `json:"turns"`
	// Error is a short reason the transcript cannot be shown ("" = fine).
	Error string `json:"error,omitempty"`
}

// ErrUnknownSubagent is OpenSubagent's answer for an id the conversation never listed.
var ErrUnknownSubagent = errors.New("sub-agent is not part of this conversation")

// OpenSubagent returns one listed sub-agent's own transcript. views are the
// conversation's sub-agents and parent its rendered window (the parent's tool
// call that spawned the child supplies the card's type and prompt).
func OpenSubagent(views []View, parent []conversation.TimelineTurn, id string, now time.Time) (SubagentView, error) {
	var (
		view  View
		found bool
		call  *conversation.ConversationToolCall
		kind  string
	)
	for _, v := range views {
		if v.Sub.ID == id {
			view, found = v, true
		}
	}
	for _, turn := range parent {
		for _, step := range turn.Steps {
			if ids, types := Linked(step.Tool, []View{view}); len(ids) > 0 && found {
				call, kind = step.Tool, types[id]
			}
		}
	}
	if !found {
		return SubagentView{}, ErrUnknownSubagent
	}
	out := SubagentView{Subagent: Card(view, kind, call, now)}
	detail, err := history.ReadSubagent(view.Sub.Provider, view.Sub.Path)
	if err != nil {
		out.Error = "Can't show this sub-agent's transcript."
		if errors.Is(err, history.ErrSubagentTooLarge) {
			out.Error = "This sub-agent's transcript is too large to preview."
		}
		out.Turns = []ChatTurn{}
		return out, nil
	}
	out.Turns = Turns(conversation.DeriveTimeline(conversation.ItemsFromTranscript(detail.Messages)), "", nil)
	return out, nil
}
