/**
 * [INPUT]: 依赖 history/models.go (TranscriptMessage, SessionFileRef), parse_utils.go (clipText, epochMS, toolInput, injectedUserContent)
 * [OUTPUT]: ParsePiTranscript, piParseState, newPiParseState, feedPiCustomMessage；会话标题优先取 agent 自己写的 title/title_change 行（随工作改名，文件序最后一行生效，空标题不清除旧值），无标题行才回退首条消息启发式；custom_message 里 attribution 用户的行（skill-prompt 的 details.prompt）作为用户轮入线，agent 通知行不入时间线
 * [POS]: history 包中 Pi / Oh My Pi 会话历史与 live append log 解析器，与 claude.go / codex.go 兄弟对称
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package history

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type piParseState struct {
	sessionID    string
	sessionTitle string
	cwd          string
	createdAt    int64
	lastTS       int64
	model        *string
	tokensUsed   int64
	unknownLines uint32
	messages     []TranscriptMessage
	toolIndex    map[string][2]int // toolCallId -> [messageIndex, toolCallIndex]
}

func newPiParseState() *piParseState {
	return &piParseState{
		toolIndex: make(map[string][2]int),
	}
}

// ParsePiTranscript parses one provider-owned Pi / Oh My Pi JSONL source.
// It is read-only and does not mutate or normalize the external file.
func ParsePiTranscript(reference SessionFileRef) (ParsedTranscript, error) {
	file, err := os.Open(reference.FilePath)
	if err != nil {
		return ParsedTranscript{}, fmt.Errorf("open Pi history: %w", err)
	}
	defer file.Close()

	state := newPiParseState()
	reader := bufio.NewReaderSize(file, 1<<20)
	for {
		line, readErr := reader.ReadString('\n')
		if strings.TrimSpace(line) != "" {
			state.feedPiLine(line)
		}
		if readErr != nil {
			if readErr != io.EOF {
				state.unknownLines++
			}
			break
		}
	}
	return state.transcript(reference), nil
}

func (s *piParseState) transcript(reference SessionFileRef) ParsedTranscript {
	// The agent's own session title is the authoritative name; the first
	// message heuristic only covers sessions that never got a title row.
	title := strings.TrimSpace(s.sessionTitle)
	if title == "" {
		title = titleFromMessages(s.messages)
		// A sub-agent's file is named after the agent (`<session>/DeployPhone.jsonl`,
		// not `<timestamp>_<id>.jsonl`): that name tells assignments apart.
		if name := subagentFileName(reference.FilePath); name != "" && title != "" {
			title = name + " · " + title
		}
	}
	if title == "" {
		title = Untitled
	}
	created := s.createdAt
	if created == 0 {
		created = reference.MtimeMS
	}
	updated := s.lastTS
	if updated == 0 {
		updated = reference.MtimeMS
	}
	var tokensUsed *int64
	if s.tokensUsed > 0 {
		val := s.tokensUsed
		tokensUsed = &val
	}
	messageCount := int64(0)
	for _, msg := range s.messages {
		if msg.Kind == MessageText {
			messageCount++
		}
	}
	return ParsedTranscript{
		Meta: SessionMeta{
			Key:          string(reference.Agent) + ":" + reference.NativeID,
			ID:           reference.NativeID,
			Agent:        reference.Agent,
			Title:        title,
			ProjectPath:  s.cwd,
			ProjectName:  projectName(s.cwd),
			FilePath:     reference.FilePath,
			CreatedAt:    created,
			UpdatedAt:    updated,
			MessageCount: messageCount,
			SizeBytes:    reference.SizeBytes,
			Model:        s.model,
			TokensUsed:   tokensUsed,
		},
		Mainline:         append([]TranscriptMessage(nil), s.messages...),
		Sidechains:       []SidechainInfo{},
		UnknownLineCount: s.unknownLines,
	}
}

func (s *piParseState) feedPiLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(line), &row); err != nil {
		s.unknownLines++
		return
	}
	rowType, _ := row["type"].(string)
	switch rowType {
	case "session":
		if id, ok := row["id"].(string); ok && id != "" {
			s.sessionID = id
		}
		if cwd, ok := row["cwd"].(string); ok && cwd != "" {
			s.cwd = cwd
		}
		if ts := epochMS(row["timestamp"]); ts > 0 {
			s.createdAt = ts
		}
	case "title", "title_change":
		// The agent names its own session: a `title` row at head and
		// `title_change` rows whenever it renames the session mid-flight
		// (real files carry both, source "auto"). Last one in file order
		// wins; blank titles never clear a previous one.
		if title, ok := row["title"].(string); ok {
			if text := strings.TrimSpace(title); text != "" {
				s.sessionTitle = text
			}
		}
	case "message":
		messageObj, ok := row["message"].(map[string]any)
		if !ok {
			s.unknownLines++
			return
		}
		timestamp := epochMS(row["timestamp"])
		if timestamp == 0 {
			timestamp = epochMS(messageObj["timestamp"])
		}
		if timestamp > s.lastTS {
			s.lastTS = timestamp
		}
		role, _ := messageObj["role"].(string)
		content := messageObj["content"]
		switch role {
		case "user":
			text := piBlocksText(content)
			if strings.TrimSpace(text) != "" {
				s.pushUser(text, timestamp)
			}
		case "assistant":
			text := piBlocksText(content)
			thinking := piBlocksThinking(content)
			tools := piBlocksTools(content)
			// A turn Pi/omp ended early (aborted, or failed with a provider
			// error) is recorded as an assistant row with empty content: it
			// still marks the turn Stopped, and an error says why.
			stopReason, _ := messageObj["stopReason"].(string)
			stopped := stopReason == "aborted" || stopReason == "error"
			if stopReason == "error" {
				if reason, _ := messageObj["errorMessage"].(string); strings.TrimSpace(reason) != "" {
					failure := "Error: " + strings.TrimSpace(reason)
					if text != "" {
						text += "\n\n" + failure
					} else {
						text = failure
					}
					content = append(piContentBlocks(content), map[string]any{"type": "text", "text": failure})
				}
			}
			if text == "" && thinking == nil && len(tools) == 0 && !stopped {
				return
			}
			if modelStr, ok := messageObj["model"].(string); ok && modelStr != "" {
				s.model = &modelStr
			}
			contextUsed := int64(0)
			if usage, ok := messageObj["usage"].(map[string]any); ok {
				if totalTokens, ok := usage["totalTokens"].(float64); ok && totalTokens > 0 {
					s.tokensUsed = int64(totalTokens)
					// One request's full accounting (cache included) is the
					// context that request filled (context ring only).
					contextUsed = int64(totalTokens)
				}
			}
			// Consecutive assistant turns (interrupted only by toolResult) merge
			// into one, matching Rust herdr-history PiSession::feed_line.
			if len(s.messages) == 0 || s.messages[len(s.messages)-1].Role != RoleAssistant {
				s.pushAssistant(timestamp)
			}
			lastIdx := len(s.messages) - 1
			last := &s.messages[lastIdx]
			if text != "" {
				if last.Text != "" {
					last.Text += "\n\n" + text
				} else {
					last.Text = text
				}
				if len(last.Text) > MaxMessageText {
					clipped, truncated := clipText(last.Text, MaxMessageText)
					last.Text = clipped
					last.Truncated = truncated
				}
			}
			if thinking != nil {
				if last.Thinking == nil {
					last.Thinking = thinking
				} else {
					combined := *last.Thinking + "\n\n" + *thinking
					clipped, _ := clipText(combined, MaxToolIO)
					last.Thinking = &clipped
				}
			}
			if s.model != nil {
				last.Model = s.model
			}
			if stopped {
				last.Stopped = true
			}
			if contextUsed > 0 {
				last.ContextTokens = &contextUsed
			}
			// Blocks keep the provider's order across merged messages: tool
			// parts index into ToolCalls, so rebase them onto what is already
			// in this merged message.
			toolBase := len(last.ToolCalls)
			for _, part := range piBlockParts(content, optionalTimestamp(timestamp)) {
				if part.Kind == PartTool {
					part.Tool += toolBase
				}
				last.Parts = append(last.Parts, part)
			}
			for _, tool := range tools {
				if tool.ID != "" {
					s.toolIndex[tool.ID] = [2]int{lastIdx, len(last.ToolCalls)}
				}
				last.ToolCalls = append(last.ToolCalls, tool)
			}
		case "toolResult":
			callID, _ := messageObj["toolCallId"].(string)
			if callID == "" {
				return
			}
			pos, found := s.toolIndex[callID]
			if !found || pos[0] >= len(s.messages) || pos[1] >= len(s.messages[pos[0]].ToolCalls) {
				return
			}
			toolCall := &s.messages[pos[0]].ToolCalls[pos[1]]
			// An empty result is still a result: Output stays non-nil so the
			// call reads as finished (nil means "no result yet").
			clipped, _ := clipText(piToolResultText(content), MaxToolIO)
			toolCall.Output = &clipped
			if isErr, ok := messageObj["isError"].(bool); ok && isErr {
				toolCall.IsError = true
			}
			toolCall.Details = piTodoSnapshot(toolCall.Name, messageObj["details"])
		default:
			s.unknownLines++
		}
	case "compaction":
		// The provider replaced earlier turns with a summary: keep one marker
		// row (never the summary body) so Chat can draw its divider.
		timestamp := epochMS(row["timestamp"])
		if timestamp > s.lastTS {
			s.lastTS = timestamp
		}
		s.messages = append(s.messages, TranscriptMessage{
			Seq:       int64(len(s.messages)),
			Role:      RoleSystem,
			Kind:      MessageCompactSummary,
			Text:      "── Context compacted ──",
			ToolCalls: []ToolCall{},
			Timestamp: optionalTimestamp(timestamp),
		})
	case "model_change", "thinking_level_change", "custom":
		// Known non-content events skipped silently.
	case "custom_message":
		s.feedPiCustomMessage(row)
	default:
		s.unknownLines++
	}
}

// feedPiCustomMessage carries the custom_message rows that hold visible
// conversation content. A skill invocation — the TUI's and the composer's
// `/skill:name …` form — is written as skill-prompt with the typed prompt in
// details.prompt, and it is the transcript's ONLY record of that user turn:
// without it Chat opens on the assistant's work and the user's first message
// is missing (2026-11-01 report). Other custom messages (launch-completion,
// async-result, …) are agent-attributed runtime notices, not turns, and stay
// out of the timeline.
func (s *piParseState) feedPiCustomMessage(row map[string]any) {
	customType, _ := row["customType"].(string)
	attribution, _ := row["attribution"].(string)
	if customType != "skill-prompt" && attribution != "user" {
		return
	}
	text := ""
	if details, ok := row["details"].(map[string]any); ok {
		text, _ = details["prompt"].(string)
		if text == "" {
			text, _ = details["args"].(string)
		}
	}
	if text == "" {
		text, _ = row["content"].(string)
	}
	timestamp := epochMS(row["timestamp"])
	if timestamp > s.lastTS {
		s.lastTS = timestamp
	}
	if strings.TrimSpace(text) != "" {
		s.pushUser(text, timestamp)
	}
}

func (s *piParseState) pushUser(text string, timestamp int64) {
	kind := MessageText
	if injectedUserContent(text) {
		kind = MessageMeta
	}
	clipped, truncated := clipText(text, MaxMessageText)
	s.messages = append(s.messages, TranscriptMessage{
		Seq:       int64(len(s.messages)),
		Role:      RoleUser,
		Kind:      kind,
		Text:      clipped,
		Truncated: truncated,
		ToolCalls: []ToolCall{},
		Timestamp: optionalTimestamp(timestamp),
	})
}

func (s *piParseState) pushAssistant(timestamp int64) {
	s.messages = append(s.messages, TranscriptMessage{
		Seq:       int64(len(s.messages)),
		Role:      RoleAssistant,
		Kind:      MessageText,
		ToolCalls: []ToolCall{},
		Timestamp: optionalTimestamp(timestamp),
	})
}

func piBlocksText(content any) string {
	switch v := content.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		var parts []string
		for _, item := range v {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if block["type"] == "text" {
				if text, ok := block["text"].(string); ok {
					trimmed := strings.TrimSpace(text)
					if trimmed != "" {
						parts = append(parts, trimmed)
					}
				}
			}
		}
		return strings.Join(parts, "\n\n")
	default:
		return ""
	}
}

// piBlockParts lists one assistant row's text / thinking / toolCall blocks
// in their written order. Tool parts carry the call's index within this row
// (the same order piBlocksTools yields); the caller rebases it.
func piBlockParts(content any, ts *int64) []MessagePart {
	switch value := content.(type) {
	case string:
		if text := strings.TrimSpace(value); text != "" {
			clipped, _ := clipText(text, MaxMessageText)
			return []MessagePart{{Kind: PartText, Text: clipped, Timestamp: ts}}
		}
	case []any:
		var parts []MessagePart
		tools := 0
		for _, item := range value {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch block["type"] {
			case "text":
				if text, _ := block["text"].(string); strings.TrimSpace(text) != "" {
					clipped, _ := clipText(strings.TrimSpace(text), MaxMessageText)
					parts = append(parts, MessagePart{Kind: PartText, Text: clipped, Timestamp: ts})
				}
			case "thinking":
				if text, _ := block["thinking"].(string); strings.TrimSpace(text) != "" {
					clipped, _ := clipText(strings.TrimSpace(text), MaxToolIO)
					parts = append(parts, MessagePart{Kind: PartThinking, Text: clipped, Timestamp: ts})
				}
			case "toolCall":
				parts = append(parts, MessagePart{Kind: PartTool, Tool: tools, Timestamp: ts})
				tools++
			}
		}
		return parts
	}
	return nil
}

func piBlocksThinking(content any) *string {
	blocks, ok := content.([]any)
	if !ok {
		return nil
	}
	var parts []string
	for _, item := range blocks {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if block["type"] == "thinking" {
			if thinking, ok := block["thinking"].(string); ok {
				trimmed := strings.TrimSpace(thinking)
				if trimmed != "" {
					parts = append(parts, trimmed)
				}
			}
		}
	}
	if len(parts) == 0 {
		return nil
	}
	combined := strings.Join(parts, "\n\n")
	clipped, _ := clipText(combined, MaxToolIO)
	return &clipped
}

func piBlocksTools(content any) []ToolCall {
	blocks, ok := content.([]any)
	if !ok {
		return nil
	}
	var tools []ToolCall
	for _, item := range blocks {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if block["type"] == "toolCall" {
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			if name == "" {
				name = "tool"
			}
			preview, full := toolInput(block["arguments"])
			tools = append(tools, ToolCall{
				ID:           id,
				Name:         name,
				Input:        full,
				InputPreview: preview,
			})
		}
	}
	return tools
}

// piContentBlocks returns content as a block list (a plain string becomes one
// text block) so an error line can join the ordered parts.
func piContentBlocks(content any) []any {
	switch v := content.(type) {
	case []any:
		return append([]any(nil), v...)
	case string:
		if v != "" {
			return []any{map[string]any{"type": "text", "text": v}}
		}
	}
	return nil
}

// piTodoSnapshot keeps the whole task list Pi's todo extension attaches to
// every result as `details.tasks` — its text output is only the increment
// ("Updated #2 (pending → in_progress)"), so the Tasks tray reads this
// snapshot instead of replaying operations. nil for any other tool or an
// error result (no list); an empty list is kept — `clear` is a statement.
func piTodoSnapshot(name string, details any) *string {
	if name != "todo" {
		return nil
	}
	obj, ok := details.(map[string]any)
	if !ok {
		return nil
	}
	tasks, ok := obj["tasks"].([]any)
	if !ok {
		return nil
	}
	data, err := json.Marshal(map[string]any{"tasks": tasks})
	if err != nil {
		return nil
	}
	snapshot, _ := clipText(string(data), MaxToolIO)
	return &snapshot
}

func piToolResultText(content any) string {
	switch v := content.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		var parts []string
		for _, item := range v {
			if str, ok := item.(string); ok {
				trimmed := strings.TrimSpace(str)
				if trimmed != "" {
					parts = append(parts, trimmed)
				}
				continue
			}
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := block["text"].(string); ok {
				trimmed := strings.TrimSpace(text)
				if trimmed != "" {
					parts = append(parts, trimmed)
				}
			} else if result, ok := block["result"].(string); ok {
				trimmed := strings.TrimSpace(result)
				if trimmed != "" {
					parts = append(parts, trimmed)
				}
			} else if output, ok := block["output"].(string); ok {
				trimmed := strings.TrimSpace(output)
				if trimmed != "" {
					parts = append(parts, trimmed)
				}
			}
		}
		return strings.Join(parts, "\n\n")
	case map[string]any:
		if text, ok := v["text"].(string); ok {
			return strings.TrimSpace(text)
		}
		data, err := json.Marshal(v)
		if err == nil {
			return string(data)
		}
	}
	return ""
}

// piRolloutNativeID extracts the stable UUID from a Pi session file stem:
// `<timestamp>_<uuid>` → `<uuid>`, falling back to the stem if no `_` exists.
func piRolloutNativeID(stem string) string {
	if idx := strings.LastIndexByte(stem, '_'); idx >= 0 {
		return stem[idx+1:]
	}
	return stem
}

// subagentFileName returns the agent name of an omp sub-agent session file —
// one whose stem is not the `<timestamp>_<id>` of a top-level session.
func subagentFileName(path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if stem == "" || strings.Contains(stem, "_") {
		return ""
	}
	if len(stem) >= 4 && stem[0] >= '0' && stem[0] <= '9' {
		return ""
	}
	return stem
}
