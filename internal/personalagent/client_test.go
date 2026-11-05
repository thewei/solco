package personalagent

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

type call struct {
	stdin string
	args  []string
}

func fake(out string, runErr error, calls *[]call) Exec {
	return func(ctx context.Context, stdin string, args ...string) ([]byte, error) {
		*calls = append(*calls, call{stdin, args})
		return []byte(out), runErr
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(fixtures + "/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestOverviewParsesAndPassesArgs(t *testing.T) {
	var calls []call
	c := NewClientWithExec(fake(readFixture(t, "overview"), nil, &calls))
	ov, err := c.Overview(t.Context())
	if err != nil || len(ov.Assistants) != 1 {
		t.Fatalf("ov=%+v err=%v", ov, err)
	}
	if strings.Join(calls[0].args, " ") != "assistant overview" {
		t.Fatalf("args = %v", calls[0].args)
	}
}

// A brief travels on stdin, never in argv.
func TestBodyGoesOnStdinNotArgv(t *testing.T) {
	var calls []call
	c := NewClientWithExec(fake(readFixture(t, "task-created"), nil, &calls))
	_, err := c.CreateTask(t.Context(), "req-1", CreateTaskInput{AssistantID: "a_1", Kind: "research", Title: "T", Brief: "SECRET BRIEF"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(calls[0].args, " "), "SECRET") {
		t.Fatalf("brief leaked into argv: %v", calls[0].args)
	}
	if !strings.Contains(calls[0].stdin, "SECRET BRIEF") || !strings.Contains(calls[0].stdin, `"requestId":"req-1"`) {
		t.Fatalf("stdin = %s", calls[0].stdin)
	}
}

// The CLI reports refusals as JSON on stdout with exit 1: the code survives.
func TestCodedErrorSurvivesNonZeroExit(t *testing.T) {
	var calls []call
	c := NewClientWithExec(fake(readFixture(t, "error-stale-revision"), errors.New("exit status 1"), &calls))
	_, err := c.GoalAction(t.Context(), "r", "pause", "g_1", 1, "")
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != "stale_revision" {
		t.Fatalf("err = %v", err)
	}
	if !IsCode(err, "stale_revision") {
		t.Fatal("IsCode misses it")
	}
}

func TestGarbageOutputIsAnError(t *testing.T) {
	var calls []call
	c := NewClientWithExec(fake("not json", nil, &calls))
	if _, err := c.Overview(t.Context()); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestMissingBinaryIsAnError(t *testing.T) {
	var calls []call
	c := NewClientWithExec(fake("", errors.New("no such file"), &calls))
	if _, err := c.Overview(t.Context()); err == nil || IsCode(err, "stale_revision") {
		t.Fatalf("err = %v", err)
	}
}

func TestGateNeedsTheAssistantsCommand(t *testing.T) {
	var calls []call
	ok := NewClientWithExec(fake(readFixture(t, "overview"), nil, &calls))
	if !ok.Available(t.Context()) {
		t.Fatal("a plugin that answers `assistant overview` must pass the gate")
	}
	// An older plugin refuses the unknown command: gate closed, no UI.
	old := NewClientWithExec(fake("unknown command: assistant", errors.New("exit status 1"), &calls))
	if old.Available(t.Context()) {
		t.Fatal("an older plugin must not open the gate")
	}
	var none *Client
	if none.Available(t.Context()) {
		t.Fatal("nil client open")
	}
}

// Q02: the Owner's words travel on stdin, a task with no review is (nil, nil),
// and the review commands name the right verbs.
func TestReviewClientVerbsAndBodies(t *testing.T) {
	var calls []call
	c := NewClientWithExec(fake(readFixture(t, "review-shape"), nil, &calls))
	if _, err := c.ResumeReview(t.Context(), "r-1", "v_1", "SECRET FEEDBACK"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RejectReview(t.Context(), "r-2", "v_1", "wrong approach"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls[0].args, " "); got != "assistant review resume v_1" || strings.Contains(got, "SECRET") {
		t.Fatalf("resume args = %q", got)
	}
	if !strings.Contains(calls[0].stdin, `"feedback":"SECRET FEEDBACK"`) || !strings.Contains(calls[0].stdin, `"requestId":"r-1"`) {
		t.Fatalf("resume stdin = %s", calls[0].stdin)
	}
	if got := strings.Join(calls[1].args, " "); got != "assistant review reject v_1" || !strings.Contains(calls[1].stdin, `"reason":"wrong approach"`) {
		t.Fatalf("reject = %q %s", got, calls[1].stdin)
	}
	none := NewClientWithExec(fake(`{"schema":1,"error":{"code":"not_found","message":"no review"}}`, errors.New("exit 1"), &calls))
	if rv, err := none.TaskReview(t.Context(), "t_1"); rv != nil || err != nil {
		t.Fatalf("no review = %+v %v", rv, err)
	}
}
