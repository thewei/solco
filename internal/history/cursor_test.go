package history

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

/**
 * [INPUT]: 依赖 cursor.go / cursor_store.go，内存假 SQLiteRunner 与（可用时）真实 sqlite3 命令行
 * [OUTPUT]: TestParseCursorTranscript, TestCursorStoreAppendsAndReplaces, TestCursorStoreRealSQLite, TestCursorLiveMatchesBatch, TestCursorStoreFile
 * [POS]: Cursor store.db 形状（真机 2026-11-01：meta['0'].latestRootBlobId、根 blob protobuf 字段 1、OpenAI 风格消息、isSummary、highLevelToolCallResult.isError）的回归锁
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// fakeCursorStore is an in-memory store.db behind the SQLiteRunner seam.
type fakeCursorStore struct {
	mu     sync.Mutex
	blobs  map[string][]byte
	root   string
	model  string
	failed error
}

func newFakeCursorStore() *fakeCursorStore { return &fakeCursorStore{blobs: map[string][]byte{}} }

func (f *fakeCursorStore) put(message any) string {
	// Real stores open with {"role":…}; Go sorts map keys, so splice it first.
	fields, _ := message.(map[string]any)
	role, _ := json.Marshal(fields["role"])
	delete(fields, "role")
	rest, _ := json.Marshal(fields)
	data := []byte(`{"role":` + string(role))
	if len(rest) > 2 {
		data = append(data, ',')
		data = append(data, rest[1:]...)
	} else {
		data = append(data, '}')
	}
	sum := sha256.Sum256(data)
	id := hex.EncodeToString(sum[:])
	f.blobs[id] = data
	return id
}

// setRoot stores a root blob listing ids in field 1 (plus unrelated fields,
// as real roots carry) and points meta at it.
func (f *fakeCursorStore) setRoot(ids ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var root []byte
	for _, id := range ids {
		raw, _ := hex.DecodeString(id)
		root = append(root, 0x0a, byte(len(raw)))
		root = append(root, raw...)
	}
	root = append(root, 0x50, 0x01)                      // field 10 varint
	root = append(root, 0x8a, 0x01, 0x03, 'c', 'l', 'i') // field 17 bytes
	sum := sha256.Sum256(root)
	f.root = hex.EncodeToString(sum[:])
	f.blobs[f.root] = root
}

func (f *fakeCursorStore) run(_ string, script string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failed != nil {
		return nil, f.failed
	}
	switch {
	case strings.HasPrefix(script, "select value from meta"):
		meta, _ := json.Marshal(map[string]string{"latestRootBlobId": f.root, "lastUsedModel": f.model})
		return []byte(hex.EncodeToString(meta) + "\n"), nil
	case strings.HasPrefix(script, "select 'r'||hex(data) from blobs where id='"):
		id := regexp.MustCompile(`id='([0-9a-f]+)'`).FindStringSubmatch(script)[1]
		data, ok := f.blobs[id]
		if !ok {
			return nil, nil
		}
		return []byte("r" + hex.EncodeToString(data) + "\n"), nil
	case strings.HasPrefix(script, "select id||'|'||hex(data)"):
		var out strings.Builder
		for _, m := range regexp.MustCompile(`'([0-9a-f]+)'`).FindAllStringSubmatch(script, -1) {
			if data, ok := f.blobs[m[1]]; ok {
				out.WriteString(m[1] + "|" + hex.EncodeToString(data) + "\n")
			}
		}
		return []byte(out.String()), nil
	}
	return nil, errors.New("unexpected script: " + script)
}

func cursorMsg(role string, content any, extra map[string]any) map[string]any {
	m := map[string]any{"role": role, "content": content}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// cursorFixtureMessages is one short chat in the shapes a real store holds.
func cursorFixtureMessages(f *fakeCursorStore) []string {
	toolCall := map[string]any{"type": "tool-call", "toolCallId": "tool_1", "toolName": "Shell", "args": map[string]any{"command": "go test ./...", "description": "run tests"}}
	failedTool := map[string]any{"type": "tool-call", "toolCallId": "tool_2", "toolName": "StrReplace", "args": map[string]any{"path": "/work/a.go", "old_string": "x", "new_string": "y"}}
	return []string{
		f.put(cursorMsg("system", "You are an AI coding assistant.", nil)),
		f.put(cursorMsg("user", "<user_info>\nOS Version: darwin\n</user_info>", nil)),
		f.put(cursorMsg("user", []any{map[string]any{"type": "text", "text": "<timestamp>Sunday</timestamp>\n<user_query>\nRun the tests\n</user_query>"}}, nil)),
		f.put(cursorMsg("assistant", []any{
			map[string]any{"type": "redacted-reasoning", "data": "AAAA"},
			map[string]any{"type": "text", "text": "Running them."},
			toolCall,
		}, map[string]any{"id": "1"})),
		f.put(cursorMsg("tool", []any{map[string]any{"type": "tool-result", "toolCallId": "tool_1", "toolName": "Shell", "result": "ok  \tpkg\t0.1s"}},
			map[string]any{"id": "tool_1", "providerOptions": map[string]any{"cursor": map[string]any{"highLevelToolCallResult": map[string]any{"isError": false}}}})),
		f.put(cursorMsg("assistant", []any{failedTool}, map[string]any{"id": "2"})),
		f.put(cursorMsg("tool", []any{map[string]any{"type": "tool-result", "toolCallId": "tool_2", "toolName": "StrReplace", "result": "no match"}},
			map[string]any{"id": "tool_2", "providerOptions": map[string]any{"cursor": map[string]any{"highLevelToolCallResult": map[string]any{"isError": true}}}})),
		f.put(cursorMsg("assistant", []any{map[string]any{"type": "text", "text": "Tests pass; the edit failed."}}, map[string]any{"id": "3"})),
	}
}

func readAll(t *testing.T, source LiveSource) string {
	t.Helper()
	size, err := source.Size()
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, size)
	if size > 0 {
		if _, err := source.ReadAt(buf, 0); err != nil {
			t.Fatal(err)
		}
	}
	return string(buf)
}

func parseCursorStream(t *testing.T, stream string) *cursorParseState {
	t.Helper()
	state := newCursorParseState()
	for _, line := range strings.SplitAfter(stream, "\n") {
		state.feedCursorLine(line)
	}
	return state
}

func TestParseCursorTranscript(t *testing.T) {
	store := newFakeCursorStore()
	store.model = "composer-2.5"
	store.setRoot(cursorFixtureMessages(store)...)
	// stat is the change signature, so the store file has to exist.
	path := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := NewCursorStoreSourceWith(path, store.run)
	state := parseCursorStream(t, readAll(t, source))
	msgs := state.messages
	if len(msgs) != 3 {
		t.Fatalf("want injected context, user, one merged assistant turn; got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != RoleUser || msgs[0].Kind != MessageMeta {
		t.Fatalf("<user_info> is injected context: %+v", msgs[0])
	}
	if msgs[1].Kind != MessageText || msgs[1].Text != "Run the tests" {
		t.Fatalf("user text is the <user_query> body: %+v", msgs[1])
	}
	assistant := msgs[2]
	if assistant.Text != "Running them.\n\nTests pass; the edit failed." || len(assistant.ToolCalls) != 2 {
		t.Fatalf("assistant messages around tool results merge: %+v", assistant)
	}
	shell := assistant.ToolCalls[0]
	if shell.Name != "Shell" || shell.InputPreview != "go test ./..." || shell.Output == nil || *shell.Output != "ok  \tpkg\t0.1s" || shell.IsError {
		t.Fatalf("shell call: %+v", shell)
	}
	if edit := assistant.ToolCalls[1]; !edit.IsError || edit.Output == nil || *edit.Output != "no match" {
		t.Fatalf("highLevelToolCallResult.isError must fail the call: %+v", edit)
	}
	if assistant.Model == nil || *assistant.Model != "composer-2.5" {
		t.Fatalf("model from meta.lastUsedModel: %v", assistant.Model)
	}
	if state.unknownLines != 0 {
		t.Fatalf("unknown lines: %d", state.unknownLines)
	}
}

func TestCursorSummaryIsCompaction(t *testing.T) {
	store := newFakeCursorStore()
	store.setRoot(
		store.put(cursorMsg("user", "Your conversation was summarized…", map[string]any{"providerOptions": map[string]any{"cursor": map[string]any{"isSummary": true}}})),
	)
	path := filepath.Join(t.TempDir(), "store.db")
	_ = os.WriteFile(path, []byte("x"), 0o600)
	state := parseCursorStream(t, readAll(t, NewCursorStoreSourceWith(path, store.run)))
	if len(state.messages) != 1 || state.messages[0].Kind != MessageCompactSummary || strings.Contains(state.messages[0].Text, "summarized") {
		t.Fatalf("summary row: %+v", state.messages)
	}
}

func TestCursorStoreAppendsAndReplaces(t *testing.T) {
	store := newFakeCursorStore()
	ids := cursorFixtureMessages(store)
	store.setRoot(ids[:4]...)
	path := filepath.Join(t.TempDir(), "store.db")
	_ = os.WriteFile(path, []byte("x"), 0o600)
	source := NewCursorStoreSourceWith(path, store.run)
	clock := time.UnixMilli(1_800_000_000_000)
	source.now = func() time.Time { return clock }

	decoder, err := NewLiveDecoder(AgentCursor)
	if err != nil {
		t.Fatal(err)
	}
	watcher := NewLiveWatcher(source, decoder)
	first, err := watcher.Poll()
	if err != nil || !first.Hydrated || len(first.Messages) != 3 {
		t.Fatalf("hydrate: %+v, %v", first, err)
	}
	if first.Messages[2].Timestamp != nil {
		t.Fatalf("history already stored when first seen has no time: %+v", first.Messages[2].Timestamp)
	}
	// Unchanged store: no work, no update.
	if again, _ := watcher.Poll(); again.HasUpdates() {
		t.Fatalf("idempotent poll reported updates: %+v", again)
	}

	// The CLI appends more messages: only they are read, stamped as observed.
	_ = os.WriteFile(path, []byte("xx"), 0o600)
	next := store.put(cursorMsg("user", "<user_query>\nnext\n</user_query>", nil))
	store.setRoot(append(append([]string{}, ids...), next)...)
	grown, err := watcher.Poll()
	if err != nil || grown.Reset || grown.Hydrated {
		t.Fatalf("extend must not reset: %+v, %v", grown, err)
	}
	if len(grown.Changed) == 0 && len(grown.Appended) == 0 {
		t.Fatalf("extend reported nothing: %+v", grown)
	}
	live := decoder.Projection()
	if len(live) != 4 || live[2].ToolCalls[0].Output == nil || live[3].Text != "next" {
		t.Fatalf("projection after extend: %+v", live)
	}
	if live[3].Timestamp == nil || *live[3].Timestamp != clock.UnixMilli() {
		t.Fatalf("a message first seen live carries the observed time: %+v", live[3].Timestamp)
	}

	// A compaction rewrites the list: the prefix no longer matches, so the
	// decoder is told to start over.
	summary := store.put(cursorMsg("user", "summary", map[string]any{"providerOptions": map[string]any{"cursor": map[string]any{"isSummary": true}}}))
	_ = os.WriteFile(path, []byte("xxx"), 0o600)
	store.setRoot(ids[0], summary, ids[7])
	rebuilt, err := watcher.Poll()
	if err != nil || !rebuilt.Reset || len(rebuilt.Messages) != 2 {
		t.Fatalf("compaction must reset and re-hydrate: %+v, %v", rebuilt, err)
	}
	if rebuilt.Messages[0].Kind != MessageCompactSummary {
		t.Fatalf("rebuilt rows: %+v", rebuilt.Messages)
	}
}

func TestCursorStoreTransientFailureKeepsStream(t *testing.T) {
	store := newFakeCursorStore()
	store.setRoot(cursorFixtureMessages(store)...)
	path := filepath.Join(t.TempDir(), "store.db")
	_ = os.WriteFile(path, []byte("x"), 0o600)
	source := NewCursorStoreSourceWith(path, store.run)
	before := readAll(t, source)
	_ = os.WriteFile(path, []byte("xx"), 0o600)
	store.failed = errors.New("database is locked")
	size, err := source.Size()
	if err != nil || size != int64(len(before)) {
		t.Fatalf("a locked store must keep the stream: %d (%d), %v", size, len(before), err)
	}
	store.failed = nil
	if _, err := source.Size(); err != nil {
		t.Fatal(err)
	}
}

// seedCursorSQLite writes the fake store into a real SQLite file (WAL mode,
// as the CLI keeps it) through the sqlite3 command; the test skips without it.
func seedCursorSQLite(t *testing.T, store *fakeCursorStore, path string) {
	t.Helper()
	binary, err := sqlite3Binary()
	if err != nil {
		t.Skip("no sqlite3 on this machine")
	}
	meta, _ := json.Marshal(map[string]string{"latestRootBlobId": store.root, "lastUsedModel": store.model})
	var script strings.Builder
	script.WriteString("pragma journal_mode=wal; create table blobs (id text primary key, data blob); create table meta (key text primary key, value text);")
	for id, data := range store.blobs {
		script.WriteString("insert into blobs values('" + id + "', X'" + hex.EncodeToString(data) + "');")
	}
	script.WriteString("insert into meta values('0','" + hex.EncodeToString(meta) + "');")
	if out, err := exec.Command(binary, path, script.String()).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
}

func TestCursorStoreRealSQLite(t *testing.T) {
	store := newFakeCursorStore()
	store.model = "composer-2.5"
	store.setRoot(cursorFixtureMessages(store)...)
	path := filepath.Join(t.TempDir(), "store.db")
	seedCursorSQLite(t, store, path)
	state := parseCursorStream(t, readAll(t, NewCursorStoreSource(path)))
	if len(state.messages) != 3 || state.messages[1].Text != "Run the tests" || state.unknownLines != 0 {
		t.Fatalf("real sqlite read: %+v (unknown %d)", state.messages, state.unknownLines)
	}
}

// The list-row read must report what the full parse would: same title, same
// message count, same model — without reading the assistant/tool bodies.
func TestCursorSummaryMatchesFullParse(t *testing.T) {
	store := newFakeCursorStore()
	store.model = "composer-2.5"
	ids := cursorFixtureMessages(store)
	summary := store.put(cursorMsg("user", "summary", map[string]any{"providerOptions": map[string]any{"cursor": map[string]any{"isSummary": true}}}))
	next := store.put(cursorMsg("user", "<user_query>\nSecond question\n</user_query>", nil))
	reply := store.put(cursorMsg("assistant", []any{map[string]any{"type": "text", "text": "Sure."}}, map[string]any{"id": "4"}))
	store.setRoot(append(ids, summary, next, reply)...)

	dir := filepath.Join(t.TempDir(), "hash", "chat-9")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "store.db")
	seedCursorSQLite(t, store, path)
	_ = os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"hasConversation":true,"cwd":"/work/demo","createdAtMs":1000,"updatedAtMs":2000}`), 0o600)

	ref := SessionFileRef{Agent: AgentCursor, NativeID: "chat-9", FilePath: path}
	full, err := ParseCursorTranscript(ref)
	if err != nil {
		t.Fatal(err)
	}
	light, err := summarizeCursorTranscript(ref)
	if err != nil {
		t.Fatal(err)
	}
	if light.Meta.Title != full.Meta.Title || light.Meta.MessageCount != full.Meta.MessageCount || light.Meta.ProjectPath != "/work/demo" {
		t.Fatalf("light %+v\nfull  %+v", light.Meta, full.Meta)
	}
	if light.Meta.Model == nil || *light.Meta.Model != "composer-2.5" || light.Meta.Title != "Run the tests" {
		t.Fatalf("light meta: %+v", light.Meta)
	}
	if descriptionFromMessages(light.Mainline) != descriptionFromMessages(full.Mainline) {
		t.Fatalf("description differs: %q vs %q", descriptionFromMessages(light.Mainline), descriptionFromMessages(full.Mainline))
	}
}

func TestCursorListRefsSkipsEmptyChatsAndStampsWAL(t *testing.T) {
	root := t.TempDir()
	mk := func(chat, meta string, wal bool) string {
		dir := filepath.Join(root, "hash", chat)
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "store.db"), []byte("12345"), 0o600)
		if meta != "" {
			_ = os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o600)
		}
		if wal {
			_ = os.WriteFile(filepath.Join(dir, "store.db-wal"), []byte("123"), 0o600)
		}
		return dir
	}
	mk("used", `{"hasConversation":true}`, true)
	mk("blank", `{"hasConversation":false}`, false)
	mk("nometa", "", false)
	refs, err := cursorListRefs(SourceRoot{Agent: AgentCursor, Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, ref := range refs {
		got[ref.NativeID] = ref.SizeBytes
	}
	if len(got) != 2 || got["used"] != 8 || got["nometa"] != 5 {
		t.Fatalf("refs: %v (a WAL counts toward the stamp, a chat with hasConversation=false is skipped)", got)
	}
	if refs, err := cursorListRefs(SourceRoot{Agent: AgentCursor, Directory: filepath.Join(root, "absent")}); err != nil || len(refs) != 0 {
		t.Fatalf("a missing root is an empty source: %v %v", refs, err)
	}
}

func TestCursorLiveMatchesBatch(t *testing.T) {
	store := newFakeCursorStore()
	store.model = "composer-2.5"
	store.setRoot(cursorFixtureMessages(store)...)
	dir := filepath.Join(t.TempDir(), "hash", "chat-1")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "store.db")
	_ = os.WriteFile(path, []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"title":"Run the tests","cwd":"/work/demo","createdAtMs":1000,"updatedAtMs":2000}`), 0o600)

	stream := readAll(t, NewCursorStoreSourceWith(path, store.run))
	decoder, _ := NewLiveDecoder(AgentCursor)
	for _, line := range strings.SplitAfter(stream, "\n") {
		decoder.Append([]byte(line))
	}
	batch := parseCursorStream(t, stream)
	live := decoder.Projection()
	if len(live) != len(batch.messages) {
		t.Fatalf("live %d rows, batch %d", len(live), len(batch.messages))
	}
	for i := range live {
		if !transcriptMessageEqual(live[i], batch.messages[i]) {
			t.Fatalf("row %d differs", i)
		}
	}

	meta := &SessionMeta{}
	applyCursorChatMeta(meta, dir)
	if meta.Title != "Run the tests" || meta.ProjectPath != "/work/demo" || meta.CreatedAt != 1000 || meta.UpdatedAt != 2000 {
		t.Fatalf("chat meta: %+v", meta)
	}
}

func TestCursorStoreFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "ea95c10165a0340343f63eb1b8422cfa", "chat-1")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "store.db"), []byte("x"), 0o600)
	if got, ok := cursorStoreFile(root, "chat-1"); !ok || got != filepath.Join(dir, "store.db") {
		t.Fatalf("lookup by chat id: %q %v", got, ok)
	}
	for _, bad := range []string{"", "..", "../x", "a/b", "missing"} {
		if _, ok := cursorStoreFile(root, bad); ok {
			t.Fatalf("%q must not resolve", bad)
		}
	}
}

func TestResolveLiveSourceCursor(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".cursor", "chats", "hash", "chat-1")
	_ = os.MkdirAll(dir, 0o755)
	store := filepath.Join(dir, "store.db")
	_ = os.WriteFile(store, []byte("x"), 0o600)
	got, err := ResolveLiveSource(home, AgentCursor, "id", "herdr:cursor", "chat-1")
	if err != nil || got != store {
		t.Fatalf("by id: %q, %v", got, err)
	}
	if _, err := ResolveLiveSource(home, AgentCursor, "id", "herdr:cursor", "unknown"); !errors.Is(err, ErrLiveSourceUnresolved) {
		t.Fatalf("unknown chat must fail closed: %v", err)
	}
	// A transcript-style path (agent-transcripts/<id>/<id>.jsonl) maps to the store.
	got, err = ResolveLiveSource(home, AgentCursor, "path", "herdr:cursor", "/x/agent-transcripts/chat-1/chat-1.jsonl")
	if err != nil || got != store {
		t.Fatalf("by transcript path: %q, %v", got, err)
	}
}

// A chat that never got a message points at the empty blob: an empty
// conversation, not an error (one such store must not fail a scan round).
func TestCursorEmptyConversationIsEmptyNotBroken(t *testing.T) {
	store := newFakeCursorStore()
	store.blobs["e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"] = nil
	store.root = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	path := filepath.Join(t.TempDir(), "store.db")
	_ = os.WriteFile(path, []byte("x"), 0o600)
	source := NewCursorStoreSourceWith(path, store.run)
	if size, err := source.Size(); err != nil || size != 0 {
		t.Fatalf("empty chat: size %d, err %v", size, err)
	}
	store.root = "00"
	_ = os.WriteFile(path, []byte("xx"), 0o600)
	if _, err := NewCursorStoreSourceWith(path, store.run).Size(); err == nil {
		t.Fatal("a root that points at no blob is damaged")
	}
}

func TestScanParseSurvivesAnUnreadableCursorStore(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "store.db"), []byte("not a database"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"hasConversation":true,"title":"Homepage Water Fix","cwd":"/work/demo"}`), 0o600)
	parsed, err := scanParseByAgent(SessionFileRef{Agent: AgentCursor, NativeID: "c", FilePath: filepath.Join(dir, "store.db")})
	if err != nil || parsed.Meta.Title != "Homepage Water Fix" || parsed.Meta.ProjectPath != "/work/demo" {
		t.Fatalf("a bad store degrades to its meta.json row: %+v, %v", parsed.Meta, err)
	}
}

func TestCursorUserRowsCarryTheRealWallClock(t *testing.T) {
	want := time.Date(2026, 11, 1, 13, 42, 0, 0, time.FixedZone("", 8*3600)).UnixMilli()
	for _, tc := range []struct {
		text string
		want int64
	}{
		{"<timestamp>Sunday, Nov 1, 2026, 1:42 PM (UTC+8)</timestamp>\n<user_query>hi</user_query>", want},
		{"<timestamp>Sunday, Nov 1, 2026, 1:42 PM (UTC+8:00)</timestamp>", want},
		{"<timestamp>Sunday, Nov 1, 2026, 5:42 AM (UTC)</timestamp>", want},
		{"<timestamp>Saturday, Oct 31, 2026, 10:42 PM (UTC-7)</timestamp>", want},
		{"<timestamp>someday (UTC+8)</timestamp>", 0},
		{"<user_query>no header</user_query>", 0},
	} {
		if got := cursorMessageTime(tc.text); got != tc.want {
			t.Errorf("cursorMessageTime(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}
	line := `{"h":"a","at":0,"m":{"role":"user","content":"<timestamp>Sunday, Nov 1, 2026, 1:42 PM (UTC+8)</timestamp>\n<user_query>hi</user_query>"}}` + "\n"
	state := parseCursorStream(t, line)
	if len(state.messages) != 1 || state.messages[0].Timestamp == nil || *state.messages[0].Timestamp != want || state.createdAt != want {
		t.Fatalf("user row time: %+v createdAt=%d", state.messages, state.createdAt)
	}
}
