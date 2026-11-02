package history

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

/**
 * [INPUT]: 依赖 history/subagents.go (Subagent, finishSubagents), agy.go (agyNativeID, agyTranscriptFile, agySummaryDB, agySQLiteRunner), cursor_store.go (querySQLite)
 * [OUTPUT]: 对外提供 agySubagents（读取 Antigravity CLI 的 conversation_summaries.db 获取属于父会话的子代理列表）
 * [POS]: internal/history 的 Antigravity 子代理适配器，被 SubagentsFor 调用，服务侧栏、Header 弹层与 Chat 抽屉
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func agySubagents(sessionPath string) []Subagent {
	parentID := agyNativeID(sessionPath)
	if parentID == "" {
		return nil
	}
	logs := filepath.Dir(sessionPath)
	gen := filepath.Dir(logs)
	convDir := filepath.Dir(gen)
	brainRoot := filepath.Dir(convDir)
	if filepath.Base(gen) != ".system_generated" || filepath.Base(logs) != "logs" {
		return nil
	}

	dbPath := agySummaryDB(brainRoot)
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}

	parentHex := hex.EncodeToString([]byte(parentID))
	query := fmt.Sprintf(
		"select hex(conversation_id)||'|'||hex(title)||'|'||hex(agent_name)||'|'||hex(status)||'|'||killed||'|'||hex(last_modified_time)||'|'||nesting_depth from conversation_summaries where hex(parent_conversation_id) = '%s';",
		parentHex,
	)

	out, err := querySQLite(agySQLiteRunner, dbPath, query)
	if err != nil {
		return nil
	}

	var list []Subagent
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r\n"), "|")
		if len(fields) != 7 {
			continue
		}
		childID := decodeHexField(fields[0])
		title := decodeHexField(fields[1])
		agentName := decodeHexField(fields[2])
		status := decodeHexField(fields[3])
		killed := fields[4] == "1" || strings.EqualFold(fields[4], "true")
		modTimeStr := decodeHexField(fields[5])
		depth, _ := strconv.Atoi(fields[6])

		if childID == "" {
			continue
		}

		childPath := agyTranscriptFile(brainRoot, childID)
		info, err := os.Stat(childPath)
		if err != nil || info.Size() == 0 {
			// fallback to transcript.jsonl if transcript_full.jsonl not found or empty
			altPath := filepath.Join(brainRoot, childID, ".system_generated", "logs", "transcript.jsonl")
			if altInfo, altErr := os.Stat(altPath); altErr == nil && altInfo.Size() > 0 {
				childPath = altPath
				info = altInfo
				err = nil
			}
		}

		updatedAt := parseAgyTime(modTimeStr)
		started := updatedAt
		if err == nil {
			updatedAt = info.ModTime()
			if started.IsZero() {
				started = info.ModTime()
			}
		}

		ended := killed || (status != "" && !strings.Contains(status, "RUNNING"))
		subType := agentName
		if subType == "" {
			subType = "subagent"
		}

		list = append(list, Subagent{
			ID:          childID,
			Provider:    AgentAntigravity,
			Type:        subType,
			Name:        agentName,
			Description: cleanTitleCandidate(title),
			Started:     started,
			UpdatedAt:   updatedAt,
			Path:        childPath,
			Depth:       depth,
			Ended:       ended,
			Stopped:     killed,
		})
	}

	return finishSubagents(list)
}

func decodeHexField(s string) string {
	raw, err := hex.DecodeString(s)
	if err != nil {
		return ""
	}
	return string(raw)
}

func parseAgyTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	formats := []string{
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999-07:00",
		"2006-01-02 15:04:05-07:00",
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
	}
	for _, layout := range formats {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
