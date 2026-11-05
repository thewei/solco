package chatsession

import (
	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/gitworkbench"
)

/**
 * [INPUT]: 依赖 conversation.TimelineTurn（DeriveTimeline 的产物）、gitworkbench.ChangesSnapshot（改动卡 +/−，可为 nil）
 * [OUTPUT]: 对外提供 ChatTurn/ChatStep/ChatTool/ChatChange 线格式与 Turns（时间线 → 页面回合）
 * [POS]: Chat 回合投影的唯一实现（2026-10-31 自 nativeui 抽出）：桌面 ChatBridge 与手机 Host 共用，页面与手机读同一形状；纯函数、无 IO
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// ChatTool is one tool run rendered inside an assistant turn.
type ChatTool struct {
	Name    string `json:"name"`
	Input   string `json:"input,omitempty"`
	Output  string `json:"output,omitempty"`
	IsError bool   `json:"isError,omitempty"`
}

// ChatStep is one ordered segment of a turn's time chain: the page
// renders thinking / narration / tool calls in their original interleaving.
type ChatStep struct {
	Kind       string `json:"kind"`
	Text       string `json:"text,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
	ToolInput  string `json:"toolInput,omitempty"`
	ToolOutput string `json:"toolOutput,omitempty"`
	ToolError  bool   `json:"toolError,omitempty"`
	// ToolDone reports that the call's result has landed (an empty result
	// still counts); false means the call is still running.
	ToolDone bool `json:"toolDone,omitempty"`
	// TsMS is the provider timestamp of the item that opened the step
	// (presentation only: the page derives "Thought for Ns" from gaps).
	TsMS *int64 `json:"tsMs,omitempty"`
	// SubagentIDs lists the sub-agents this tool call spawned (internal/nativeui/chat_subagents.go);
	// the page draws their card/roster in place of the plain tool row.
	SubagentIDs []string `json:"subagentIds,omitempty"`
}

// ChatChange is one file row of a turn's changes card. Counted is set
// only when Additions/Deletions come from the live working-tree snapshot
// (latest settled turn, local repository); otherwise the row is a name alone.
type ChatChange struct {
	Path      string `json:"path"`
	Additions int    `json:"additions,omitempty"`
	Deletions int    `json:"deletions,omitempty"`
	Counted   bool   `json:"counted,omitempty"`
}

// ChatTurn is one derived timeline row (0.6 §13 contract): user rows
// verbatim; assistant turns carry visible narration, consolidated thinking
// and the (possibly grouped) tool activity. Steps preserves the original
// interleaving for the timeline rendering.
type ChatTurn struct {
	User      bool    `json:"user"`
	Text      string  `json:"text,omitempty"`
	Narration string  `json:"narration,omitempty"`
	Thinking  *string `json:"thinking,omitempty"`
	Failed    bool    `json:"failed,omitempty"`
	Compacted bool    `json:"compacted,omitempty"`
	// CompactedBefore draws the "Context compacted" divider above this row.
	CompactedBefore bool       `json:"compactedBefore,omitempty"`
	// NoteBefore is a system note (the Coordinator's standing instructions)
	// drawn as a collapsed chip above this row; it is never a person's message.
	NoteBefore string `json:"noteBefore,omitempty"`
	Live       bool   `json:"live,omitempty"`
	TsMS       *int64 `json:"tsMs,omitempty"`
	Tools           []ChatTool `json:"tools,omitempty"`
	Steps           []ChatStep `json:"steps,omitempty"`
	// Changes lists the files this settled turn's edit tools touched (the
	// changes card); see chatTurnChanges for the data-source contract.
	Changes []ChatChange `json:"changes,omitempty"`
}

// Turns maps derived timeline rows onto the page's turns. root/snap
// feed the changes card (both empty for a sub-agent transcript, which has
// no working-tree card).
func Turns(turns []conversation.TimelineTurn, root string, snap *gitworkbench.ChangesSnapshot) []ChatTurn {
	out := make([]ChatTurn, 0, len(turns))
	lastAssistant := -1
	for index, turn := range turns {
		if !turn.UserRow {
			lastAssistant = index
		}
	}
	for index, turn := range turns {
		item := ChatTurn{
			User:            turn.UserRow,
			Text:            turn.Text,
			Narration:       turn.Narration,
			Thinking:        turn.Thinking,
			Failed:          turn.Failed,
			Compacted:       turn.Compacted,
			CompactedBefore: turn.CompactedBefore,
			Live:            turn.Live,
			TsMS:            turn.TsMS,
		}
		for _, run := range turn.ToolRuns {
			tool := ChatTool{Name: run.Name, Input: run.InputPreview, IsError: run.IsError}
			if run.Output != nil {
				tool.Output = *run.Output
			}
			item.Tools = append(item.Tools, tool)
		}
		for _, step := range turn.Steps {
			step_ := ChatStep{Kind: step.Kind, Text: step.Text, TsMS: step.TsMS}
			if step.Tool != nil {
				step_.ToolName = step.Tool.Name
				step_.ToolInput = step.Tool.InputPreview
				step_.ToolError = step.Tool.IsError
				if step.Tool.Output != nil {
					step_.ToolOutput = *step.Tool.Output
					step_.ToolDone = true
				}
			}
			item.Steps = append(item.Steps, step_)
		}
		if !turn.UserRow && !turn.Live && !turn.Failed {
			item.Changes = TurnChanges(turn.Steps, root, snap, index == lastAssistant)
		}
		out = append(out, item)
	}
	return out
}
