package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeSubagent(t *testing.T, dir, id, meta string, started, updated time.Time) {
	t.Helper()
	metaPath := filepath.Join(dir, "agent-"+id+".meta.json")
	if err := os.WriteFile(metaPath, []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(metaPath, started, started)
	if !updated.IsZero() {
		transcript := filepath.Join(dir, "agent-"+id+".jsonl")
		if err := os.WriteFile(transcript, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(transcript, updated, updated)
	}
}

// TestClaudeSubagentsReadsMetaAndLiveness pins the layout Claude Code uses
// (verified on a real session, 2026-10-30): meta fields, spawn order, and the
// transcript's mtime deciding who is still running.
func TestClaudeSubagentsReadsMetaAndLiveness(t *testing.T) {
	root := t.TempDir()
	session := filepath.Join(root, "0f10.jsonl")
	dir := filepath.Join(root, "0f10", "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	writeSubagent(t, dir, "b2", `{"agentType":"general-purpose","description":"review copy","spawnDepth":1}`, now.Add(-30*time.Second), now.Add(-5*time.Second))
	writeSubagent(t, dir, "a1", `{"agentType":"Explore","description":"scan pricing"}`, now.Add(-2*time.Minute), now.Add(-90*time.Second))
	writeSubagent(t, dir, "bad", `not json`, now, time.Time{})

	subs := ClaudeSubagents(session)
	if len(subs) != 2 || subs[0].ID != "a1" || subs[1].ID != "b2" {
		t.Fatalf("subs = %+v, want a1 then b2 (spawn order, bad meta skipped)", subs)
	}
	if subs[0].Type != "Explore" || subs[0].Description != "scan pricing" {
		t.Fatalf("meta not read: %+v", subs[0])
	}
	if subs[0].Active(now) || !subs[1].Active(now) {
		t.Fatalf("liveness wrong: a1=%v b2=%v", subs[0].Active(now), subs[1].Active(now))
	}
}

func TestClaudeSubagentsQuietWithoutTheLayout(t *testing.T) {
	root := t.TempDir()
	if got := ClaudeSubagents(filepath.Join(root, "none.jsonl")); got != nil {
		t.Fatalf("missing dir = %+v", got)
	}
	if got := ClaudeSubagents(filepath.Join(root, "notes.txt")); got != nil {
		t.Fatalf("non-transcript = %+v", got)
	}
}
