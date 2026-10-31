package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

/**
 * [INPUT]: 一个 omp 会话 transcript 路径
 * [OUTPUT]: ompSubagents（`<会话目录>/<名>.jsonl` 里头部 parentSession 指回该会话的子代理）
 * [POS]: omp 的 `task` 工具把子代理写在父会话同名目录里（已在真机核对，2026-10-31）：`<名>.jsonl` 第二行 `session.parentSession` 是父会话路径（真实父子关系），结束时最后一行是 `session_exit`；`<名>.md` 是最终产出。只读头部与尾部，不解析对话
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// ompHeadBytes / ompTailBytes bound the two reads that classify one file:
// the session header sits within the first lines, the end marker in the last.
const (
	ompHeadLines = 4
	ompTailBytes = 4 << 10
)

type ompSessionHeader struct {
	Type          string `json:"type"`
	Timestamp     string `json:"timestamp"`
	ParentSession string `json:"parentSession"`
}

func ompSubagents(sessionPath string) []Subagent {
	if !strings.HasSuffix(sessionPath, ".jsonl") {
		return nil
	}
	dir := strings.TrimSuffix(sessionPath, ".jsonl")
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil || len(files) == 0 {
		return nil
	}
	parent := filepath.Clean(sessionPath)
	list := make([]Subagent, 0, len(files))
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil || info.IsDir() {
			continue
		}
		header, ok := ompHeader(file)
		if !ok || filepath.Clean(header.ParentSession) != parent {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(file), ".jsonl")
		started := info.ModTime()
		if at, err := time.Parse(time.RFC3339Nano, header.Timestamp); err == nil {
			started = at
		}
		list = append(list, Subagent{
			ID:        name,
			Provider:  AgentOMP,
			Type:      "task",
			Started:   started,
			UpdatedAt: info.ModTime(),
			Path:      file,
			Name:      name,
			Ended:     tailContains(file, info.Size(), ompTailBytes, `"customType":"session_exit"`),
		})
	}
	return finishSubagents(list)
}

// ompHeader reads the `session` row out of a file's first lines.
func ompHeader(path string) (ompSessionHeader, bool) {
	file, err := os.Open(path)
	if err != nil {
		return ompSessionHeader{}, false
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 64<<10)
	for i := 0; i < ompHeadLines; i++ {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && len(line) <= 64<<10 {
			var header ompSessionHeader
			if json.Unmarshal(bytes.TrimSpace(line), &header) == nil && header.Type == "session" {
				return header, true
			}
		}
		if err != nil {
			break
		}
	}
	return ompSessionHeader{}, false
}

// tailContains reports whether a provider's own end marker sits in the
// file's last n bytes.
func tailContains(path string, size, n int64, marker string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	if _, err := file.Seek(max(0, size-n), io.SeekStart); err != nil {
		return false
	}
	tail, err := io.ReadAll(io.LimitReader(file, n))
	if err != nil {
		return false
	}
	return bytes.Contains(tail, []byte(marker))
}
