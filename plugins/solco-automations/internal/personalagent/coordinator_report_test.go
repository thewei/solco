package personalagent

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func readyReportCoordinator(t *testing.T, s *Store, a Assistant) Coordinator {
	t.Helper()
	c, _, err := s.StartCoordinator("report-start-"+a.ID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.ClaimTask(c.TaskID, "daemon")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkStarting(r.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLaunch(r.ID, Session{HerdrSession: "default", WorkspaceID: "w1", PaneID: "w1:p1"}); err != nil {
		t.Fatal(err)
	}
	// The real Dispatcher owns this post-RecordLaunch projection.
	currentRun, err := s.GetRun(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	currentTask, err := s.GetTask(c.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	s.syncCoordinator(currentTask, currentRun)
	c, err = s.GetCoordinator(a.ID)
	if err != nil || c.State != CoordinatorReady {
		t.Fatalf("Coordinator not ready: %+v %v", c, err)
	}
	return c
}

func TestReportPumpUsesRealCoordinatorAndFactWatermark(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	s, _ := openTest(t, WithClock(func() time.Time { return now }))
	a := mustAssistant(t, s, "report-owner", "Ada")
	b := mustAssistant(t, s, "report-other", "Bo")
	setPolicy(t, s, a, func(p *ProactivityInput) { p.ReportEveryMin = 1 })
	p := &ReportPump{Store: s}
	if r, err := p.Tick(); err != nil || r.Submitted != 0 {
		t.Fatalf("without original Coordinator: %+v %v", r, err)
	}
	readyReportCoordinator(t, s, a)
	worker := mkTask(t, s, "report-worker", a.ID, "Read docs")
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactTaskAdded, TaskID: worker.ID, Text: "Task delegated"}, "report:a1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.newFact(Fact{AssistantID: b.ID, Kind: FactPulse, Text: "OTHER ASSISTANT PRIVATE"}, "report:b1"); err != nil {
		t.Fatal(err)
	}
	var prompts []string
	p.Submit = func(session, paneID, text string) error {
		if session != "default" || paneID != "w1:p1" {
			t.Fatalf("wrong live coordinator %s %s", session, paneID)
		}
		prompts = append(prompts, text)
		return nil
	}
	res, err := p.Tick()
	if err != nil || res.Submitted != 1 || len(prompts) != 1 {
		t.Fatalf("submit: %+v %v %d", res, err, len(prompts))
	}
	if !strings.Contains(prompts[0], "Task "+worker.ID) || !strings.Contains(prompts[0], "Task delegated") ||
		!strings.Contains(prompts[0], "NOT a message from the human") || !strings.Contains(prompts[0], "Run") ||
		strings.Contains(prompts[0], "OTHER ASSISTANT PRIVATE") {
		t.Fatalf("source audit: %q", prompts[0])
	}
	got, err := s.GetCoordinatorReport(a.ID)
	if err != nil || got.State != ReportSubmitted || got.SubmittedSeq < 1 {
		t.Fatalf("report status: %+v %v", got, err)
	}
	if r, err := p.Tick(); err != nil || r.Submitted != 0 || len(prompts) != 1 {
		t.Fatalf("duplicate: %+v %v", r, err)
	}
	now = now.Add(time.Minute + time.Second)
	if r, err := p.Tick(); err != nil || r.Submitted != 0 {
		t.Fatalf("no new facts: %+v %v", r, err)
	}
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactBlocked, TaskID: worker.ID, Text: "Review required"}, "report:a2"); err != nil {
		t.Fatal(err)
	}
	if r, err := p.Tick(); err != nil || r.Submitted != 1 || len(prompts) != 2 {
		t.Fatalf("fresh fact: %+v %v", r, err)
	}
	got, err = s.GetCoordinatorReport(a.ID)
	if err != nil || got.SubmittedSeq <= 1 {
		t.Fatalf("watermark: %+v %v", got, err)
	}
	if strings.Contains(prompts[1], "Task delegated") {
		t.Fatalf("second report replayed previous Facts: %q", prompts[1])
	}
}

func TestReportPumpHonorsQuietAndDoesNotStartCoordinator(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	s, _ := openTest(t, WithClock(func() time.Time { return now }))
	a := mustAssistant(t, s, "report-quiet", "Ada")
	setPolicy(t, s, a, func(p *ProactivityInput) {
		p.ReportEveryMin = 1
		p.QuietStart = "00:00"
		p.QuietEnd = "23:59"
		p.TZ = "UTC"
	})
	readyReportCoordinator(t, s, a)
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactPulse, Text: "Still working"}, "report:quiet"); err != nil {
		t.Fatal(err)
	}
	called := 0
	p := &ReportPump{Store: s, Submit: func(_, _, _ string) error { called++; return nil }}
	if rep, err := p.Tick(); err != nil || rep.Submitted != 0 || called != 0 {
		t.Fatalf("quiet: %+v %v", rep, err)
	}
	cur, err := s.GetCoordinatorReport(a.ID)
	if err != nil || cur.State != ReportIdle {
		t.Fatalf("quiet must not claim: %+v %v", cur, err)
	}
	now = now.Add(12 * time.Hour)
	if r, err := p.Tick(); err != nil || r.Submitted != 0 || called != 0 {
		t.Fatalf("silent window still applies: %+v %v", r, err)
	}
	setPolicy(t, s, a, func(p *ProactivityInput) { p.ReportEveryMin = 1; p.QuietStart = ""; p.QuietEnd = ""; p.TZ = "" })
	if r, err := p.Tick(); err != nil || r.Submitted != 1 || called != 1 {
		t.Fatalf("after quiet: %+v %v", r, err)
	}
	// A logically stopped Coordinator must not be woken by future facts.
	if _, _, err := s.StopCoordinator("report-stop", a.ID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactPulse, Text: "more"}, "report:after-stop"); err != nil {
		t.Fatal(err)
	}
	if r, err := p.Tick(); err != nil || r.Submitted != 0 || called != 1 {
		t.Fatalf("stopped Coordinator woke: %+v %v", r, err)
	}
}

func TestReportPumpDoesNotTypeIntoBlockedHerdrDialog(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "report-dialog", "Ada")
	readyReportCoordinator(t, s, a)
	setPolicy(t, s, a, func(p *ProactivityInput) { p.ReportEveryMin = 1 })
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactPulse, Text: "Still working"}, "dialog"); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	p := &ReportPump{Store: s, Blocked: func(_, _ string) (string, bool) { return "Trust this folder?", true },
		Submit: func(_, _, _ string) error { attempts++; return nil }}
	if r, err := p.Tick(); err != nil || r.Submitted != 0 || attempts != 0 {
		t.Fatalf("dialog intercepted: %+v %v", r, err)
	}
	state, _ := s.GetCoordinatorReport(a.ID)
	if state.State != ReportQueued {
		t.Fatalf("dialog must keep report queued, not claimed: %+v", state)
	}
	p.Blocked = func(_, _ string) (string, bool) { return "", false }
	if r, err := p.Tick(); err != nil || r.Submitted != 1 || attempts != 1 {
		t.Fatalf("after trust: %+v %v", r, err)
	}
}

func TestReportPumpSafePreSubmitRefusalRetry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	s, _ := openTest(t, WithClock(func() time.Time { return now }))
	a := mustAssistant(t, s, "report-refused", "Ada")
	readyReportCoordinator(t, s, a)
	setPolicy(t, s, a, func(p *ProactivityInput) { p.ReportEveryMin = 1 })
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactPulse, Text: "Active"}, "report:refused"); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	pump := &ReportPump{Store: s, Submit: func(_, _, _ string) error {
		attempts++
		if attempts == 1 {
			return ErrSubmitRefused
		}
		return nil
	}}
	if r, err := pump.Tick(); err != nil || r.Queued != 1 {
		t.Fatalf("refused: %+v %v", r, err)
	}
	row, err := s.GetCoordinatorReport(a.ID)
	if err != nil || row.State != ReportQueued || row.Attempts != 1 || row.NextAt <= now.UnixMilli() {
		t.Fatalf("safe retry state: %+v %v", row, err)
	}
	if _, err := pump.Tick(); err != nil || attempts != 1 {
		t.Fatalf("early retry: %v attempts=%d", err, attempts)
	}
	now = now.Add(6 * time.Second)
	if r, err := pump.Tick(); err != nil || r.Submitted != 1 || attempts != 2 {
		t.Fatalf("positive retry: %+v %v attempts=%d", r, err, attempts)
	}
}

func TestReportPumpUncertainIsPermanentUntilOwnerResolves(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	s, path := openTest(t, WithClock(func() time.Time { return now }))
	a := mustAssistant(t, s, "report-uncertain", "Ada")
	readyReportCoordinator(t, s, a)
	setPolicy(t, s, a, func(p *ProactivityInput) { p.ReportEveryMin = 1 })
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactBlocked, Text: "Needs review"}, "report:uncertain"); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	p := &ReportPump{Store: s, Submit: func(_, _, _ string) error { attempts++; return errors.New("ambiguous transport") }}
	if r, err := p.Tick(); err != nil || r.Uncertain != 1 {
		t.Fatalf("uncertain: %+v %v", r, err)
	}
	state, err := s.GetCoordinatorReport(a.ID)
	if err != nil || state.State != ReportUncertain || state.SubmittedSeq != 0 {
		t.Fatalf("uncertain claim: %+v %v", state, err)
	}
	for i := 0; i < 3; i++ {
		now = now.Add(time.Hour)
		if _, err := p.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	if attempts != 1 {
		t.Fatalf("unsafe automatic retry: %d", attempts)
	}
	if _, _, err := s.ResolveCoordinatorReport("missing-confirmation", a.ID, state.ThroughSeq, "retry", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("confirmation bypassed: %v", err)
	}
	if _, _, err := s.ResolveCoordinatorReport("stale", a.ID, state.ThroughSeq+1, "skip", true); !errors.Is(err, ErrBadState) {
		t.Fatalf("stale owner action accepted: %v", err)
	}
	// Reopening a daemon doesn't silently replay the potentially delivered prompt.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if n, err := reopened.RecoverCoordinatorReports(); err != nil || n != 0 {
		t.Fatalf("restart: %d %v", n, err)
	}
	p.Store = reopened
	if _, err := p.Tick(); err != nil || attempts != 1 {
		t.Fatalf("restart duplicated: %v attempts=%d", err, attempts)
	}
	row, replay, err := reopened.ResolveCoordinatorReport("human-reviewed", a.ID, state.ThroughSeq, "skip", true)
	if err != nil || replay || row.State != ReportSubmitted || row.SubmittedSeq != state.ThroughSeq {
		t.Fatalf("explicit skip: %+v %t %v", row, replay, err)
	}
	if again, replayed, err := reopened.ResolveCoordinatorReport("human-reviewed", a.ID, state.ThroughSeq, "skip", true); err != nil || !replayed || again.SubmittedSeq != row.SubmittedSeq {
		t.Fatalf("receipt replay: %+v %t %v", again, replayed, err)
	}
	if _, _, err := reopened.ResolveCoordinatorReport("human-reviewed", a.ID, state.ThroughSeq, "retry", true); !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("receipt mismatch: %v", err)
	}
	// The Fact raised *by* this uncertain report is not a new work update.
	// Do not let it trigger another automatic Coordinator self-report.
	if pass, err := p.Tick(); err != nil || pass.Submitted != 0 || attempts != 1 {
		t.Fatalf("report must not chase its own warning: %+v %v %d", pass, err, attempts)
	}
}

func TestReportPumpClaimCrashAlwaysBecomesUncertain(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "crash-report", "Ada")
	if _, err := s.db.Exec(`INSERT INTO coordinator_reports(assistant_id,state,through_seq) VALUES(?,?,?)`, a.ID, ReportClaimed, 12); err != nil {
		t.Fatal(err)
	}
	n, err := s.RecoverCoordinatorReports()
	if err != nil || n != 1 {
		t.Fatalf("recover: %d %v", n, err)
	}
	if again, err := s.RecoverCoordinatorReports(); err != nil || again != 0 {
		t.Fatalf("idempotent recover: %d %v", again, err)
	}
	state, _ := s.GetCoordinatorReport(a.ID)
	if state.State != ReportUncertain {
		t.Fatalf("claim auto resumed: %+v", state)
	}
	facts, err := s.Facts(a.ID, "", 20)
	if err != nil || len(facts) != 1 || facts[0].Kind != FactBlocked {
		t.Fatalf("visible uncertain fact: %+v %v", facts, err)
	}
	api := &API{Store: s}
	probe, err := call(t, api, "", "assistant", "report", "probe")
	if err != nil || probe["supported"] != true {
		t.Fatalf("capability probe: %v %v", probe, err)
	}
	got, err := call(t, api, "", "assistant", "report", "show", a.ID)
	if err != nil || got["report"].(map[string]any)["state"] != ReportUncertain {
		t.Fatalf("CLI status: %+v %v", got, err)
	}
	body := `{"requestId":"manual","throughSeq":12,"action":"retry","confirmed":true}`
	got, err = call(t, api, body, "assistant", "report", "resolve", a.ID)
	if err != nil || got["report"].(map[string]any)["state"] != ReportQueued {
		t.Fatalf("explicit retry: %+v %v", got, err)
	}
}

func TestDeletingArchivedAssistantAlsoDeletesReportOutbox(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "delete-report", "Ada")
	if _, err := s.db.Exec(`INSERT INTO coordinator_reports(assistant_id,state,submitted_seq,through_seq) VALUES(?,?,?,?)`, a.ID, ReportSubmitted, 3, 3); err != nil {
		t.Fatal(err)
	}
	archived, _, err := s.ArchiveAssistant("archive-report", a.ID, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteArchivedAssistant(a.ID, archived.Revision); err != nil {
		t.Fatalf("cannot purge report: %v", err)
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM coordinator_reports WHERE assistant_id=?", a.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphan report row: %d %v", count, err)
	}
}

func TestReportPumpDefaultOffAndBoundedRefusals(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	s, _ := openTest(t, WithClock(func() time.Time { return now }))
	a := mustAssistant(t, s, "reports-off", "Ada")
	readyReportCoordinator(t, s, a)
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactTaskAdded, Text: "Work appeared"}, "reports-off-fact"); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	p := &ReportPump{Store: s, Submit: func(_, _, _ string) error { attempts++; return ErrSubmitRefused }}
	for i := 0; i < 3; i++ {
		if rep, err := p.Tick(); err != nil || rep.Submitted != 0 || attempts != 0 {
			t.Fatalf("reportEveryMin=0 auto-prompted OMP: %+v %v", rep, err)
		}
	}
	setPolicy(t, s, a, func(input *ProactivityInput) { input.ReportEveryMin = 1; input.Level = LevelRespond })
	if _, err := p.Tick(); err != nil || attempts != 0 {
		t.Fatalf("respond-only mode auto-prompted OMP: %v", err)
	}
	setPolicy(t, s, a, func(input *ProactivityInput) { input.ReportEveryMin = 1; input.Level = LevelFollowUp })
	for i := 0; i < reportRetryLimit; i++ {
		now = now.Add(time.Minute)
		if _, err := p.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	if attempts != reportRetryLimit {
		t.Fatalf("attempts = %d, expected %d", attempts, reportRetryLimit)
	}
	state, err := s.GetCoordinatorReport(a.ID)
	if err != nil || state.State != ReportUncertain {
		t.Fatalf("bounded failure not surfaced to human: %+v %v", state, err)
	}
	now = now.Add(24 * time.Hour)
	if _, err := p.Tick(); err != nil || attempts != reportRetryLimit {
		t.Fatalf("unexpected retry of exhausted delivery: %d %v", attempts, err)
	}
}

// Herdr pane reads may outlive the original policy snapshot; switching the
// Assistant to respond-only while checking the pane must cancel the submit.
func TestReportPumpRechecksPolicyBeforeIrrevocableHerdrClaim(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	s, _ := openTest(t, WithClock(func() time.Time { return now }))
	a := mustAssistant(t, s, "report-policy-race", "Ada")
	readyReportCoordinator(t, s, a)
	setPolicy(t, s, a, func(p *ProactivityInput) { p.ReportEveryMin = 1 })
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactPulse, Text: "Working"}, "policy-race"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	p := &ReportPump{Store: s, Submit: func(_, _, _ string) error { calls++; return nil }, Blocked: func(_, _ string) (string, bool) {
		setPolicy(t, s, a, func(p *ProactivityInput) { p.Level = LevelRespond })
		return "", false
	}}
	if r, err := p.Tick(); err != nil || r.Submitted != 0 || calls != 0 {
		t.Fatalf("owner changed policy during Herdr read: %+v %v; calls=%d", r, err, calls)
	}
	p.Blocked = nil
	setPolicy(t, s, a, func(p *ProactivityInput) { p.Level = LevelFollowUp })
	if r, err := p.Tick(); err != nil || r.Submitted != 1 || calls != 1 {
		t.Fatalf("safe resume after restored owner setting: %+v %v; calls=%d", r, err, calls)
	}
}
