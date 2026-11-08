package personalagent

import (
	"testing"
	"time"
)

// Gap U07: the person can read when the heartbeat will next look, from the
// core's own schedule rather than from a guess in the page.
func TestHeartbeatShowsWhenItNextLooks(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "u07", "Ada")
	if h, _ := s.GetHeartbeat(a.ID); h.NextBeatAt != 0 {
		t.Fatalf("no schedule, yet next = %d", h.NextBeatAt)
	}
	if _, _, err := s.SetHeartbeat("hb1", HeartbeatInput{AssistantID: a.ID, Cron: "0 9 * * *"}, 0); err != nil {
		t.Fatal(err)
	}
	h, err := s.GetHeartbeat(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(s.nowMS())
	if h.NextBeatAt < now.UnixMilli() {
		t.Fatalf("next look %v is in the past", time.UnixMilli(h.NextBeatAt))
	}
	next := time.UnixMilli(h.NextBeatAt).Local()
	if next.Hour() != 9 && !next.Equal(now) {
		t.Fatalf("a 09:00 rule next looks at %v", next)
	}
}
