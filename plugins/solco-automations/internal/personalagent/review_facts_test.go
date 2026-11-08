package personalagent

import "testing"

// Real-device finding (AC-04, 2026-11-05): after Merge the Assistant page's
// progress line still read "Verifying … is ready" because a merged or rejected
// review wrote no ledger fact. The newest fact must describe the newest step.
func TestMergedAndRejectedReviewsLeaveTheirOwnFact(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	f.finish()
	rev := f.start("s1")
	sv := &Supervisor{Store: f.s}
	if _, err := sv.Tick(); err != nil {
		t.Fatal(err)
	}
	last := func() Fact {
		t.Helper()
		facts, err := f.s.Facts(f.asst.ID, "", 20)
		if err != nil || len(facts) == 0 {
			t.Fatalf("facts: %v %v", facts, err)
		}
		return facts[0]
	}
	if got := last(); got.Kind != FactVerifying {
		t.Fatalf("after review start: %+v", got)
	}
	rev = f.approveMerge(rev)
	if _, err := f.r.Merge(rev.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sv.Tick(); err != nil {
		t.Fatal(err)
	}
	if got := last(); got.Kind != FactVerified {
		t.Fatalf("a merged review must say so, newest fact is %+v", got)
	}

	g := newReview(t)
	g.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	g.finish()
	r2 := g.start("s2")
	sv2 := &Supervisor{Store: g.s}
	_, _ = sv2.Tick()
	if _, err := g.r.Reject("rej-1", r2.ID, "no"); err != nil {
		t.Fatal(err)
	}
	if _, err := sv2.Tick(); err != nil {
		t.Fatal(err)
	}
	facts, _ := g.s.Facts(g.asst.ID, "", 20)
	if len(facts) == 0 || facts[0].Kind != FactRunFinished {
		t.Fatalf("a rejected review must not leave 'verifying' as the newest fact: %+v", facts)
	}
}
