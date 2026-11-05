package personalagent

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// A03: an Assistant's Autopilot checkpoint is durable, revision-fenced, owned
// by one Assistant, and its "last observed event" comes from the core's own
// ledger — never from what the Agent claims.
func TestGapA03CheckpointIsDurableFencedAndCoreStampsTheObservedSeq(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "cp-a", "Ada")
	var top int64
	if _, _, err := execute(s, "", "cp.fact", nil, func(tx *sql.Tx) (bool, error) {
		return addFactTx(tx, Fact{AssistantID: a.ID, Kind: FactRunFinished, Text: "a run finished"}, "cp:f1", s.nowMS())
	}); err != nil {
		t.Fatal(err)
	}
	_ = s.db.QueryRow(`SELECT COALESCE(MAX(rowid),0) FROM goal_facts WHERE assistant_id=?`, a.ID).Scan(&top)

	empty, err := s.GetCheckpoint(a.ID)
	if err != nil || empty.Revision != 0 {
		t.Fatalf("a never-saved checkpoint is revision 0: %+v %v", empty, err)
	}
	cp, replayed, err := s.SetCheckpoint("cp-1", CheckpointInput{AssistantID: a.ID, Progress: "digest delivered", NextAction: "wait for tomorrow's run", Blockers: "", StopWhen: "the person cancels the routine"}, 0)
	if err != nil || replayed || cp.Revision != 1 || cp.LastSeq != top {
		t.Fatalf("checkpoint = %+v replayed=%v err=%v (core must stamp lastSeq=%d)", cp, replayed, err, top)
	}
	if again, replayed, err := s.SetCheckpoint("cp-1", CheckpointInput{AssistantID: a.ID, Progress: "digest delivered", NextAction: "wait for tomorrow's run", StopWhen: "the person cancels the routine"}, 0); err != nil || !replayed || again.Revision != 1 {
		t.Fatalf("the same requestId must replay, not write twice: %+v %v %v", again, replayed, err)
	}
	if _, _, err := s.SetCheckpoint("cp-2", CheckpointInput{AssistantID: a.ID, Progress: "stale writer"}, 0); !strings.Contains(fmt.Sprint(err), "revision") {
		t.Fatalf("a stale expectedRevision must be refused: %v", err)
	}
	if _, _, err := s.SetCheckpoint("cp-3", CheckpointInput{AssistantID: a.ID, Progress: "token sk-abcdefghijklmnopqrstuvwxyz123456"}, 1); err == nil {
		t.Fatal("a credential must never be kept in a checkpoint")
	}
	if _, _, err := s.SetCheckpoint("cp-4", CheckpointInput{AssistantID: a.ID, Progress: strings.Repeat("x", 5000)}, 1); err == nil {
		t.Fatal("an oversized checkpoint must be refused")
	}
	cp2, _, err := s.SetCheckpoint("cp-5", CheckpointInput{AssistantID: a.ID, Progress: "second", NextAction: "idle"}, 1)
	if err != nil || cp2.Revision != 2 {
		t.Fatalf("%+v %v", cp2, err)
	}
	other := mustAssistant(t, s, "cp-b", "Bob")
	if got, _ := s.GetCheckpoint(other.ID); got.Revision != 0 || got.Progress != "" {
		t.Fatalf("a checkpoint leaked across Assistants: %+v", got)
	}
}
