package personalagent

import (
	"fmt"
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

	g, err := call(t, a, fmt.Sprintf(`{"requestId":"g1","assistantId":%q,"title":"Watch PR","commitments":[{"kind":"pr_watch","spec":"{\"repo\":\"o/r\",\"number\":12}"}]}`, aid), "assistant", "goal", "create")
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

	m, err := call(t, a, fmt.Sprintf(`{"requestId":"m1","assistantId":%q,"content":"Likes brief reports","sourceRef":"run:r_1"}`, aid), "assistant", "memory", "propose")
	if err != nil {
		t.Fatal(err)
	}
	mid := field(m, "memory", "id").(string)
	vis, _ := call(t, a, ``, "assistant", "memory", "visible", aid)
	if len(vis["memory"].([]any)) != 0 {
		t.Fatalf("candidate visible: %v", vis)
	}
	if _, err := call(t, a, `{"requestId":"m2","expectedRevision":1}`, "assistant", "memory", "approve", mid); err != nil {
		t.Fatal(err)
	}
	vis, _ = call(t, a, ``, "assistant", "memory", "visible", aid)
	if len(vis["memory"].([]any)) != 1 {
		t.Fatalf("approved not visible: %v", vis)
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
