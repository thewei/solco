package feedback

/**
 * [INPUT]: 旧 mcp-feedback 的 decisions.jsonl（每行一条已结束的会话：时间、项目根、caller_info、问题与答案）
 * [OUTPUT]: 对外提供 ImportDecisions（一次性把旧历史按线程规则回放进 Store）、LegacyDecisionsPath
 * [POS]: 迁移旧反馈历史（2026-11-09 决策）：旧记录没有 MCP 客户端名，客户端取 caller_info 的前缀；导入后写 imported.json，之后不再读旧文件；
 *        图片和描述没有被旧日志保存，导入的 Ask 只有问题与答案
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// LegacyDecisionsPath is where the Bun daemon kept its audit log.
func LegacyDecisionsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "interactive-feedback-mcp", "decisions.jsonl")
}

type legacyRecord struct {
	TS          int64  `json:"ts"`
	SessionID   string `json:"sessionId"`
	Status      string `json:"status"`
	Reason      string `json:"reason"`
	ProjectRoot string `json:"projectRoot"`
	TaskID      string `json:"taskId"`
	CallerInfo  string `json:"callerInfo"`
	CreatedAt   int64  `json:"createdAt"`
	Decisions   []struct {
		Q        string   `json:"q"`
		Type     string   `json:"type"`
		Selected []string `json:"selected"`
		Custom   string   `json:"custom"`
	} `json:"decisions"`
}

// ImportDecisions replays an old decisions.jsonl into the Store, oldest first, and returns how many records it
// took. dir/imported.json marks it done: a second call returns 0.
func (s *Store) ImportDecisions(dir, file string) (int, error) {
	marker := filepath.Join(dir, "imported.json")
	if _, err := os.Stat(marker); err == nil {
		return 0, nil
	}
	f, err := os.Open(file)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()
	var recs []legacyRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		var r legacyRecord
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.SessionID != "" && len(r.Decisions) > 0 {
			recs = append(recs, r)
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].CreatedAt < recs[j].CreatedAt })

	s.mu.Lock()
	n := 0
	for _, r := range recs {
		if _, dup := s.byAsk[r.SessionID]; dup {
			continue
		}
		var qs []Question
		answers := map[string]Answer{}
		for i, d := range r.Decisions {
			id := fmt.Sprintf("q%d", i+1)
			qs = append(qs, Question{ID: id, Text: clipBytes(d.Q, maxQuestionText), Type: d.Type, AllowCustom: d.Type != "text"})
			sel := d.Selected
			if sel == nil {
				sel = []string{}
			}
			answers[id] = Answer{Selected: sel, Custom: clipBytes(d.Custom, maxAnswerText)}
		}
		qs, err := normalizeQuestions(qs)
		if err != nil {
			continue
		}
		created, resolved := r.CreatedAt, r.TS
		if resolved < created {
			resolved = created
		}
		t := s.assignLocked(CreateInput{CallerInfo: r.CallerInfo, ProjectRoot: r.ProjectRoot, TaskID: r.TaskID}, created)
		a := &Ask{ID: r.SessionID, ThreadID: t.ID, CreatedAt: created, ResolvedAt: resolved, LastHeartbeat: resolved,
			Questions: qs, State: StateEnded, Message: clipRunes(r.Reason, 200), Answers: nil}
		if r.Status == "answered" {
			a.State, a.Answers, a.Message = StateAnswered, answers, ""
		}
		t.Asks = append(t.Asks, a)
		t.LastAt = resolved
		s.byAsk[a.ID] = t
		s.trimLocked(t)
		n++
	}
	s.saveLocked()
	s.mu.Unlock()
	body, _ := json.Marshal(map[string]any{"source": "mcp-feedback decisions.jsonl", "records": n})
	if err := os.WriteFile(marker, body, 0o600); err != nil {
		return n, err
	}
	return n, nil
}
