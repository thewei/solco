package remotehost

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thewei/solco/internal/feedback"
)

/**
 * [INPUT]: server_test.go 的 harness、feedback.go / feedback_watch.go 与 Options.Feedback 缝
 * [OUTPUT]: /api/v3/feedback 的行为回归与黄金文件：无缝 404、需配对、离线是 200 online:false、详情不带 Mac 路径、答/结束的错误码映射、能力位、broker 事件 → `feedback` 帧只含 id 与计数
 * [POS]: 缝由 fakeFeedback 注入，绝不连真实 broker（真 broker 的端到端在 internal/feedback 与 nativeui 测试里）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

type fakeFeedback struct {
	mu        sync.Mutex
	offline   bool
	threads   []feedback.ThreadSummary
	detail    feedback.Thread
	err       error
	answered  string
	answers   map[string]feedback.Answer
	refs      []feedback.ContextRef
	picked    string
	thumbsOf  string
	ended     string
	events    chan feedback.Event
	streamErr chan struct{}
	deleted   []string
	purgeDays int
	purgeDry  bool
	searched  string
	retention int
}

func (f *fakeFeedback) Threads(context.Context, bool, string) ([]feedback.ThreadSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.offline {
		return nil, feedback.ErrOffline
	}
	return f.threads, nil
}

func (f *fakeFeedback) Thread(_ context.Context, id string) (feedback.Thread, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.detail.ID {
		return feedback.Thread{}, feedback.ErrNotFound
	}
	return f.detail, nil
}

func (f *fakeFeedback) Answer(_ context.Context, id string, a map[string]feedback.Answer, _ []feedback.Attachment, refs []feedback.ContextRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answered, f.answers, f.refs = id, a, refs
	return f.err
}

func (f *fakeFeedback) End(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = id
	return f.err
}

// The management side: fakeFeedback is a feedback.Manager.
func (f *fakeFeedback) DeleteThreads(_ context.Context, ids []string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = ids
	return len(ids), f.err
}

func (f *fakeFeedback) Purge(_ context.Context, days int, dry bool) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.purgeDays, f.purgeDry = days, dry
	return 3, f.err
}

func (f *fakeFeedback) Search(_ context.Context, q string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searched = q
	return []string{"t_1"}, nil
}

func (f *fakeFeedback) Pick(_ context.Context, thread, kind, q string) ([]feedback.PickItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.picked = thread + "|" + kind + "|" + q
	return []feedback.PickItem{{Kind: "skill", Name: "think", Detail: "Plan before coding"}}, nil
}

func (f *fakeFeedback) Thumbnails(_ context.Context, askID string) ([]feedback.Attachment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.thumbsOf = askID
	return []feedback.Attachment{{MimeType: "image/jpeg", DataBase64: "AQID", Name: "shot.png"}, {Name: "huge.tiff"}}, f.err
}

func (f *fakeFeedback) Retention(context.Context) (int, error) { return f.retention, nil }

func (f *fakeFeedback) SetRetention(_ context.Context, days int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retention = days
	return f.err
}

func (f *fakeFeedback) Stream(ctx context.Context, fn func(feedback.Event)) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-f.events:
			fn(e)
		}
	}
}

func sampleFeedback() *fakeFeedback {
	ask := &feedback.Ask{
		ID: "a_1", ThreadID: "t_1", CreatedAt: 1786042800000, TimeoutMs: 1800000, State: feedback.StatePending, LastHeartbeat: 1786042800000,
		Description: "Pick the migration path",
		Suggest:     []feedback.ContextRef{{Kind: "file", Name: "docs/api.md"}, {Kind: "skill", Name: "think"}},
		Questions: []feedback.Question{
			{ID: "q1", Text: "Which database should the service use?", Type: "single", Options: []string{"SQLite", "Postgres"}, AllowCustom: true, Required: true},
			{ID: "q2", Text: "Anything else?", Type: "text", Placeholder: "optional"},
		},
	}
	done := &feedback.Ask{
		ID: "a_0", ThreadID: "t_1", CreatedAt: 1786039200000, ResolvedAt: 1786039500000, TimeoutMs: 1800000, State: feedback.StateAnswered,
		Questions:     []feedback.Question{{ID: "q1", Text: "Keep the old API?", Type: "single", Options: []string{"Yes", "No"}, Required: true}},
		Answers:       map[string]feedback.Answer{"q1": {Selected: []string{"Yes"}}},
		Context:       []feedback.ContextRef{{Kind: "file", Name: "docs/api.md", Path: "/Users/me/solco/docs/api.md"}, {Kind: "skill", Name: "think", Path: "/Users/me/.agents/skills/think/SKILL.md"}},
		LastHeartbeat: 1786039500000,
	}
	return &fakeFeedback{
		events:  make(chan feedback.Event, 4),
		threads: []feedback.ThreadSummary{{ID: "t_1", Project: "solco", Branch: "main", Client: "Claude Code", Title: "solco · Claude Code", LastAt: 1786042800000, Waiting: 1, AskCount: 2, Preview: "Which database should the service use?", LastState: "pending"}},
		detail:  feedback.Thread{ID: "t_1", ProjectKey: "/Users/me/solco", ProjectRoot: "/Users/me/solco", Project: "solco", Branch: "main", Client: "Claude Code", CallerInfo: "claude-code 2.1", Title: "solco · Claude Code", Task: "Move the service to a managed database without downtime.", CreatedAt: 1786039200000, LastAt: 1786042800000, Waiting: 1, Asks: []*feedback.Ask{done, ask}},
	}
}

func feedbackHarness(t *testing.T) (*harness, *fakeFeedback) {
	t.Helper()
	h := newHarness(t)
	f := sampleFeedback()
	h.srv.opts.Feedback = f
	return h, f
}

func TestFeedbackAbsentWithoutSource(t *testing.T) {
	h := newHarness(t)
	if resp := h.get("/api/v3/feedback", h.token, nil); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "feedback_unavailable" {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if code, _ := h.send("POST", "/api/v3/feedback/asks/a_1/end", h.token, ""); code != http.StatusNotFound {
		t.Fatalf("end without a source: %d", code)
	}
	var boot struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	_ = json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&boot)
	if boot.Capabilities["feedback"] {
		t.Fatal("feedback advertised without a source")
	}
}

func TestFeedbackNeedsAuth(t *testing.T) {
	h, _ := feedbackHarness(t)
	if resp := h.get("/api/v3/feedback", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("read: %d", resp.StatusCode)
	}
	if code, _ := h.send("POST", "/api/v3/feedback/asks/a_1/answer", "", `{"answers":{"q1":{"selected":["SQLite"]}}}`); code != http.StatusUnauthorized {
		t.Fatalf("answer: %d", code)
	}
}

func TestFeedbackSnapshotHidesTheMacPath(t *testing.T) {
	h, _ := feedbackHarness(t)
	body := bodyOf(t, h.get("/api/v3/feedback?thread=t_1", h.token, nil))
	for _, leak := range []string{"/Users/me", "claude-code 2.1", "projectRoot", "callerInfo"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("snapshot leaks %q", leak)
		}
	}
	var snap feedback.Snapshot
	if err := json.Unmarshal(body, &snap); err != nil || !snap.Online || snap.Waiting != 1 || snap.Detail == nil || len(snap.Detail.Asks) != 2 {
		t.Fatalf("snapshot: %v %+v", err, snap)
	}
}

func TestFeedbackOfflineAndGoneThreadAreStatesNotErrors(t *testing.T) {
	h, f := feedbackHarness(t)
	var snap feedback.Snapshot
	if err := json.Unmarshal(bodyOf(t, h.get("/api/v3/feedback?thread=t_missing", h.token, nil)), &snap); err != nil || !snap.Online || snap.Detail != nil || snap.Selected != "" {
		t.Fatalf("gone thread: %v %+v", err, snap)
	}
	f.offline = true
	resp := h.get("/api/v3/feedback", h.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("offline status %d", resp.StatusCode)
	}
	var off feedback.Snapshot
	if err := json.Unmarshal(bodyOf(t, resp), &off); err != nil || off.Online || off.Threads == nil {
		t.Fatalf("offline: %v %+v", err, off)
	}
}

func TestFeedbackWritesForwardAndMapErrors(t *testing.T) {
	h, f := feedbackHarness(t)
	if code, _ := h.send("POST", "/api/v3/feedback/asks/a_1/answer", h.token, `{"answers":{"q1":{"selected":["SQLite"]}}}`); code != http.StatusOK || f.answered != "a_1" || f.answers["q1"].Selected[0] != "SQLite" {
		t.Fatalf("answer: %d %q %v", code, f.answered, f.answers)
	}
	if code, _ := h.send("POST", "/api/v3/feedback/asks/a_9/end", h.token, ""); code != http.StatusOK || f.ended != "a_9" {
		t.Fatalf("end: %d %q", code, f.ended)
	}
	for _, bad := range []string{``, `{}`, `{"answers":{}}`, `nope`} {
		before := f.answered
		f.answered = ""
		if code, _ := h.send("POST", "/api/v3/feedback/asks/a_1/answer", h.token, bad); code != http.StatusBadRequest || f.answered != "" {
			t.Errorf("body %q: %d, source touched=%v", bad, code, f.answered != "")
		}
		f.answered = before
	}
	for err, want := range map[error]int{feedback.ErrNotFound: 404, feedback.ErrFinal: 409, feedback.ErrInvalid: 400, feedback.ErrOffline: 503, context.DeadlineExceeded: 502} {
		f.err = err
		if code, _ := h.send("POST", "/api/v3/feedback/asks/a_1/answer", h.token, `{"answers":{"q1":{"selected":["x"]}}}`); code != want {
			t.Errorf("%v: %d, want %d", err, code, want)
		}
	}
}

func TestFeedbackCapabilityFollowsTheSource(t *testing.T) {
	h, _ := feedbackHarness(t)
	var boot struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&boot); err != nil || !boot.Capabilities["feedback"] {
		t.Fatalf("capabilities %v %v", boot.Capabilities, err)
	}
}

// A broker event becomes one events frame: ids, the waiting count and the notification's words (project, Agent,
// the question's first line) — never the description, an answer or a path.
func TestFeedbackEventBecomesAFrameWithTheNotificationWords(t *testing.T) {
	h, f := feedbackHarness(t)
	q, unsubscribe := h.srv.subscribeNotices()
	defer unsubscribe()
	h.srv.startFeedbackWatch()
	defer h.srv.stopFeedbackWatch()
	f.events <- feedback.Event{Type: "ask_created", ThreadID: "t_1", AskID: "a_1", Project: "solco", Client: "Claude Code", Title: "solco · Claude Code", Preview: "Which database should the service use?"}
	select {
	case frame := <-q:
		raw, _ := json.Marshal(frame)
		if string(raw) != `{"askId":"a_1","client":"Claude Code","kind":"ask","preview":"Which database should the service use?","project":"solco","t":"feedback","threadId":"t_1","waiting":1}` {
			t.Fatalf("frame %s", raw)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no frame")
	}
	f.events <- feedback.Event{Type: "ask_resolved", ThreadID: "t_1", AskID: "a_1"}
	select {
	case frame := <-q:
		if frame["kind"] != "resolved" || frame["preview"] != nil || frame["project"] != nil {
			t.Fatalf("frame %v", frame)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no resolved frame")
	}
}

func TestFeedbackFrameWordsAreBounded(t *testing.T) {
	long := strings.Repeat("长", 500)
	if got := []rune(clip(long, feedbackPreviewMax)); len(got) != feedbackPreviewMax || got[len(got)-1] != '…' {
		t.Fatalf("clip: %d runes", len(got))
	}
	if clip("short", feedbackPreviewMax) != "short" {
		t.Fatal("a short text must pass unchanged")
	}
}

func TestFeedbackContextPickerAndAnswerReferences(t *testing.T) {
	h, f := feedbackHarness(t)
	var thread feedback.Snapshot
	if err := json.Unmarshal(bodyOf(t, h.get("/api/v3/feedback?thread=t_1", h.token, nil)), &thread); err != nil {
		t.Fatal(err)
	}
	for _, ref := range thread.Detail.Asks[0].Context {
		if ref.Path != "" || ref.Name == "" {
			t.Fatalf("a phone sees what was picked, never where it lives: %+v", ref)
		}
	}
	if f.detail.Asks[0].Context[0].Path == "" {
		t.Fatal("stripping the paths must not touch the source's own thread")
	}
	if code, body := h.send("GET", "/api/v3/feedback/context?thread=t_1&kind=skills&q=th", h.token, ""); code != http.StatusOK || f.picked != "t_1|skills|th" || !strings.Contains(string(body), `"think"`) {
		t.Fatalf("pick: %d %s %q", code, body, f.picked)
	}
	for _, bad := range []string{"?kind=files", "?thread=t_1&kind=secrets"} {
		if code, _ := h.send("GET", "/api/v3/feedback/context"+bad, h.token, ""); code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, code)
		}
	}
	if code, _ := h.send("GET", "/api/v3/feedback/context?thread=t_1&kind=files", "", ""); code != http.StatusUnauthorized {
		t.Errorf("picker needs auth: %d", code)
	}
	if code, _ := h.send("POST", "/api/v3/feedback/asks/a_1/answer", h.token, `{"answers":{"q1":{"selected":["SQLite"]}},"context":[{"kind":"file","name":"docs/api.md"}]}`); code != http.StatusOK || len(f.refs) != 1 || f.refs[0].Name != "docs/api.md" {
		t.Fatalf("answer with context: %d %+v", code, f.refs)
	}
}

func TestFeedbackThumbnailsForwardAndNeedAuth(t *testing.T) {
	h, f := feedbackHarness(t)
	code, body := h.send("GET", "/api/v3/feedback/asks/a_1/thumbnails", h.token, "")
	if code != http.StatusOK || f.thumbsOf != "a_1" || !strings.Contains(string(body), `"dataBase64":"AQID"`) {
		t.Fatalf("thumbnails: %d %s %q", code, body, f.thumbsOf)
	}
	if code, _ := h.send("GET", "/api/v3/feedback/asks/a_1/thumbnails", "", ""); code != http.StatusUnauthorized {
		t.Errorf("thumbnails need auth: %d", code)
	}
}

func TestFeedbackManagementForwardsAndSearches(t *testing.T) {
	h, f := feedbackHarness(t)
	f.retention = 90
	var snap feedback.Snapshot
	if err := json.Unmarshal(bodyOf(t, h.get("/api/v3/feedback?q=database", h.token, nil)), &snap); err != nil || f.searched != "database" || snap.RetentionDays != 90 {
		t.Fatalf("search: %v %q %+v", err, f.searched, snap)
	}
	if code, body := h.send("POST", "/api/v3/feedback/threads/delete", h.token, `{"ids":["t_1","t_2"]}`); code != http.StatusOK || len(f.deleted) != 2 || !strings.Contains(string(body), `"deleted":2`) {
		t.Fatalf("delete: %d %s %v", code, body, f.deleted)
	}
	if code, body := h.send("POST", "/api/v3/feedback/threads/cleanup", h.token, `{"olderThanDays":30,"dryRun":true}`); code != http.StatusOK || f.purgeDays != 30 || !f.purgeDry || !strings.Contains(string(body), `"matched":3`) || !strings.Contains(string(body), `"deleted":0`) {
		t.Fatalf("cleanup dry run: %d %s", code, body)
	}
	if code, body := h.send("POST", "/api/v3/feedback/threads/cleanup", h.token, `{"olderThanDays":30}`); code != http.StatusOK || f.purgeDry || !strings.Contains(string(body), `"deleted":3`) {
		t.Fatalf("cleanup: %d %s", code, body)
	}
	if code, _ := h.send("POST", "/api/v3/feedback/retention", h.token, `{"days":0}`); code != http.StatusOK || f.retention != 0 {
		t.Fatalf("retention 0 means forever and must be accepted: %d", code)
	}
	for path, bad := range map[string]string{
		"/api/v3/feedback/threads/delete":  `{"ids":[]}`,
		"/api/v3/feedback/threads/cleanup": `{"olderThanDays":0}`,
		"/api/v3/feedback/retention":       `{}`,
	} {
		if code, _ := h.send("POST", path, h.token, bad); code != http.StatusBadRequest {
			t.Errorf("%s %s: %d", path, bad, code)
		}
		if code, _ := h.send("POST", path, "", bad); code != http.StatusUnauthorized {
			t.Errorf("%s needs auth: %d", path, code)
		}
	}
	var boot struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	_ = json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&boot)
	if !boot.Capabilities["feedbackManage"] {
		t.Fatal("a source that can manage advertises feedbackManage")
	}
	if !boot.Capabilities["feedbackContext"] {
		t.Fatal("and the picker")
	}
}
