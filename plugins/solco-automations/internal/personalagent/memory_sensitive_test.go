package personalagent

import (
	"errors"
	"strings"
	"testing"
)

// Gap A08 (user default, 2026-11-05): a memory of kind "sensitive" (health,
// money, anything a person files that way) is never auto-saved by a grant and
// never leaves its Assistant, even when a person shares by mistake.
func TestSensitiveMemoryIsNeverAutoSavedEvenWithAGrantForIt(t *testing.T) {
	e := newGrantEnv(t, true)
	e.create(t, "g1", e.grant(gid1, e.ada.ID, CapMemoryAutosave, map[string][]string{"kind": {"sensitive", "preference"}}))
	if m := e.propose(t, "p1", "preference", "Likes short answers"); m.State != MemoryApproved {
		t.Fatalf("control: %+v", m)
	}
	if m := e.propose(t, "p2", KindSensitive, "Takes medication at 8"); m.State != MemoryCandidate {
		t.Fatalf("sensitive proposal was kept without a person: %+v", m)
	}
}

func TestSensitiveMemoryCannotBeSharedWithAnotherAssistant(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	b := mustAssistant(t, s, "a2", "Bo")
	m, _, err := s.AddMemory("add1", a.ID, "Salary is private", KindSensitive)
	if err != nil || m.State != MemoryApproved {
		t.Fatalf("add: %+v %v", m, err)
	}
	if _, _, err := s.ShareMemory("sh1", m.ID, b.ID, m.Revision); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("sharing sensitive memory must be refused, got %v", err)
	}
	if vis, _ := s.VisibleMemory(b.ID); len(vis) != 0 {
		t.Fatalf("Bo sees sensitive memory: %+v", vis)
	}
}

// A10: the scope table fails closed — a Coordinator verb must say whose
// record it touches, and the sharing verbs are not in it at all.
func TestEveryCoordinatorVerbNamesWhoseRecordItTouches(t *testing.T) {
	for verb, r := range coordinatorScope {
		if r.target == "" && r.own == "" {
			t.Errorf("%q declares neither a target nor an owner and would act on anyone", verb)
		}
		if strings.HasPrefix(verb, "memory share") || strings.HasPrefix(verb, "memory unshare") || strings.HasPrefix(verb, "memory approve") {
			t.Errorf("%q is a person's decision and must not be in the Coordinator scope", verb)
		}
	}
}
