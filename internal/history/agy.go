/**
 * [INPUT]: 依赖 history/models.go (TranscriptMessage, SessionFileRef), parse_utils.go (clipText, epochMS, toolInput), claude_blocks.go (optionalTimestamp)
 * [OUTPUT]: 对外提供 ParseAgyTranscript, agyParseState, newAgyParseState, agyTranscriptFile, agyNativeID
 * [POS]: Antigravity CLI（agy）会话转录解析器：只读 `brain/<conversationId>/.system_generated/logs/transcript_full.jsonl`——
 *        agy 自己的追加式 JSONL（step_index / source / type / status / created_at，PLANNER_RESPONSE 带 thinking · content · tool_calls · token，
 *        工具结果是紧随其后的 GENERIC 步骤、无 call id，按先进先出配对）。与 pi.go 对称，同一份 feed 同时服务整文件解析与 live 增量解码
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package history

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// agyTranscriptName is the one transcript file of a conversation that is
// complete: `transcript.jsonl` clips long arguments and `chunks/` repeat the
// same rows in pieces, so neither is ever tailed.
const agyTranscriptName = "transcript_full.jsonl"

// agyTranscriptFile is the transcript path of one conversation under a
// brain root (`~/.gemini/antigravity-cli/brain`).
func agyTranscriptFile(brainRoot, conversationID string) string {
	return filepath.Join(brainRoot, conversationID, ".system_generated", "logs", agyTranscriptName)
}

// agyNativeID is the conversation id of a transcript path
// (`…/brain/<id>/.system_generated/logs/transcript_full.jsonl`), "" when the
// path does not have that shape.
func agyNativeID(path string) string {
	logs := filepath.Dir(path)
	if filepath.Base(path) != agyTranscriptName || filepath.Base(logs) != "logs" {
		return ""
	}
	generated := filepath.Dir(logs)
	if filepath.Base(generated) != ".system_generated" {
		return ""
	}
	return filepath.Base(filepath.Dir(generated))
}

var (
	agyUserRequest  = regexp.MustCompile(`(?s)<USER_REQUEST>\s*(.*?)\s*</USER_REQUEST>`)
	agyModelSetting = regexp.MustCompile("changed setting `Model Selection` from .+? to (.+?)\\.(?:\\s|$)")
)

type agyParseState struct {
	createdAt    int64
	lastTS       int64
	model        *string
	tokensUsed   int64
	unknownLines uint32
	messages     []TranscriptMessage
	// pending holds the calls still waiting for their result row, oldest
	// first: [messageIndex, toolCallIndex].
	pending [][2]int
}

func newAgyParseState() *agyParseState { return &agyParseState{} }

// ParseAgyTranscript parses one provider-owned Antigravity CLI transcript.
// It is read-only and does not mutate or normalize the external file.
func ParseAgyTranscript(reference SessionFileRef) (ParsedTranscript, error) {
	file, err := os.Open(reference.FilePath)
	if err != nil {
		return ParsedTranscript{}, fmt.Errorf("open Antigravity history: %w", err)
	}
	defer file.Close()

	state := newAgyParseState()
	reader := bufio.NewReaderSize(file, 1<<20)
	for {
		line, readErr := reader.ReadString('\n')
		if strings.TrimSpace(line) != "" {
			state.feedAgyLine(line)
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

func (s *agyParseState) transcript(reference SessionFileRef) ParsedTranscript {
	title := titleFromMessages(s.messages)
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

func (s *agyParseState) feedAgyLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(line), &row); err != nil {
		s.unknownLines++
		return
	}
	stepType, _ := row["type"].(string)
	status, _ := row["status"].(string)
	timestamp := epochMS(row["created_at"])
	if timestamp > 0 {
		if s.createdAt == 0 {
			s.createdAt = timestamp
		}
		if timestamp > s.lastTS {
			s.lastTS = timestamp
		}
	}
	content, _ := row["content"].(string)

	switch stepType {
	case "USER_INPUT":
		s.noteModel(content)
		text := content
		if match := agyUserRequest.FindStringSubmatch(content); match != nil {
			text = match[1]
		}
		if strings.TrimSpace(text) != "" {
			s.pushText(RoleUser, MessageText, text, timestamp)
		}
	case "SYSTEM_MESSAGE":
		if strings.TrimSpace(content) != "" {
			s.pushText(RoleSystem, MessageMeta, content, timestamp)
		}
	case "CHECKPOINT":
		// A compaction: the provider replaces the earlier steps with a
		// summary row. The summary text is the agent's own prompt, not a
		// message to show.
		s.pushText(RoleSystem, MessageCompactSummary, "── Context compacted ──", timestamp)
	case "PLANNER_RESPONSE":
		s.feedPlanner(row, content, timestamp)
	case "GENERIC":
		s.feedResult(row, content, status)
	default:
		s.unknownLines++
	}
}

// noteModel reads the model name out of the settings-change note agy
// appends to a user step ("changed setting `Model Selection` from None to
// Gemini 3.8 Flash (High)."). Absent notes leave the model unset.
func (s *agyParseState) noteModel(content string) {
	if match := agyModelSetting.FindStringSubmatch(content); match != nil {
		if name := strings.TrimSpace(match[1]); name != "" && name != "None" {
			s.model = &name
		}
	}
}

func (s *agyParseState) feedPlanner(row map[string]any, content string, timestamp int64) {
	text := strings.TrimSpace(content)
	thinking, _ := row["thinking"].(string)
	thinking = strings.TrimSpace(thinking)
	step := int64(0)
	if value, ok := row["step_index"].(float64); ok {
		step = int64(value)
	}
	var tools []ToolCall
	if calls, ok := row["tool_calls"].([]any); ok {
		for at, item := range calls {
			call, _ := item.(map[string]any)
			name, _ := call["name"].(string)
			if name == "" {
				name = "tool"
			}
			preview, full := agyToolInput(call["args"])
			tools = append(tools, ToolCall{
				// agy states no call id: the step number is the stable one.
				ID:           "agy-" + strconv.FormatInt(step, 10) + "-" + strconv.Itoa(at),
				Name:         name,
				Input:        full,
				InputPreview: preview,
			})
		}
	}
	if text == "" && thinking == "" && len(tools) == 0 {
		return
	}

	contextUsed := int64(0)
	for _, key := range []string{"input_tokens", "cache_read_tokens", "output_tokens"} {
		if value, ok := row[key].(float64); ok && value > 0 {
			contextUsed += int64(value)
		}
	}
	if contextUsed > 0 {
		s.tokensUsed = contextUsed
	}

	// Consecutive model steps (interrupted only by tool results) merge into
	// one assistant message, as for pi.
	if len(s.messages) == 0 || s.messages[len(s.messages)-1].Role != RoleAssistant {
		s.messages = append(s.messages, TranscriptMessage{
			Seq:       int64(len(s.messages)),
			Role:      RoleAssistant,
			Kind:      MessageText,
			ToolCalls: []ToolCall{},
			Timestamp: optionalTimestamp(timestamp),
		})
	}
	lastIdx := len(s.messages) - 1
	last := &s.messages[lastIdx]
	ts := optionalTimestamp(timestamp)
	if thinking != "" {
		clipped, _ := clipText(thinking, MaxToolIO)
		if last.Thinking == nil {
			last.Thinking = &clipped
		} else {
			combined, _ := clipText(*last.Thinking+"\n\n"+clipped, MaxToolIO)
			last.Thinking = &combined
		}
		last.Parts = append(last.Parts, MessagePart{Kind: PartThinking, Text: clipped, Timestamp: ts})
	}
	if text != "" {
		if last.Text != "" {
			last.Text += "\n\n" + text
		} else {
			last.Text = text
		}
		if len(last.Text) > MaxMessageText {
			clipped, truncated := clipText(last.Text, MaxMessageText)
			last.Text, last.Truncated = clipped, truncated
		}
		clipped, _ := clipText(text, MaxMessageText)
		last.Parts = append(last.Parts, MessagePart{Kind: PartText, Text: clipped, Timestamp: ts})
	}
	for _, tool := range tools {
		last.Parts = append(last.Parts, MessagePart{Kind: PartTool, Tool: len(last.ToolCalls), Timestamp: ts})
		s.pending = append(s.pending, [2]int{lastIdx, len(last.ToolCalls)})
		last.ToolCalls = append(last.ToolCalls, tool)
	}
	if s.model != nil {
		last.Model = s.model
	}
	if contextUsed > 0 {
		last.ContextTokens = &contextUsed
	}
}

// feedResult attaches a tool-result step to the oldest call still waiting.
// ERROR steps carry the failure in `error`; a RUNNING step is a background
// task's first report (the call counts as answered, the final result comes
// as a SYSTEM_MESSAGE row).
func (s *agyParseState) feedResult(row map[string]any, content, status string) {
	if len(s.pending) == 0 {
		return
	}
	pos := s.pending[0]
	s.pending = s.pending[1:]
	if pos[0] >= len(s.messages) || pos[1] >= len(s.messages[pos[0]].ToolCalls) {
		return
	}
	call := &s.messages[pos[0]].ToolCalls[pos[1]]
	text := content
	if status == "ERROR" {
		call.IsError = true
		if failure, _ := row["error"].(string); strings.TrimSpace(failure) != "" && strings.TrimSpace(text) == "" {
			text = failure
		}
	}
	clipped, _ := clipText(strings.TrimSpace(text), MaxToolIO)
	call.Output = &clipped
}

func (s *agyParseState) pushText(role Role, kind MessageKind, text string, timestamp int64) {
	clipped, truncated := clipText(text, MaxMessageText)
	if role == RoleUser && injectedUserContent(clipped) {
		kind = MessageMeta
	}
	s.messages = append(s.messages, TranscriptMessage{
		Seq:       int64(len(s.messages)),
		Role:      role,
		Kind:      kind,
		Text:      clipped,
		Truncated: truncated,
		ToolCalls: []ToolCall{},
		Timestamp: optionalTimestamp(timestamp),
	})
}

// agyToolInput is toolInput for agy's argument names (`CommandLine`,
// `Path`, `Query`, …): the one-line preview is the command, path or query,
// and the full input stays the arguments' JSON.
func agyToolInput(input any) (preview string, full *string) {
	preview, full = toolInput(input)
	object, ok := input.(map[string]any)
	if !ok {
		return preview, full
	}
	for _, key := range []string{"CommandLine", "Path", "AbsolutePath", "TargetFile", "Query", "query", "Url", "URL"} {
		if value, ok := object[key].(string); ok && strings.TrimSpace(value) != "" {
			clipped, _ := clipText(strings.TrimSpace(value), 200)
			return clipped, full
		}
	}
	return preview, full
}

// agySummary is what Antigravity's own conversation index knows about one
// conversation and the transcript does not: its title, the workspace it ran
// in, and its parent (sub-agent conversations are children).
type agySummary struct {
	Title     string
	Workspace string
	Parent    string
}

var (
	agySummaryMu    sync.Mutex
	agySummaryIndex = map[string]agySummary{}
	// agySQLiteRunner reads `conversation_summaries.db`; tests substitute it.
	agySQLiteRunner SQLiteRunner = commandSQLiteRunner
)

// agySummaryDB is the index file beside the CLI's brain folder. The IDE's
// conversations are recorded in it too.
func agySummaryDB(brainRoot string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(brainRoot)), "antigravity-cli", "conversation_summaries.db")
}

// refreshAgySummaries reloads the index (one sqlite3 call per listing). A
// missing or unreadable index leaves the transcript-only view: titles from
// the first prompt and no project.
func refreshAgySummaries(dbPath string) {
	if _, err := os.Stat(dbPath); err != nil {
		return
	}
	out, err := querySQLite(agySQLiteRunner, dbPath,
		"select hex(conversation_id)||'|'||hex(title)||'|'||hex(workspace_uris)||'|'||hex(parent_conversation_id) from conversation_summaries;")
	if err != nil {
		return
	}
	next := map[string]agySummary{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r\n"), "|")
		if len(fields) != 4 {
			continue
		}
		decoded := make([]string, 4)
		ok := true
		for i, field := range fields {
			raw, err := hex.DecodeString(field)
			if err != nil {
				ok = false
				break
			}
			decoded[i] = string(raw)
		}
		if !ok || decoded[0] == "" {
			continue
		}
		next[decoded[0]] = agySummary{Title: strings.TrimSpace(decoded[1]), Workspace: firstWorkspacePath(decoded[2]), Parent: decoded[3]}
	}
	agySummaryMu.Lock()
	agySummaryIndex = next
	agySummaryMu.Unlock()
}

// firstWorkspacePath is the first `file://` URI of the index's workspace
// list (a JSON array) as a filesystem path.
func firstWorkspacePath(raw string) string {
	var uris []string
	if json.Unmarshal([]byte(raw), &uris) != nil {
		return ""
	}
	for _, uri := range uris {
		if parsed, err := url.Parse(uri); err == nil && parsed.Scheme == "file" && parsed.Path != "" {
			return parsed.Path
		}
	}
	return ""
}

func agySummaryOf(conversationID string) (agySummary, bool) {
	agySummaryMu.Lock()
	defer agySummaryMu.Unlock()
	summary, ok := agySummaryIndex[conversationID]
	return summary, ok
}

// agyListRefs lists one brain root's conversations: one reference per
// `<id>/.system_generated/logs/transcript_full.jsonl`, sub-agent
// conversations (those with a parent) left out — they belong to their parent.
func agyListRefs(root SourceRoot) ([]SessionFileRef, error) {
	entries, err := os.ReadDir(root.Directory)
	if err != nil {
		if os.IsNotExist(err) {
			return []SessionFileRef{}, nil
		}
		return nil, err
	}
	refreshAgySummaries(agySummaryDB(root.Directory))
	references := []SessionFileRef{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		if summary, ok := agySummaryOf(id); ok && summary.Parent != "" {
			continue
		}
		path := agyTranscriptFile(root.Directory, id)
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			continue
		}
		references = append(references, SessionFileRef{
			Agent:     root.Agent,
			NativeID:  id,
			FilePath:  path,
			MtimeMS:   info.ModTime().UnixMilli(),
			SizeBytes: info.Size(),
		})
	}
	return references, nil
}

// parseAgyWithSummary is ParseAgyTranscript plus the index's title and
// workspace for the list row.
func parseAgyWithSummary(reference SessionFileRef) (ParsedTranscript, error) {
	parsed, err := ParseAgyTranscript(reference)
	if err != nil {
		return parsed, err
	}
	if summary, ok := agySummaryOf(reference.NativeID); ok {
		if summary.Title != "" {
			parsed.Meta.Title = cleanTitleCandidate(summary.Title)
		}
		if summary.Workspace != "" {
			parsed.Meta.ProjectPath = summary.Workspace
			parsed.Meta.ProjectName = projectName(summary.Workspace)
		}
	}
	return parsed, nil
}
