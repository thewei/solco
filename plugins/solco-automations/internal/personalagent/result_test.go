package personalagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- a tiny OMP transcript writer: the same row shapes OMP 18.16 writes ----

type tx struct{ rows []map[string]any }

func newTx(sessionID string) *tx {
	return &tx{rows: []map[string]any{{"type": "session", "version": 3, "id": sessionID, "cwd": "/tmp/x"}}}
}

func (t *tx) user(text string) *tx {
	t.rows = append(t.rows, map[string]any{"type": "message", "id": "u" + strings.Repeat("x", len(t.rows)), "message": map[string]any{
		"role": "user", "content": []map[string]any{{"type": "text", "text": text}}}})
	return t
}

func (t *tx) assistant(id, stop string, blocks ...map[string]any) *tx {
	t.rows = append(t.rows, map[string]any{"type": "message", "id": id, "message": map[string]any{
		"role": "assistant", "stopReason": stop, "content": blocks}})
	return t
}

func (t *tx) result(tool, text string, isErr bool) *tx {
	t.rows = append(t.rows, map[string]any{"type": "message", "id": "r" + strings.Repeat("y", len(t.rows)), "message": map[string]any{
		"role": "toolResult", "toolName": tool, "isError": isErr, "content": []map[string]any{{"type": "text", "text": text}}}})
	return t
}

func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }
func toolCall(name string, args map[string]any) map[string]any {
	return map[string]any{"type": "toolCall", "id": "c1", "name": name, "arguments": args}
}

func (t *tx) write(t2 *testing.T, dir, name string) {
	t2.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t2.Fatal(err)
	}
	var b strings.Builder
	for _, r := range t.rows {
		raw, _ := json.Marshal(r)
		b.Write(raw)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o600); err != nil {
		t2.Fatal(err)
	}
}

func workerTx(runID string) *tx {
	return newTx("sess-1").user(RunMarkerLine(runID)+"\nDigest X.com").
		assistant("m1", "toolUse", text("reading the skill"), toolCall("read", map[string]any{"path": "/Users/u/.agents/skills/ego-browser/SKILL.md"})).
		result("read", "ego-browser skill ...", false).
		assistant("m2", "toolUse", toolCall("bash", map[string]any{"command": "ego-browser open https://x.com/home"})).
		result("bash", "opened https://x.com/home and found https://x.com/a/status/1 (12k likes)", false).
		assistant("m3", "stop", text("Top post: https://x.com/a/status/1 — 12k likes. Also https://x.com/made/up."))
}

func TestGapW02ReaderReturnsTheFinalAnswerWithProvenance(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "r_1")
	workerTx("r_1").write(t, filepath.Join(dir, "--cwd--"), "s.jsonl")
	got, err := ReadOMPWorkerResult(dir, "r_1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Evidence != EvidenceOMPTranscript || got.AgentSessionID != "sess-1" || got.MessageID != "m3" || got.SHA256 == "" {
		t.Fatalf("provenance: %+v", got)
	}
	if !strings.Contains(got.Summary, "Top post") {
		t.Fatalf("summary: %q", got.Summary)
	}
	if got.Tools["read"] != 1 || got.Tools["bash"] != 1 || got.ToolErrors != 0 {
		t.Fatalf("tools: %+v errors=%d", got.Tools, got.ToolErrors)
	}
	if len(got.Skills) != 1 || got.Skills[0] != "ego-browser" {
		t.Fatalf("skill use must come from the transcript: %+v", got.Skills)
	}
	grounded := map[string]bool{}
	for _, s := range got.Sources {
		grounded[s.URL] = s.Grounded
	}
	if !grounded["https://x.com/a/status/1"] {
		t.Fatalf("a URL a tool really returned must be grounded: %+v", got.Sources)
	}
	if g, ok := grounded["https://x.com/made/up"]; !ok || g {
		t.Fatalf("a URL no tool returned must be listed but NOT grounded: %+v", got.Sources)
	}
}

func TestGapW02ReaderRefusesAnythingThatIsNotAFinishedAnswer(t *testing.T) {
	cases := map[string]func(dir string){
		"no directory": func(string) {},
		"ended in a tool call": func(dir string) {
			newTx("s").user(RunMarkerLine("r_1")).assistant("m1", "toolUse", toolCall("bash", map[string]any{"command": "ls"})).write(t, dir, "s.jsonl")
		},
		"aborted": func(dir string) {
			newTx("s").user(RunMarkerLine("r_1")).assistant("m1", "aborted", text("partial")).write(t, dir, "s.jsonl")
		},
		"model error": func(dir string) {
			newTx("s").user(RunMarkerLine("r_1")).assistant("m1", "error", text("boom")).write(t, dir, "s.jsonl")
		},
		"empty final text": func(dir string) {
			newTx("s").user(RunMarkerLine("r_1")).assistant("m1", "stop", text("   ")).write(t, dir, "s.jsonl")
		},
		"another run's transcript": func(dir string) {
			newTx("s").user(RunMarkerLine("r_other")).assistant("m1", "stop", text("done")).write(t, dir, "s.jsonl")
		},
		"two root sessions": func(dir string) {
			workerTx("r_1").write(t, dir, "a.jsonl")
			newTx("sess-2").user(RunMarkerLine("r_1")).assistant("m9", "stop", text("other")).write(t, dir, "b.jsonl")
		},
		"answer before the marker only": func(dir string) {
			newTx("s").assistant("m0", "stop", text("hello")).user(RunMarkerLine("r_1")).write(t, dir, "s.jsonl")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "r_1")
			if name != "no directory" {
				_ = os.MkdirAll(dir, 0o700)
			}
			setup(dir)
			if _, err := ReadOMPWorkerResult(dir, "r_1"); !errors.Is(err, ErrNoResult) {
				t.Fatalf("want ErrNoResult, got %v", err)
			}
		})
	}
}

func TestGapW02ReaderIgnoresChildSessionsAndRefusesASymlinkedDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "r_1")
	workerTx("r_1").write(t, dir, "root.jsonl")
	child := newTx("child-1")
	child.rows[0]["parentSession"] = "sess-1"
	child.user("subtask").assistant("c9", "stop", text("child says")).write(t, dir, "child.jsonl")
	got, err := ReadOMPWorkerResult(dir, "r_1")
	if err != nil || got.AgentSessionID != "sess-1" {
		t.Fatalf("a child session must not become the answer: %+v %v", got, err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOMPWorkerResult(link, "r_1"); !errors.Is(err, ErrNoResult) {
		t.Fatalf("a symlinked session dir is not evidence: %v", err)
	}
}

// ---- Dispatcher: an Agent going idle is not a finished task ----

type collectingLauncher struct {
	fakeLauncher
	cmu     sync.Mutex
	collect []func() (WorkerResult, error)
	calls   int
}

func (c *collectingLauncher) Collect(context.Context, Session, LaunchSpec) (WorkerResult, error) {
	c.cmu.Lock()
	defer c.cmu.Unlock()
	i := c.calls
	c.calls++
	if i >= len(c.collect) {
		i = len(c.collect) - 1
	}
	return c.collect[i]()
}

func okResult(summary string) func() (WorkerResult, error) {
	return func() (WorkerResult, error) {
		return WorkerResult{Summary: summary, Evidence: EvidenceOMPTranscript, AgentSessionID: "s1", MessageID: "m3", SHA256: "abc", Tools: map[string]int{"bash": 1}}, nil
	}
}

func noResult() (WorkerResult, error) { return WorkerResult{}, ErrNoResult }

func newResultDispatcher(s *Store, l Launcher) *Dispatcher {
	return &Dispatcher{Store: s, Launcher: l, Claimant: "daemon", CollectTries: 2, CollectDelay: time.Millisecond}
}

// Gap W02 RED→GREEN: before this change dispatch.go finished every quiet Agent
// with FinishRun(RunDone, ""), so a research run whose Agent left nothing read
// was reported done with an empty result.
func TestGapW02QuietAgentWithoutAnswerIsUncertainNeverDone(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	l := &collectingLauncher{collect: []func() (WorkerResult, error){noResult}}
	d := newResultDispatcher(s, l)
	run, err := d.RunTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if run.Status != RunUncertain || !strings.HasPrefix(run.Error, ResultUnknownPrefix) || run.Result != "" {
		t.Fatalf("run = %+v", run)
	}
	if l.calls != 2 {
		t.Fatalf("transcript reads must be bounded and retried: %d", l.calls)
	}
	got, _ := s.GetTask(task.ID)
	if got.State != TaskUncertain || !got.NeedsYou {
		t.Fatalf("task = %+v", got)
	}
	if _, ok, _ := s.RunResult(run.ID); ok {
		t.Fatal("no result may exist for a run with no answer")
	}
	if l.works != 1 || l.provisions != 1 {
		t.Fatalf("an unreadable answer must never relaunch: provisions=%d works=%d", l.provisions, l.works)
	}
}

func TestGapW02ReadableAnswerFinishesWithStoredEvidence(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	l := &collectingLauncher{collect: []func() (WorkerResult, error){noResult, okResult("Three posts. https://x.com/a/status/1")}}
	d := newResultDispatcher(s, l)
	run, err := d.RunTask(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != RunDone || !strings.Contains(run.Result, "Three posts") {
		t.Fatalf("a late-flushed transcript must still finish the run: %+v", run)
	}
	res, ok, err := s.RunResult(run.ID)
	if err != nil || !ok || res.MessageID != "m3" || res.Evidence != EvidenceOMPTranscript {
		t.Fatalf("stored result: %+v %v %v", res, ok, err)
	}
	// The Agent is told which run it works for, and its transcript is pinned.
	spec := l.specs[0]
	if !strings.HasPrefix(spec.Prompt, RunMarkerLine(run.ID)) || !spec.NeedResult {
		t.Fatalf("prompt must open with the run marker: %q", spec.Prompt[:min(60, len(spec.Prompt))])
	}
	if !containsPair(spec.AgentArgs, "--session-dir", WorkerSessionDir(run.ID)) {
		t.Fatalf("session dir not pinned: %v", spec.AgentArgs)
	}
}

func containsPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestGapW02RecollectResolvesAnUnknownRunOnce(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	l := &collectingLauncher{collect: []func() (WorkerResult, error){noResult, noResult, okResult("late answer")}}
	d := newResultDispatcher(s, l)
	run, _ := d.RunTask(t.Context(), task.ID)
	if run.Status != RunUncertain {
		t.Fatalf("setup: %+v", run)
	}
	again, err := d.Recollect(t.Context(), run.ID)
	if err != nil || again.Status != RunDone || again.Result != "late answer" {
		t.Fatalf("recollect: %+v %v", again, err)
	}
	if got, _ := s.GetTask(task.ID); got.State != TaskDone {
		t.Fatalf("task = %s", got.State)
	}
	// Not repeatable: a done run cannot be recollected.
	if _, err := d.Recollect(t.Context(), run.ID); !errors.Is(err, ErrBadState) {
		t.Fatalf("second recollect: %v", err)
	}
}

func TestGapW02ReconcilePendingRecollectsWithoutRelaunch(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	l := &collectingLauncher{collect: []func() (WorkerResult, error){noResult, noResult, okResult("now readable")}}
	d := newResultDispatcher(s, l)
	run, _ := d.RunTask(t.Context(), task.ID)
	if n, err := d.ReconcilePending(t.Context()); err != nil || n != 1 {
		t.Fatalf("reconcile: %d %v", n, err)
	}
	if got, _ := s.GetRun(run.ID); got.Status != RunDone || got.Result != "now readable" {
		t.Fatalf("run = %+v", got)
	}
	if l.provisions != 1 || l.works != 1 {
		t.Fatalf("recollect must not launch: %d/%d", l.provisions, l.works)
	}
}

func TestGapW02ChatAgentsAreNotWorkers(t *testing.T) {
	s, _ := openTest(t)
	d := newResultDispatcher(s, &collectingLauncher{collect: []func() (WorkerResult, error){noResult}})
	if d.requiresResult(Task{Kind: KindCoordinator}) || d.requiresResult(Task{Kind: KindGroupAgent}) {
		t.Fatal("a standing Chat agent has no final answer to wait for")
	}
	if !d.requiresResult(Task{Kind: KindResearch}) || !d.requiresResult(Task{Kind: KindCoding}) || !d.requiresResult(Task{Kind: KindManual}) {
		t.Fatal("every Worker kind needs a result")
	}
	if (&Dispatcher{Store: s, Launcher: &fakeLauncher{}}).requiresResult(Task{Kind: KindResearch}) {
		t.Fatal("a launcher with no reader keeps the old behaviour (test doubles only)")
	}
}
