package personalagent

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// hookWakeEnv is a hook-armed Assistant with a ready Coordinator whose first
// webhook has already become a queued wake.
func hookWakeEnv(t *testing.T) (*hookEnv, *Beater) {
	t.Helper()
	e := newHookEnv(t)
	h := setBeat(t, e.s, e.a, HeartbeatInput{Cron: "0 9 * * *", WakeBudget: 5, Events: map[string]string{"hook.check": "wake"}})
	approveWake(t, e.s, h)
	readyReportCoordinator(t, e.s, e.a)
	b := beater(e.s, &e.now)
	_, _ = b.Tick() // baseline
	if _, err := e.s.IngestHook(e.delivery("fault-1", `{"source":"ci","event":"check.requested"}`)); err != nil {
		t.Fatal(err)
	}
	if r, err := b.Tick(); err != nil || r.Woke != 1 {
		t.Fatalf("wake: %+v %v", r, err)
	}
	return e, b
}

// HOOK-05: the wake was typed (Herdr accepted it) but OMP never proves a
// reply — across a Host restart and a re-sent webhook it is never typed twice
// and never claimed as checked; it waits for the person.
func TestHookWakeAcceptedWithoutAProvenReplyIsNeverRetypedOrClaimed(t *testing.T) {
	e, b := hookWakeEnv(t)
	sends := 0
	pump := func(s *Store) *ReportPump {
		return &ReportPump{Store: s, Ready: reportTestReady, Blocked: reportTestClear,
			Submit: func(_, _, _ string) error { sends++; return nil },
			Verify: func(string, int64) (ReplyEvidence, error) { return ReplyEvidence{}, nil }}
	}
	if r, err := pump(e.s).Tick(); err != nil || r.Submitted != 1 || sends != 1 {
		t.Fatalf("first delivery: %+v %v sends=%d", r, err, sends)
	}
	// The Host dies after the accepted prompt; the database is all that is left.
	_ = e.s.Close()
	s2, err := Open(e.path, WithClock(func() time.Time { return e.now }))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	e.s = s2
	b.Store = s2
	// The sender never saw an answer and sends the same event again, then a new one.
	if r, err := s2.IngestHook(e.delivery("fault-1", `{"source":"ci","event":"check.requested"}`)); err != nil || !r.Replayed {
		t.Fatalf("redelivery after restart: %+v %v", r, err)
	}
	if _, err := s2.IngestHook(e.delivery("fault-2", `{"source":"ci","event":"check.requested"}`)); err != nil {
		t.Fatal(err)
	}
	p := pump(s2)
	for i := 0; i < 4; i++ {
		e.now = e.now.Add(31 * time.Minute)
		_, _ = b.Tick()
		if _, err := p.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	if sends != 1 {
		t.Fatalf("the wake was typed %d times", sends)
	}
	rep, err := s2.GetCoordinatorReport(e.a.ID)
	if err != nil || rep.State != ReportUncertain || rep.ReplyState != ReplyAwaiting {
		t.Fatalf("a wake with no proven reply must not look checked: %+v %v", rep, err)
	}
}

// HOOK-05: a Herdr trust/permission dialog on the Coordinator's pane holds the
// wake — nothing is typed into the dialog and the wake is not lost; once the
// dialog is gone it is delivered exactly once.
func TestHookWakeWaitsOutADialogAndIsDeliveredOnce(t *testing.T) {
	e, b := hookWakeEnv(t)
	dialog := true
	var prompts []string
	p := &ReportPump{Store: e.s, Ready: reportTestReady,
		Blocked: func(_, _ string) (string, bool) { return "trust this folder?", dialog },
		Submit:  func(_, _, text string) error { prompts = append(prompts, text); return nil },
		Verify: func(string, int64) (ReplyEvidence, error) {
			return ReplyEvidence{State: ReplyVerified, SessionID: "s", MessageID: "m"}, nil
		}}
	for i := 0; i < 3; i++ {
		e.now = e.now.Add(time.Minute)
		_, _ = b.Tick()
		if _, err := p.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	if len(prompts) != 0 {
		t.Fatalf("typed into a dialog: %v", prompts)
	}
	if rep, _ := e.s.GetCoordinatorReport(e.a.ID); rep.State != ReportQueued {
		t.Fatalf("a held wake must stay queued: %+v", rep)
	}
	dialog = false
	for i := 0; i < 3; i++ {
		_, _ = p.Tick()
	}
	if len(prompts) != 1 || !strings.Contains(prompts[0], "Webhook ci") {
		t.Fatalf("after the dialog the wake is delivered once: %d %v", len(prompts), prompts)
	}
}

// HOOK-05: the Coordinator's OMP is gone (never ready). The wake expires and
// says so; the same webhook sent again is a replay, not a second wake; nothing
// is typed anywhere.
func TestHookWakeForAGoneCoordinatorExpiresAndRedeliveryMakesNoSecondWake(t *testing.T) {
	e, _ := hookWakeEnv(t)
	p := &ReportPump{Store: e.s, Ready: func(_, _ string) bool { return false }, Blocked: reportTestClear,
		Submit: func(_, _, _ string) error { t.Fatal("typed into a Coordinator that is gone"); return nil }}
	e.now = e.now.Add(WakeExpiry + time.Minute)
	_, _ = p.Tick()
	if rep, _ := e.s.GetCoordinatorReport(e.a.ID); rep.State == ReportQueued {
		t.Fatalf("an undeliverable wake is still queued: %+v", rep)
	}
	facts, _ := e.s.Facts(e.a.ID, "", 20)
	said := false
	for _, f := range facts {
		if f.Kind == FactHeld && strings.Contains(f.Text, "expired") {
			said = true
		}
	}
	if !said {
		t.Fatalf("the person must be told the wake expired: %+v", facts)
	}
	if r, err := e.s.IngestHook(e.delivery("fault-1", `{"source":"ci","event":"check.requested"}`)); err != nil || !r.Replayed {
		t.Fatalf("redelivery: %+v %v", r, err)
	}
	evs, _ := e.s.Events(e.a.ID, 0, 100)
	n := 0
	for _, ev := range evs {
		if ev.Kind == "hook.check" {
			n++
		}
	}
	if n != 1 {
		t.Fatal(fmt.Sprintf("hook events = %d, want 1", n))
	}
}
