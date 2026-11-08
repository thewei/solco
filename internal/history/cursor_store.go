/**
 * [INPUT]: 依赖系统 `sqlite3` 命令行（只读 URI，零新依赖）、Cursor 自己的 `store.db`（blobs(id,data) + meta(key,value)）
 * [OUTPUT]: 对外提供 CursorStoreSource（实现 LiveSource 与 LiveWatcher 的 Replaced 接缝）、SQLiteRunner、NewCursorStoreSource、NewCursorStoreSourceWith
 * [POS]: Cursor 没有 JSONL 转录：对话在内容寻址的 SQLite 里（meta['0'] 的 latestRootBlobId → 根 blob 的 protobuf 字段 1 = 按序的消息 blob 哈希 → 每个 blob 一条 OpenAI 风格 JSON 消息）。
 *        本文件把它合成为一条只追加的 JSONL 流，让 LiveWatcher/LiveDecoder 照常增量读；根的哈希前缀不再吻合（压缩/回滚）时重建并报告 Replaced，解码器随之整体重读。
 *        只读：绝不写 store.db；读之前看 stat 签名，库没变就不起进程
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package history

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SQLiteRunner runs one read-only SQL script against a database URI and
// returns its stdout. It is the seam that lets an embedded driver replace
// the sqlite3 command line later.
type SQLiteRunner func(dbURI, script string) ([]byte, error)

const (
	cursorQueryTimeout = 10 * time.Second
	cursorFetchBatch   = 100
)

// sqlite3Binary prefers the macOS system copy; PATH is the fallback.
func sqlite3Binary() (string, error) {
	if info, err := os.Stat("/usr/bin/sqlite3"); err == nil && !info.IsDir() {
		return "/usr/bin/sqlite3", nil
	}
	return exec.LookPath("sqlite3")
}

// commandSQLiteRunner is the production SQLiteRunner.
func commandSQLiteRunner(dbURI, script string) ([]byte, error) {
	binary, err := sqlite3Binary()
	if err != nil {
		return nil, fmt.Errorf("sqlite3 not found: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cursorQueryTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-batch", "-noheader", "-cmd", ".timeout 3000", dbURI, script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("sqlite3: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// CursorStoreSource presents one Cursor chat store as an append-only JSONL
// byte stream. Safe for concurrent use.
type CursorStoreSource struct {
	path string
	run  SQLiteRunner
	now  func() time.Time

	mu       sync.Mutex
	sig      string
	rootID   string
	hashes   []string
	buf      []byte
	model    string
	loaded   bool // false until the first full read; later lines get an observed time
	replaced bool
}

// NewCursorStoreSource tails the store at path through the sqlite3 command.
func NewCursorStoreSource(path string) *CursorStoreSource {
	return NewCursorStoreSourceWith(path, commandSQLiteRunner)
}

// NewCursorStoreSourceWith is NewCursorStoreSource with an explicit runner
// (tests, or a future embedded driver).
func NewCursorStoreSourceWith(path string, run SQLiteRunner) *CursorStoreSource {
	return &CursorStoreSource{path: path, run: run, now: time.Now}
}

// Size refreshes from the store and reports the synthesised stream length.
func (s *CursorStoreSource) Size() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refresh(); err != nil {
		var missing *os.PathError
		if errors.As(err, &missing) || len(s.buf) == 0 {
			return 0, err
		}
		// A transient read failure (the CLI holding a write lock) keeps the
		// stream as it was; the next poll tries again.
	}
	return int64(len(s.buf)), nil
}

// ReadAt reads from the synthesised stream.
func (s *CursorStoreSource) ReadAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if off < 0 || off > int64(len(s.buf)) {
		return 0, io.EOF
	}
	n := copy(p, s.buf[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// Replaced reports (once) that the stream was rebuilt rather than extended —
// the stored conversation was compacted or rewound — so the decoder must
// re-read from the start.
func (s *CursorStoreSource) Replaced() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	replaced := s.replaced
	s.replaced = false
	return replaced
}

func fileSig(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(info.Size(), 10) + "@" + strconv.FormatInt(info.ModTime().UnixNano(), 10), nil
}

// signature is the stat state of the database and its WAL: a write by the
// CLI moves one of them.
func (s *CursorStoreSource) signature() (string, error) {
	main, err := fileSig(s.path)
	if err != nil {
		return "", err
	}
	wal, _ := fileSig(s.path + "-wal")
	return main + "|" + wal, nil
}

// sqliteReadURIs lists the read-only URIs to try for a database: a live WAL
// needs mode=ro (the log is part of the picture); without one immutable=1
// avoids creating sidecar files. The other form is the fallback.
func sqliteReadURIs(path string) []string {
	build := func(query string) string {
		return (&url.URL{Scheme: "file", Path: path, RawQuery: query}).String()
	}
	ro, immutable := build("mode=ro"), build("immutable=1")
	if info, err := os.Stat(path + "-wal"); err == nil && info.Size() > 0 {
		return []string{ro, immutable}
	}
	return []string{immutable, ro}
}

// querySQLite runs one read-only script against the database at path.
func querySQLite(run SQLiteRunner, path, script string) ([]byte, error) {
	var firstErr error
	for _, uri := range sqliteReadURIs(path) {
		out, err := run(uri, script)
		if err == nil {
			return out, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

func (s *CursorStoreSource) query(script string) ([]byte, error) {
	return querySQLite(s.run, s.path, script)
}

func isHexID(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

type cursorStoreMeta struct {
	RootID string `json:"latestRootBlobId"`
	Model  string `json:"lastUsedModel"`
}

// refresh brings the stream up to the store's current root. Callers hold mu.
func (s *CursorStoreSource) refresh() error {
	sig, err := s.signature()
	if err != nil {
		return err
	}
	if s.loaded && sig == s.sig {
		return nil
	}

	meta, ids, err := s.loadRoot()
	if err != nil {
		return err
	}

	if meta.RootID == s.rootID && s.loaded {
		s.appendModel(meta.Model)
		s.sig = sig
		return nil
	}

	extends := len(ids) >= len(s.hashes)
	for i := 0; extends && i < len(s.hashes); i++ {
		if ids[i] != s.hashes[i] {
			extends = false
		}
	}
	base := len(s.hashes)
	if !extends {
		base = 0
	}
	fetched, err := s.fetchMessages(ids[base:])
	if err != nil {
		return err
	}

	if !extends {
		s.buf = nil
		s.hashes = nil
		s.model = ""
		s.replaced = s.loaded
	}
	stamp := int64(0)
	if s.loaded && extends {
		stamp = s.now().UnixMilli()
	}
	s.appendModel(meta.Model)
	for i, id := range ids[base:] {
		s.buf = append(s.buf, cursorStreamLine(id, stamp, fetched[i])...)
		s.hashes = append(s.hashes, id)
	}
	s.rootID = meta.RootID
	s.sig = sig
	s.loaded = true
	return nil
}

// loadRoot reads the chat's current root: meta's latestRootBlobId and the
// ordered message hashes in the root blob.
func (s *CursorStoreSource) loadRoot() (cursorStoreMeta, []string, error) {
	out, err := s.query("select value from meta where key='0';")
	if err != nil {
		return cursorStoreMeta{}, nil, err
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(out)))
	if err != nil {
		return cursorStoreMeta{}, nil, fmt.Errorf("cursor store meta: %w", err)
	}
	var meta cursorStoreMeta
	if err := json.Unmarshal(raw, &meta); err != nil || !isHexID(meta.RootID) {
		return cursorStoreMeta{}, nil, errors.New("cursor store meta: no root")
	}
	// 'r' marks that the row exists: a chat that never got a message points
	// at the empty blob (zero bytes), which is an empty conversation, not a
	// damaged one.
	rootHex, err := s.query("select 'r'||hex(data) from blobs where id='" + meta.RootID + "';")
	if err != nil {
		return cursorStoreMeta{}, nil, err
	}
	rootText := strings.TrimSpace(string(rootHex))
	if !strings.HasPrefix(rootText, "r") {
		return cursorStoreMeta{}, nil, errors.New("cursor store: root blob missing")
	}
	rootData, err := hex.DecodeString(rootText[1:])
	if err != nil {
		return cursorStoreMeta{}, nil, errors.New("cursor store: root blob unreadable")
	}
	hashes := protoBytesField(rootData, 1)
	ids := make([]string, 0, len(hashes))
	for _, h := range hashes {
		ids = append(ids, hex.EncodeToString(h))
	}
	return meta, ids, nil
}

// appendModel adds a model note line when the CLI's last-used model changed.
func (s *CursorStoreSource) appendModel(model string) {
	if model == "" || model == s.model {
		return
	}
	line, _ := json.Marshal(map[string]string{"model": model})
	s.buf = append(s.buf, line...)
	s.buf = append(s.buf, '\n')
	s.model = model
}

// cursorStreamLine wraps one stored message as a stream line. A blob that is
// not JSON keeps its slot (hash alignment) as an unknown line.
func cursorStreamLine(id string, at int64, blob []byte) []byte {
	var compact bytes.Buffer
	if json.Compact(&compact, blob) != nil {
		return []byte(`{"h":"` + id + `","bad":true}` + "\n")
	}
	var line bytes.Buffer
	line.WriteString(`{"h":"` + id + `","at":` + strconv.FormatInt(at, 10) + `,"m":`)
	line.Write(compact.Bytes())
	line.WriteString("}\n")
	return line.Bytes()
}

// fetchMessages reads the blobs of ids in order, in bounded batches.
func (s *CursorStoreSource) fetchMessages(ids []string) ([][]byte, error) {
	out := make([][]byte, 0, len(ids))
	for start := 0; start < len(ids); start += cursorFetchBatch {
		end := start + cursorFetchBatch
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		quoted := make([]string, len(batch))
		for i, id := range batch {
			if !isHexID(id) {
				return nil, errors.New("cursor store: malformed blob id")
			}
			quoted[i] = "'" + id + "'"
		}
		raw, err := s.query("select id||'|'||hex(data) from blobs where id in (" + strings.Join(quoted, ",") + ");")
		if err != nil {
			return nil, err
		}
		byID := make(map[string][]byte, len(batch))
		for _, line := range strings.Split(string(raw), "\n") {
			id, payload, ok := strings.Cut(strings.TrimSpace(line), "|")
			if !ok {
				continue
			}
			data, err := hex.DecodeString(payload)
			if err != nil {
				continue
			}
			byID[id] = data
		}
		for _, id := range batch {
			data, ok := byID[id]
			if !ok {
				return nil, errors.New("cursor store: message blob missing")
			}
			out = append(out, data)
		}
	}
	return out, nil
}

// protoBytesField lists every length-delimited value of one field number in
// a protobuf message (the root blob keeps its message hashes in field 1).
func protoBytesField(data []byte, field uint64) [][]byte {
	var out [][]byte
	i := 0
	readVarint := func() (uint64, bool) {
		var v uint64
		for shift := uint(0); shift < 64; shift += 7 {
			if i >= len(data) {
				return 0, false
			}
			b := data[i]
			i++
			v |= uint64(b&0x7f) << shift
			if b < 0x80 {
				return v, true
			}
		}
		return 0, false
	}
	for i < len(data) {
		key, ok := readVarint()
		if !ok {
			return out
		}
		switch key & 7 {
		case 0:
			if _, ok := readVarint(); !ok {
				return out
			}
		case 1:
			i += 8
		case 5:
			i += 4
		case 2:
			length, ok := readVarint()
			if !ok || length > uint64(len(data)-i) {
				return out
			}
			if key>>3 == field {
				out = append(out, data[i:i+int(length)])
			}
			i += int(length)
		default:
			return out
		}
	}
	return out
}
