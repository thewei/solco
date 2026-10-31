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
		ended := tailContains(file, info.Size(), ompTailBytes, `"customType":"session_exit"`)
		list = append(list, Subagent{
			ID:        name,
			Provider:  AgentOMP,
			Type:      "task",
			Started:   started,
			UpdatedAt: info.ModTime(),
			Path:      file,
			Name:      name,
			Ended:     ended,
			Stopped:   ended && ompLastMessageAborted(file, info.Size()),
		})
	}
	return finishSubagents(list)
}

// ompAbortTail covers an aborted assistant row plus the rows after it.
const ompAbortTail = 32 << 10

// ompLastMessageAborted reports whether the last conversation row (custom
// rows such as session_exit and tool_execution_* are skipped) is an assistant
// message omp itself marked `"stopReason":"aborted"` — what a child that was
// cut short ends on (checked on real sessions, 2026-10-31). A row longer than
// the tail is only partly seen and never matches.
func ompLastMessageAborted(path string, size int64) bool {
	tail := readTail(path, size, ompAbortTail)
	lines := bytes.Split(bytes.TrimRight(tail, "\r\n"), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if i == 0 && size > ompAbortTail {
			return false // the first line of a mid-file tail is cut
		}
		line := lines[i]
		if !bytes.HasPrefix(line, []byte(`{"type":"message"`)) {
			continue
		}
		return bytes.Contains(line, []byte(`"role":"assistant"`)) && bytes.Contains(line, []byte(`"stopReason":"aborted"`))
	}
	return false
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

// readTail returns the file's last n bytes.
func readTail(path string, size, n int64) []byte {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	if _, err := file.Seek(max(0, size-n), io.SeekStart); err != nil {
		return nil
	}
	tail, err := io.ReadAll(io.LimitReader(file, n))
	if err != nil {
		return nil
	}
	return tail
}

// tailContains reports whether a provider's own end marker sits in the
// file's last n bytes.
func tailContains(path string, size, n int64, marker string) bool {
	return bytes.Contains(readTail(path, size, n), []byte(marker))
}

// lastLineContains reports whether the file's final line holds marker. A
// last line longer than the tail is only partly seen and never matches.
func lastLineContains(path string, size, n int64, marker string) bool {
	tail := bytes.TrimRight(readTail(path, size, n), "\r\n")
	if i := bytes.LastIndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	}
	return bytes.Contains(tail, []byte(marker))
}
