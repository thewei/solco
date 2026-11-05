package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
	"github.com/thewei/solco/plugins/solco-automations/internal/herdr"
)

func writeTranscript(t *testing.T, dir, userText string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	row := `{"type":"session","id":"s1"}` + "\n" +
		`{"type":"message","id":"u1","message":{"role":"user","content":[{"type":"text","text":` + jsonString(userText) + `}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(row), 0o600); err != nil {
		t.Fatal(err)
	}
}

func jsonString(s string) string {
	b := []byte{'"'}
	for _, r := range s {
		switch r {
		case '"':
			b = append(b, '\\', '"')
		case '\n':
			b = append(b, '\\', 'n')
		default:
			b = append(b, string(r)...)
		}
	}
	return string(append(b, '"'))
}

// Gap W10 / AC-07 RED→GREEN: a prompt whose own transcript already holds it was
// delivered. A fast Agent can finish inside herdr's five-second window, which
// reads as "stalled"; Enter and a retype would then run the whole task twice.
func TestSubmitNeverRepeatsAPromptTheTranscriptAlreadyHolds(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "r_9")
	prompt := "Solco-Run-Id: r_9\n\nDo the work."
	writeTranscript(t, dir, prompt)
	ops := &fakeOps{
		agentSubmit: func(string, string) error { return apiErr("agent prompt", herdr.CodeStalled) },
		agentStatus: func(string) (string, error) { return "idle", nil }, // already finished
	}
	a := config.Automation{Prompt: prompt, AgentArgs: []string{"--session-dir", dir}}
	if err := agentWorkWith(ops, a).submit(Session{PaneID: "p"}); err != nil {
		t.Fatalf("a delivered prompt is not a failure: %v", err)
	}
	if ops.submits != 1 || ops.pending != 0 {
		t.Fatalf("prompt repeated: submits=%d pending=%d", ops.submits, ops.pending)
	}
}

func TestSubmitStillNudgesWhenTheTranscriptDoesNotHoldThePrompt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "r_9")
	writeTranscript(t, dir, "something else entirely")
	ops := &fakeOps{
		agentSubmit: func(string, string) error { return apiErr("agent prompt", herdr.CodeStalled) },
		agentStatus: func(string) (string, error) { return "idle", nil },
	}
	a := config.Automation{Prompt: "Solco-Run-Id: r_9\n\nDo the work.", AgentArgs: []string{"--session-dir", dir}}
	if err := agentWorkWith(ops, a).submit(Session{PaneID: "p"}); err == nil {
		t.Fatal("an undelivered prompt must still fail after its nudges")
	}
	if ops.submits != 2 || ops.pending != 1 {
		t.Fatalf("nudges lost: submits=%d pending=%d", ops.submits, ops.pending)
	}
}

func TestPromptReceiptNeedsAMarkerAndAPinnedDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "r_9")
	writeTranscript(t, dir, "Solco-Run-Id: r_9\n")
	for name, a := range map[string]config.Automation{
		"no session dir": {Prompt: "Solco-Run-Id: r_9\nx"},
		"no marker":      {Prompt: "plain prompt", AgentArgs: []string{"--session-dir", dir}},
		"dir missing":    {Prompt: "Solco-Run-Id: r_9\nx", AgentArgs: []string{"--session-dir", filepath.Join(dir, "nope")}},
	} {
		if promptDelivered(a) {
			t.Errorf("%s: must not claim a receipt", name)
		}
	}
	if !promptDelivered(config.Automation{Prompt: "Solco-Run-Id: r_9\nx", AgentArgs: []string{"--session-dir", dir}}) {
		t.Error("marker + pinned dir + matching user turn is a receipt")
	}
}
