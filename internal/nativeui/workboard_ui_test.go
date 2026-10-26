package nativeui

import (
	"testing"
	"time"

	"github.com/wh-studio/solco/internal/commandcenter"
	"github.com/wh-studio/solco/internal/herdr"
	"github.com/wh-studio/solco/internal/workboard"
)

func workboardAction(t *testing.T, s *Shell) commandcenter.Action {
	t.Helper()
	for _, action := range s.buildCommandCenterActions() {
		if action.ID == "app.work-board" {
			return action
		}
	}
	t.Fatal("registry has no app.work-board entry")
	return commandcenter.Action{}
}

// The work board leaves no trace until the herdr-projects plugin is detected
// as installed, enabled and in range: the palette entry exists in the
// registry but is unavailable, so it never surfaces as a result.
func TestWorkBoardPaletteEntryFollowsThePluginGate(t *testing.T) {
	s := NewShell()
	if workboardAction(t, s).Available {
		t.Fatal("no detection yet: the entry must be unavailable")
	}
	index := commandcenter.Build(s.buildCommandCenterActions())
	if got := index.Query("work board", commandcenter.ScopeAll); len(got) != 0 {
		t.Fatalf("an ungated palette surfaced the work board: %+v", got)
	}

	for _, reason := range []workboard.Reason{
		workboard.ReasonNotInstalled, workboard.ReasonDisabled,
		workboard.ReasonVersionUnsupported, workboard.ReasonMissingEntrypoint, workboard.ReasonUnreadable,
	} {
		s.workboard.avail = workboard.Availability{Installed: true, Reason: reason}
		if workboardAction(t, s).Available {
			t.Fatalf("reason %q must keep the entry hidden", reason)
		}
	}

	s.workboard.avail = workboard.Availability{Installed: true, Enabled: true, Version: "1.2.26", Supported: true}
	if !workboardAction(t, s).Available {
		t.Fatal("a supported plugin on a local instance must expose the entry")
	}
	index = commandcenter.Build(s.buildCommandCenterActions())
	if got := index.Query("work board", commandcenter.ScopeAll); len(got) != 1 {
		t.Fatalf("supported palette results = %d, want 1", len(got))
	}
}

// Plugins live on this machine: a remote Herdr instance never shows the board
// even when the local plugin is supported.
func TestWorkBoardHidesOnRemoteInstances(t *testing.T) {
	s := NewShell()
	s.workboard.avail = workboard.Availability{Installed: true, Enabled: true, Version: "1.2.26", Supported: true}
	s.instances = []herdr.Instance{{Name: "inst-1", Machine: &herdr.Machine{ID: "m1"}}}
	s.activeInstance = "inst-1"
	if workboardAction(t, s).Available {
		t.Fatal("a remote instance must hide the work board")
	}
}

// A palette entry built while the plugin was present can outlive it; the
// executor revalidates against the current gate and starts nothing.
func TestStaleWorkBoardEntryRevalidates(t *testing.T) {
	s := NewShell()
	s.workboard.avail = workboard.Availability{Supported: true}
	action := workboardAction(t, s)
	s.workboard.avail = workboard.Availability{Reason: workboard.ReasonNotInstalled}
	generation := s.generation.Load()
	s.executeCommandCenterResult(commandcenter.Result{Action: action})
	if s.status != "The work board is not available." {
		t.Fatalf("status = %q, want the revalidation message", s.status)
	}
	if s.generation.Load() != generation {
		t.Fatal("a revalidated entry must not start a mutation")
	}
}

// Focus hand-backs are frequent; only a detection older than the gap runs
// again, and an in-flight one is never doubled.
func TestWorkBoardDetectionIsDebounced(t *testing.T) {
	s := NewShell()
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	restore := timeNow
	timeNow = func() time.Time { return now }
	defer func() { timeNow = restore }()

	s.workboard.lastDetect = now.Add(-workboardDetectGap / 2)
	s.detectWorkboard(false)
	if s.workboard.detecting {
		t.Fatal("a detection inside the gap must not start")
	}

	s.workboard.detecting = true
	before := s.workboard.lastDetect
	s.detectWorkboard(true)
	if !s.workboard.lastDetect.Equal(before) {
		t.Fatal("an in-flight detection must not be doubled, even when forced")
	}
}

func TestApplyWorkboardAvailabilityClearsTheInFlightFlag(t *testing.T) {
	s := NewShell()
	s.workboard.detecting = true
	s.workboard.board.loading = true // the gate opening starts a board read; keep the test free of that goroutine
	s.applyWorkboardAvailability(workboard.Availability{Installed: true, Enabled: true, Version: "1.2.26", Supported: true})
	if s.workboard.detecting || !s.workboardAvailable() {
		t.Fatalf("state after apply = %+v", s.workboard)
	}
	s.applyWorkboardAvailability(workboard.Unreadable())
	if s.workboardAvailable() {
		t.Fatal("a later unreadable detection must close the gate again")
	}
}
