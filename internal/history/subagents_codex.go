package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

/**
 * [INPUT]: 一个 Codex 会话 rollout 路径（`<root>/sessions/YYYY/MM/DD/rollout-*.jsonl`）
 * [OUTPUT]: codexSubagents（session_meta.source.subagent.thread_spawn.parent_thread_id 指向该会话的 rollout）
 * [POS]: Codex 的子代理是同一棵 sessions 树里的独立 rollout（真机核对，2026-10-31）：首行 session_meta 带 parent_thread_id / depth / agent_path（`/root/<task_name>`）/ agent_nickname；没有「父 → 子」目录，所以只在父会话日期之后的日期目录里按首行反查。每个文件的首行分类结果永不变化，按路径缓存，常驻刷新只为新文件付出读取成本。只读首行，不解析对话
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// codexFirstLineLimit bounds one session_meta read (it carries the base
// instructions, tens of KiB).
const codexFirstLineLimit = 1 << 20

// codexTailBytes covers the token_count rows that follow task_complete.
const codexTailBytes = 16 << 10

type codexSessionMeta struct {
	Type    string `json:"type"`
	Payload struct {
		ID        string `json:"id"`
		Timestamp string `json:"timestamp"`
		// Source is a plain string ("exec", "cli") for an ordinary session and
		// an object only for a spawned one.
		Source json.RawMessage `json:"source"`
	} `json:"payload"`
}

type codexSpawn struct {
	Subagent struct {
		ThreadSpawn struct {
			ParentThreadID string `json:"parent_thread_id"`
			Depth          int    `json:"depth"`
			AgentPath      string `json:"agent_path"`
			AgentNickname  string `json:"agent_nickname"`
		} `json:"thread_spawn"`
	} `json:"subagent"`
}

// codexClass is the cached verdict on one rollout's first line: which
// session spawned it (empty = an ordinary, user-started session).
type codexClass struct {
	parent   string
	id       string
	name     string
	nickname string
	depth    int
	started  time.Time
}

var (
	codexClassMu    sync.Mutex
	codexClassCache = map[string]codexClass{}
)

const codexClassCacheLimit = 4096

func codexClassify(path string) (codexClass, bool) {
	codexClassMu.Lock()
	cached, ok := codexClassCache[path]
	codexClassMu.Unlock()
	if ok {
		return cached, true
	}
	meta, ok := codexReadMeta(path)
	if !ok {
		return codexClass{}, false
	}
	var source codexSpawn
	if len(meta.Payload.Source) > 0 && meta.Payload.Source[0] == '{' {
		_ = json.Unmarshal(meta.Payload.Source, &source)
	}
	spawn := source.Subagent.ThreadSpawn
	class := codexClass{
		parent:   spawn.ParentThreadID,
		id:       meta.Payload.ID,
		name:     strings.TrimPrefix(spawn.AgentPath, "/root/"),
		nickname: spawn.AgentNickname,
		depth:    spawn.Depth,
	}
	if at, err := time.Parse(time.RFC3339Nano, meta.Payload.Timestamp); err == nil {
		class.started = at
	}
	codexClassMu.Lock()
	if len(codexClassCache) >= codexClassCacheLimit {
		codexClassCache = map[string]codexClass{}
	}
	codexClassCache[path] = class
	codexClassMu.Unlock()
	return class, true
}

func codexReadMeta(path string) (codexSessionMeta, bool) {
	file, err := os.Open(path)
	if err != nil {
		return codexSessionMeta{}, false
	}
	defer file.Close()
	line, err := bufio.NewReaderSize(file, 128<<10).ReadBytes('\n')
	if len(line) == 0 || len(line) > codexFirstLineLimit || (err != nil && len(line) == 0) {
		return codexSessionMeta{}, false
	}
	var meta codexSessionMeta
	if json.Unmarshal(line, &meta) != nil || meta.Type != "session_meta" {
		return codexSessionMeta{}, false
	}
	return meta, true
}

func codexSubagents(parentPath string) []Subagent {
	if !strings.HasSuffix(parentPath, ".jsonl") {
		return nil
	}
	// <root>/sessions/YYYY/MM/DD/rollout-*.jsonl
	dayDir := filepath.Dir(parentPath)
	root := filepath.Dir(filepath.Dir(filepath.Dir(dayDir)))
	if filepath.Base(root) != "sessions" {
		return nil
	}
	parent, ok := codexClassify(parentPath)
	if !ok || parent.id == "" {
		return nil
	}
	day, err := time.ParseInLocation("2006/01/02", filepath.Join(filepath.Base(filepath.Dir(filepath.Dir(dayDir))), filepath.Base(filepath.Dir(dayDir)), filepath.Base(dayDir)), time.Local)
	if err != nil {
		return nil
	}
	var list []Subagent
	today := time.Now()
	for cursor := day; !cursor.After(today); cursor = cursor.AddDate(0, 0, 1) {
		files, err := filepath.Glob(filepath.Join(root, cursor.Format("2006/01/02"), "rollout-*.jsonl"))
		if err != nil {
			continue
		}
		for _, file := range files {
			if file == parentPath {
				continue
			}
			info, err := os.Stat(file)
			if err != nil || (!parent.started.IsZero() && info.ModTime().Before(parent.started)) {
				continue
			}
			class, ok := codexClassify(file)
			if !ok || class.parent != parent.id {
				continue
			}
			started := class.started
			if started.IsZero() {
				started = info.ModTime()
			}
			tail := readTail(file, info.Size(), codexTailBytes)
			complete := bytes.LastIndex(tail, []byte(`"type":"task_complete"`))
			aborted := bytes.LastIndex(tail, []byte(`"type":"turn_aborted"`))
			list = append(list, Subagent{
				ID:        class.id,
				Provider:  AgentCodex,
				Type:      class.nickname,
				Started:   started,
				UpdatedAt: info.ModTime(),
				Path:      file,
				Name:      class.name,
				Depth:     class.depth,
				Ended:     complete >= 0 || aborted >= 0,
				Stopped:   aborted > complete,
			})
		}
	}
	return finishSubagents(list)
}
