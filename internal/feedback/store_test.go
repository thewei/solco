package feedback

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func q1(text string) []Question {
	return []Question{{ID: "a", Text: text, Type: "single", Options: []string{"x", "y"}}}
}

func clockStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	s := NewMemory()
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }
	return s, &now
}

func mkRepo(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(d, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	return d
}

func TestThreadingByProjectClientAndGap(t *testing.T) {
	s, now := clockStore(t)
	repo := mkRepo(t)
	_, th1, err := s.Create(CreateInput{CallerInfo: "Cursor Agent - plans", ProjectRoot: repo, Questions: q1("one?")})
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(20 * time.Minute)
	_, th2, _ := s.Create(CreateInput{CallerInfo: "Cursor Agent - plans 2", ProjectRoot: repo, Questions: q1("two?")})
	if th1.ID != th2.ID {
		t.Fatalf("same project, client, short gap must share a thread")
	}
	_, th3, _ := s.Create(CreateInput{CallerInfo: "Claude Code - fix", ProjectRoot: repo, Questions: q1("three?")})
	if th3.ID == th1.ID {
		t.Fatalf("another client must get its own thread")
	}
	*now = now.Add(61 * time.Minute)
	_, th4, _ := s.Create(CreateInput{CallerInfo: "Cursor Agent - plans 3", ProjectRoot: repo, Questions: q1("four?")})
	if th4.ID == th1.ID {
		t.Fatalf("a gap over the window must start a new thread")
	}
	if th1.Project != filepath.Base(repo) || th1.Branch != "main" || th1.Title != "plans" || th1.Client != "Cursor" {
		t.Fatalf("thread fields: %+v", th1)
	}
}

func TestExplicitThreadAndTaskWin(t *testing.T) {
	s, now := clockStore(t)
	repo := mkRepo(t)
	_, a, _ := s.Create(CreateInput{CallerInfo: "Cursor Agent", ProjectRoot: repo, ThreadID: "work-1", Questions: q1("1")})
	*now = now.Add(5 * time.Hour)
	_, b, _ := s.Create(CreateInput{CallerInfo: "Claude Code", ProjectRoot: repo, ThreadID: "work-1", Questions: q1("2")})
	if a.ID != b.ID {
		t.Fatalf("explicit thread_id must join across client and gap")
	}
	_, c, _ := s.Create(CreateInput{CallerInfo: "Cursor Agent", ProjectRoot: repo, TaskID: "task-9", Questions: q1("3")})
	*now = now.Add(5 * time.Hour)
	_, d, _ := s.Create(CreateInput{CallerInfo: "Codex", ProjectRoot: repo, TaskID: "task-9", Questions: q1("4")})
	if c.ID != d.ID {
		t.Fatalf("task_id must join")
	}
	if c.ID == a.ID {
		t.Fatalf("task thread and explicit thread are distinct")
	}
}

func TestWorktreeFoldsIntoMainRepository(t *testing.T) {
	main := mkRepo(t)
	wt := t.TempDir()
	gitdir := filepath.Join(main, ".git", "worktrees", "feat")
	if err := os.MkdirAll(gitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o644)
	_ = os.WriteFile(filepath.Join(gitdir, "HEAD"), []byte("ref: refs/heads/feat\n"), 0o644)
	_ = os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644)
	sub := filepath.Join(wt, "pkg", "deep")
	_ = os.MkdirAll(sub, 0o755)
	k1, b1, w1 := ResolveProject(main)
	k2, b2, w2 := ResolveProject(sub)
	if k1 != k2 || k1 != filepath.Clean(main) {
		t.Fatalf("keys differ: %q vs %q", k1, k2)
	}
	if b1 != "main" || b2 != "feat" || w1 || !w2 {
		t.Fatalf("branch/worktree: %q %q %v %v", b1, b2, w1, w2)
	}
	if k, _, _ := ResolveProject(""); k != "" {
		t.Fatal("empty root has no key")
	}
}

func TestAskLifecycleAndLateAnswer(t *testing.T) {
	s, now := clockStore(t)
	repo := mkRepo(t)
	ev, cancel := s.Subscribe()
	defer cancel()
	a1, _, _ := s.Create(CreateInput{CallerInfo: "Cursor Agent", ProjectRoot: repo, Questions: q1("first?"), TimeoutMs: 60_000})
	if e := <-ev; e.Type != "ask_created" || e.AskID != a1.ID || e.Preview != "first?" {
		t.Fatalf("event %+v", e)
	}
	*now = now.Add(2 * time.Minute)
	s.Heartbeat(a1.ID)
	if n := s.Tick(); n != 1 {
		t.Fatalf("tick expired %d", n)
	}
	if r := s.Result(a1.ID); r.Status != "expired" {
		t.Fatalf("status %q", r.Status)
	}
	if err := s.Resolve(a1.ID, map[string]Answer{"a": {Selected: []string{"x"}}, "zzz": {Custom: "dropped"}}, nil); err != nil {
		t.Fatal(err)
	}
	th, _ := s.ThreadOfAsk(a1.ID)
	got, _ := s.Thread(th)
	if !got.Asks[0].Late || got.Asks[0].State != StateAnswered || len(got.Asks[0].Answers) != 1 {
		t.Fatalf("late answer not recorded: %+v", got.Asks[0])
	}
	// The next Ask in the thread hands the late answer over exactly once.
	a2, _, _ := s.Create(CreateInput{CallerInfo: "Cursor Agent", ProjectRoot: repo, Questions: q1("second?")})
	if err := s.Resolve(a2.ID, map[string]Answer{"a": {Selected: []string{"y"}}}, nil); err != nil {
		t.Fatal(err)
	}
	r := s.Result(a2.ID)
	if r.Status != "answered" || len(r.LateAnswers) != 1 || r.LateAnswers[0].AskID != a1.ID {
		t.Fatalf("late answers: %+v", r)
	}
	if r2 := s.Result(a2.ID); len(r2.LateAnswers) != 0 {
		t.Fatal("late answers must be delivered once")
	}
	if err := s.Resolve(a2.ID, nil, nil); !errors.Is(err, ErrFinal) {
		t.Fatalf("answered ask is final, got %v", err)
	}
	if err := s.Resolve("nope", nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestEndIsFinalAndLostAgentExpires(t *testing.T) {
	s, now := clockStore(t)
	a, _, _ := s.Create(CreateInput{CallerInfo: "X", Questions: q1("?")})
	if err := s.End(a.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Resolve(a.ID, nil, nil); !errors.Is(err, ErrFinal) {
		t.Fatalf("ended ask cannot be answered: %v", err)
	}
	b, _, _ := s.Create(CreateInput{CallerInfo: "X", Questions: q1("again?")})
	*now = now.Add(6 * time.Minute)
	if s.Tick() != 1 {
		t.Fatal("silent agent should expire")
	}
	if r := s.Result(b.ID); r.Status != "expired" || r.Message != "Agent connection lost" {
		t.Fatalf("%+v", r)
	}
}

func TestValidationAndAttachments(t *testing.T) {
	s := NewMemory()
	if _, _, err := s.Create(CreateInput{Questions: nil}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no questions: %v", err)
	}
	if _, _, err := s.Create(CreateInput{Questions: []Question{{Text: "  "}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank question: %v", err)
	}
	a, _, err := s.Create(CreateInput{CallerInfo: "X", Questions: []Question{{Text: "pick", Type: "weird"}}, TimeoutMs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if a.TimeoutMs != minTimeout.Milliseconds() || a.Questions[0].Type != "text" || a.Questions[0].ID != "q1" {
		t.Fatalf("normalised: %+v", a)
	}
	err = s.Resolve(a.ID, map[string]Answer{"q1": {Custom: "hi"}}, []Attachment{
		{MimeType: "image/png", DataBase64: "AAAA", Name: "ok.png"},
		{MimeType: "text/plain", DataBase64: "AAAA"},
		{MimeType: "image/png"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r := s.Result(a.ID); len(r.Attachments) != 1 || r.Attachments[0].Name != "ok.png" {
		t.Fatalf("attachments: %+v", r.Attachments)
	}
	th, _ := s.ThreadOfAsk(a.ID)
	if got, _ := s.Thread(th); got.Asks[0].AttachmentCount != 1 {
		t.Fatal("history keeps the count")
	}
}

func TestPersistenceAndBounds(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.Create(CreateInput{CallerInfo: "Cursor Agent - t", ProjectRoot: "/tmp/proj-x", Questions: q1("persist?")})
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if r := s2.Result(a.ID); r.Status != "pending" {
		t.Fatalf("pending ask must survive a restart: %+v", r)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "feedback.json")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if err := s2.Resolve(a.ID, map[string]Answer{"a": {Selected: []string{"x"}}}, nil); err != nil {
		t.Fatal(err)
	}

	m := NewMemory()
	for i := 0; i < maxAsksPerThread+5; i++ {
		x, _, _ := m.Create(CreateInput{CallerInfo: "X", ProjectRoot: "/tmp/p", Questions: q1("q")})
		_ = m.Resolve(x.ID, nil, nil)
	}
	th := m.Threads(Filter{})
	got, _ := m.Thread(th[0].ID)
	if len(got.Asks) != maxAsksPerThread {
		t.Fatalf("asks per thread bound: %d", len(got.Asks))
	}

	_ = os.WriteFile(filepath.Join(dir, "feedback.json"), []byte("{broken"), 0o600)
	if s3, err := Open(dir); err != nil || len(s3.Threads(Filter{})) != 0 {
		t.Fatalf("a corrupt file starts empty: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "feedback.json.corrupt")); err != nil {
		t.Fatal("corrupt file must be kept for inspection")
	}
}

func TestListingAndRecall(t *testing.T) {
	s, now := clockStore(t)
	a, _, _ := s.Create(CreateInput{CallerInfo: "Cursor Agent - a", ProjectRoot: "/tmp/alpha", Questions: q1("Which database?")})
	_ = s.Resolve(a.ID, map[string]Answer{"a": {Selected: []string{"Postgres"}, Custom: "with pgvector"}}, nil)
	*now = now.Add(time.Minute)
	b, _, _ := s.Create(CreateInput{CallerInfo: "Claude Code - b", ProjectRoot: "/tmp/beta", Questions: q1("Ship it?")})
	rows := s.Threads(Filter{})
	if len(rows) != 2 || rows[0].Waiting != 1 || rows[0].Project != "beta" {
		t.Fatalf("waiting thread first: %+v", rows)
	}
	if w := s.Threads(Filter{Waiting: true}); len(w) != 1 || w[0].Project != "beta" {
		t.Fatalf("waiting filter: %+v", w)
	}
	if p := s.Threads(Filter{Project: "alpha"}); len(p) != 1 {
		t.Fatalf("project filter: %+v", p)
	}
	items := s.Recall("/tmp/alpha", "database", 5)
	if len(items) != 1 || items[0].Pairs[0].Answer != "Postgres; with pgvector" {
		t.Fatalf("recall: %+v", items)
	}
	if s.Recall("/tmp/alpha", "nothing-like-this", 5) != nil {
		t.Fatal("no match must be empty")
	}
	if got := s.Recall("/tmp/beta", "", 5); len(got) != 0 {
		t.Fatalf("an unanswered ask is not recalled: %+v", got)
	}
	_ = b
}

func TestAbandonedAskStaysAnswerableAsLate(t *testing.T) {
	s, _ := clockStore(t)
	repo := mkRepo(t)
	a, th, _ := s.Create(CreateInput{CallerInfo: "Cursor Agent", ProjectRoot: repo, Questions: q1("ship?")})
	if got := time.Duration(a.TimeoutMs) * time.Millisecond; got != 4*time.Hour {
		t.Fatalf("default timeout is 4 h, got %v", got)
	}
	if err := s.Abandon(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Abandon(a.ID); err == nil {
		t.Fatal("a finished ask cannot be abandoned twice")
	}
	if err := s.Resolve(a.ID, map[string]Answer{"q1": {Selected: []string{"yes"}}}, nil); err != nil {
		t.Fatalf("an abandoned ask is still answerable: %v", err)
	}
	b, _, _ := s.Create(CreateInput{CallerInfo: "Cursor Agent", ProjectRoot: repo, Questions: q1("again?")})
	if b.ThreadID != th.ID {
		t.Fatal("same thread expected")
	}
	if err := s.Abandon(b.ID); err != nil {
		t.Fatal(err)
	}
	r := s.Result(b.ID)
	if r.Status == "ended" || len(r.LateAnswers) != 1 {
		t.Fatalf("the earlier answer must come back as a late answer, not an end: %+v", r)
	}
}

func TestTaskIsKeptFromTheFirstAskThatHasOne(t *testing.T) {
	s, _ := clockStore(t)
	repo := mkRepo(t)
	_, th, _ := s.Create(CreateInput{CallerInfo: "Cursor Agent", ProjectRoot: repo, Questions: q1("one?")})
	if got, _ := s.Thread(th.ID); got.Task != "" {
		t.Fatalf("no task given: %q", got.Task)
	}
	s.Create(CreateInput{CallerInfo: "Cursor Agent", ProjectRoot: repo, Task: "  Ship the **thing**  ", Questions: q1("two?")})
	s.Create(CreateInput{CallerInfo: "Cursor Agent", ProjectRoot: repo, Task: "another", Questions: q1("three?")})
	if got, _ := s.Thread(th.ID); got.Task != "Ship the **thing**" {
		t.Fatalf("the first task wins: %q", got.Task)
	}
}

func TestDeletePurgeAndRetentionSparePendingThreads(t *testing.T) {
	s, now := clockStore(t)
	mk := func(client string) (string, string) {
		a, th, err := s.Create(CreateInput{CallerInfo: client, ProjectRoot: mkRepo(t), Questions: q1("q?")})
		if err != nil {
			t.Fatal(err)
		}
		return a.ID, th.ID
	}
	oldAsk, oldThread := mk("Cursor Agent")
	_ = s.Resolve(oldAsk, map[string]Answer{"a": {Selected: []string{"x"}}}, nil)
	waitAsk, waitThread := mk("Claude Code")
	_ = waitAsk
	*now = now.Add(40 * 24 * time.Hour)
	freshAsk, freshThread := mk("Codex")
	_ = s.Resolve(freshAsk, map[string]Answer{"a": {Selected: []string{"y"}}}, nil)

	if n := s.Purge(30*24*time.Hour, true); n != 1 {
		t.Fatalf("dry run counts only the old finished thread: %d", n)
	}
	if len(s.Threads(Filter{})) != 3 {
		t.Fatal("a dry run removes nothing")
	}
	if n := s.DeleteThreads([]string{waitThread, "nope"}); n != 0 {
		t.Fatalf("a thread an Agent waits on is never deleted: %d", n)
	}
	// The waiting thread is 40 days old too but pending, so a purge spares it.
	if n := s.Purge(30*24*time.Hour, false); n != 1 {
		t.Fatalf("purge: %d", n)
	}
	if _, ok := s.Thread(oldThread); ok {
		t.Fatal("old finished thread must be gone")
	}
	if _, ok := s.ThreadOfAsk(oldAsk); ok {
		t.Fatal("its asks must leave the index too")
	}
	if n := s.DeleteThreads([]string{freshThread}); n != 1 {
		t.Fatalf("delete: %d", n)
	}

	if err := s.SetRetentionDays(-1); err == nil {
		t.Fatal("a negative retention is invalid")
	}
	mkOld := func() string {
		a, th, _ := s.Create(CreateInput{CallerInfo: "Zed", ProjectRoot: mkRepo(t), Questions: q1("q?")})
		_ = s.Resolve(a.ID, map[string]Answer{"a": {Selected: []string{"x"}}}, nil)
		return th.ID
	}
	old := mkOld()
	*now = now.Add(100 * 24 * time.Hour)
	_, livePending, _ := s.Create(CreateInput{CallerInfo: "Aider", ProjectRoot: mkRepo(t), Questions: q1("q?")})
	if s.Tick(); len(s.Threads(Filter{})) != 3 {
		t.Fatal("retention 0 keeps everything")
	}
	if err := s.SetRetentionDays(90); err != nil {
		t.Fatal(err)
	}
	s.Tick()
	if _, ok := s.Thread(old); ok {
		t.Fatal("retention must remove a finished thread past the window")
	}
	if _, ok := s.Thread(livePending.ID); !ok {
		t.Fatal("retention never removes a thread with a pending ask")
	}
}

func TestSearchReadsTitlesQuestionsAndAnswers(t *testing.T) {
	s, _ := clockStore(t)
	a, th, _ := s.Create(CreateInput{CallerInfo: "Cursor Agent", ProjectRoot: mkRepo(t), Description: "Migrating the BILLING tables", Questions: q1("Which index?")})
	_ = s.Resolve(a.ID, map[string]Answer{"a": {Custom: "use the composite one"}}, nil)
	s.Create(CreateInput{CallerInfo: "Claude Code", ProjectRoot: mkRepo(t), Questions: q1("Unrelated?")})
	for q, want := range map[string]int{"billing": 1, "composite": 1, "which index": 1, "unrelated": 1, "zzz": 0, " ": 0} {
		if got := s.Search(q); len(got) != want || (want == 1 && q != "unrelated" && got[0] != th.ID) {
			t.Errorf("search %q: %v", q, got)
		}
	}
}
