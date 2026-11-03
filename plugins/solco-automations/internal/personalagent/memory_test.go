package personalagent

import (
	"errors"
	"testing"
)

func proposeMem(t *testing.T, s *Store, req, assistant, content string) MemoryItem {
	t.Helper()
	m, _, err := s.ProposeMemory(req, ProposeMemoryInput{AssistantID: assistant, Content: content, SourceRef: "run:r_1", Kind: "fact"})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	return m
}

// SOLCO-M01/M02: a proposal is only a candidate and carries its provenance.
func TestMemoryCandidateNeedsApproval(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	m := proposeMem(t, s, "m1", a.ID, "Prefers morning summaries")
	if m.State != MemoryCandidate || m.SourceRef != "run:r_1" || m.Scope != MemoryScopeAssistant || m.Revision != 1 {
		t.Fatalf("memory = %+v", m)
	}
	if vis, _ := s.VisibleMemory(a.ID); len(vis) != 0 {
		t.Fatalf("a candidate leaked into context: %+v", vis)
	}
	m, _, err := s.ApproveMemory("m2", m.ID, m.Revision)
	if err != nil || m.State != MemoryApproved {
		t.Fatalf("approve: %+v %v", m, err)
	}
	if vis, _ := s.VisibleMemory(a.ID); len(vis) != 1 {
		t.Fatalf("approved memory not visible: %+v", vis)
	}
}

func TestMemoryNeedsASourceRef(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	_, _, err := s.ProposeMemory("m1", ProposeMemoryInput{AssistantID: a.ID, Content: "x"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want invalid, got %v", err)
	}
}

// SOLCO-A03 / M04: memory never crosses Assistants without an explicit grant,
// and revoking the grant takes it back at once.
func TestMemoryIsolationShareAndRevoke(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	b := mustAssistant(t, s, "a2", "Bo")
	m := proposeMem(t, s, "m1", a.ID, "Ada's secret plan")
	m, _, _ = s.ApproveMemory("m2", m.ID, m.Revision)

	if vis, _ := s.VisibleMemory(b.ID); len(vis) != 0 {
		t.Fatalf("Bo reads Ada's memory: %+v", vis)
	}
	m, _, err := s.ShareMemory("m3", m.ID, b.ID, m.Revision)
	if err != nil || len(m.SharedWith) != 1 {
		t.Fatalf("share: %+v %v", m, err)
	}
	if vis, _ := s.VisibleMemory(b.ID); len(vis) != 1 {
		t.Fatalf("shared memory not visible to Bo: %+v", vis)
	}
	// Bo cannot edit or re-share what Ada owns.
	if _, _, err := s.ShareMemory("m3x", m.ID, "a_other", m.Revision+99); err == nil {
		t.Fatal("stale share accepted")
	}
	m, _, err = s.UnshareMemory("m4", m.ID, b.ID, m.Revision)
	if err != nil || len(m.SharedWith) != 0 {
		t.Fatalf("unshare: %+v %v", m, err)
	}
	if vis, _ := s.VisibleMemory(b.ID); len(vis) != 0 {
		t.Fatalf("revoked memory still visible: %+v", vis)
	}
}

func TestCannotShareWithYourselfOrAStranger(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	m := proposeMem(t, s, "m1", a.ID, "x")
	m, _, _ = s.ApproveMemory("m2", m.ID, m.Revision)
	if _, _, err := s.ShareMemory("m3", m.ID, a.ID, m.Revision); !errors.Is(err, ErrInvalid) {
		t.Fatalf("self share: %v", err)
	}
	if _, _, err := s.ShareMemory("m4", m.ID, "a_nobody", m.Revision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stranger share: %v", err)
	}
}

// SOLCO-M03 (store half): correcting or forgetting changes what is visible at once.
func TestMemoryCorrectionAndForget(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	m := proposeMem(t, s, "m1", a.ID, "Lives in Oslo")
	m, _, _ = s.ApproveMemory("m2", m.ID, m.Revision)
	m, _, err := s.CorrectMemory("m3", m.ID, "Lives in Bergen", m.Revision)
	if err != nil || m.Content != "Lives in Bergen" || m.Revision != 3 {
		t.Fatalf("correct: %+v %v", m, err)
	}
	vis, _ := s.VisibleMemory(a.ID)
	if len(vis) != 1 || vis[0].Content != "Lives in Bergen" {
		t.Fatalf("visible = %+v", vis)
	}
	if _, _, err := s.ForgetMemory("m4", m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	if vis, _ := s.VisibleMemory(a.ID); len(vis) != 0 {
		t.Fatalf("forgotten memory visible: %+v", vis)
	}
	got, _ := s.GetMemory(m.ID)
	if got.Content != "" || got.State != MemoryForgotten {
		t.Fatalf("content kept after forget: %+v", got)
	}
}
