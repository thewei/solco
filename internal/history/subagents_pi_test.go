package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePiChild(t *testing.T, dir, rel, parent, name, last string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel), "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session","version":3,"id":"x","timestamp":"2026-11-02T01:00:00.000Z","cwd":"/w","parentSession":"` + parent + `"}` + "\n" +
		`{"type":"session_info","id":"a","name":"` + name + `"}` + "\n" +
		`{"type":"message","id":"u","message":{"role":"user","content":[{"type":"text","text":"Task: hello"}]}}` + "\n" + last + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestPiSubagentsReadsChildSessions pins the pi-subagents layout checked on a
// real session (2026-11-02): parentSession in the header, agent kind in
// session_info.name, the end from the last assistant row's stopReason.
func TestPiSubagentsReadsChildSessions(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "p.jsonl")
	dir := filepath.Join(root, "p")
	uuid := "47e08c18-de02-47d3-99d2-dfe1f08b32e9"
	writePiChild(t, dir, "aaa/run-0", parent, "subagent-reviewer-"+uuid+"-1",
		`{"type":"message","id":"m","message":{"role":"assistant","content":[],"stopReason":"stop"}}`)
	writePiChild(t, dir, "bbb/run-0", parent, "subagent-worker-"+uuid+"-1",
		`{"type":"message","id":"m","message":{"role":"assistant","content":[],"stopReason":"aborted"}}`)
	writePiChild(t, dir, "ccc/run-0", parent, "subagent-worker-"+uuid+"-1",
		`{"type":"message","id":"m","message":{"role":"assistant","content":[],"stopReason":"toolUse"}}`)
	writePiChild(t, dir, "ddd/run-0", "/other/parent.jsonl", "subagent-worker-"+uuid+"-1", `{}`)

	subs := SubagentsFor(AgentPi, parent)
	if len(subs) != 3 {
		t.Fatalf("subs = %+v, want 3 (foreign parent skipped)", subs)
	}
	by := map[string]Subagent{}
	for _, s := range subs {
		by[s.ID] = s
	}
	if a := by["aaa/run-0"]; a.Type != "reviewer" || !a.Ended || a.Stopped {
		t.Fatalf("done child = %+v", a)
	}
	if b := by["bbb/run-0"]; b.Type != "worker" || !b.Ended || !b.Stopped {
		t.Fatalf("aborted child = %+v", b)
	}
	if c := by["ccc/run-0"]; c.Ended {
		t.Fatalf("toolUse child must not be ended: %+v", c)
	}
	if strings.Contains(by["aaa/run-0"].Path, "ddd") {
		t.Fatal("wrong path")
	}
}

// TestPiSubagentsLinkRunsFromParentNotifications pins the evidence channel:
// an incremental child notify names workflow run -> child run, a final notify
// names async run -> session file.
func TestPiSubagentsLinkRunsFromParentNotifications(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "p.jsonl")
	dir := filepath.Join(root, "p")
	wfChild := "53291212-115f-4a80-b959-0f042a72f333"
	other := "0a5e5516-2f41-47bc-a524-87c7a9a05635"
	stop := `{"type":"message","id":"m","message":{"role":"assistant","content":[],"stopReason":"stop"}}`
	wf := writePiChild(t, dir, "aaa/run-0", parent, "subagent-reviewer-"+wfChild+"-1", stop)
	async := writePiChild(t, dir, "bbb/run-0", parent, "subagent-worker-1e127c36-aaaa-bbbb-cccc-1234567890ab-1", stop)
	body := `{"type":"custom_message","customType":"subagent-incremental-child-notify","content":"Workflow child completed: **task-2**\nWorkflow run: wf-1\nChild run: ` + wfChild + `\nOutput: x"}` + "\n" +
		`{"type":"custom_message","customType":"subagent-notify","content":"done","details":{"runs":[{"runId":"` + other + `","sessionValue":"` + async + `"}]}}` + "\n"
	if err := os.WriteFile(parent, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	by := map[string]Subagent{}
	for _, s := range SubagentsFor(AgentPi, parent) {
		by[s.ID] = s
	}
	if got := by["aaa/run-0"].CallRun; got != "wf-1" {
		t.Fatalf("workflow child CallRun = %q, want wf-1", got)
	}
	if got := by["bbb/run-0"].CallRun; got != other {
		t.Fatalf("async child CallRun = %q, want %s", got, other)
	}
	_ = wf
}
