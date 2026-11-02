package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DnzzL/herdr-automations/internal/history"
)

func setup(t *testing.T, yaml string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", dir)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	if err := os.WriteFile(filepath.Join(dir, "automations.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
}

// What the panel draws its list from: every entry, the broken ones too, with
// its next run and its last.
func TestListGivesThePanelEverythingItShows(t *testing.T) {
	setup(t, `backend: hc
paused: true
automations:
  - name: triage
    cron: "0 9 * * 1-5"
    repo: /x
    model: sonnet
    prompt: p
    disabled: true
  - name: broken
    cron: "0 99 * * *"
    repo: /x
    prompt: p
`)
	if err := history.Append(history.Record{RunID: "r1", Automation: "triage", Status: history.StatusDone, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	l, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if l.Backend != "hc" || !l.Paused || len(l.Automations) != 1 || len(l.Invalid) != 1 {
		t.Fatalf("got %+v", l)
	}
	a := l.Automations[0]
	if a.Name != "triage" || !a.Disabled || a.Model != "sonnet" || a.Next.IsZero() || a.Next.Hour() != 9 {
		t.Fatalf("automation %+v", a)
	}
	if a.Last == nil || a.Last.RunID != "r1" || a.Last.Status != "done" {
		t.Fatalf("last %+v", a.Last)
	}
	if l.Invalid[0].Name != "broken" || l.Invalid[0].Line == 0 {
		t.Fatalf("invalid %+v", l.Invalid)
	}
}

func TestHistoryCarriesDurationAndSeen(t *testing.T) {
	setup(t, "automations: []\n")
	base := time.Now().Add(-time.Hour)
	for _, r := range []history.Record{
		{RunID: "r1", Automation: "triage", Status: history.StatusRunning, At: base, PaneID: "t7"},
		{RunID: "r1", Automation: "triage", Status: history.StatusDone, At: base.Add(4 * time.Minute), PaneID: "t7"},
	} {
		if err := history.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := history.MarkSeen("r1"); err != nil {
		t.Fatal(err)
	}
	runs, err := History("", 10)
	if err != nil {
		t.Fatal(err)
	}
	r := runs[0]
	if r.Terminal != "t7" || !r.Seen || r.Seconds != 240 {
		t.Fatalf("got %+v", r)
	}
}

// The contract's fields ride alongside the upstream ones, so the Solco
// client decodes the same bytes the old panel did.
func TestListCarriesTheSolcoContractFields(t *testing.T) {
	setup(t, `automations:
  - name: nightly
    cron: "0 9 * * 1-5"
    repo: /x
    agent: claude
    prompt: p
    draft: true
    rationale: nightly triage draft
    on_overlap: queue
    cleanup_ttl_hours: 72
`)
	now := time.Now()
	if err := history.Append(history.Record{
		RunID: "r1", Automation: "nightly", Status: history.StatusDone,
		At: now, PaneID: "p9", WorkspaceID: "w1", Worktree: "auto/nightly-x",
	}); err != nil {
		t.Fatal(err)
	}
	l, err := List()
	if err != nil {
		t.Fatal(err)
	}
	a := l.Automations[0]
	if !a.Draft || a.Rationale != "nightly triage draft" || a.OnOverlap != "queue" || a.CleanupTTLHours != 72 {
		t.Fatalf("contract fields not carried: %+v", a)
	}
	if a.ScheduleText != "Weekdays 09:00" {
		t.Errorf("schedule_text = %q", a.ScheduleText)
	}
	if a.OnceAt != nil {
		t.Error("once_at must be null: this plugin has no one-shot schedules")
	}
	if a.NextRun != nil {
		t.Error("a draft has no next run; next_run must be null")
	}
	if a.Next.IsZero() {
		t.Error("upstream next must keep its meaning for old clients")
	}
	if a.LastRun == nil || a.LastRun.RunID != "r1" || a.LastRun.Worktree != "auto/nightly-x" ||
		a.LastRun.WorkspaceID != "w1" || a.LastRun.PaneID != "p9" {
		t.Fatalf("last_run = %+v", a.LastRun)
	}
	if a.LastRun.DurationSeconds != 0 || a.LastRun.Status != "done" {
		t.Errorf("last_run = %+v, want the run's facts", a.LastRun)
	}
	if a.History30["done"] != 1 {
		t.Errorf("history30 = %+v, want one done run", a.History30)
	}

	hj, err := HistoryJSON("nightly", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hj.Result.Runs) != 1 {
		t.Fatalf("runs = %+v", hj.Result.Runs)
	}
	r := hj.Result.Runs[0]
	if r.Name != "nightly" || r.Automation != "nightly" || r.Agent != "claude" ||
		r.Worktree != "auto/nightly-x" || r.WorkspaceID != "w1" || r.PaneID != "p9" {
		t.Errorf("run = %+v, want the contract's run shape", r)
	}
	if r.FinishedAt.IsZero() || r.StartedAt.IsZero() || r.DurationSeconds != 0 {
		t.Errorf("run times = %+v", r)
	}
}

func TestListGivesEnabledAutomationsANextRun(t *testing.T) {
	setup(t, `automations:
  - name: live
    cron: "0 9 * * 1-5"
    repo: /x
    prompt: p
`)
	l, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if l.Automations[0].NextRun == nil {
		t.Error("next_run = null for an enabled automation")
	}
}
