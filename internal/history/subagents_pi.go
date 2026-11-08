package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

/**
 * [INPUT]: 一个 pi 会话 transcript 路径
 * [OUTPUT]: piSubagents（`<会话目录>/<uuid>/run-N/session.jsonl` 里头部 parentSession 指回该会话的 pi-subagents 子代理）
 * [POS]: pi 的 pi-subagents 扩展把子代理写在父会话同名目录的 `<uuid>/run-N/session.jsonl`（已在真机核对，2026-11-02）：头部 `session.parentSession` 是父会话路径，`session_info.name` 形如 `subagent-<agent>-<runId>-<n>`；结束时最后一条对话行是非 toolUse 的 assistant（stopReason stop/aborted/error）。与父调用的链接不靠 id（异步 run 的 id 与目录名无关），而靠任务正文：子代理首条 user 消息是 `Task: <父调用的 task>`（chatsession.Linked）。只读头部与尾部
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// piNameRe pulls the agent kind out of `subagent-<agent>-<uuid>-<n>`.
var piNameRe = regexp.MustCompile(`"name":"subagent-(.+?)-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})-\d+"`)

// piChildNotifyRe reads a `subagent-incremental-child-notify` message: the
// workflow run a child belongs to and the child's own run id.
var piChildNotifyRe = regexp.MustCompile(`Workflow run: (\S+)\s+Child run: (\S+)`)

// piTailBytes covers the last assistant row plus the rows after it.
const piTailBytes = 32 << 10

func piSubagents(sessionPath string) []Subagent {
	if !strings.HasSuffix(sessionPath, ".jsonl") {
		return nil
	}
	dir := strings.TrimSuffix(sessionPath, ".jsonl")
	files, err := filepath.Glob(filepath.Join(dir, "*", "run-*", "session.jsonl"))
	if err != nil || len(files) == 0 {
		return nil
	}
	parent := filepath.Clean(sessionPath)
	links := piRunLinks(parent)
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
		rel, _ := filepath.Rel(dir, filepath.Dir(file))
		started := info.ModTime()
		if at, err := time.Parse(time.RFC3339Nano, header.Timestamp); err == nil {
			started = at
		}
		kind, childRun := "subagent", ""
		if m := piNameRe.FindSubmatch(readHead(file, 8<<10)); m != nil {
			kind, childRun = string(m[1]), string(m[2])
		}
		callRun := links.byChild[childRun]
		if callRun == "" {
			callRun = links.byPath[filepath.Clean(file)]
		}
		reason := piLastStopReason(file, info.Size())
		ended := reason != "" && reason != "toolUse"
		list = append(list, Subagent{
			ID:        filepath.ToSlash(rel),
			Provider:  AgentPi,
			Type:      kind,
			Started:   started,
			UpdatedAt: info.ModTime(),
			Path:      file,
			CallRun:   callRun,
			Ended:     ended,
			Stopped:   ended && reason == "aborted",
		})
	}
	return finishSubagents(list)
}

// readHead returns the file's first n bytes.
func readHead(path string, n int64) []byte {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	buf := make([]byte, n)
	read, _ := file.Read(buf)
	return buf[:read]
}

// piLastStopReason is the stopReason of the file's last conversation row when
// that row is an assistant message ("" otherwise, or when the row is longer
// than the tail and only partly seen).
func piLastStopReason(path string, size int64) string {
	tail := readTail(path, size, piTailBytes)
	lines := bytes.Split(bytes.TrimRight(tail, "\r\n"), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if i == 0 && size > piTailBytes {
			return ""
		}
		line := lines[i]
		if !bytes.HasPrefix(line, []byte(`{"type":"message"`)) {
			continue
		}
		if !bytes.Contains(line, []byte(`"role":"assistant"`)) {
			return ""
		}
		const key = `"stopReason":"`
		at := bytes.LastIndex(line, []byte(key))
		if at < 0 {
			return ""
		}
		rest := line[at+len(key):]
		end := bytes.IndexByte(rest, '"')
		if end < 0 {
			return ""
		}
		return string(rest[:end])
	}
	return ""
}

// piLinks is what the parent's own notify messages say about which call run
// started which child.
type piLinks struct {
	byChild map[string]string // child run id -> the workflow run that spawned it
	byPath  map[string]string // child session file -> the async run that started it
}

var piLinkCache = struct {
	sync.Mutex
	m map[string]piLinksEntry
}{m: map[string]piLinksEntry{}}

type piLinksEntry struct {
	mtime time.Time
	size  int64
	links piLinks
}

// piRunLinks reads the parent transcript's `subagent-*` custom messages (the
// extension writes them as each run completes) once per file state.
func piRunLinks(parent string) piLinks {
	info, err := os.Stat(parent)
	if err != nil {
		return piLinks{}
	}
	piLinkCache.Lock()
	cached, ok := piLinkCache.m[parent]
	piLinkCache.Unlock()
	if ok && cached.mtime.Equal(info.ModTime()) && cached.size == info.Size() {
		return cached.links
	}
	links := piLinks{byChild: map[string]string{}, byPath: map[string]string{}}
	if file, err := os.Open(parent); err == nil {
		defer file.Close()
		reader := bufio.NewReaderSize(file, 256<<10)
		for {
			line, err := reader.ReadBytes('\n')
			if bytes.Contains(line, []byte(`"customType":"subagent-`)) {
				piReadLink(line, &links)
			}
			if err != nil {
				break
			}
		}
	}
	piLinkCache.Lock()
	if len(piLinkCache.m) >= 64 {
		piLinkCache.m = map[string]piLinksEntry{}
	}
	piLinkCache.m[parent] = piLinksEntry{info.ModTime(), info.Size(), links}
	piLinkCache.Unlock()
	return links
}

func piReadLink(line []byte, links *piLinks) {
	var row struct {
		Type       string `json:"type"`
		CustomType string `json:"customType"`
		Content    string `json:"content"`
		Details    struct {
			Runs []struct {
				RunID        string `json:"runId"`
				SessionValue string `json:"sessionValue"`
			} `json:"runs"`
		} `json:"details"`
	}
	if json.Unmarshal(bytes.TrimSpace(line), &row) != nil || row.Type != "custom_message" {
		return
	}
	switch row.CustomType {
	case "subagent-incremental-child-notify":
		if m := piChildNotifyRe.FindStringSubmatch(row.Content); m != nil {
			links.byChild[m[2]] = m[1]
		}
	case "subagent-notify":
		for _, run := range row.Details.Runs {
			if run.RunID != "" && run.SessionValue != "" {
				links.byPath[filepath.Clean(run.SessionValue)] = run.RunID
			}
		}
	}
}
