package personalagent

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReportPumpReconcilesActualOMPFinalAndGatesNextBatch(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	s, _ := openTest(t, WithClock(func() time.Time { return now }))
	a := mustAssistant(t, s, "report-reply-e2e", "Ada")
	readyReportCoordinator(t, s, a)
	setPolicy(t, s, a, func(p *ProactivityInput) { p.ReportEveryMin = 1 })
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactTaskAdded, Text: "First progress"}, "reply:first"); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	var prompts []string
	pump := &ReportPump{Store: s, Ready: reportTestReady, Blocked: reportTestClear,
		Submit: func(_, _, text string) error { prompts = append(prompts, text); return nil },
		Verify: func(marker string, _ int64) (ReplyEvidence, error) { return FindOMPReportReply(home, marker) }}
	if status, err := pump.Tick(); err != nil || status.Submitted != 1 {
		t.Fatalf("first accepted: %+v %v", status, err)
	}
	row, err := s.GetCoordinatorReport(a.ID)
	if err != nil || row.State != ReportSubmitted || row.ReplyState != ReplyAwaiting || !validReportMarker(row.ReportMarker) || !strings.Contains(prompts[0], reportReplyPrefix+row.ReportMarker) {
		t.Fatalf("claimed batch not correlated: %+v %v", row, err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactPulse, Text: "More progress"}, "reply:second"); err != nil {
		t.Fatal(err)
	}
	if status, err := pump.Tick(); err != nil || status.Submitted != 0 || len(prompts) != 1 {
		t.Fatalf("must not stack over unfinished OMP: %+v %v", status, err)
	}
	testOMPSource(t, home, "report", "actual-omp-session", testOMPUser(row.ReportMarker)+
		`{"type":"message","id":"tool-1","message":{"role":"assistant","stopReason":"toolUse","content":[]}}`+"\n")
	now = now.Add(11 * time.Second)
	if status, err := pump.Tick(); err != nil || status.Submitted != 0 {
		t.Fatalf("toolUse counted as reply: %+v %v", status, err)
	}
	// OMP writes a final answer into ITS OWN file, not into Solco DB.
	testOMPSource(t, home, "report", "actual-omp-session", testOMPUser(row.ReportMarker)+
		`{"type":"message","id":"tool-1","message":{"role":"assistant","stopReason":"toolUse","content":[]}}`+"\n"+testOMPDone)
	now = now.Add(11 * time.Second)
	if status, err := pump.Tick(); err != nil || status.Verified != 1 || len(prompts) != 1 {
		t.Fatalf("provider final reply not verified: %+v %v", status, err)
	}
	verified, err := s.GetCoordinatorReport(a.ID)
	if err != nil || verified.ReplyState != ReplyVerified || verified.ReplyMessageID != "final-123" {
		t.Fatalf("provider completion evidence lost: %+v %v", verified, err)
	}
	if status, err := pump.Tick(); err != nil || status.Submitted != 1 || len(prompts) != 2 {
		t.Fatalf("verified first reply should unblock second batch: %+v %v", status, err)
	}
	follow, err := s.GetCoordinatorReport(a.ID)
	if err != nil || follow.ReplyState != ReplyAwaiting || follow.ReportMarker == row.ReportMarker ||
		follow.SubmittedSeq <= row.SubmittedSeq {
		t.Fatalf("second batch did not advance safely: %+v %v", follow, err)
	}
	// The previous message remains only in the authentic provider file.
	ev, err := FindOMPReportReply(home, row.ReportMarker)
	if err != nil || ev.State != ReplyVerified || ev.MessageID != "final-123" {
		t.Fatalf("lost OMP evidence: %+v %v", ev, err)
	}
}

func TestReportPumpPersistsVerifiedProviderMessageIDAcrossRestart(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	s, path := openTest(t, WithClock(func() time.Time { return now }))
	a := mustAssistant(t, s, "reply-persistent", "Ada")
	readyReportCoordinator(t, s, a)
	setPolicy(t, s, a, func(p *ProactivityInput) { p.ReportEveryMin = 1 })
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactPulse, Text: "work"}, "reply:persist"); err != nil {
		t.Fatal(err)
	}
	p := &ReportPump{Store: s, Ready: reportTestReady, Blocked: reportTestClear,
		Submit: func(_, _, _ string) error { return nil }}
	if _, err := p.Tick(); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetCoordinatorReport(a.ID)
	if err != nil || before.ReplyState != ReplyAwaiting {
		t.Fatalf("before restart: %+v %v", before, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if n, err := reopened.RecoverCoordinatorReports(); err != nil || n != 0 {
		t.Fatalf("submitted batch must survive restart: %d %v", n, err)
	}
	home := t.TempDir()
	testOMPSource(t, home, "report", "persist-session", testOMPUser(before.ReportMarker)+testOMPDone)
	if probe, err := FindOMPReportReply(home, before.ReportMarker); err != nil || probe.State != ReplyVerified {
		t.Fatalf("direct provider verification failed: %+v %v", probe, err)
	}
	now = now.Add(11 * time.Second)
	sends := 0
	p.Store = reopened
	p.Submit = func(_, _, _ string) error { sends++; return nil }
	p.Verify = func(marker string, _ int64) (ReplyEvidence, error) { return FindOMPReportReply(home, marker) }
	res, err := p.Tick()
	if err != nil || res.Verified != 1 || sends != 0 {
		status, _ := reopened.GetCoordinatorReport(a.ID)
		t.Fatalf("restart reconciliation: %+v %v sends=%d report=%+v", res, err, sends, status)
	}
	after, err := reopened.GetCoordinatorReport(a.ID)
	if err != nil || after.ReplyState != ReplyVerified || after.ReplySessionID != "persist-session" ||
		after.ReplyMessageID != "final-123" || after.ReplyVerifiedAt == 0 {
		t.Fatalf("provider evidence not persisted: %+v %v", after, err)
	}
	facts, err := reopened.Facts(a.ID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	var notices int
	for _, f := range facts {
		if f.Kind == FactPulse && strings.Contains(f.Text, "verified final OMP reply") {
			notices++
			if !strings.Contains(f.Evidence, "final-123") {
				t.Fatalf("notice lacks true OMP evidence: %+v", f)
			}
		}
	}
	if notices != 1 {
		t.Fatalf("expected exactly one durable Activity reply notice, got %d from %+v", notices, facts)
	}
	now = now.Add(2 * time.Minute)
	if _, err := p.Tick(); err != nil || sends != 0 {
		t.Fatalf("verified notice caused report-on-report loop: %v sends=%d", err, sends)
	}
}

func TestReportPumpAbortedOMPRequiresExplicitOwnerReview(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	s, _ := openTest(t, WithClock(func() time.Time { return now }))
	a := mustAssistant(t, s, "reply-interrupted", "Ada")
	readyReportCoordinator(t, s, a)
	setPolicy(t, s, a, func(p *ProactivityInput) { p.ReportEveryMin = 1 })
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactPulse, Text: "work"}, "reply:interrupted"); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	sends := 0
	p := &ReportPump{Store: s, Ready: reportTestReady, Blocked: reportTestClear,
		Submit: func(_, _, _ string) error { sends++; return nil },
		Verify: func(marker string, _ int64) (ReplyEvidence, error) { return FindOMPReportReply(home, marker) }}
	if _, err := p.Tick(); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetCoordinatorReport(a.ID)
	testOMPSource(t, home, "report", "aborted-session", testOMPUser(before.ReportMarker)+
		`{"type":"message","id":"aborted-msg","message":{"role":"assistant","stopReason":"aborted","content":[]}}`+"\n")
	now = now.Add(12 * time.Second)
	res, err := p.Tick()
	if err != nil || res.Uncertain != 1 {
		t.Fatalf("interrupted turn: %+v %v", res, err)
	}
	state, _ := s.GetCoordinatorReport(a.ID)
	if state.State != ReportUncertain || state.ReplyState != ReplyInterrupted {
		t.Fatalf("interrupted state not visible: %+v", state)
	}
	now = now.Add(time.Hour)
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactPulse, Text: "more"}, "reply:after-interrupted"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Tick(); err != nil || sends != 1 {
		t.Fatalf("unapproved replay after abort: %v sends=%d", err, sends)
	}
	resolved, replayed, err := s.ResolveCoordinatorReport("owner-reviewed-abort", a.ID, state.ThroughSeq, "skip", true)
	if err != nil || replayed || resolved.ReplyState != ReplyHumanHandled || resolved.State != ReportSubmitted {
		t.Fatalf("explicit owner handled did not clear interruption: %+v %v", resolved, err)
	}
	now = now.Add(61 * time.Second)
	if _, err := p.Tick(); err != nil || sends != 2 {
		t.Fatalf("owner-handled batch did not unblock later progress: %v sends=%d", err, sends)
	}
}

func TestReportPumpMissingReplyTimesOutToManualReviewNeverAutoRepeats(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	s, _ := openTest(t, WithClock(func() time.Time { return now }))
	a := mustAssistant(t, s, "reply-timeout", "Ada")
	readyReportCoordinator(t, s, a)
	setPolicy(t, s, a, func(p *ProactivityInput) { p.ReportEveryMin = 1 })
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactPulse, Text: "work"}, "reply:timeout"); err != nil {
		t.Fatal(err)
	}
	sends := 0
	p := &ReportPump{Store: s, Ready: reportTestReady, Blocked: reportTestClear,
		Submit: func(_, _, _ string) error { sends++; return nil },
		Verify: func(string, int64) (ReplyEvidence, error) { return ReplyEvidence{}, nil }}
	if _, err := p.Tick(); err != nil {
		t.Fatal(err)
	}
	now = now.Add(31 * time.Minute)
	res, err := p.Tick()
	if err != nil || res.Uncertain != 1 || sends != 1 {
		t.Fatalf("missing reply not reported to person: %+v %v", res, err)
	}
	state, _ := s.GetCoordinatorReport(a.ID)
	if state.State != ReportUncertain || state.ReplyState != ReplyAwaiting {
		t.Fatalf("missing reply auto-claimed completion: %+v", state)
	}
	for i := 0; i < 3; i++ {
		now = now.Add(time.Hour)
		if _, err := p.Tick(); err != nil || sends != 1 {
			t.Fatalf("missing reply retried itself: %v sends=%d", err, sends)
		}
	}
}

func TestReportPumpMissingVerifierBecomesOwnerReviewAfterDeadline(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	s, _ := openTest(t, WithClock(func() time.Time { return now }))
	a := mustAssistant(t, s, "missing-reply-verifier", "Ada")
	readyReportCoordinator(t, s, a)
	setPolicy(t, s, a, func(p *ProactivityInput) { p.ReportEveryMin = 1 })
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactPulse, Text: "work"}, "report:no-verifier"); err != nil {
		t.Fatal(err)
	}
	sends := 0
	pump := &ReportPump{Store: s, Ready: reportTestReady, Blocked: reportTestClear,
		Submit: func(_, _, _ string) error { sends++; return nil }}
	if rep, err := pump.Tick(); err != nil || rep.Submitted != 1 {
		t.Fatalf("first: %+v %v", rep, err)
	}
	now = now.Add(31 * time.Minute)
	if rep, err := pump.Tick(); err != nil || rep.Uncertain != 1 || sends != 1 {
		t.Fatalf("missing callback must become reviewable: %+v %v", rep, err)
	}
	row, err := s.GetCoordinatorReport(a.ID)
	if err != nil || row.State != ReportUncertain || row.ReplyState != ReplyAwaiting {
		t.Fatalf("unresolved OMP reply must not be marked verified: %+v %v", row, err)
	}
}

// A v21 Herdr receipt is not a verified OMP answer. Upgrading an existing
// installation must preserve the accepted watermark but require explicit
// human review before another automatic progress prompt can be submitted.
func TestLegacyHerdrReportMigrationRequiresManualReview(t *testing.T) {
	original := migrations
	migrations = original[:len(original)-1]
	defer func() { migrations = original }()
	path := filepath.Join(t.TempDir(), "legacy-reports.db")
	legacy, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	owner := mustAssistant(t, legacy, "legacy-report", "Ada")
	if _, err := legacy.db.Exec(`INSERT INTO coordinator_reports(assistant_id,state,from_seq,through_seq,submitted_seq) VALUES(?,?,?,?,?)`, owner.ID, ReportSubmitted, 0, 5, 5); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	migrations = original
	modern, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer modern.Close()
	got, err := modern.GetCoordinatorReport(owner.ID)
	if err != nil || got.State != ReportUncertain || got.ReplyState != ReplyUntracked || got.SubmittedSeq != 5 || got.ThroughSeq != 5 {
		t.Fatalf("legacy accepted but never verified must require review: %+v %v", got, err)
	}
	after, replayed, err := modern.ResolveCoordinatorReport("reviewed-legacy", owner.ID, 5, "skip", true)
	if err != nil || replayed || after.State != ReportSubmitted || after.ReplyState != ReplyHumanHandled {
		t.Fatalf("legacy manual recovery failed: %+v replayed=%t err=%v", after, replayed, err)
	}
}
