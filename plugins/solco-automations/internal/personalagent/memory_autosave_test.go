package personalagent

import (
	"testing"
)

// Gap A04: the first real caller of the grant engine. An Agent's memory
// proposal is a candidate until a person approves it — unless the owner has
// sealed a memory.autosave grant for that kind of memory. Nothing else changes.

func receiptCount(t *testing.T, e *grantEnv, where string, args ...any) int {
	t.Helper()
	var n int
	if err := e.s.db.QueryRow(`SELECT COUNT(*) FROM authorization_receipts WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *grantEnv) propose(t *testing.T, req, kind, content string) MemoryItem {
	t.Helper()
	m, _, err := e.s.ProposeMemory(req, ProposeMemoryInput{AssistantID: e.ada.ID, Kind: kind, Content: content, SourceRef: "run:r_1"})
	if err != nil {
		t.Fatalf("propose %s: %v", req, err)
	}
	return m
}

func TestMemoryProposalStaysACandidateWithoutAGrantAndWritesNoReceipt(t *testing.T) {
	e := newGrantEnv(t, true)
	m := e.propose(t, "p1", "preference", "Likes short answers")
	if m.State != MemoryCandidate {
		t.Fatalf("state = %s", m.State)
	}
	if n := receiptCount(t, e, "1=1"); n != 0 {
		t.Fatalf("an ordinary proposal must not write authorization receipts, got %d", n)
	}
}

func TestSealedGrantAutoApprovesOnlyItsKindAndLeavesAReceipt(t *testing.T) {
	e := newGrantEnv(t, true)
	e.create(t, "g1", e.grant(gid1, e.ada.ID, CapMemoryAutosave, map[string][]string{"kind": {"preference"}}))
	pref := e.propose(t, "p1", "preference", "Likes short answers")
	if pref.State != MemoryApproved || pref.Revision != 2 {
		t.Fatalf("preference = %+v", pref)
	}
	if fact := e.propose(t, "p2", "fact", "Works on Solco"); fact.State != MemoryCandidate {
		t.Fatalf("a kind outside the grant must stay a candidate: %+v", fact)
	}
	if n := receiptCount(t, e, "decision='allow' AND grant_id=? AND capability=?", gid1, CapMemoryAutosave); n != 1 {
		t.Fatalf("allow receipts = %d", n)
	}
	// The version history says who wrote it; the receipt says why it was kept.
	hist, err := e.s.MemoryHistory(pref.ID)
	if err != nil || len(hist) != 2 || hist[0].State != MemoryApproved || hist[0].Author != MemByAgent {
		t.Fatalf("history = %+v err=%v", hist, err)
	}
	// A replay of the same request is the same item, and no second receipt.
	again, replayed, err := e.s.ProposeMemory("p1", ProposeMemoryInput{AssistantID: e.ada.ID, Kind: "preference", Content: "Likes short answers", SourceRef: "run:r_1"})
	if err != nil || !replayed || again.ID != pref.ID {
		t.Fatalf("replay = %+v %v %v", again, replayed, err)
	}
	if n := receiptCount(t, e, "decision='allow'"); n != 1 {
		t.Fatalf("replay wrote another receipt: %d", n)
	}
}

func TestAutosaveHonoursLimitRevocationAndAnotherAssistant(t *testing.T) {
	e := newGrantEnv(t, true)
	e.create(t, "g1", e.grant(gid1, e.ada.ID, CapMemoryAutosave, map[string][]string{"kind": {"preference"}}, func(in *GrantInput) { in.MaxPerDay = 1 }))
	if m := e.propose(t, "p1", "preference", "one"); m.State != MemoryApproved {
		t.Fatalf("first = %+v", m)
	}
	if m := e.propose(t, "p2", "preference", "two"); m.State != MemoryCandidate {
		t.Fatalf("the daily limit must stop the second: %+v", m)
	}
	// Bob is not Ada.
	b, _, err := e.s.ProposeMemory("pb", ProposeMemoryInput{AssistantID: e.bob.ID, Kind: "preference", Content: "bob", SourceRef: "run:r_2"})
	if err != nil || b.State != MemoryCandidate {
		t.Fatalf("bob = %+v %v", b, err)
	}
	// Revoking ends it at once (new grant would be needed).
	e2 := newGrantEnv(t, true)
	e2.create(t, "g1", e2.grant(gid1, e2.ada.ID, CapMemoryAutosave, map[string][]string{"kind": {"preference"}}))
	if _, _, err := e2.s.RevokeGrant("rv", gid1); err != nil {
		t.Fatal(err)
	}
	if m := e2.propose(t, "p3", "preference", "after revoke"); m.State != MemoryCandidate {
		t.Fatalf("revoked grant still approved: %+v", m)
	}
}

func TestAutosaveNeverAppliesToFileEditsOrCredentialsOrAForgedGrant(t *testing.T) {
	e := newGrantEnv(t, true)
	e.create(t, "g1", e.grant(gid1, e.ada.ID, CapMemoryAutosave, map[string][]string{"kind": {"preference"}}))
	// A file a person (or an Agent with a shell) dropped is not an Agent proposal.
	f, _, err := e.s.proposeAs(MemByFiles, "pf", ProposeMemoryInput{AssistantID: e.ada.ID, Kind: "preference", Content: "from a file", SourceRef: "file:notes/a.md"})
	if err != nil || f.State != MemoryCandidate {
		t.Fatalf("file proposal = %+v %v", f, err)
	}
	if _, _, err := e.s.ProposeMemory("pc", ProposeMemoryInput{AssistantID: e.ada.ID, Kind: "preference", Content: "token sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", SourceRef: "run:r_1"}); err == nil {
		t.Fatal("a credential must still be refused under a grant")
	}
	// No owner key at all: the same sealed grant does nothing.
	n := newGrantEnv(t, false)
	n.create(t, "g1", n.grant(gid1, n.ada.ID, CapMemoryAutosave, map[string][]string{"kind": {"preference"}}))
	if m := n.propose(t, "p1", "preference", "x"); m.State != MemoryCandidate {
		t.Fatalf("no owner key must mean no auto-approval: %+v", m)
	}
}
