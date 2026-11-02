package conversation

import "github.com/thewei/solco/internal/agent"

// ConversationSource distinguishes live sessions from history records.
type ConversationSource string

const (
	SourceLive    ConversationSource = "live"
	SourceHistory ConversationSource = "history"
)

// ConversationItemKind is the provider-neutral item vocabulary.
type ConversationItemKind string

const (
	KindUser           ConversationItemKind = "user"
	KindAssistant      ConversationItemKind = "assistant"
	KindReasoning      ConversationItemKind = "reasoning"
	KindTool           ConversationItemKind = "tool"
	KindActivity       ConversationItemKind = "activity"
	KindMeta           ConversationItemKind = "meta"
	KindCompactSummary ConversationItemKind = "compact-summary"
)

// ConversationToolCall is one tool invocation inside an item.
type ConversationToolCall struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	InputPreview string `json:"input_preview"`
	// Input is the provider's full tool input (JSON, bounded by the parser);
	// presentation derives structured cards from it (task list, question).
	Input   *string `json:"input,omitempty"`
	Output  *string `json:"output,omitempty"`
	IsError bool    `json:"is_error"`
	// Details mirrors history.ToolCall.Details (a provider's structured
	// result snapshot, e.g. Pi's whole todo list).
	Details *string `json:"details,omitempty"`
}

// ConversationSummary is the list-page projection.
type ConversationSummary struct {
	ID           ConversationID          `json:"id"`
	ProjectID    string                  `json:"project_id,omitempty"`
	Source       ConversationSource      `json:"source"`
	Provider     string                  `json:"provider"`
	Title        string                  `json:"title"`
	RuntimePhase agent.AgentRuntimePhase `json:"runtime_phase,omitempty"`
	Sendability  agent.AgentSendability  `json:"sendability,omitempty"`
	UpdatedAtMS  *int64                  `json:"updated_at_ms,omitempty"`
}

// ConversationItem is one normalized timeline item.
type ConversationItem struct {
	ID          string                 `json:"id"`
	Seq         uint64                 `json:"seq"`
	Kind        ConversationItemKind   `json:"kind"`
	Role        string                 `json:"role"`
	Text        string                 `json:"text"`
	Thinking    *string                `json:"thinking,omitempty"`
	ToolCalls   []ConversationToolCall `json:"tool_calls,omitempty"`
	TimestampMS *int64                 `json:"timestamp_ms,omitempty"`
	Model       *string                `json:"model,omitempty"`
	Truncated   bool                   `json:"truncated"`
	// Stopped mirrors history.TranscriptMessage.Stopped (the provider ended
	// this assistant message early: aborted / error).
	Stopped bool `json:"stopped,omitempty"`
	// Parts is the item's blocks in provider order (text / thinking / tool
	// interleaved) when the source merged several provider messages into one
	// item and kept the order; empty means Text → Thinking → ToolCalls.
	Parts []ItemPart `json:"parts,omitempty"`
}

// ItemPartKind names one ordered block of an item.
type ItemPartKind string

const (
	PartText     ItemPartKind = "text"
	PartThinking ItemPartKind = "thinking"
	PartTool     ItemPartKind = "tool"
)

// ItemPart is one ordered block of a ConversationItem; tool parts index
// into the item's ToolCalls.
type ItemPart struct {
	Kind        ItemPartKind `json:"kind"`
	Text        string       `json:"text,omitempty"`
	Tool        int          `json:"tool,omitempty"`
	TimestampMS *int64       `json:"timestamp_ms,omitempty"`
}

// ConversationWindow bounds both History and Live reads (0.6 §6): the
// default read half-window is 80, maximum before/after 200. Never load an
// unbounded transcript into UI state.
const (
	DefaultHalfWindow = 80
	MaxBeforeAfter    = 200
)

// ConversationWindow is the shared bounded read shape.
type ConversationWindow struct {
	ConversationID ConversationID     `json:"conversation_id"`
	Revision       uint64             `json:"revision"`
	Items          []ConversationItem `json:"items"`
	FirstSeq       *uint64            `json:"first_seq,omitempty"`
	LastSeq        *uint64            `json:"last_seq,omitempty"`
	HasOlder       bool               `json:"has_older"`
	HasNewer       bool               `json:"has_newer"`
}

// PromptDisposition is the SSOT prompt disposition derived from the 0.5
// sendability classifier (0.6 §10). The composer label is presentation
// only; the service decides again at commit time.
type PromptDisposition string

const (
	DispositionSentNow         PromptDisposition = "sent-now"
	DispositionQueuedAfterTurn PromptDisposition = "queued-after-turn"
	DispositionNeedsTerminal   PromptDisposition = "needs-terminal"
	DispositionUnknown         PromptDisposition = "unknown"
)

// DispositionFor maps the runtime sendability onto the prompt disposition,
// failing closed for unknown.
func DispositionFor(sendability agent.AgentSendability) PromptDisposition {
	switch sendability {
	case agent.Sendable:
		return DispositionSentNow
	case agent.MidTurn:
		return DispositionQueuedAfterTurn
	case agent.NeedsTerminal:
		return DispositionNeedsTerminal
	default:
		return DispositionUnknown
	}
}
