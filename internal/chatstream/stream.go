/**
 * [INPUT]: 依赖 OMP Extension 上报的 typed delta 与 chatsession.ChatTurn（唯一 UI 线格式）
 * [OUTPUT]: Event.Validate、Buffer.Apply/Overlay/Reconciled/Reset，提供有界、去重、可丢弃的 Chat 临时投影
 * [POS]: internal/chatstream 不持久化、不拥有 Agent 生命周期/提示词/会话；在 Provider JSONL 真值到来前临时补齐增量
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package chatstream

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/thewei/solco/internal/chatsession"
)

const (
	MaxDeltaBytes = 4096
	MaxTextBytes  = 32 * 1024
	MaxTools      = 64
)

// Event is one bounded, same-session OMP extension notification.
type Event struct {
	Agent       string `json:"agent"`
	PaneID      string `json:"pane_id"`
	SessionPath string `json:"session_path"`
	Epoch       string `json:"epoch"`
	Seq         uint64 `json:"seq"`
	Kind        string `json:"kind"`
	MessageID   string `json:"message_id,omitempty"`
	Delta       string `json:"delta,omitempty"`
	ToolCallID  string `json:"tool_call_id,omitempty"`
	ToolName    string `json:"tool_name,omitempty"`
	ToolError   bool   `json:"tool_error,omitempty"`
}

var ErrInvalidEvent = errors.New("invalid chat stream event")

func (e Event) Validate() error {
	if e.Agent != "omp" || len(e.PaneID) == 0 || len(e.PaneID) > 128 ||
		len(e.SessionPath) == 0 || len(e.SessionPath) > 4096 || !filepath.IsAbs(e.SessionPath) ||
		len(e.Epoch) == 0 || len(e.Epoch) > 128 || e.Seq == 0 ||
		len(e.MessageID) > 128 || len(e.ToolCallID) > 128 || len(e.ToolName) > 128 ||
		len(e.Delta) > MaxDeltaBytes {
		return ErrInvalidEvent
	}
	switch e.Kind {
	case "message_start", "text_delta", "thinking_delta", "message_end":
		if e.MessageID == "" || e.ToolCallID != "" || e.ToolName != "" {
			return ErrInvalidEvent
		}
		if (e.Kind == "text_delta" || e.Kind == "thinking_delta") && e.Delta == "" {
			return ErrInvalidEvent
		}
	case "tool_start", "tool_end":
		if e.MessageID == "" || e.ToolCallID == "" || e.ToolName == "" || e.Delta != "" {
			return ErrInvalidEvent
		}
	default:
		return ErrInvalidEvent
	}
	if (e.Kind != "text_delta" && e.Kind != "thinking_delta") && e.Delta != "" {
		return ErrInvalidEvent
	}
	return nil
}

type tool struct {
	id, name     string
	done, failed bool
}

// Buffer belongs to one Chat-bound Pane and session; it is UI-thread confined.
type Buffer struct {
	Epoch     string
	Seq       uint64
	MessageID string
	Text      string
	Thinking  string
	Tools     []tool
	Ended     bool
}

func (b *Buffer) Reset() { *b = Buffer{} }

// Apply returns false for invalid, stale or unrelated events. The caller
// independently validates pane/session identity before reaching this method.
func (b *Buffer) Apply(e Event) bool {
	if e.Validate() != nil {
		return false
	}
	if b.Epoch != "" && e.Epoch != b.Epoch {
		// New process epochs must move forward and begin with a new message.
		// A delayed frame from the previous process must never erase the
		// newer stream. Epochs use fixed-width process-start timestamps.
		if e.Epoch < b.Epoch || e.Kind != "message_start" {
			return false
		}
		b.Reset()
	}
	if b.Epoch == e.Epoch && e.Seq <= b.Seq {
		return false
	}
	// Failed event preconditions must not move the high watermark.
	switch e.Kind {
	case "text_delta", "thinking_delta", "message_end":
		if e.MessageID != b.MessageID {
			return false
		}
	case "tool_start", "tool_end":
		// Tool execution may begin after the finished assistant message
		// has already reconciled into JSONL. In that case the stream can
		// restart from the tool's measured message identity.
		if b.MessageID == "" && e.Kind != "tool_start" {
			return false
		}
	}
	switch e.Kind {
	case "message_start":
		b.MessageID, b.Text, b.Thinking, b.Tools, b.Ended = e.MessageID, "", "", nil, false
	case "text_delta":
		if len(b.Text)+len(e.Delta) > MaxTextBytes {
			return false
		}
		b.Text += e.Delta
	case "thinking_delta":
		if len(b.Thinking)+len(e.Delta) > MaxTextBytes {
			return false
		}
		b.Thinking += e.Delta
	case "tool_start":
		if b.MessageID == "" {
			b.MessageID = e.MessageID
		}
		for _, item := range b.Tools {
			if item.id == e.ToolCallID {
				return false
			}
		}
		if len(b.Tools) >= MaxTools {
			return false
		}
		b.Tools = append(b.Tools, tool{id: e.ToolCallID, name: e.ToolName})
	case "tool_end":
		found := false
		for i := range b.Tools {
			if b.Tools[i].id == e.ToolCallID && !b.Tools[i].done {
				b.Tools[i].done, b.Tools[i].failed, found = true, e.ToolError, true
				break
			}
		}
		if !found {
			return false
		}
	case "message_end":
		b.Ended = true
	}
	b.Epoch, b.Seq = e.Epoch, e.Seq
	return true
}

// Reconciled reports that the authoritative transcript already contains the
// provisional assistant content. Only completed messages can be retired.
func (b *Buffer) Reconciled(base []chatsession.ChatTurn) bool {
	if !b.Ended || b.Text == "" {
		return false
	}
	for _, item := range b.Tools {
		if !item.done {
			return false
		}
	}
	for i := len(base) - 1; i >= 0 && i >= len(base)-3; i-- {
		if !base[i].User && strings.Contains(base[i].Narration, b.Text) {
			if b.Thinking == "" || (base[i].Thinking != nil && strings.Contains(*base[i].Thinking, b.Thinking)) {
				return true
			}
		}
	}
	return false
}

func allToolsDone(tools []tool) bool {
	for _, item := range tools {
		if !item.done {
			return false
		}
	}
	return true
}

// Overlay is presentation only. It never mutates base or writes source files.
// If the transcript contains a prefix of the same assistant reply, extend
// that row rather than adding a duplicate; otherwise render a transient row.
func (b *Buffer) Overlay(base []chatsession.ChatTurn) []chatsession.ChatTurn {
	if b.MessageID == "" || (b.Text == "" && b.Thinking == "" && len(b.Tools) == 0) {
		return base
	}
	if b.Reconciled(base) {
		return base
	}
	for i := len(base) - 1; i >= 0 && i >= len(base)-2; i-- {
		if !base[i].User && ((b.Text != "" && strings.Contains(base[i].Narration, b.Text)) ||
			(b.Text == "" && len(b.Tools) > 0 && i == len(base)-1)) {
			if len(b.Tools) == 0 {
				return base
			}
			// The transcript may have committed narration before an executing
			// tool finished. Patch that exact assistant row, never duplicate it.
			out := append([]chatsession.ChatTurn(nil), base...)
			row := out[i]
			row.Steps = append([]chatsession.ChatStep(nil), row.Steps...)
			row.Tools = append([]chatsession.ChatTool(nil), row.Tools...)
			for _, item := range b.Tools {
				found := false
				for j := range row.Steps {
					if row.Steps[j].Kind == "tool" && row.Steps[j].ToolName == item.name {
						row.Steps[j].ToolDone = row.Steps[j].ToolDone || item.done
						row.Steps[j].ToolError = row.Steps[j].ToolError || item.failed
						found = true
						break
					}
				}
				if !found {
					row.Tools = append(row.Tools, chatsession.ChatTool{Name: item.name, IsError: item.failed})
					row.Steps = append(row.Steps, chatsession.ChatStep{Kind: "tool", ToolName: item.name, ToolDone: item.done, ToolError: item.failed})
				}
			}
			row.Live = !b.Ended || !allToolsDone(b.Tools)
			out[i] = row
			return out
		}
	}
	row := chatsession.ChatTurn{Live: true, Narration: b.Text}
	if b.Thinking != "" {
		thinking := b.Thinking
		row.Thinking = &thinking
		row.Steps = append(row.Steps, chatsession.ChatStep{Kind: "thinking", Text: thinking})
	}
	if b.Text != "" {
		row.Steps = append(row.Steps, chatsession.ChatStep{Kind: "narration", Text: b.Text})
	}
	for _, t := range b.Tools {
		row.Tools = append(row.Tools, chatsession.ChatTool{Name: t.name, IsError: t.failed})
		row.Steps = append(row.Steps, chatsession.ChatStep{Kind: "tool", ToolName: t.name, ToolDone: t.done, ToolError: t.failed})
	}
	// Preserve an already rendered assistant row if its narration is an exact
	// prefix of the live text and it is the tail. The new row takes over only
	// its visual text; the canonical turn is never edited in-place.
	if len(base) > 0 && !base[len(base)-1].User &&
		b.Text != "" && base[len(base)-1].Narration != "" &&
		strings.HasPrefix(b.Text, base[len(base)-1].Narration) {
		out := append([]chatsession.ChatTurn(nil), base...)
		previous := base[len(base)-1]
		previous.Live = true
		previous.Narration = b.Text
		// Update the existing narration step in the same position; when
		// no step matches, add the suffix as a final narration step.
		previous.Steps = append([]chatsession.ChatStep(nil), previous.Steps...)
		replaced := false
		for i := len(previous.Steps) - 1; i >= 0; i-- {
			if previous.Steps[i].Kind == "narration" && strings.HasPrefix(b.Text, previous.Steps[i].Text) {
				previous.Steps[i].Text = b.Text
				replaced = true
				break
			}
		}
		if !replaced {
			previous.Steps = append(previous.Steps, chatsession.ChatStep{Kind: "narration", Text: b.Text[len(base[len(base)-1].Narration):]})
		}
		out[len(out)-1] = previous
		return out
	}
	return append(append([]chatsession.ChatTurn(nil), base...), row)
}
