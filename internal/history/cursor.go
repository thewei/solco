/**
 * [INPUT]: 依赖 history/models.go (TranscriptMessage, SessionFileRef), parse_utils.go (clipText, extractTag, toolInput, stringifyToolResult), cursor_store.go (CursorStoreSource 合成流)
 * [OUTPUT]: 对外提供 ParseCursorTranscript, cursorParseState, newCursorParseState, cursorStoreFile
 * [POS]: Cursor Agent（cursor-agent）会话解析器：输入是 CursorStoreSource 把 `~/.cursor/chats/<cwdMD5>/<chatId>/store.db` 的消息 blob 合成的 JSONL 流
 *        （`{"h":hash,"at":观察毫秒,"m":OpenAI 风格消息}` 与 `{"model":…}` 行）；system 行不显示、`<user_query>` 取出用户原话、
 *        `providerOptions.cursor.isSummary` 是压缩摘要、tool-result 按 toolCallId 回填（`highLevelToolCallResult.isError` 标错）。
 *        Cursor 的消息没有逐条时间：live 行的时间是「首次被我们看到」的时刻，整文件解析为空；但每条用户消息头带 `<timestamp>Sunday, Nov 1, 2026, 1:42 PM (UTC+8)</timestamp>`（真实挂钟），用户行与会话起止时间以它为准（cursorMessageTime）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package history

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// cursorStoreName is the SQLite file one Cursor chat lives in.
const cursorStoreName = "store.db"

// cursorStoreFile is the store of one chat under a chats root
// (`~/.cursor/chats`): `<root>/<md5(cwd)>/<chatId>/store.db`. The directory
// hash is not recomputed — the chat id alone locates it.
func cursorStoreFile(chatsRoot, chatID string) (string, bool) {
	if chatID == "" || strings.ContainsAny(chatID, `/\`) || chatID == "." || chatID == ".." {
		return "", false
	}
	matches, err := filepath.Glob(filepath.Join(chatsRoot, "*", chatID, cursorStoreName))
	if err != nil || len(matches) == 0 {
		return "", false
	}
	return matches[0], true
}

// cursorListRefs lists the chats under `~/.cursor/chats`: one reference per
// `<cwdMD5>/<chatId>/store.db`. The stamp covers the WAL as well — the CLI's
// newest writes live there until a checkpoint, so the main file's own mtime
// would not move while a chat is active.
func cursorListRefs(root SourceRoot) ([]SessionFileRef, error) {
	info, err := os.Stat(root.Directory)
	if err != nil {
		if os.IsNotExist(err) {
			return []SessionFileRef{}, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("history root %s is not a directory", root.Directory)
	}
	stores, err := filepath.Glob(filepath.Join(root.Directory, "*", "*", cursorStoreName))
	if err != nil {
		return nil, err
	}
	references := []SessionFileRef{}
	for _, store := range stores {
		meta, err := os.Stat(store)
		if err != nil || meta.Size() == 0 {
			continue
		}
		dir := filepath.Dir(store)
		if chat, ok := readCursorChatMeta(dir); ok && !chat.HasConversation {
			continue // a chat that never got a prompt
		}
		mtime, size := meta.ModTime().UnixMilli(), meta.Size()
		if wal, err := os.Stat(store + "-wal"); err == nil {
			size += wal.Size()
			if ms := wal.ModTime().UnixMilli(); ms > mtime {
				mtime = ms
			}
		}
		references = append(references, SessionFileRef{
			Agent:     root.Agent,
			NativeID:  filepath.Base(dir),
			FilePath:  store,
			MtimeMS:   mtime,
			SizeBytes: size,
		})
	}
	return references, nil
}

var cursorRolePrefix = regexp.MustCompile(`^\{"role":"(\w+)"`)

// summarizeCursorTranscript is the list-row read of a chat: the same rows as
// ParseCursorTranscript for user turns (their bodies are small), with each
// assistant message reduced to a stub and tool results skipped — the bulk of
// a store is tool output, and a cold scan must not pull 600 MB through a
// pipe. Counts and titles come out as the full parse's would.
func summarizeCursorTranscript(reference SessionFileRef) (ParsedTranscript, error) {
	source := NewCursorStoreSource(reference.FilePath)
	meta, ids, err := source.loadRoot()
	if err != nil {
		return ParsedTranscript{}, fmt.Errorf("read Cursor history: %w", err)
	}
	roles := make([]string, len(ids))
	for start := 0; start < len(ids); start += cursorFetchBatch {
		end := min(start+cursorFetchBatch, len(ids))
		quoted := make([]string, 0, end-start)
		for _, id := range ids[start:end] {
			quoted = append(quoted, "'"+id+"'")
		}
		raw, err := source.query("select id||'|'||hex(substr(data,1,24)) from blobs where id in (" + strings.Join(quoted, ",") + ");")
		if err != nil {
			return ParsedTranscript{}, fmt.Errorf("read Cursor history: %w", err)
		}
		byID := map[string]string{}
		for _, line := range strings.Split(string(raw), "\n") {
			id, payload, ok := strings.Cut(strings.TrimSpace(line), "|")
			if !ok {
				continue
			}
			if prefix, err := hex.DecodeString(payload); err == nil {
				if match := cursorRolePrefix.FindSubmatch(prefix); match != nil {
					byID[id] = string(match[1])
				}
			}
		}
		for i, id := range ids[start:end] {
			roles[start+i] = byID[id]
		}
	}
	// A message whose JSON does not open with its role is read whole to
	// learn it (never seen in real stores; the cost is the rare blob).
	var unknown []int
	var unknownIDs []string
	for i, role := range roles {
		if role == "" {
			unknown = append(unknown, i)
			unknownIDs = append(unknownIDs, ids[i])
		}
	}
	if len(unknownIDs) > 0 {
		blobs, err := source.fetchMessages(unknownIDs)
		if err != nil {
			return ParsedTranscript{}, fmt.Errorf("read Cursor history: %w", err)
		}
		for n, blob := range blobs {
			var head struct {
				Role string `json:"role"`
			}
			if json.Unmarshal(blob, &head) == nil {
				roles[unknown[n]] = head.Role
			}
		}
	}

	var userIDs []string
	for i, role := range roles {
		if role == "user" {
			userIDs = append(userIDs, ids[i])
		}
	}
	users, err := source.fetchMessages(userIDs)
	if err != nil {
		return ParsedTranscript{}, fmt.Errorf("read Cursor history: %w", err)
	}
	state := newCursorParseState()
	if meta.Model != "" {
		model := meta.Model
		state.model = &model
	}
	next := 0
	for i, role := range roles {
		switch role {
		case "user":
			state.feedCursorLine(string(cursorStreamLine(ids[i], 0, users[next])))
			next++
		case "assistant":
			state.feedCursorLine(`{"at":0,"m":{"role":"assistant","content":[{"type":"text","text":"."}]}}`)
		}
	}
	parsed := state.transcript(reference)
	applyCursorChatMeta(&parsed.Meta, filepath.Dir(reference.FilePath))
	return parsed, nil
}

type cursorParseState struct {
	createdAt    int64
	lastTS       int64
	model        *string
	unknownLines uint32
	messages     []TranscriptMessage
	toolIndex    map[string][2]int // toolCallId -> [messageIndex, toolCallIndex]
}

func newCursorParseState() *cursorParseState {
	return &cursorParseState{toolIndex: make(map[string][2]int)}
}

// ParseCursorTranscript parses one Cursor chat store. It is read-only: the
// store is opened through CursorStoreSource's read-only sqlite3 session.
func ParseCursorTranscript(reference SessionFileRef) (ParsedTranscript, error) {
	source := NewCursorStoreSource(reference.FilePath)
	size, err := source.Size()
	if err != nil {
		return ParsedTranscript{}, fmt.Errorf("read Cursor history: %w", err)
	}
	buf := make([]byte, size)
	if size > 0 {
		if _, err := source.ReadAt(buf, 0); err != nil {
			return ParsedTranscript{}, fmt.Errorf("read Cursor history: %w", err)
		}
	}
	state := newCursorParseState()
	for _, line := range strings.SplitAfter(string(buf), "\n") {
		state.feedCursorLine(line)
	}
	parsed := state.transcript(reference)
	applyCursorChatMeta(&parsed.Meta, filepath.Dir(reference.FilePath))
	return parsed, nil
}

// cursorChatMeta is `meta.json` beside store.db: the CLI's own title, cwd and
// timestamps for the chat.
type cursorChatMeta struct {
	HasConversation bool   `json:"hasConversation"`
	Title           string `json:"title"`
	CWD             string `json:"cwd"`
	CreatedAtMS     int64  `json:"createdAtMs"`
	UpdatedAtMS     int64  `json:"updatedAtMs"`
}

func readCursorChatMeta(dir string) (cursorChatMeta, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return cursorChatMeta{}, false
	}
	var meta cursorChatMeta
	if json.Unmarshal(data, &meta) != nil {
		return cursorChatMeta{}, false
	}
	return meta, true
}

func applyCursorChatMeta(meta *SessionMeta, dir string) {
	chat, ok := readCursorChatMeta(dir)
	if !ok {
		return
	}
	if title := strings.TrimSpace(chat.Title); title != "" {
		meta.Title = cleanTitleCandidate(title)
	}
	if chat.CWD != "" {
		meta.ProjectPath = chat.CWD
		meta.ProjectName = projectName(chat.CWD)
	}
	if chat.CreatedAtMS > 0 {
		meta.CreatedAt = chat.CreatedAtMS
	}
	if chat.UpdatedAtMS > 0 {
		meta.UpdatedAt = chat.UpdatedAtMS
	}
}

func (s *cursorParseState) transcript(reference SessionFileRef) ParsedTranscript {
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
		},
		Mainline:         append([]TranscriptMessage(nil), s.messages...),
		Sidechains:       []SidechainInfo{},
		UnknownLineCount: s.unknownLines,
	}
}

// feedCursorLine folds one synthesised stream line. Lines are either a model
// note (`{"model":…}`) or one stored message wrapped with its hash and the
// time the source first saw it.
func (s *cursorParseState) feedCursorLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	var row struct {
		Model   string          `json:"model"`
		At      int64           `json:"at"`
		Message json.RawMessage `json:"m"`
	}
	if err := json.Unmarshal([]byte(line), &row); err != nil {
		s.unknownLines++
		return
	}
	if row.Model != "" {
		model := row.Model
		s.model = &model
		return
	}
	if len(row.Message) == 0 {
		s.unknownLines++
		return
	}
	if row.At > 0 {
		if s.createdAt == 0 {
			s.createdAt = row.At
		}
		if row.At > s.lastTS {
			s.lastTS = row.At
		}
	}
	var message map[string]any
	if err := json.Unmarshal(row.Message, &message); err != nil {
		s.unknownLines++
		return
	}
	role, _ := message["role"].(string)
	switch role {
	case "system":
		// The agent's own system prompt: never shown.
	case "user":
		s.feedUser(message, row.At)
	case "assistant":
		s.feedAssistant(message, row.At)
	case "tool":
		s.feedToolResults(message)
	default:
		s.unknownLines++
	}
}

func (s *cursorParseState) feedUser(message map[string]any, at int64) {
	text := cursorContentText(message["content"])
	if strings.TrimSpace(text) == "" {
		return
	}
	if cursorIsSummary(message) {
		s.pushText(RoleSystem, MessageCompactSummary, "── Context compacted ──", at)
		return
	}
	// Cursor stamps every user message with the real wall-clock time the turn
	// began; unlike the source's first-seen time it also covers rows that
	// existed before Solco looked, so it wins when present.
	if stamped := cursorMessageTime(text); stamped > 0 {
		at = stamped
		if s.createdAt == 0 || stamped < s.createdAt {
			s.createdAt = stamped
		}
		if stamped > s.lastTS {
			s.lastTS = stamped
		}
	}
	if query := strings.TrimSpace(extractTag(text, "user_query")); query != "" {
		s.pushText(RoleUser, MessageText, query, at)
		return
	}
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if strings.HasPrefix(trimmed, "<user_info>") || strings.HasPrefix(trimmed, "<system_reminder>") {
		s.pushText(RoleUser, MessageMeta, text, at)
		return
	}
	s.pushText(RoleUser, MessageText, text, at)
}

func cursorIsSummary(message map[string]any) bool {
	options, _ := message["providerOptions"].(map[string]any)
	cursor, _ := options["cursor"].(map[string]any)
	summary, _ := cursor["isSummary"].(bool)
	return summary
}

func (s *cursorParseState) feedAssistant(message map[string]any, at int64) {
	var parts []MessagePart
	var tools []ToolCall
	var textParts, thinkingParts []string
	ts := optionalTimestamp(at)

	switch content := message["content"].(type) {
	case string:
		if text := strings.TrimSpace(content); text != "" {
			textParts = append(textParts, text)
			clipped, _ := clipText(text, MaxMessageText)
			parts = append(parts, MessagePart{Kind: PartText, Text: clipped, Timestamp: ts})
		}
	case []any:
		for _, item := range content {
			block, _ := item.(map[string]any)
			switch block["type"] {
			case "text":
				if text, _ := block["text"].(string); strings.TrimSpace(text) != "" {
					text = strings.TrimSpace(text)
					textParts = append(textParts, text)
					clipped, _ := clipText(text, MaxMessageText)
					parts = append(parts, MessagePart{Kind: PartText, Text: clipped, Timestamp: ts})
				}
			case "reasoning":
				// Cursor mostly stores `redacted-reasoning` (opaque); a
				// readable reasoning block is shown when a model writes one.
				if text, _ := block["text"].(string); strings.TrimSpace(text) != "" {
					text = strings.TrimSpace(text)
					thinkingParts = append(thinkingParts, text)
					clipped, _ := clipText(text, MaxToolIO)
					parts = append(parts, MessagePart{Kind: PartThinking, Text: clipped, Timestamp: ts})
				}
			case "tool-call":
				id, _ := block["toolCallId"].(string)
				name, _ := block["toolName"].(string)
				if name == "" {
					name = "tool"
				}
				preview, full := cursorToolInput(block["args"])
				parts = append(parts, MessagePart{Kind: PartTool, Tool: len(tools), Timestamp: ts})
				tools = append(tools, ToolCall{ID: id, Name: name, Input: full, InputPreview: preview})
			}
		}
	}
	if len(parts) == 0 {
		return
	}

	// Consecutive assistant messages (only tool results between them) are
	// one turn of the agent: merge them as pi does.
	if len(s.messages) == 0 || s.messages[len(s.messages)-1].Role != RoleAssistant {
		s.messages = append(s.messages, TranscriptMessage{
			Seq:       int64(len(s.messages)),
			Role:      RoleAssistant,
			Kind:      MessageText,
			ToolCalls: []ToolCall{},
			Timestamp: ts,
		})
	}
	lastIdx := len(s.messages) - 1
	last := &s.messages[lastIdx]
	if len(textParts) > 0 {
		joined := strings.Join(textParts, "\n\n")
		if last.Text != "" {
			last.Text += "\n\n" + joined
		} else {
			last.Text = joined
		}
		if len(last.Text) > MaxMessageText {
			last.Text, last.Truncated = clipText(last.Text, MaxMessageText)
		}
	}
	if len(thinkingParts) > 0 {
		joined := strings.Join(thinkingParts, "\n\n")
		if last.Thinking != nil {
			joined = *last.Thinking + "\n\n" + joined
		}
		clipped, _ := clipText(joined, MaxToolIO)
		last.Thinking = &clipped
	}
	toolBase := len(last.ToolCalls)
	for _, part := range parts {
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
	if s.model != nil {
		last.Model = s.model
	}
}

func (s *cursorParseState) feedToolResults(message map[string]any) {
	blocks, _ := message["content"].([]any)
	failed := cursorToolFailed(message)
	for _, item := range blocks {
		block, _ := item.(map[string]any)
		if block["type"] != "tool-result" {
			continue
		}
		id, _ := block["toolCallId"].(string)
		pos, found := s.toolIndex[id]
		if !found || pos[0] >= len(s.messages) || pos[1] >= len(s.messages[pos[0]].ToolCalls) {
			continue
		}
		call := &s.messages[pos[0]].ToolCalls[pos[1]]
		clipped, _ := clipText(strings.TrimSpace(cursorResultText(block["result"])), MaxToolIO)
		call.Output = &clipped
		if failed {
			call.IsError = true
		}
	}
}

// cursorToolFailed reads the CLI's own failure flag of a tool message:
// `providerOptions.cursor.highLevelToolCallResult.isError`.
func cursorToolFailed(message map[string]any) bool {
	options, _ := message["providerOptions"].(map[string]any)
	cursor, _ := options["cursor"].(map[string]any)
	result, _ := cursor["highLevelToolCallResult"].(map[string]any)
	failed, _ := result["isError"].(bool)
	return failed
}

func (s *cursorParseState) pushText(role Role, kind MessageKind, text string, at int64) {
	clipped, truncated := clipText(text, MaxMessageText)
	s.messages = append(s.messages, TranscriptMessage{
		Seq:       int64(len(s.messages)),
		Role:      role,
		Kind:      kind,
		Text:      clipped,
		Truncated: truncated,
		ToolCalls: []ToolCall{},
		Timestamp: optionalTimestamp(at),
	})
}

// cursorContentText is a message's text blocks joined (content is a string
// or an array of typed blocks).
func cursorContentText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var parts []string
		for _, item := range value {
			block, _ := item.(map[string]any)
			if block["type"] == "text" {
				if text, _ := block["text"].(string); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n\n")
	}
	return ""
}

func cursorResultText(result any) string {
	switch value := result.(type) {
	case string:
		return value
	case nil:
		return ""
	default:
		return stringifyToolResult(value)
	}
}

// cursorToolInput is toolInput plus Cursor's `glob_pattern` for the one-line
// preview.
func cursorToolInput(input any) (preview string, full *string) {
	preview, full = toolInput(input)
	if object, ok := input.(map[string]any); ok {
		for _, key := range []string{"glob_pattern", "search_term"} {
			if value, ok := object[key].(string); ok && strings.TrimSpace(value) != "" && strings.HasPrefix(preview, "{") {
				clipped, _ := clipText(strings.TrimSpace(value), 200)
				return clipped, full
			}
		}
	}
	return preview, full
}

// cursorTimestampLayout is the form of Cursor's `<timestamp>` header, e.g.
// "Sunday, Nov 1, 2026, 1:42 PM (UTC+8)" (captured from cursor-agent 2026.10.22).
const cursorTimestampLayout = "Monday, Jan 2, 2006, 3:04 PM"

// cursorMessageTime reads the `<timestamp>` header of a user message as Unix
// milliseconds, or 0 when absent or in an unknown shape.
func cursorMessageTime(text string) int64 {
	raw := strings.TrimSpace(extractTag(text, "timestamp"))
	date, zone, ok := strings.Cut(raw, " (UTC")
	if !ok {
		return 0
	}
	offset := 0
	if zone = strings.TrimSuffix(zone, ")"); zone != "" {
		sign := 1
		switch zone[0] {
		case '+':
		case '-':
			sign = -1
		default:
			return 0
		}
		hours, minutes, _ := strings.Cut(zone[1:], ":")
		h, errH := strconv.Atoi(hours)
		m := 0
		var errM error
		if minutes != "" {
			m, errM = strconv.Atoi(minutes)
		}
		if errH != nil || errM != nil {
			return 0
		}
		offset = sign * (h*3600 + m*60)
	}
	parsed, err := time.ParseInLocation(cursorTimestampLayout, strings.TrimSpace(date), time.FixedZone("", offset))
	if err != nil {
		return 0
	}
	return parsed.UnixMilli()
}
