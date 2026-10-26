package workboard

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// Captured from herdr-projects 1.2.26 on 2026-10-26 (isolated HERDR_PROJECTS_HOME):
// `pin repoA/1`, `pin repoB`, then `list --pinned --json`.
const realPinned = `{
  "machine": null,
  "projects": [
    "repoB"
  ],
  "tasks": [
    {
      "id": "repoA/1",
      "state": "idle",
      "title": "first task",
      "msg": "",
      "stage": "todo"
    }
  ]
}`

// `coordinator --project probe-nonexistent status --json`, nothing running.
const realCoordinatorNone = `{
  "name": "probe-nonexistent coordinator",
  "state": "none",
  "agent": "claude",
  "model": "",
  "effort": "",
  "oneOff": false,
  "default": "claude · default model · default effort",
  "pane": "",
  "queued": 0,
  "error": "",
  "said": ""
}`

func TestParsePinnedReadsProjectsAndTaskIDs(t *testing.T) {
	got, err := ParsePinned(realPinned)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, Pinned{Projects: []string{"repoB"}, Tasks: []string{"repoA/1"}}) {
		t.Fatalf("pinned = %+v", got)
	}
	if _, err := ParsePinned(`[]`); err == nil {
		t.Fatal("a plain list is not the pinned shape")
	}
}

func TestParseCoordinatorAndRunning(t *testing.T) {
	c, err := ParseCoordinator(realCoordinatorNone)
	if err != nil || c.State != "none" || c.Running() {
		t.Fatalf("none = %+v err=%v", c, err)
	}
	live, _ := ParseCoordinator(`{"name":"app coordinator","state":"working","pane":"w1-3","queued":1}`)
	if !live.Running() || live.Queued != 1 {
		t.Fatalf("live = %+v", live)
	}
	gone, _ := ParseCoordinator(`{"state":"exited","pane":"w1-3"}`)
	if gone.Running() {
		t.Fatal("an exited coordinator has nothing to go into")
	}
}

func TestPinCoordinatorArgvAndRefusals(t *testing.T) {
	var calls []string
	c := NewClientWithExec(func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return realCoordinatorNone, nil
	})
	ctx := context.Background()
	_ = c.Pin(ctx, "repoA/1")
	_ = c.Unpin(ctx, "repoB")
	_, _ = c.Pinned(ctx)
	_, _ = c.Coordinator(ctx, "repoB")
	want := "pin repoA/1|unpin repoB|list --pinned --json|coordinator --project repoB status --json"
	if got := strings.Join(calls, "|"); got != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	calls = nil
	if c.Pin(ctx, "") == nil || c.Unpin(ctx, "--all") == nil {
		t.Fatal("empty and option-shaped targets are refused")
	}
	if _, err := c.Coordinator(ctx, "-x"); err == nil {
		t.Fatal("option-shaped project refused")
	}
	if len(calls) != 0 {
		t.Fatalf("refusals ran %q", calls)
	}
}

func TestCoordinatorSaidReadsAsTextAndNeverFailsTheStatus(t *testing.T) {
	c, err := ParseCoordinator(`{"name":"r","state":"idle","said":"Two tasks need you."}`)
	if err != nil || string(c.Said) != "Two tasks need you." {
		t.Fatalf("said = %q err=%v", c.Said, err)
	}
	c, err = ParseCoordinator(`{"name":"r","state":"idle","said":{"odd":1}}`)
	if err != nil || c.Said != "" || c.State != "idle" {
		t.Fatalf("a said of another shape = %+v err=%v", c, err)
	}
}
