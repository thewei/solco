package personalagent

import (
	"errors"
	"testing"
	"time"
)

func requestApproval(t *testing.T, s *Store, req string, a Assistant, task Task, action string) Approval {
	t.Helper()
	ap, _, err := s.RequestApproval(req, RequestApprovalInput{AssistantID: a.ID, SubjectKind: "task", SubjectID: task.ID, Action: action, TTL: time.Hour})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return ap
}

// Approval binds an action hash and the subject's revision; the first valid
// decision wins and a second phone gets a conflict.
func TestApprovalDecidesOnce(t *testing.T) {
	s, _ := openTest(t)
	a, task := newQueuedTask(t, s, KindResearch)
	ap := requestApproval(t, s, "r1", a, task, "merge branch work/x")
	if ap.State != ApprovalPending || ap.ActionHash == "" || ap.ExpectedRevision != task.Revision {
		t.Fatalf("approval = %+v", ap)
	}
	done, _, err := s.DecideApproval("d1", ap.ID, ap.ActionHash, true)
	if err != nil || done.State != ApprovalApproved {
		t.Fatalf("decide: %+v %v", done, err)
	}
	if _, _, err := s.DecideApproval("d2", ap.ID, ap.ActionHash, false); !errors.Is(err, ErrBadState) {
		t.Fatalf("second phone: want bad_state, got %v", err)
	}
	// The same request id (a phone retry) is a replay, not a conflict.
	again, replay, err := s.DecideApproval("d1", ap.ID, ap.ActionHash, true)
	if err != nil || !replay || again.State != ApprovalApproved {
		t.Fatalf("replay: %+v %v %v", again, replay, err)
	}
}

func TestApprovalWithWrongHashIsRefused(t *testing.T) {
	s, _ := openTest(t)
	a, task := newQueuedTask(t, s, KindResearch)
	ap := requestApproval(t, s, "r1", a, task, "merge")
	if _, _, err := s.DecideApproval("d1", ap.ID, "deadbeef", true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want invalid, got %v", err)
	}
}

// The task changed after the card was issued: the card is stale.
func TestApprovalGoesStaleWhenTheSubjectChanges(t *testing.T) {
	s, _ := openTest(t)
	a, task := newQueuedTask(t, s, KindResearch)
	ap := requestApproval(t, s, "r1", a, task, "merge")
	if _, _, err := s.ClaimTask(task.ID, "d"); err != nil { // bumps the task revision
		t.Fatal(err)
	}
	if _, _, err := s.DecideApproval("d1", ap.ID, ap.ActionHash, true); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("want stale_revision, got %v", err)
	}
}

func TestApprovalExpires(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s, _ := openTest(t, WithClock(func() time.Time { return now }))
	a, task := newQueuedTask(t, s, KindResearch)
	ap := requestApproval(t, s, "r1", a, task, "merge")
	now = now.Add(2 * time.Hour)
	if _, _, err := s.DecideApproval("d1", ap.ID, ap.ActionHash, true); !errors.Is(err, ErrBadState) {
		t.Fatalf("want bad_state (expired), got %v", err)
	}
	list, _ := s.ListApprovals(a.ID)
	if len(list) != 1 || list[0].State != ApprovalExpired {
		t.Fatalf("list = %+v", list)
	}
}

// An approval belongs to its Assistant: another one cannot request it.
func TestApprovalCannotTargetAnotherAssistantsTask(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	b := mustAssistant(t, s, "other", "Bo")
	_, _, err := s.RequestApproval("r1", RequestApprovalInput{AssistantID: b.ID, SubjectKind: "task", SubjectID: task.ID, Action: "merge", TTL: time.Hour})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want invalid, got %v", err)
	}
}
