package history

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestOMPSubagentsFollowParentSessionAndExitMarker pins omp's layout (real
// session, 2026-10-31): children are `<session>/<name>.jsonl` whose `session`
// header names the parent, and `session_exit` ends them.
func TestOMPSubagentsFollowParentSessionAndExitMarker(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "s1.jsonl")
	writeFile(t, parent, "{}\n")
	header := func(p string) string {
		return fmt.Sprintf(`{"type":"session","timestamp":"2026-10-31T02:00:00.000Z","parentSession":%q}`+"\n", p)
	}
	writeFile(t, filepath.Join(root, "s1", "scoutA.jsonl"), header(parent)+`{"type":"custom_message","customType":"session_exit"}`+"\n")
	writeFile(t, filepath.Join(root, "s1", "scoutB.jsonl"), header(parent)+`{"type":"message"}`+"\n")
	writeFile(t, filepath.Join(root, "s1", "stranger.jsonl"), header("/elsewhere.jsonl"))

	subs := SubagentsFor(AgentOMP, parent)
	if len(subs) != 2 {
		t.Fatalf("subs = %+v, want the two children of s1", subs)
	}
	byName := map[string]Subagent{}
	for _, s := range subs {
		byName[s.Name] = s
	}
	if !byName["scoutA"].Ended || byName["scoutB"].Ended {
		t.Fatalf("session_exit decides Ended: A=%v B=%v", byName["scoutA"].Ended, byName["scoutB"].Ended)
	}
	if byName["scoutA"].Provider != AgentOMP || byName["scoutA"].Path == "" {
		t.Fatalf("provider/path not filled: %+v", byName["scoutA"])
	}
}

func codexMeta(id, source, ts string) string {
	return fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"timestamp":%q,"source":%s}}`+"\n", id, ts, source)
}

// TestCodexSubagentsClassifyByFirstLine pins the Codex layout: a child is a
// sibling rollout whose first line carries thread_spawn.parent_thread_id; an
// ordinary session's `source` is a plain string and must not break the read.
func TestCodexSubagentsClassifyByFirstLine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	day := time.Now().Format("2006/01/02")
	dir := filepath.Join(root, day)
	ts := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	parent := filepath.Join(dir, "rollout-p.jsonl")
	writeFile(t, parent, codexMeta("P1", `"exec"`, ts))
	child := filepath.Join(dir, "rollout-c.jsonl")
	writeFile(t, child, codexMeta("C1", `{"subagent":{"thread_spawn":{"parent_thread_id":"P1","depth":1,"agent_path":"/root/ping","agent_nickname":"Nietzsche"}}}`, ts)+
		`{"type":"event_msg","payload":{"type":"task_complete"}}`+"\n")
	writeFile(t, filepath.Join(dir, "rollout-other.jsonl"), codexMeta("C2", `{"subagent":{"thread_spawn":{"parent_thread_id":"ZZ","depth":1}}}`, ts))
	writeFile(t, filepath.Join(dir, "rollout-bad.jsonl"), "not json\n")

	subs := SubagentsFor(AgentCodex, parent)
	if len(subs) != 1 {
		t.Fatalf("subs = %+v, want only C1", subs)
	}
	got := subs[0]
	if got.ID != "C1" || got.Name != "ping" || got.Type != "Nietzsche" || got.Depth != 1 || !got.Ended || got.Path != child {
		t.Fatalf("child = %+v", got)
	}
	if got := SubagentsFor(AgentCodex, child); len(got) != 0 {
		t.Fatalf("a child with no children = %+v", got)
	}
	if got := SubagentsFor(AgentCodex, filepath.Join(t.TempDir(), "rollout-x.jsonl")); got != nil {
		t.Fatalf("outside a sessions tree = %+v", got)
	}
}

func TestReadSubagentAcceptsClaudeSidechainRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-a1.jsonl")
	rows := []string{
		`{"type":"user","isSidechain":true,"uuid":"u1","timestamp":"2026-10-31T02:00:00.000Z","message":{"role":"user","content":"Scan the pricing page\nand report"}}`,
		`{"type":"assistant","isSidechain":true,"uuid":"a1","timestamp":"2026-10-31T02:00:01.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Grep","input":{"pattern":"price"}}]}}`,
		`{"type":"assistant","isSidechain":true,"uuid":"a2","timestamp":"2026-10-31T02:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Found 3 places."}]}}`,
	}
	writeFile(t, path, strings.Join(rows, "\n")+"\n")
	detail, err := ReadSubagent(AgentClaudeCode, path)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Result != "Found 3 places." || detail.ToolCalls != 1 || detail.TailName != "Grep" {
		t.Fatalf("detail = %+v", detail)
	}
	if detail.FirstTs != 1793412000000 {
		t.Fatalf("first timestamp = %d", detail.FirstTs)
	}
	if detail.Description != "Scan the pricing page" || !strings.HasPrefix(detail.Prompt, "Scan the pricing page") {
		t.Fatalf("prompt/description = %q / %q", detail.Prompt, detail.Description)
	}
}

func TestReadSubagentRefusesOversizeAndMissing(t *testing.T) {
	if _, err := ReadSubagent(AgentClaudeCode, filepath.Join(t.TempDir(), "none.jsonl")); err == nil {
		t.Fatal("missing file must error")
	}
	big := filepath.Join(t.TempDir(), "big.jsonl")
	f, _ := os.Create(big)
	f.Truncate(SubagentReadLimit + 1)
	f.Close()
	if _, err := ReadSubagent(AgentClaudeCode, big); err != ErrSubagentTooLarge {
		t.Fatalf("oversize err = %v", err)
	}
}

// TestReadSubagentKnowsPendingToolAndHandback: a transcript ending on a tool
// call without a result is waiting (a long `sleep`), not finished; Claude's
// SubagentHandback carries the report and is neither work nor pending.
func TestReadSubagentKnowsPendingToolAndHandback(t *testing.T) {
	dir := t.TempDir()
	user := `{"type":"user","isSidechain":true,"uuid":"u1","timestamp":"2026-10-31T02:00:00.000Z","message":{"role":"user","content":"go"}}`
	bash := `{"type":"assistant","isSidechain":true,"uuid":"a1","timestamp":"2026-10-31T02:00:01.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"sleep 20"}}]}}`
	waiting := filepath.Join(dir, "waiting.jsonl")
	writeFile(t, waiting, user+"\n"+bash+"\n")
	detail, err := ReadSubagent(AgentClaudeCode, waiting)
	if err != nil || !detail.PendingTool || detail.TailName != "Bash" {
		t.Fatalf("waiting = %+v err=%v", detail, err)
	}

	handback := `{"type":"assistant","isSidechain":true,"uuid":"a2","timestamp":"2026-10-31T02:00:30.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"SubagentHandback","input":{"message":"Report: all good"}}]}}`
	done := filepath.Join(dir, "done.jsonl")
	writeFile(t, done, user+"\n"+bash+"\n"+handback+"\n")
	detail, err = ReadSubagent(AgentClaudeCode, done)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Result != "Report: all good" || detail.ToolCalls != 1 || detail.PendingTool {
		t.Fatalf("handback = %+v", detail)
	}
}

func TestSubagentActiveHonoursBusyWindow(t *testing.T) {
	now := time.Now()
	quiet := Subagent{UpdatedAt: now.Add(-time.Minute)}
	if quiet.Active(now) || !quiet.NeedsBusyCheck(now) {
		t.Fatal("a minute-quiet sub-agent is a candidate, not active")
	}
	quiet.Busy = true
	if !quiet.Active(now) {
		t.Fatal("a pending tool call keeps it running")
	}
	quiet.UpdatedAt = now.Add(-time.Hour)
	if quiet.Active(now) || quiet.NeedsBusyCheck(now) {
		t.Fatal("an hour of silence is over, whatever the tool says")
	}
	if (Subagent{UpdatedAt: now, Ended: true}).Active(now) {
		t.Fatal("ended is never active")
	}
}

// TestSubagentStoppedNeedsTheProviderInterruptMarker pins the two markers
// checked on real runs (2026-10-31): Claude Code ends a stopped child's file
// with a user row "[Request interrupted by user]" (TaskStop and Esc alike);
// Codex records `turn_aborted` after the last task event. A marker followed
// by more rows is a resumed run, not a stopped one.
func TestSubagentStoppedNeedsTheProviderInterruptMarker(t *testing.T) {
	root := t.TempDir()
	session := filepath.Join(root, "s.jsonl")
	writeFile(t, session, "{}\n")
	dir := filepath.Join(root, "s", "subagents")
	interrupted := `{"isSidechain":true,"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}` + "\n"
	meta := `{"agentType":"general-purpose","description":"d","toolUseId":"toolu_1"}`
	for id, body := range map[string]string{
		"stopped": `{"type":"assistant"}` + "\n" + interrupted,
		"resumed": interrupted + `{"type":"assistant"}` + "\n",
		"plain":   `{"type":"assistant"}` + "\n",
	} {
		writeFile(t, filepath.Join(dir, "agent-"+id+".meta.json"), meta)
		writeFile(t, filepath.Join(dir, "agent-"+id+".jsonl"), body)
	}
	got := map[string]Subagent{}
	for _, sub := range ClaudeSubagents(session) {
		got[sub.ID] = sub
	}
	if !got["stopped"].Stopped || !got["stopped"].Ended || got["stopped"].Active(time.Now()) {
		t.Fatalf("interrupted child = %+v", got["stopped"])
	}
	if got["resumed"].Stopped || got["plain"].Stopped {
		t.Fatalf("only a closing marker stops a run: resumed=%v plain=%v", got["resumed"].Stopped, got["plain"].Stopped)
	}

	cdir := filepath.Join(root, "sessions", time.Now().Format("2006/01/02"))
	ts := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	parent := filepath.Join(cdir, "rollout-p.jsonl")
	writeFile(t, parent, codexMeta("P1", `"exec"`, ts))
	spawn := func(id string) string {
		return codexMeta(id, `{"subagent":{"thread_spawn":{"parent_thread_id":"P1","depth":1,"agent_path":"/root/`+id+`"}}}`, ts)
	}
	writeFile(t, filepath.Join(cdir, "rollout-a.jsonl"), spawn("A")+`{"type":"event_msg","payload":{"type":"turn_aborted","reason":"interrupted"}}`+"\n")
	writeFile(t, filepath.Join(cdir, "rollout-d.jsonl"), spawn("D")+`{"type":"event_msg","payload":{"type":"task_complete"}}`+"\n")
	byName := map[string]Subagent{}
	for _, sub := range SubagentsFor(AgentCodex, parent) {
		byName[sub.Name] = sub
	}
	if !byName["A"].Stopped || !byName["A"].Ended || byName["D"].Stopped || !byName["D"].Ended {
		t.Fatalf("codex A=%+v D=%+v", byName["A"], byName["D"])
	}
}
