package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type recNotifier struct {
	mu sync.Mutex
	ns []Notice
}

func (r *recNotifier) Notify(_ context.Context, n Notice) {
	r.mu.Lock()
	r.ns = append(r.ns, n)
	r.mu.Unlock()
}
func (r *recNotifier) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.ns) }

func TestGuardRefusesBrowsersAndStrangers(t *testing.T) {
	b := &Broker{Store: NewMemory(), Token: "tok-tok-tok"}
	srv := httptest.NewServer(NewHandler(b))
	defer srv.Close()
	do := func(mut func(*http.Request)) int {
		req, _ := http.NewRequest("GET", srv.URL+"/v1/inbox/threads", nil)
		mut(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("no CORS headers, ever")
		}
		return resp.StatusCode
	}
	if c := do(func(r *http.Request) {}); c != 401 {
		t.Fatalf("no token: %d", c)
	}
	if c := do(func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }); c != 401 {
		t.Fatalf("wrong token: %d", c)
	}
	if c := do(func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer tok-tok-tok")
		r.Header.Set("Origin", "https://evil.example")
	}); c != 403 {
		t.Fatalf("origin with a valid token must still be refused: %d", c)
	}
	if c := do(func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok-tok-tok"); r.Host = "evil.example" }); c != 403 {
		t.Fatalf("rebinding host: %d", c)
	}
	if c := do(func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok-tok-tok") }); c != 200 {
		t.Fatalf("valid: %d", c)
	}
}

func TestBrokerRoundTripThroughClient(t *testing.T) {
	dir := t.TempDir()
	token, err := EnsureToken(dir)
	if err != nil || len(token) < 32 {
		t.Fatalf("token: %v", err)
	}
	if t2, _ := EnsureToken(dir); t2 != token {
		t.Fatal("token must be stable")
	}
	notes := &recNotifier{}
	b := &Broker{Store: NewMemory(), Token: token, Notifier: notes}
	srv := httptest.NewServer(NewHandler(b))
	defer srv.Close()
	writeEndpoint(t, dir, srv.Listener.Addr().String())
	c := NewClient(dir)
	ctx := context.Background()

	if err := c.Health(ctx); err != nil {
		t.Fatal(err)
	}
	repo := mkRepo(t)
	askID, threadID, err := c.Create(ctx, CreateInput{CallerInfo: "Cursor Agent - ship", Client: "cursor", ProjectRoot: repo, Questions: q1("Deploy?")})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return notes.count() == 1 })
	if n := notes.ns[0]; n.Thread.ID != threadID || n.Ask.ID != askID || n.UIAttached {
		t.Fatalf("notice: %+v", n)
	}
	if r, _ := c.Result(ctx, askID); r.Status != "pending" {
		t.Fatalf("%+v", r)
	}
	rows, _ := c.Threads(ctx, true, "")
	if len(rows) != 1 || rows[0].Waiting != 1 || rows[0].ID != threadID {
		t.Fatalf("rows: %+v", rows)
	}

	events := make(chan Event, 4)
	sctx, stop := context.WithCancel(ctx)
	defer stop()
	go func() { _ = c.Stream(sctx, func(e Event) { events <- e }) }()
	waitFor(t, b.UIAttached)

	if err := c.Answer(ctx, askID, map[string]Answer{"a": {Selected: []string{"x"}}}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-events:
		if e.Type != "ask_resolved" || e.AskID != askID || e.State != StateAnswered {
			t.Fatalf("event %+v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no SSE event")
	}
	r, _ := c.Result(ctx, askID)
	if r.Status != "answered" || r.Answers["a"].Selected[0] != "x" {
		t.Fatalf("%+v", r)
	}
	if err := c.Answer(ctx, askID, nil, nil); !errors.Is(err, ErrFinal) {
		t.Fatalf("second answer: %v", err)
	}
	if err := c.Answer(ctx, "missing", nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	th, err := c.Thread(ctx, threadID)
	if err != nil || len(th.Asks) != 1 || th.Client != "Cursor" {
		t.Fatalf("thread: %+v %v", th, err)
	}
	items, _ := c.Recall(ctx, repo, "deploy", 3)
	if len(items) != 1 {
		t.Fatalf("recall: %+v", items)
	}
	if _, _, err := c.Create(ctx, CreateInput{Questions: nil}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid: %v", err)
	}
}

func TestClientReportsOfflineAndFollowsANewPort(t *testing.T) {
	dir := t.TempDir()
	c := NewClient(dir)
	if err := c.Health(context.Background()); !errors.Is(err, ErrOffline) {
		t.Fatalf("no endpoint file: %v", err)
	}
	token, _ := EnsureToken(dir)
	writeEndpoint(t, dir, "127.0.0.1:1") // nothing listens
	if err := c.Health(context.Background()); !errors.Is(err, ErrOffline) {
		t.Fatalf("dead port: %v", err)
	}
	srv := httptest.NewServer(NewHandler(&Broker{Store: NewMemory(), Token: token}))
	defer srv.Close()
	writeEndpoint(t, dir, srv.Listener.Addr().String())
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("must rediscover the moved broker: %v", err)
	}
}

func TestRunOwnsOnceAndStandbyTakesOver(t *testing.T) {
	dir := t.TempDir()
	notes := &recNotifier{}
	ctx1, stop1 := context.WithCancel(context.Background())
	done1 := make(chan struct{})
	go func() { _ = Run(ctx1, Options{Dir: dir, Retry: 50 * time.Millisecond, Notifier: notes}); close(done1) }()
	c := NewClient(dir)
	waitFor(t, func() bool { return c.Health(context.Background()) == nil })
	askID, _, err := c.Create(context.Background(), CreateInput{CallerInfo: "A", ProjectRoot: "/tmp/p", Questions: q1("alive?")})
	if err != nil {
		t.Fatal(err)
	}

	ctx2, stop2 := context.WithCancel(context.Background())
	defer stop2()
	go func() { _ = Run(ctx2, Options{Dir: dir, Retry: 50 * time.Millisecond}) }()
	time.Sleep(200 * time.Millisecond)
	ep1, _ := os.ReadFile(filepath.Join(dir, endpointName))

	stop1()
	<-done1
	if _, err := os.Stat(filepath.Join(dir, endpointName)); err == nil {
		// the standby may already have rewritten it; it must then be a different address
		ep2, _ := os.ReadFile(filepath.Join(dir, endpointName))
		if string(ep1) == string(ep2) {
			t.Fatal("the stopped owner must remove its endpoint")
		}
	}
	waitFor(t, func() bool { return c.Health(context.Background()) == nil })
	// The pending Ask survived the hand-over: the new owner loaded it from feedback.json.
	r, err := c.Result(context.Background(), askID)
	if err != nil || r.Status != "pending" {
		t.Fatalf("after takeover: %+v %v", r, err)
	}
}

func TestLegacyImport(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "decisions.jsonl")
	lines := []string{
		`{"ts":1000000,"sessionId":"s1","status":"answered","projectRoot":"/tmp/imp","callerInfo":"Cursor Agent - one","createdAt":900000,"decisions":[{"q":"Scope?","type":"single","selected":["all"]}]}`,
		`{"ts":1500000,"sessionId":"s2","status":"answered","projectRoot":"/tmp/imp","callerInfo":"Cursor Agent - two","createdAt":1200000,"decisions":[{"q":"Branch?","type":"text","selected":[],"custom":"main"}]}`,
		`{"ts":9000000,"sessionId":"s3","status":"expired","reason":"timeout","projectRoot":"/tmp/imp","callerInfo":"Cursor Agent - three","createdAt":8000000,"decisions":[{"q":"Later?","type":"single","selected":[]}]}`,
		`not json`,
	}
	_ = os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	s := NewMemory()
	n, err := s.ImportDecisions(dir, file)
	if err != nil || n != 3 {
		t.Fatalf("imported %d %v", n, err)
	}
	rows := s.Threads(Filter{})
	if len(rows) != 2 {
		t.Fatalf("two threads expected (a 2 h gap splits them): %+v", rows)
	}
	if again, _ := s.ImportDecisions(dir, file); again != 0 {
		t.Fatal("import runs once")
	}
	if got := s.Recall("/tmp/imp", "main", 5); len(got) != 1 || got[0].Pairs[0].Answer != "main" {
		t.Fatalf("recall from imported: %+v", got)
	}
	var tr Thread
	for _, r := range rows {
		if r.AskCount == 1 {
			tr, _ = s.Thread(r.ID)
		}
	}
	if tr.Asks[0].State != StateEnded {
		t.Fatalf("imported unanswered asks are history, not answerable: %+v", tr.Asks[0])
	}
}

func writeEndpoint(t *testing.T, dir, addr string) {
	t.Helper()
	b, _ := json.Marshal(endpointFile{Addr: addr, PID: 1})
	if err := os.WriteFile(filepath.Join(dir, endpointName), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not reached in time")
}

// TestLegacyImportRealFile replays the developer's own history when asked: SOLCO_FEEDBACK_REAL=1 go test -run RealFile -v
func TestLegacyImportRealFile(t *testing.T) {
	if os.Getenv("SOLCO_FEEDBACK_REAL") == "" {
		t.Skip("set SOLCO_FEEDBACK_REAL=1 to replay ~/.config/interactive-feedback-mcp/decisions.jsonl")
	}
	s := NewMemory()
	n, err := s.ImportDecisions(t.TempDir(), LegacyDecisionsPath())
	if err != nil {
		t.Fatal(err)
	}
	rows := s.Threads(Filter{})
	projects := map[string]bool{}
	asks := make([]int, 0, len(rows))
	for _, r := range rows {
		projects[r.Project] = true
		asks = append(asks, r.AskCount)
	}
	sort.Ints(asks)
	t.Logf("records=%d threads=%d projects=%d median=%d", n, len(rows), len(projects), asks[len(asks)/2])
}
