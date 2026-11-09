package feedback

/**
 * [INPUT]: 数据目录里的 feedback.json（原子写、0600）、ResolveProject/ClientName/TitleFor
 * [OUTPUT]: 对外提供 Store（Open/NewMemory/Create/Resolve/End/Heartbeat/Tick/Threads/Thread/Result/Recall/Attachments/Subscribe）、ErrNotFound/ErrFinal、Filter、AskResult、LateAnswer、RecallItem
 * [POS]: 收件箱的唯一状态所有者（2026-11-09 决策，取代 Bun 版内存 Map）：线程归属、Ask 状态机、迟到回答、有界落盘（500 线程 × 200 条）；
 *        重启后 pending 的 Ask 原样回来，Agent 侧 shim 按 id 继续轮询；图片只在内存里等 Agent 取走，历史只留数量
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxThreads        = 500
	maxAsksPerThread  = 200
	maxQuestions      = 20
	maxDescription    = 32 << 10
	maxQuestionText   = 4 << 10
	maxOptions        = 50
	maxOptionText     = 500
	maxAnswerText     = 32 << 10
	minTimeout        = time.Minute
	maxTimeout        = 24 * time.Hour
	defaultTimeout    = 4 * time.Hour
	disconnectedAfter = 60 * time.Second
	lostAfter         = 5 * time.Minute
	attachmentKeep    = time.Hour
	maxAttachments    = 5
	maxAttachmentSize = 4 << 20
)

var (
	ErrNotFound = errors.New("feedback: ask not found")
	ErrFinal    = errors.New("feedback: ask is already final")
	ErrInvalid  = errors.New("feedback: invalid ask")
)

type persisted struct {
	Version int `json:"version"`
	// RetentionDays: finished threads older than this many days are removed (0 = keep forever).
	RetentionDays int       `json:"retentionDays,omitempty"`
	Threads       []*Thread `json:"threads"`
}

// Store owns every Thread and Ask.
type Store struct {
	mu      sync.Mutex
	path    string
	threads []*Thread
	// retentionDays is the user's keep-for setting (0 = forever); Tick applies it.
	retentionDays int
	byAsk         map[string]*Thread
	atts          map[string][]Attachment
	subs          map[int]chan Event
	nextSub       int
	now           func() time.Time
}

// NewMemory returns a Store that never touches the disk.
func NewMemory() *Store {
	return &Store{byAsk: map[string]*Thread{}, atts: map[string][]Attachment{}, subs: map[int]chan Event{}, now: time.Now}
}

// Open loads (or starts) the Store kept in dir/feedback.json.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := NewMemory()
	s.path = filepath.Join(dir, "feedback.json")
	b, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, err
	}
	var p persisted
	if err := json.Unmarshal(b, &p); err != nil {
		// Keep the unreadable file for inspection and start clean rather than refusing to serve.
		_ = os.Rename(s.path, s.path+".corrupt")
		slog.Warn("feedback: store unreadable, started empty", "error", err)
		return s, nil
	}
	s.retentionDays = p.RetentionDays
	for _, t := range p.Threads {
		s.threads = append(s.threads, t)
		for _, a := range t.Asks {
			s.byAsk[a.ID] = t
		}
	}
	return s, nil
}

func (s *Store) saveLocked() {
	if s.path == "" {
		return
	}
	b, err := json.Marshal(persisted{Version: 1, RetentionDays: s.retentionDays, Threads: s.threads})
	if err != nil {
		slog.Warn("feedback: encode failed", "error", err)
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		slog.Warn("feedback: write failed", "error", err)
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		slog.Warn("feedback: rename failed", "error", err)
	}
}

func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// NewAskID returns a fresh Ask id (UUID shaped, so a shim and the old wire format agree).
func NewAskID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

func normalizeQuestions(in []Question) ([]Question, error) {
	if len(in) == 0 || len(in) > maxQuestions {
		return nil, fmt.Errorf("%w: need 1 to %d questions", ErrInvalid, maxQuestions)
	}
	out := make([]Question, 0, len(in))
	seen := map[string]bool{}
	for i, q := range in {
		q.Text = clipBytes(strings.TrimSpace(q.Text), maxQuestionText)
		if q.Text == "" {
			return nil, fmt.Errorf("%w: question %d has no text", ErrInvalid, i+1)
		}
		if q.ID = strings.TrimSpace(q.ID); q.ID == "" || seen[q.ID] {
			q.ID = fmt.Sprintf("q%d", i+1)
		}
		seen[q.ID] = true
		switch q.Type {
		case "single", "multiple", "text":
		default:
			q.Type = "text"
		}
		if len(q.Options) > maxOptions {
			q.Options = q.Options[:maxOptions]
		}
		for j := range q.Options {
			q.Options[j] = clipBytes(q.Options[j], maxOptionText)
		}
		q.AllowCustom = q.Type != "text"
		out = append(out, q)
	}
	return out, nil
}

func clampTimeout(ms int64) int64 {
	d := time.Duration(ms) * time.Millisecond
	if d <= 0 {
		d = defaultTimeout
	}
	if d < minTimeout {
		d = minTimeout
	}
	if d > maxTimeout {
		d = maxTimeout
	}
	return d.Milliseconds()
}

// Create registers an Ask and files it under a Thread.
func (s *Store) Create(in CreateInput) (Ask, ThreadSummary, error) {
	qs, err := normalizeQuestions(in.Questions)
	if err != nil {
		return Ask{}, ThreadSummary{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.ID == "" {
		in.ID = NewAskID()
	}
	if _, dup := s.byAsk[in.ID]; dup {
		return Ask{}, ThreadSummary{}, fmt.Errorf("%w: duplicate id", ErrInvalid)
	}
	nowMs := s.now().UnixMilli()
	a := &Ask{
		ID: in.ID, CreatedAt: nowMs, LastHeartbeat: nowMs, State: StatePending,
		Description: clipBytes(in.Description, maxDescription), Questions: qs, TimeoutMs: clampTimeout(in.TimeoutMs),
	}
	t := s.assignLocked(in, nowMs)
	if t.Task == "" {
		t.Task = clipBytes(strings.TrimSpace(in.Task), maxDescription)
	}
	a.ThreadID = t.ID
	t.Asks = append(t.Asks, a)
	t.LastAt = nowMs
	s.byAsk[a.ID] = t
	s.trimLocked(t)
	s.saveLocked()
	s.emitLocked(Event{Type: "ask_created", ThreadID: t.ID, AskID: a.ID, State: a.State, Project: t.Project,
		Client: t.Client, Title: t.Title, Preview: firstLine(qs[0].Text)})
	return *a, summarize(t), nil
}

// assignLocked applies the thread rules: explicit thread_id, then task_id, then project + client + gap.
func (s *Store) assignLocked(in CreateInput, nowMs int64) *Thread {
	key, branch, worktree := ResolveProject(in.ProjectRoot)
	client := ClientName(in.Client, in.CallerInfo)
	explicit := strings.TrimSpace(in.ThreadID)
	task := strings.TrimSpace(in.TaskID)
	var found *Thread
	for _, t := range s.threads {
		switch {
		case explicit != "":
			if t.ExplicitID == explicit {
				found = t
			}
		case task != "":
			if t.TaskID == task && t.ProjectKey == key {
				found = t
			}
		default:
			if t.ExplicitID == "" && t.TaskID == "" && t.ProjectKey == key && strings.EqualFold(t.Client, client) &&
				nowMs-t.LastAt <= GapWindow.Milliseconds() && (found == nil || t.LastAt > found.LastAt) {
				found = t
			}
		}
		if found != nil && (explicit != "" || task != "") {
			break
		}
	}
	if found != nil {
		if branch != "" {
			found.Branch = branch
		}
		if in.ProjectRoot != "" {
			found.ProjectRoot = in.ProjectRoot
		}
		if in.ClientVersion != "" {
			found.ClientVersion = in.ClientVersion
		}
		return found
	}
	t := &Thread{
		ID: newID("t_"), ProjectKey: key, ProjectRoot: strings.TrimSpace(in.ProjectRoot), Branch: branch, Worktree: worktree,
		Client: client, ClientVersion: in.ClientVersion, CallerInfo: clipRunes(strings.TrimSpace(in.CallerInfo), 200),
		Title: TitleFor(in.CallerInfo, client, branch, key), ExplicitID: explicit, TaskID: task,
		CreatedAt: nowMs, LastAt: nowMs,
	}
	t.Project = projectName(key)
	s.threads = append(s.threads, t)
	return t
}

func projectName(key string) string {
	if key == "" {
		return "(no project)"
	}
	return filepath.Base(key)
}

func (s *Store) trimLocked(t *Thread) {
	if len(t.Asks) > maxAsksPerThread {
		drop := 0
		for _, a := range t.Asks {
			if len(t.Asks)-drop <= maxAsksPerThread {
				break
			}
			if a.State != StatePending {
				delete(s.byAsk, a.ID)
				delete(s.atts, a.ID)
				drop++
			} else {
				break
			}
		}
		t.Asks = t.Asks[drop:]
	}
	if len(s.threads) > maxThreads {
		sort.SliceStable(s.threads, func(i, j int) bool { return s.threads[i].LastAt > s.threads[j].LastAt })
		keep := s.threads[:0]
		for i, th := range s.threads {
			if i >= maxThreads && pendingCount(th) == 0 {
				for _, a := range th.Asks {
					delete(s.byAsk, a.ID)
					delete(s.atts, a.ID)
				}
				continue
			}
			keep = append(keep, th)
		}
		s.threads = keep
	}
}

func pendingCount(t *Thread) int {
	n := 0
	for _, a := range t.Asks {
		if a.State == StatePending {
			n++
		}
	}
	return n
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return clipRunes(s, 120)
}

func summarize(t *Thread) ThreadSummary {
	sum := ThreadSummary{ID: t.ID, Project: t.Project, Branch: t.Branch, Worktree: t.Worktree, Client: t.Client,
		Title: t.Title, LastAt: t.LastAt, Waiting: pendingCount(t), AskCount: len(t.Asks)}
	if n := len(t.Asks); n > 0 {
		last := t.Asks[n-1]
		sum.LastState = string(last.State)
		if len(last.Questions) > 0 {
			sum.Preview = firstLine(last.Questions[0].Text)
		}
	}
	return sum
}

func (s *Store) emitLocked(e Event) {
	for _, ch := range s.subs {
		select {
		case ch <- e:
		default: // a slow subscriber misses events; it refetches on reconnect
		}
	}
}

// Subscribe returns a channel of Events and a cancel func.
func (s *Store) Subscribe() (<-chan Event, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextSub
	s.nextSub++
	ch := make(chan Event, 64)
	s.subs[id] = ch
	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if c, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(c)
		}
	}
}

func (s *Store) askLocked(id string) (*Thread, *Ask) {
	t := s.byAsk[id]
	if t == nil {
		return nil, nil
	}
	for _, a := range t.Asks {
		if a.ID == id {
			return t, a
		}
	}
	return nil, nil
}

func sanitizeAttachments(in []Attachment) []Attachment {
	var out []Attachment
	for _, a := range in {
		if len(out) >= maxAttachments {
			break
		}
		if !strings.HasPrefix(a.MimeType, "image/") || a.DataBase64 == "" || len(a.DataBase64)*3/4 > maxAttachmentSize {
			continue
		}
		a.Name = clipRunes(a.Name, 120)
		out = append(out, a)
	}
	return out
}

// Resolve records the user's answer. A pending Ask becomes answered; an expired one becomes a late answer that
// the Agent receives with its next Ask in the same thread. Ended and already answered Asks are final.
func (s *Store) Resolve(id string, answers map[string]Answer, atts []Attachment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, a := s.askLocked(id)
	if a == nil {
		return ErrNotFound
	}
	if a.State != StatePending && a.State != StateExpired {
		return ErrFinal
	}
	known := map[string]bool{}
	for _, q := range a.Questions {
		known[q.ID] = true
	}
	clean := map[string]Answer{}
	for qid, ans := range answers {
		if !known[qid] {
			continue
		}
		ans.Custom = clipBytes(ans.Custom, maxAnswerText)
		if ans.Selected == nil {
			ans.Selected = []string{}
		}
		clean[qid] = ans
	}
	atts = sanitizeAttachments(atts)
	a.Late = a.State == StateExpired
	a.State = StateAnswered
	a.Answers = clean
	a.Message = ""
	a.ResolvedAt = s.now().UnixMilli()
	a.AttachmentCount = len(atts)
	if len(atts) > 0 {
		s.atts[id] = atts
	}
	if a.ResolvedAt > t.LastAt {
		t.LastAt = a.ResolvedAt
	}
	s.saveLocked()
	s.emitLocked(Event{Type: "ask_resolved", ThreadID: t.ID, AskID: id, State: a.State, Project: t.Project, Client: t.Client, Title: t.Title})
	return nil
}

// End closes a pending Ask without an answer.
func (s *Store) End(id, message string) error {
	return s.finish(id, StateEnded, message, true)
}

// Abandon records that the Agent stopped waiting (its client cancelled the call). The Ask expires at once but stays
// answerable: the user's answer comes back to the Agent as a late answer with its next question in the thread.
func (s *Store) Abandon(id string) error {
	return s.finish(id, StateExpired, "Agent stopped waiting", false)
}

func (s *Store) finish(id string, st AskState, message string, userAction bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, a := s.askLocked(id)
	if a == nil {
		return ErrNotFound
	}
	if a.State != StatePending {
		return ErrFinal
	}
	if message == "" && userAction {
		message = "User ended this question"
	}
	a.State = st
	a.Message = clipRunes(message, 200)
	a.ResolvedAt = s.now().UnixMilli()
	s.saveLocked()
	s.emitLocked(Event{Type: "ask_resolved", ThreadID: t.ID, AskID: id, State: st, Project: t.Project, Client: t.Client, Title: t.Title})
	return nil
}

// Heartbeat keeps a pending Ask's Agent marked as connected.
func (s *Store) Heartbeat(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, a := s.askLocked(id)
	if a == nil || a.State != StatePending {
		return false
	}
	a.LastHeartbeat = s.now().UnixMilli()
	return true
}

// Tick expires Asks past their timeout or whose Agent stopped polling, and drops old attachment bytes.
func (s *Store) Tick() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	nowMs := s.now().UnixMilli()
	n := 0
	for _, t := range s.threads {
		for _, a := range t.Asks {
			if a.State == StatePending {
				var msg string
				switch {
				case nowMs-a.CreatedAt > a.TimeoutMs:
					msg = "User did not respond in time"
				case nowMs-a.LastHeartbeat > lostAfter.Milliseconds():
					msg = "Agent connection lost"
				}
				if msg != "" {
					a.State, a.Message, a.ResolvedAt = StateExpired, msg, nowMs
					n++
					s.emitLocked(Event{Type: "ask_resolved", ThreadID: t.ID, AskID: a.ID, State: a.State, Project: t.Project, Client: t.Client, Title: t.Title})
				}
			} else if a.ResolvedAt > 0 && nowMs-a.ResolvedAt > attachmentKeep.Milliseconds() {
				delete(s.atts, a.ID)
			}
		}
	}
	if s.retentionDays > 0 {
		cutoff := s.now().Add(-time.Duration(s.retentionDays) * 24 * time.Hour).UnixMilli()
		if gone := s.removeFinishedLocked(cutoff, false); gone > 0 {
			n++
		}
	}
	if n > 0 {
		s.saveLocked()
	}
	return n
}

// Filter narrows Threads.
type Filter struct {
	Waiting bool   // only threads with a pending Ask
	Project string // display name or project key
	Limit   int
}

// Threads lists thread rows: waiting first, then by recent activity.
func (s *Store) Threads(f Filter) []ThreadSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ThreadSummary
	for _, t := range s.threads {
		if f.Project != "" && t.Project != f.Project && t.ProjectKey != f.Project {
			continue
		}
		sum := summarize(t)
		if f.Waiting && sum.Waiting == 0 {
			continue
		}
		out = append(out, sum)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Waiting > 0) != (out[j].Waiting > 0) {
			return out[i].Waiting > 0
		}
		return out[i].LastAt > out[j].LastAt
	})
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out
}

// Thread returns one thread with its Asks, oldest first.
func (s *Store) Thread(id string) (Thread, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	nowMs := s.now().UnixMilli()
	for _, t := range s.threads {
		if t.ID != id {
			continue
		}
		cp := *t
		cp.Waiting = pendingCount(t)
		cp.Asks = make([]*Ask, len(t.Asks))
		for i, a := range t.Asks {
			c := *a
			c.Disconnected = c.State == StatePending && nowMs-c.LastHeartbeat > disconnectedAfter.Milliseconds()
			cp.Asks[i] = &c
		}
		return cp, true
	}
	return Thread{}, false
}

// ThreadOfAsk returns the thread id an Ask belongs to.
func (s *Store) ThreadOfAsk(askID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.byAsk[askID]; t != nil {
		return t.ID, true
	}
	return "", false
}

// LateAnswer is an answer that arrived after its Ask timed out.
type LateAnswer struct {
	AskID     string            `json:"askId"`
	AskedAt   int64             `json:"askedAt"`
	Questions []Question        `json:"questions"`
	Answers   map[string]Answer `json:"answers"`
}

// AskResult is what an Agent's shim reads.
type AskResult struct {
	Status      string            `json:"status"` // pending | answered | expired | ended | not_found
	Answers     map[string]Answer `json:"answers,omitempty"`
	Attachments []Attachment      `json:"attachments,omitempty"`
	Message     string            `json:"message,omitempty"`
	LateAnswers []LateAnswer      `json:"lateAnswers,omitempty"`
}

// Result reports an Ask to its Agent. The first terminal read of an Ask also hands over the late answers of its
// thread that were never delivered.
func (s *Store) Result(id string) AskResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, a := s.askLocked(id)
	if a == nil {
		return AskResult{Status: "not_found", Message: "Ask not found"}
	}
	switch a.State {
	case StatePending:
		return AskResult{Status: "pending"}
	case StateAnswered:
		r := AskResult{Status: "answered", Answers: a.Answers, Attachments: s.atts[id]}
		r.LateAnswers = s.popLateLocked(t, id)
		return r
	default:
		r := AskResult{Status: string(a.State), Message: a.Message}
		r.LateAnswers = s.popLateLocked(t, id)
		return r
	}
}

func (s *Store) popLateLocked(t *Thread, except string) []LateAnswer {
	var out []LateAnswer
	for _, a := range t.Asks {
		if a.ID == except || !a.Late || a.LateDelivered || a.State != StateAnswered {
			continue
		}
		a.LateDelivered = true
		out = append(out, LateAnswer{AskID: a.ID, AskedAt: a.CreatedAt, Questions: a.Questions, Answers: a.Answers})
	}
	if len(out) > 0 {
		s.saveLocked()
	}
	return out
}

// Attachments returns an answered Ask's image bytes while they are still held.
func (s *Store) Attachments(id string) []Attachment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Attachment(nil), s.atts[id]...)
}

// RecallItem is one earlier answer an Agent can look up.
type RecallItem struct {
	AskedAt  int64        `json:"askedAt"`
	Project  string       `json:"project"`
	Title    string       `json:"title,omitempty"`
	Pairs    []RecallPair `json:"pairs"`
	Late     bool         `json:"late,omitempty"`
	ThreadID string       `json:"threadId"`
}

// RecallPair is a question and what the user chose or wrote.
type RecallPair struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// Recall finds earlier answers in a project, newest first. project matches a display name, a project key or a
// project root; query is a case-insensitive substring over questions and answers; an empty query returns the latest.
func (s *Store) Recall(project, query string, limit int) []RecallItem {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	key := project
	if project != "" {
		if k, _, _ := ResolveProject(project); k != "" {
			key = k
		}
	}
	q := strings.ToLower(strings.TrimSpace(query))
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []RecallItem
	for _, t := range s.threads {
		if project != "" && t.ProjectKey != key && t.Project != project && t.ProjectRoot != project {
			continue
		}
		for _, a := range t.Asks {
			if a.State != StateAnswered {
				continue
			}
			item := RecallItem{AskedAt: a.CreatedAt, Project: t.Project, Title: t.Title, Late: a.Late, ThreadID: t.ID}
			match := q == ""
			for _, qu := range a.Questions {
				ans := a.Answers[qu.ID]
				text := strings.TrimSpace(strings.Join(append(append([]string{}, ans.Selected...), ans.Custom), "; "))
				text = strings.Trim(text, "; ")
				item.Pairs = append(item.Pairs, RecallPair{Question: qu.Text, Answer: text})
				if q != "" && (strings.Contains(strings.ToLower(qu.Text), q) || strings.Contains(strings.ToLower(text), q)) {
					match = true
				}
			}
			if match {
				out = append(out, item)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].AskedAt > out[j].AskedAt })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// removeThreadLocked drops one thread and what hangs off its Asks.
func (s *Store) removeThreadLocked(t *Thread) {
	for _, a := range t.Asks {
		delete(s.byAsk, a.ID)
		delete(s.atts, a.ID)
	}
	for i, x := range s.threads {
		if x == t {
			s.threads = append(s.threads[:i], s.threads[i+1:]...)
			return
		}
	}
}

// removeFinishedLocked counts (dryRun) or removes the threads with no pending Ask whose last activity is before cutoffMs.
func (s *Store) removeFinishedLocked(cutoffMs int64, dryRun bool) int {
	var doomed []*Thread
	for _, t := range s.threads {
		if pendingCount(t) == 0 && t.LastAt < cutoffMs {
			doomed = append(doomed, t)
		}
	}
	if !dryRun {
		for _, t := range doomed {
			s.removeThreadLocked(t)
		}
	}
	return len(doomed)
}

// DeleteThreads removes the named threads that have no pending Ask and reports how many went; a thread an Agent is
// still waiting on is never deleted.
func (s *Store) DeleteThreads(ids []string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var doomed []*Thread
	for _, t := range s.threads {
		if want[t.ID] && pendingCount(t) == 0 {
			doomed = append(doomed, t)
		}
	}
	for _, t := range doomed {
		s.removeThreadLocked(t)
	}
	if len(doomed) > 0 {
		s.saveLocked()
	}
	return len(doomed)
}

// Purge removes (or, with dryRun, only counts) the finished threads idle for longer than olderThan.
func (s *Store) Purge(olderThan time.Duration, dryRun bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.removeFinishedLocked(s.now().Add(-olderThan).UnixMilli(), dryRun)
	if n > 0 && !dryRun {
		s.saveLocked()
	}
	return n
}

// RetentionDays is the keep-for setting (0 = forever).
func (s *Store) RetentionDays() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retentionDays
}

// SetRetentionDays sets how long finished threads are kept (0 = forever, at most ten years).
func (s *Store) SetRetentionDays(days int) error {
	if days < 0 || days > 3650 {
		return fmt.Errorf("%w: retention days", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retentionDays = days
	s.saveLocked()
	return nil
}

// Search returns the ids of the threads whose title, project, branch, Agent, task, descriptions, questions or answers
// contain q (case-insensitive).
func (s *Store) Search(q string) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := []string{}
	if q == "" {
		return ids
	}
	has := func(v string) bool { return v != "" && strings.Contains(strings.ToLower(v), q) }
	for _, t := range s.threads {
		hit := has(t.Title) || has(t.Project) || has(t.Branch) || has(t.Client) || has(t.Task)
		for _, a := range t.Asks {
			if hit {
				break
			}
			hit = has(a.Description)
			for _, qu := range a.Questions {
				hit = hit || has(qu.Text)
			}
			for _, an := range a.Answers {
				hit = hit || has(an.Custom)
				for _, sel := range an.Selected {
					hit = hit || has(sel)
				}
			}
		}
		if hit {
			ids = append(ids, t.ID)
		}
	}
	return ids
}
