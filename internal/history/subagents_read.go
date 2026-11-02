package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

/**
 * [INPUT]: 一个子代理自己的 transcript 路径与 Provider（Subagent.Path）
 * [OUTPUT]: SubagentDetail（提示、说明、最终结果、最后一步、工具调用数、完整消息）、ReadSubagent、NewSubagentDecoder
 * [POS]: Chat 子代理卡片的实时尾巴/结果摘要与下钻抽屉的数据读取（2026-10-31，design: designs/subagents）：复用各 Provider 的 LiveDecoder，Claude 额外放行 isSidechain 行（子代理文件整份都是 sidechain 行）；只读、整份读入内存并设上限，不持久化任何对话内容
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// SubagentReadLimit bounds one sub-agent transcript read; a larger file is
// refused rather than materialized.
const SubagentReadLimit = 16 << 20

// ErrSubagentTooLarge is returned for a transcript above SubagentReadLimit.
var ErrSubagentTooLarge = errors.New("sub-agent transcript too large to preview")

const (
	subagentPromptRunes = 1200
	subagentResultRunes = 800
	subagentTailRunes   = 120
)

// SubagentDetail is what one read of a sub-agent transcript yields.
type SubagentDetail struct {
	// Prompt is the task the parent gave (the first user message).
	Prompt string
	// Description is the first line of the task, used when the provider's
	// own listing carries none (omp, Codex).
	Description string
	// Result is the last assistant text the sub-agent wrote.
	Result string
	// TailName/TailInput name the last tool call: the live line.
	TailName  string
	TailInput string
	ToolCalls int
	// PendingTool reports that the transcript ends on a tool call without a
	// result: the sub-agent is waiting on it (a long command), not finished.
	PendingTool bool
	// FirstTs is the first provider timestamp (ms; 0 when the transcript
	// records none): the true start, which a meta file's mtime only
	// approximates. The end is the file's own last write.
	FirstTs  int64
	Messages []TranscriptMessage
}

// NewSubagentDecoder builds the line decoder for a sub-agent's own
// transcript. It is NewLiveDecoder plus Claude's sidechain rows.
func NewSubagentDecoder(agent AgentID) (*LiveDecoder, error) {
	decoder, err := NewLiveDecoder(agent)
	if err != nil {
		return nil, err
	}
	if decoder.claude != nil {
		decoder.claude.sidechain = true
	}
	return decoder, nil
}

// ReadSubagent reads one sub-agent transcript whole.
func ReadSubagent(provider AgentID, path string) (SubagentDetail, error) {
	info, err := os.Stat(path)
	if err != nil {
		return SubagentDetail{}, err
	}
	if info.Size() > SubagentReadLimit {
		return SubagentDetail{}, ErrSubagentTooLarge
	}
	decoder, err := NewSubagentDecoder(provider)
	if err != nil {
		return SubagentDetail{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return SubagentDetail{}, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, SubagentReadLimit))
	if err != nil {
		return SubagentDetail{}, err
	}
	decoder.Append(raw)
	decoder.Finalize()
	detail := summarizeSubagent(decoder.Projection())
	if pending := decoder.PendingTail(); pending != nil {
		detail.Messages = append(detail.Messages, *pending)
	}
	if provider == AgentCodex {
		// The first user row is Codex's injected AGENTS.md block, never the task.
		detail.Prompt = codexTaskPayload(path)
		detail.Description = ""
	}
	if detail.Description == "" && detail.Prompt != "" {
		detail.Description = promptHeadline(detail.Prompt)
	}
	if provider == AgentPi {
		// pi-subagents opens every child with a literal `Task: ` label.
		detail.Description = strings.TrimPrefix(detail.Description, "Task: ")
	}
	return detail, nil
}

func summarizeSubagent(messages []TranscriptMessage) SubagentDetail {
	detail := SubagentDetail{Messages: messages}
	if n := len(messages); n > 0 && messages[n-1].Role == RoleAssistant {
		if calls := messages[n-1].ToolCalls; len(calls) > 0 && calls[len(calls)-1].Output == nil && calls[len(calls)-1].Name != "SubagentHandback" {
			detail.PendingTool = true
		}
	}
	for _, message := range messages {
		if message.Timestamp != nil && *message.Timestamp > 0 {
			if detail.FirstTs == 0 {
				detail.FirstTs = *message.Timestamp
			}
		}
		switch message.Role {
		case RoleUser:
			if detail.Prompt == "" && message.Kind == MessageText {
				detail.Prompt = clipRunes(strings.TrimSpace(message.Text), subagentPromptRunes)
			}
		case RoleAssistant:
			if text := strings.TrimSpace(message.Text); text != "" {
				detail.Result = clipRunes(text, subagentResultRunes)
			}
			for _, call := range message.ToolCalls {
				if call.Name == "SubagentHandback" {
					// Claude's sub-agent reports through this call; its message
					// is the result, and the call is not work worth a tail.
					if text := handbackMessage(call); text != "" {
						detail.Result = clipRunes(text, subagentResultRunes)
					}
					continue
				}
				detail.ToolCalls++
				detail.TailName = call.Name
				detail.TailInput = clipRunes(strings.TrimSpace(call.InputPreview), subagentTailRunes)
			}
		}
	}
	return detail
}

// promptHeadline is the first meaningful line of a task prompt: omp wraps
// the assignment in a fixed "Complete assignment thoroughly:" lead.
func promptHeadline(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(strings.ToLower(line), "complete assignment") {
			continue
		}
		return clipRunes(line, 120)
	}
	return ""
}

func clipRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

// codexTaskPayload reads the NEW_TASK message a Codex parent sent its
// sub-agent (an `agent_message` response item near the file's start, which
// the transcript decoder does not turn into a user row).
func codexTaskPayload(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 256<<10)
	for i := 0; i < 80; i++ {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 && len(line) < codexFirstLineLimit && bytes.Contains(line, []byte("NEW_TASK")) {
			var row struct {
				Payload struct {
					Type    string `json:"type"`
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"payload"`
			}
			if json.Unmarshal(line, &row) == nil && row.Payload.Type == "agent_message" && len(row.Payload.Content) > 0 {
				text := row.Payload.Content[0].Text
				if at := strings.Index(text, "Payload:"); at >= 0 {
					text = text[at+len("Payload:"):]
				}
				return clipRunes(strings.TrimSpace(text), subagentPromptRunes)
			}
		}
		if readErr != nil {
			break
		}
	}
	return ""
}

// handbackMessage is the report text inside a SubagentHandback tool call.
func handbackMessage(call ToolCall) string {
	if call.Input == nil {
		return ""
	}
	var args struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(*call.Input), &args) != nil {
		return ""
	}
	return strings.TrimSpace(args.Message)
}
