package personalagent

import (
	"fmt"
	"strings"
	"testing"
)

func field(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		cur = cur.(map[string]any)[p]
	}
	return cur
}

func TestAPIGoalMemoryApprovalFlow(t *testing.T) {
	s, _ := openTest(t)
	a := &API{Store: s}

	asst, _ := call(t, a, `{"requestId":"a1","name":"Ada"}`, "assistant", "create")
	aid := field(asst, "assistant", "id").(string)

	g, err := call(t, a, fmt.Sprintf(`{"requestId":"g1","assistantId":%q,"title":"Watch PR","commitments":[{"kind":"manual"}]}`, aid), "assistant", "goal", "create")
	if err != nil {
		t.Fatal(err)
	}
	gid := field(g, "goal", "id").(string)
	if field(g, "goal", "state") != "draft" {
		t.Fatalf("goal = %v", g)
	}
	g, err = call(t, a, `{"requestId":"g2","expectedRevision":1}`, "assistant", "goal", "approve", gid)
	if err != nil || field(g, "goal", "state") != "active" {
		t.Fatalf("approve: %v %v", g, err)
	}
	if _, err := call(t, a, `{"requestId":"g3","expectedRevision":1}`, "assistant", "goal", "pause", gid); codeOf(err) != "stale_revision" {
		t.Fatalf("stale pause code = %q", codeOf(err))
	}
	if _, err := call(t, a, `{"requestId":"g4","expectedRevision":2,"evidence":""}`, "assistant", "goal", "complete", gid); codeOf(err) != "invalid" {
		t.Fatalf("complete without evidence code = %q", codeOf(err))
	}
	list, _ := call(t, a, ``, "assistant", "goal", "list", "--assistant", aid)
	if len(list["goals"].([]any)) != 1 {
		t.Fatalf("goals = %v", list)
	}

	t.Setenv("SOLCO_ASSISTANTS_MEMORY", t.TempDir())
	empty, err := call(t, a, ``, "assistant", "memory", "read", aid)
	if err != nil {
		t.Fatal(err)
	}
	if field(empty, "notes", "user", "text") != "" || field(empty, "notes", "assistant", "text") != "" {
		t.Fatalf("a new Assistant has no memory: %v", empty)
	}
	if _, err := call(t, a, `{"note":"Likes brief reports"}`, "assistant", "memory", "add", aid); err != nil {
		t.Fatal(err)
	}
	// No approval step: the note is in the file at once, and a repeat is not doubled.
	if again, err := call(t, a, `{"note":"likes  brief reports"}`, "assistant", "memory", "add", aid); err != nil || field(again, "note", "added") != false {
		t.Fatalf("a repeated note is not added twice: %v %v", again, err)
	}
	read, _ := call(t, a, ``, "assistant", "memory", "read", aid)
	if txt := field(read, "notes", "assistant", "text").(string); strings.Count(txt, "brief reports") != 1 {
		t.Fatalf("assistant file = %q", txt)
	}
	// The person writes their own file; a stale rev is refused, not merged.
	rev := field(read, "notes", "user", "rev").(string)
	if _, err := call(t, a, fmt.Sprintf(`{"text":"Call me Wei","expectedRev":%q}`, rev), "assistant", "memory", "write-user", aid); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, a, fmt.Sprintf(`{"text":"overwrite","expectedRev":%q}`, rev), "assistant", "memory", "write-user", aid); codeOf(err) != "stale_revision" {
		t.Fatalf("a stale write code = %q", codeOf(err))
	}

	task, _ := call(t, a, fmt.Sprintf(`{"requestId":"t1","assistantId":%q,"kind":"research","title":"x","brief":"x"}`, aid), "assistant", "task", "create")
	tid := field(task, "task", "id").(string)
	ap, err := call(t, a, fmt.Sprintf(`{"requestId":"p1","assistantId":%q,"subjectKind":"task","subjectId":%q,"action":"merge"}`, aid, tid), "assistant", "approval", "request")
	if err != nil {
		t.Fatal(err)
	}
	pid, hash := field(ap, "approval", "id").(string), field(ap, "approval", "actionHash").(string)
	if _, err := call(t, a, fmt.Sprintf(`{"requestId":"p2","actionHash":"nope","approve":true}`), "assistant", "approval", "decide", pid); codeOf(err) != "invalid" {
		t.Fatalf("wrong hash code = %q", codeOf(err))
	}
	done, err := call(t, a, fmt.Sprintf(`{"requestId":"p3","actionHash":%q,"approve":true}`, hash), "assistant", "approval", "decide", pid)
	if err != nil || field(done, "approval", "state") != "approved" {
		t.Fatalf("decide: %v %v", done, err)
	}
	if _, err := call(t, a, fmt.Sprintf(`{"requestId":"p4","actionHash":%q,"approve":false}`, hash), "assistant", "approval", "decide", pid); codeOf(err) != "bad_state" {
		t.Fatalf("second decision code = %q", codeOf(err))
	}
	ov, _ := call(t, a, ``, "assistant", "overview")
	row := ov["assistants"].([]any)[0].(map[string]any)
	if row["goals"] != float64(1) {
		t.Fatalf("overview = %v", row)
	}
}
