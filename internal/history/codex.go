package history

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

/**
 * [INPUT]: 依赖 io/bufio/encoding/json 读取 Codex rollout JSONL 与 session_index.jsonl
 * [OUTPUT]: 对外提供 ParseCodexTranscript
 * [POS]: internal/history 的 Codex 会话解析器，支持从 session_index.jsonl 提取原生对话标题并回退首条用户消息
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// codexParseState is the line-level interpretation state for one Codex
// rollout JSONL file. It mirrors the audited Rust CodexSession semantics:
// response_item rows are the real transcript, event_msg user/agent messages
// are only a fallback view used when no real content exists.
type codexParseState struct {
	messages       []TranscriptMessage
	eventFallback  []TranscriptMessage
	toolIndex      map[string][2]int
	sawSessionMeta bool
	cwd            string
	gitBranch      *string
	model          *string
	source         *string
	tokensUsed     int64
	lastContext    *int64
	createdAt      int64
	updatedAt      int64
	unknownLines   uint32
}

// ParseCodexTranscript parses one provider-owned Codex rollout JSONL source.
// It is read-only and does not mutate or normalize the external file. The
// Codex state database (state_5.sqlite) is deliberately not consulted here;
// the rollout itself is the parsing authority.
func ParseCodexTranscript(reference SessionFileRef) (ParsedTranscript, error) {
	file, err := os.Open(reference.FilePath)
	if err != nil {
		return ParsedTranscript{}, fmt.Errorf("open Codex history: %w", err)
	}
	defer file.Close()

	state := newCodexParseState()
	reader := bufio.NewReaderSize(file, 1<<20)
	for {
		line, readErr := reader.ReadString('\n')
		if strings.TrimSpace(line) != "" {
			state.feedCodexLine(line)
		}
		if readErr != nil {
			if readErr != io.EOF {
				state.unknownLines++
			}
			break
		}
	}
	return state.codexTranscript(reference), nil
}

func newCodexParseState() *codexParseState {
	return &codexParseState{toolIndex: make(map[string][2]int)}
}

// codexFriendlySource maps the session_meta originator onto the display fact
// pinned by the Rust adapter.
func codexFriendlySource(originator string) *string {
	switch originator {
	case "codex_cli_rs", "codex-tui":
		value := "CLI"
		return &value
	case "codex_exec":
		value := "exec"
		return &value
	case "codex_vscode":
		value := "IDE extension"
		return &value
	case "codex_work_desktop":
		value := "Codex Desktop"
		return &value
	case "":
		return nil
	default:
		return &originator
	}
}

// rolloutNativeID extracts the stable native session id from a rollout file
// stem: "rollout-YYYY-MM-DDTHH-MM-SS-<uuid>.jsonl" yields <uuid>.
func rolloutNativeID(stem string) string {
	if rest, ok := strings.CutPrefix(stem, "rollout-"); ok {
		if len(rest) > 20 && rest[10] == 'T' {
			return rest[20:]
		}
	}
	return stem
}

func (s *codexParseState) pushCodex(message TranscriptMessage) {
	message.Seq = int64(len(s.messages))
	message.ContextTokens = s.lastContext
	s.messages = append(s.messages, message)
}

func (s *codexParseState) pushFallback(message TranscriptMessage) {
	message.Seq = int64(len(s.eventFallback))
	s.eventFallback = append(s.eventFallback, message)
}

// codexFallbackActive mirrors the Rust fallback rule: the event view is
// presented only while no real text content exists.
func (s *codexParseState) codexFallbackActive() bool {
	for _, message := range s.messages {
		if message.Kind == MessageText && message.Text != "" {
			return false
		}
	}
	return len(s.eventFallback) > 0
}

func (s *codexParseState) codexMessages() []TranscriptMessage {
	if s.codexFallbackActive() {
		return s.eventFallback
	}
	return s.messages
}

type codexSessionIndexEntry struct {
	ID         string `json:"id"`
	ThreadName string `json:"thread_name"`
	UpdatedAt  string `json:"updated_at"`
}

var (
	codexTitleMu         sync.Mutex
	codexTitleMtime      time.Time
	codexTitleCache      map[string]string // session_id -> thread_name
	codexTitleCachedPath string
)

// codexSessionIndexPath locates the session_index.jsonl associated with a rollout path.
func codexSessionIndexPath(filePath string) string {
	if filePath != "" {
		cur := filepath.Dir(filePath)
		for i := 0; i < 6; i++ {
			base := filepath.Base(cur)
			if base == "sessions" || base == "archived_sessions" || base == ".codex" {
				candidate := filepath.Join(filepath.Dir(cur), "session_index.jsonl")
				if base == ".codex" {
					candidate = filepath.Join(cur, "session_index.jsonl")
				}
				if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
					return candidate
				}
			}
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
	}

	if codexHome := os.Getenv("CODEX_HOME"); codexHome != "" {
		candidate := filepath.Join(codexHome, "session_index.jsonl")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidate := filepath.Join(home, ".codex", "session_index.jsonl")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// lookupCodexTitle queries session_index.jsonl for the thread title of sessionID.
// Returns empty string if not found, allowing caller to fall back to first user message.
func lookupCodexTitle(filePath, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	indexPath := codexSessionIndexPath(filePath)
	if indexPath == "" {
		return ""
	}

	codexTitleMu.Lock()
	defer codexTitleMu.Unlock()

	info, err := os.Stat(indexPath)
	if err != nil || info.IsDir() {
		return ""
	}

	if codexTitleCache == nil || indexPath != codexTitleCachedPath || !info.ModTime().Equal(codexTitleMtime) {
		file, err := os.Open(indexPath)
		if err != nil {
			return ""
		}
		defer file.Close()

		newCache := make(map[string]string)
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 64*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var entry codexSessionIndexEntry
			if err := json.Unmarshal(line, &entry); err == nil && entry.ID != "" && entry.ThreadName != "" {
				newCache[entry.ID] = entry.ThreadName
			}
		}
		codexTitleCache = newCache
		codexTitleCachedPath = indexPath
		codexTitleMtime = info.ModTime()
	}

	if name, ok := codexTitleCache[sessionID]; ok {
		clean := cleanTitleCandidate(name)
		if clean != "" {
			return clean
		}
	}
	return ""
}

func (s *codexParseState) codexTranscript(reference SessionFileRef) ParsedTranscript {
	messages := s.codexMessages()
	title := lookupCodexTitle(reference.FilePath, reference.NativeID)
	if title == "" {
		title = titleFromMessages(messages)
	}
	if title == "" {
		title = Untitled
	}
	created := s.createdAt
	if created == 0 {
		created = reference.MtimeMS
	}
	updated := s.updatedAt
	if updated == 0 {
		updated = reference.MtimeMS
	}
	var tokensUsed *int64
	if s.tokensUsed > 0 {
		value := s.tokensUsed
		tokensUsed = &value
	}
	messageCount := int64(0)
	for _, message := range messages {
		if message.Kind == MessageText {
			messageCount++
		}
	}
	return ParsedTranscript{
		Meta: SessionMeta{
			Key:          string(AgentCodex) + ":" + reference.NativeID,
			ID:           reference.NativeID,
			Agent:        AgentCodex,
			Title:        title,
			ProjectPath:  s.cwd,
			ProjectName:  projectName(s.cwd),
			FilePath:     reference.FilePath,
			CreatedAt:    created,
			UpdatedAt:    updated,
			MessageCount: messageCount,
			SizeBytes:    reference.SizeBytes,
			GitBranch:    s.gitBranch,
			Model:        s.model,
			TokensUsed:   tokensUsed,
			Source:       s.source,
		},
		Mainline:         append([]TranscriptMessage(nil), messages...),
		Sidechains:       []SidechainInfo{},
		UnknownLineCount: s.unknownLines,
	}
}

func (s *codexParseState) feedCodexLine(line string) {
	var row map[string]any
	if err := json.Unmarshal([]byte(line), &row); err != nil {
		s.unknownLines++
		return
	}
	timestamp := epochMS(row["timestamp"])
	if timestamp > 0 {
		if s.createdAt == 0 {
			s.createdAt = timestamp
		}
		if timestamp > s.updatedAt {
			s.updatedAt = timestamp
		}
	}
	rowType, _ := row["type"].(string)
	payload, hasPayload := row["payload"]
	if !hasPayload {
		if rowType != "compacted" && rowType != "world_state" {
			s.unknownLines++
		}
		return
	}
	payloadObject, isObject := payload.(map[string]any)

	switch rowType {
	case "session_meta":
		if s.sawSessionMeta || !isObject {
			return
		}
		s.sawSessionMeta = true
		s.cwd, _ = payloadObject["cwd"].(string)
		originator, _ := payloadObject["originator"].(string)
		s.source = codexFriendlySource(originator)
		if git, ok := payloadObject["git"].(map[string]any); ok {
			if branch, ok := git["branch"].(string); ok && branch != "" {
				s.gitBranch = stringPointer(branch)
			}
		}
	case "turn_context":
		if !isObject {
			return
		}
		if s.cwd == "" {
			s.cwd, _ = payloadObject["cwd"].(string)
		}
		if model, ok := payloadObject["model"].(string); ok && model != "" {
			s.model = stringPointer(model)
		}
	case "response_item":
		if isObject {
			s.parseCodexResponseItem(payloadObject, timestamp)
		}
	case "event_msg":
		if !isObject {
			return
		}
		eventType, _ := payloadObject["type"].(string)
		switch eventType {
		case "token_count":
			if info, ok := payloadObject["info"].(map[string]any); ok {
				if usage, ok := info["total_token_usage"].(map[string]any); ok {
					if total, ok := usage["total_tokens"].(float64); ok {
						s.tokensUsed = int64(total)
					}
				}
				// last_token_usage is a sibling of total_token_usage: the
				// request that just ran, i.e. the context it filled. The
				// report follows its assistant message, so that message
				// takes it too (context ring only).
				if last, ok := info["last_token_usage"].(map[string]any); ok {
					if total, ok := last["total_tokens"].(float64); ok && total > 0 {
						used := int64(total)
						s.lastContext = &used
						if n := len(s.messages); n > 0 && s.messages[n-1].Role == RoleAssistant {
							s.messages[n-1].ContextTokens = &used
						}
					}
				}
			}
		case "user_message":
			if text, ok := payloadObject["message"].(string); ok && strings.TrimSpace(text) != "" {
				trimmed := strings.TrimSpace(text)
				s.pushFallback(TranscriptMessage{
					Role:      RoleUser,
					Kind:      userKind(trimmed),
					Text:      trimmed,
					Timestamp: optionalTimestamp(timestamp),
				})
			}
		case "agent_message":
			if text, ok := payloadObject["message"].(string); ok && strings.TrimSpace(text) != "" {
				trimmed := strings.TrimSpace(text)
				s.pushFallback(TranscriptMessage{
					Role:      RoleAssistant,
					Kind:      MessageText,
					Text:      trimmed,
					Timestamp: optionalTimestamp(timestamp),
				})
			}
		}
	case "compacted":
		s.pushCodex(TranscriptMessage{
			Role:      RoleSystem,
			Kind:      MessageCompactSummary,
			Text:      "── Context compacted ──",
			Timestamp: optionalTimestamp(timestamp),
		})
	case "world_state":
	default:
		s.unknownLines++
	}
}
