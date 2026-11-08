package personalagent

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// briefRig starts a Coordinator over and over against one Assistant, each time
// on a launcher that finishes its turn at once, and hands back the spec the
// Dispatcher built.
type briefRig struct {
	t      *testing.T
	s      *Store
	a      Assistant
	resume string
	n      int
}

// start brings a new Coordinator up: the previous one is stopped first and
// Herdr is taken to have closed its pane.
func (r *briefRig) start() Coordinator {
	r.t.Helper()
	r.n++
	id := strings.Repeat("x", r.n)
	if r.n > 1 {
		if _, _, err := r.s.StopCoordinator("brief-stop-"+id, r.a.ID); err != nil {
			r.t.Fatal(err)
		}
	}
	gone := func(string, string, string) (bool, error) { return true, nil }
	c, _, err := r.s.StartCoordinatorAs("brief-start-"+id, r.a.ID, "", "", gone)
	if err != nil {
		r.t.Fatal(err)
	}
	return c
}

func (r *briefRig) launch() LaunchSpec {
	r.t.Helper()
	c := r.start()
	f := &fakeLauncher{}
	d := &Dispatcher{Store: r.s, Launcher: f, Claimant: "test", ResumeSession: func(string) string { return r.resume }}
	if _, err := d.RunTask(r.t.Context(), c.TaskID); err != nil {
		r.t.Fatal(err)
	}
	return f.specs[0]
}

func newBriefRig(t *testing.T, opts ...Option) *briefRig {
	t.Helper()
	s, _ := openTest(t, opts...)
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &briefRig{t: t, s: s, a: mustAssistant(t, s, "brief-skip", "Ada"), resume: transcript}
}

// A restart whose launch prompt is byte-identical to what the resumed
// transcript already holds types nothing; the conversation is still resumed.
func TestRestartTypesNoBriefThatTheTranscriptAlreadyHolds(t *testing.T) {
	r := newBriefRig(t)

	first := r.launch()
	if !strings.Contains(first.Prompt, "You are the Coordinator of the Solco Assistant") || first.BriefHash == "" {
		t.Fatalf("a first launch must type the brief: hash=%q", first.BriefHash)
	}
	if !strings.HasPrefix(first.Prompt, "Solco-Run-Id:") {
		t.Fatalf("the brief carries its run marker: %.60q", first.Prompt)
	}

	second := r.launch()
	if second.Prompt != "" || second.BriefHash != "" {
		t.Fatalf("an identical restart typed the brief again: prompt=%d chars hash=%q", len(second.Prompt), second.BriefHash)
	}
	if !containsPair(second.AgentArgs, "--resume", r.resume) {
		t.Fatalf("the conversation must still be resumed: %v", second.AgentArgs)
	}
	for _, arg := range second.AgentArgs {
		if arg == "--extension" {
			return
		}
	}
	t.Fatalf("the tool policy must still be passed: %v", second.AgentArgs)
}

// Whatever changes the text changes what is sent: the Assistant's own
// instructions, or the transcript being resumed.
func TestRestartTypesTheBriefAgainWhenItChangedOrTheTranscriptIsAnother(t *testing.T) {
	r := newBriefRig(t)
	r.launch()
	if r.launch().Prompt != "" {
		t.Fatal("precondition: an identical restart skips the brief")
	}

	ins, err := r.s.GetInstructions(r.a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.s.SetInstructions("brief-edit", r.a.ID, "Always answer in haiku.", ins.Revision); err != nil {
		t.Fatal(err)
	}
	edited := r.launch()
	if !strings.Contains(edited.Prompt, "haiku") || edited.BriefHash == "" {
		t.Fatalf("new instructions must reach the Agent: hash=%q", edited.BriefHash)
	}
	if r.launch().Prompt != "" {
		t.Fatal("the edited brief, once delivered, is skipped like any other")
	}

	other := filepath.Join(filepath.Dir(r.resume), "other.jsonl")
	if err := os.WriteFile(other, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.resume = other
	if got := r.launch(); got.Prompt == "" || got.BriefHash == "" {
		t.Fatal("a transcript the brief was never typed into must get it")
	}
}

// A delivery is only trusted for a day: OMP compacts long conversations.
func TestRestartTypesTheBriefAgainOnceTheDeliveryIsOld(t *testing.T) {
	var offset atomic.Int64
	r := newBriefRig(t, WithClock(func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }))
	r.launch()
	if r.launch().Prompt != "" {
		t.Fatal("precondition: an identical restart skips the brief")
	}
	offset.Store(int64(briefMaxAge + time.Hour))
	if got := r.launch(); got.Prompt == "" {
		t.Fatal("a day-old delivery must be repeated")
	}
}

// A launch that did not finish its turn proves nothing about the transcript.
func TestARunThatDidNotFinishRecordsNoBrief(t *testing.T) {
	r := newBriefRig(t)
	c := r.start()
	f := &fakeLauncher{workErr: ErrWorkCancelled}
	d := &Dispatcher{Store: r.s, Launcher: f, Claimant: "test", ResumeSession: func(string) string { return r.resume }}
	_, _ = d.RunTask(t.Context(), c.TaskID)

	if got := r.launch(); got.Prompt == "" {
		t.Fatal("a cancelled launch never delivered anything; the next one must type the brief")
	}
}
