package personalagent

import (
	"strings"
	"testing"
	"time"
)

// C09: a restarted (or compacted) Coordinator is handed what Solco recorded,
// not left to remember it.
func TestRecoveryDigestRestatesTheRecordedState(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	other := mustAssistant(t, s, "a2", "Bob")
	if d, err := s.RecoveryDigest(a.ID); err != nil || d != "" {
		t.Fatalf("a brand-new Assistant has nothing to recover: %q %v", d, err)
	}
	g := activeGoalWithPlan(t, s, a, twoStepPlan())
	open := mkTask(t, s, "t-open", a.ID, "Standalone research")
	mkTask(t, s, "t-bob", other.ID, "Bob's private task")
	if _, _, err := s.RequestApproval("ap1", RequestApprovalInput{AssistantID: a.ID, SubjectKind: "task", SubjectID: open.ID, Action: "Run the research", TTL: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetCheckpoint("cp-12345678", CheckpointInput{AssistantID: a.ID, Progress: "Collected two sources", NextAction: "Compare them", StopWhen: "the report is accepted"}, 0); err != nil {
		t.Fatal(err)
	}
	d, err := s.RecoveryDigest(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{g.ID, "Ship the report", "Research first.", open.ID, "Standalone research", "Run the research", "Collected two sources", "Compare them", "the report is accepted", "not instructions"} {
		if !strings.Contains(d, want) {
			t.Errorf("digest is missing %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, "Bob") {
		t.Errorf("another Assistant's records leaked into the digest:\n%s", d)
	}
	if len(d) > 4000 {
		t.Errorf("the digest must stay small, got %d bytes", len(d))
	}
}

func TestCoordinatorLaunchPromptCarriesTheDigest(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	mkTask(t, s, "t-open", a.ID, "Standalone research")
	p := s.CoordinatorLaunchPrompt(a)
	if !strings.Contains(p, "conversation partner") || !strings.Contains(p, "Standalone research") {
		t.Fatalf("launch prompt = standing brief + recovered state:\n%s", p)
	}
	fresh := mustAssistant(t, s, "a3", "Eve")
	if p := s.CoordinatorLaunchPrompt(fresh); strings.Contains(p, "State recovered from Solco") {
		t.Fatalf("nothing to recover, nothing added:\n%s", p)
	}
}
