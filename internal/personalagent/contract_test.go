package personalagent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The CLI's wire is pinned by golden files the plugin module writes
// (plugins/solco-automations/internal/personalagent/testdata). Decoding them
// strictly here means a field added on one side and forgotten on the other
// fails a test instead of silently dropping data.
const fixtures = "../../plugins/solco-automations/internal/personalagent/testdata"

func load(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtures, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func TestGoldenFixturesDecodeStrictly(t *testing.T) {
	var a struct {
		Assistant Assistant `json:"assistant"`
		Schema    int       `json:"schema"`
		Replayed  bool      `json:"replayed"`
	}
	load(t, "assistant-created", &a)
	if a.Schema != 1 || a.Assistant.Name != "Ada" || a.Assistant.Status != "active" {
		t.Fatalf("assistant = %+v", a)
	}

	var list struct {
		Schema     int         `json:"schema"`
		Assistants []Assistant `json:"assistants"`
	}
	load(t, "assistant-list", &list)

	var g struct {
		Schema int  `json:"schema"`
		Goal   Goal `json:"goal"`
		Replay bool `json:"replayed"`
	}
	load(t, "goal-active", &g)
	if g.Goal.State != "active" || len(g.Goal.Commitments) != 1 || g.Goal.Commitments[0].Kind != "pr_watch" {
		t.Fatalf("goal = %+v", g.Goal)
	}
	load(t, "goal-draft", &g)

	var td struct {
		Schema int   `json:"schema"`
		Task   Task  `json:"task"`
		Runs   []Run `json:"runs"`
	}
	load(t, "task-detail", &td)
	if td.Task.State != "done" || len(td.Runs) != 1 || td.Runs[0].PaneID != "w1:p1" {
		t.Fatalf("task = %+v runs=%+v", td.Task, td.Runs)
	}
	var tc struct {
		Schema int  `json:"schema"`
		Task   Task `json:"task"`
		Replay bool `json:"replayed"`
	}
	load(t, "task-created", &tc)

	var m struct {
		Schema int        `json:"schema"`
		Memory MemoryItem `json:"memory"`
		Replay bool       `json:"replayed"`
	}
	load(t, "memory-candidate", &m)
	load(t, "memory-approved", &m)
	if m.Memory.State != "approved" {
		t.Fatalf("memory = %+v", m.Memory)
	}

	var ap struct {
		Schema   int      `json:"schema"`
		Approval Approval `json:"approval"`
		Replay   bool     `json:"replayed"`
	}
	load(t, "approval-pending", &ap)
	if ap.Approval.State != "pending" || ap.Approval.ActionHash == "" {
		t.Fatalf("approval = %+v", ap.Approval)
	}

	var ov Overview
	load(t, "overview", &ov)
	if len(ov.Assistants) != 1 || ov.NeedsYou != 1 {
		t.Fatalf("overview = %+v", ov)
	}

	var e struct {
		Schema int    `json:"schema"`
		Error  *Error `json:"error"`
	}
	load(t, "error-stale-revision", &e)
	if e.Error == nil || e.Error.Code != "stale_revision" {
		t.Fatalf("error = %+v", e)
	}
}
