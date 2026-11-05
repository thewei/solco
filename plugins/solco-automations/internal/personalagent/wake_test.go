package personalagent

import (
	"strings"
	"testing"
	"time"
)

// A01/A02/A03: a heartbeat wake with a live Coordinator is ONE bounded turn of
// the original Assistant (inspect → choose → act → evaluate → save → stop), not a
// read-only side Task. It is deduplicated, merged while one is open, never lost,
// carries the last checkpoint, and is delivered through the same fenced,
// proven pump as reports.
func TestGapA01WakeGoesToTheOriginalCoordinatorWithItsCheckpoint(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s, _ := openTest(t, WithClock(func() time.Time { return now }))
	a := mustAssistant(t, s, "wk-a", "Ada")
	h := setBeat(t, s, a, HeartbeatInput{Cron: "0 9 * * *", WakeBudget: 3, Checklist: "- look for stuck tasks", Events: map[string]string{"task.failed": "wake"}})
	approveWake(t, s, h)
	readyReportCoordinator(t, s, a)
	if _, _, err := s.SetCheckpoint("wk-cp", CheckpointInput{AssistantID: a.ID, Progress: "digest sent yesterday", NextAction: "watch tomorrow's run"}, 0); err != nil {
		t.Fatal(err)
	}
	b := beater(s, &now)
	_, _ = b.Tick() // baseline
	_, failed := runningTask(t, s, a, "wk-t1", "Failing one")
	_ = s.FinishRun(failed.ID, RunFailed, "boom")

	r, err := b.Tick()
	if err != nil || r.Woke != 1 {
		t.Fatalf("tick = %+v %v", r, err)
	}
	if side := taskTitles(t, s, a, "Heartbeat"); len(side) != 0 {
		t.Fatalf("a live Coordinator must be woken, not a side task: %+v", side)
	}
	rep, _ := s.GetCoordinatorReport(a.ID)
	if rep.Kind != "wake" || rep.State != ReportQueued {
		t.Fatalf("outbox = %+v", rep)
	}

	// While one wake is open, more news is merged into held — not lost, not a second wake, no second charge.
	_, failed2 := runningTask(t, s, a, "wk-t2", "Second failure")
	_ = s.FinishRun(failed2.ID, RunFailed, "boom2")
	if r, _ := b.Tick(); r.Woke != 0 {
		t.Fatalf("a second wake was queued while one was open: %+v", r)
	}
	if hb, _ := s.GetHeartbeat(a.ID); hb.WakesToday != 1 || len(hb.Held) != 1 || !strings.Contains(hb.Held[0], "Second failure") {
		t.Fatalf("budget/held after merge: wakes=%d held=%v", hb.WakesToday, hb.Held)
	}

	var prompts []string
	pump := &ReportPump{Store: s, Ready: reportTestReady, Blocked: reportTestClear,
		Submit: func(_, _, text string) error { prompts = append(prompts, text); return nil },
		Verify: func(marker string, _ int64) (ReplyEvidence, error) {
			return ReplyEvidence{State: ReplyVerified, SessionID: "sess", MessageID: "msg"}, nil
		}}
	if res, err := pump.Tick(); err != nil || res.Submitted != 1 || len(prompts) != 1 {
		t.Fatalf("delivery: %+v %v prompts=%d", res, err, len(prompts))
	}
	p := prompts[0]
	for _, want := range []string{"NOT a message from the human", reportReplyPrefix, "Failing one", "look for stuck tasks",
		"solco_assistant", "checkpoint.set", "digest sent yesterday", "watch tomorrow's run", "inspect"} {
		if !strings.Contains(p, want) {
			t.Errorf("wake prompt lacks %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "do not delegate, create tasks") {
		t.Errorf("a wake may act through the broker; the report-only fence must not be there:\n%s", p)
	}
	// The same wake is never typed twice, and a verified reply frees the next.
	if res, _ := pump.Tick(); res.Verified != 1 || len(prompts) != 1 {
		t.Fatalf("reply proof: %+v prompts=%d", res, len(prompts))
	}
	if res, _ := pump.Tick(); res.Submitted != 0 || len(prompts) != 1 {
		t.Fatalf("a delivered wake was retyped: %+v", res)
	}
	now = now.Add(11 * time.Minute)
	if r, _ := b.Tick(); r.Woke != 1 { // the held news wakes again once the first turn is proven
		t.Fatalf("after the proven reply the held news should wake the next turn: %+v", r)
	}
}

// A02: a wake that could not be delivered in time expires instead of replaying
// hours later into a stale conversation, and says so.
func TestGapA02UndeliveredWakeExpiresAndSaysSo(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	s, _ := openTest(t, WithClock(func() time.Time { return now }))
	a := mustAssistant(t, s, "wk-b", "Bo")
	h := setBeat(t, s, a, HeartbeatInput{Cron: "0 9 * * *", WakeBudget: 3, Events: map[string]string{"task.failed": "wake"}})
	approveWake(t, s, h)
	readyReportCoordinator(t, s, a)
	bt := &Beater{Store: s, Now: func() time.Time { return now }}
	_, _ = bt.Tick()
	_, failed := runningTask(t, s, a, "wk-t3", "Failing")
	_ = s.FinishRun(failed.ID, RunFailed, "boom")
	if r, _ := bt.Tick(); r.Woke != 1 {
		t.Fatal("wake not queued")
	}
	busy := func(_, _ string) bool { return false } // the Coordinator never becomes idle
	pump := &ReportPump{Store: s, Ready: busy, Blocked: reportTestClear, Submit: func(_, _, _ string) error { t.Fatal("must not type into a busy pane"); return nil }}
	now = now.Add(WakeExpiry + time.Minute)
	_, _ = pump.Tick()
	rep, _ := s.GetCoordinatorReport(a.ID)
	if rep.State == ReportQueued {
		t.Fatalf("an expired wake is still queued: %+v", rep)
	}
	facts, _ := s.Facts(a.ID, "", 20)
	found := false
	for _, f := range facts {
		if f.Kind == FactHeld && strings.Contains(f.Text, "expired") {
			found = true
		}
	}
	if !found {
		t.Fatalf("an expired wake must leave a visible fact: %+v", facts)
	}
}
