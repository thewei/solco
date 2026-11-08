package usage

import (
	"testing"
	"time"
)

func TestLevelOfThresholds(t *testing.T) {
	cases := []struct {
		pct  float64
		want Level
	}{{0, LevelOK}, {79.9, LevelOK}, {80, LevelWarn}, {94.9, LevelWarn}, {95, LevelBad}, {100, LevelBad}}
	for _, c := range cases {
		if got := LevelOf(c.pct); got != c.want {
			t.Errorf("LevelOf(%v) = %v, want %v", c.pct, got, c.want)
		}
	}
}

func TestWindowPace(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	win := func(used float64, elapsed time.Duration) Window {
		return Window{UsedPct: used, Span: week, Resets: now.Add(week - elapsed)}
	}
	t.Run("too early to extrapolate", func(t *testing.T) {
		// 10% into the week at 19% used would project a burn-out, but the
		// sample is too short to say.
		if state, _ := win(19, week/10).Pace(now); state != PaceUnknown {
			t.Fatalf("state = %v, want PaceUnknown", state)
		}
	})
	t.Run("on pace", func(t *testing.T) {
		if state, _ := win(40, week/2).Pace(now); state != PaceOn {
			t.Fatalf("state = %v, want PaceOn", state)
		}
	})
	t.Run("runs out before the reset", func(t *testing.T) {
		// 87% spent in 5 of 7 days: 13% left lasts 5d*13/87 ≈ 17h 56m.
		state, in := win(87, 5*24*time.Hour).Pace(now)
		if state != PaceRunsOut {
			t.Fatalf("state = %v, want PaceRunsOut", state)
		}
		spent := 5 * 24 * time.Hour
		want := time.Duration(float64(spent) * 13 / 87)
		if d := in - want; d < -time.Second || d > time.Second {
			t.Fatalf("in = %v, want %v", in, want)
		}
	})
	t.Run("exceeded", func(t *testing.T) {
		if state, _ := win(100, week/2).Pace(now); state != PaceExceeded {
			t.Fatalf("state = %v, want PaceExceeded", state)
		}
	})
	t.Run("nothing spent or no span", func(t *testing.T) {
		if state, _ := win(0, week/2).Pace(now); state != PaceUnknown {
			t.Fatalf("zero use: state = %v", state)
		}
		if state, _ := (Window{UsedPct: 50}).Pace(now); state != PaceUnknown {
			t.Fatalf("no span: state = %v", state)
		}
	})
}

func TestSnapshotTightest(t *testing.T) {
	s := Snapshot{Windows: []Window{{Name: "a", UsedPct: 10}, {Name: "b", UsedPct: 87}, {Name: "c", UsedPct: 40}}}
	if w, ok := s.Tightest(); !ok || w.Name != "b" {
		t.Fatalf("Tightest = %+v, %v", w, ok)
	}
	if _, ok := (Snapshot{}).Tightest(); ok {
		t.Fatal("empty snapshot has no tightest window")
	}
}

func TestSpanName(t *testing.T) {
	cases := map[time.Duration]string{
		5 * time.Hour:       "5-hour",
		7 * 24 * time.Hour:  "Weekly",
		30 * 24 * time.Hour: "Monthly",
		3 * 24 * time.Hour:  "3-day",
		90 * time.Minute:    "90-minute",
		0:                   "Window",
	}
	for d, want := range cases {
		if got := spanName(d); got != want {
			t.Errorf("spanName(%v) = %q, want %q", d, got, want)
		}
	}
}
