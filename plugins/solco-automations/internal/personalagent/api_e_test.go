package personalagent

import "testing"

// The CLI surface of stage E: checks (approval-gated), review, merge.
func TestAPIReviewAndMergeThroughTheCLI(t *testing.T) {
	f := newReview(t)
	api := &API{Store: f.s, Reviewer: f.r}
	f.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	f.finish()

	if got, err := call(t, api, `{"requestId":"k1","commands":["test -f x.txt"]}`, "assistant", "checks", "set", f.asst.ID); err != nil {
		t.Fatal(err)
	} else if got["approval"].(map[string]any)["state"] != ApprovalPending {
		t.Fatalf("checks card = %v", got)
	}
	if got, _ := call(t, api, ``, "assistant", "checks", "show", f.asst.ID); len(got["checks"].([]any)) != 0 {
		t.Fatalf("a check was active before approval: %v", got)
	}

	started, err := call(t, api, `{"requestId":"s1"}`, "assistant", "review", "start", f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	rv := started["review"].(map[string]any)
	if rv["state"] != ReviewReady || len(rv["checks"].([]any)) != 0 {
		t.Fatalf("review = %v", rv)
	}
	if _, err := call(t, api, ``, "assistant", "review", "merge", rv["id"].(string)); codeOf(err) != "bad_state" {
		t.Fatalf("merge without approval = %q (%v)", codeOf(err), err)
	}
	asked, err := call(t, api, `{"requestId":"m1"}`, "assistant", "review", "request-merge", rv["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	ap := asked["approval"].(map[string]any)
	if _, _, err := f.s.DecideApproval("d1", ap["id"].(string), ap["actionHash"].(string), true); err != nil {
		t.Fatal(err)
	}
	merged, err := call(t, api, ``, "assistant", "review", "merge", rv["id"].(string))
	if err != nil || merged["review"].(map[string]any)["state"] != ReviewMerged {
		t.Fatalf("merge = %v %v", merged, err)
	}
	shown, err := call(t, api, ``, "assistant", "review", "show", f.task.ID)
	if err != nil || shown["review"].(map[string]any)["state"] != ReviewMerged {
		t.Fatalf("show = %v %v", shown, err)
	}
	if _, err := call(t, &API{Store: f.s}, ``, "assistant", "review", "show", f.task.ID); codeOf(err) != "unavailable" {
		t.Fatalf("no reviewer = %q", codeOf(err))
	}
}

// Q02 through the CLI: the Owner sends work back or closes it; unknown fields are refused.
func TestAPIReviewResumeAndRejectThroughTheCLI(t *testing.T) {
	f := newReview(t)
	api := &API{Store: f.s, Reviewer: f.r}
	f.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	f.finish()
	started, err := call(t, api, `{"requestId":"s1"}`, "assistant", "review", "start", f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	id := started["review"].(map[string]any)["id"].(string)
	if _, err := call(t, api, `{"requestId":"r0","feedback":""}`, "assistant", "review", "resume", id); codeOf(err) != "invalid" {
		t.Fatalf("empty feedback = %q", codeOf(err))
	}
	if _, err := call(t, api, `{"requestId":"r1","feedbak":"typo"}`, "assistant", "review", "resume", id); codeOf(err) != "invalid" {
		t.Fatalf("typo'd field = %q", codeOf(err))
	}
	got, err := call(t, api, `{"requestId":"r2","feedback":"add a test"}`, "assistant", "review", "resume", id)
	if err != nil || got["review"].(map[string]any)["state"] != ReviewChangesRequested {
		t.Fatalf("resume = %v %v", got, err)
	}
	if _, err := call(t, api, `{"requestId":"r3","reason":"no"}`, "assistant", "review", "reject", id); codeOf(err) != "bad_state" {
		t.Fatalf("reject after resume = %q", codeOf(err))
	}
	// Second round: reject.
	f.agentWork(map[string]string{"y.txt": "2\n"}, "again")
	f.finish()
	s2, err := call(t, api, `{"requestId":"s2"}`, "assistant", "review", "start", f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	id2 := s2["review"].(map[string]any)["id"].(string)
	rj, err := call(t, api, `{"requestId":"rj","reason":"wrong approach"}`, "assistant", "review", "reject", id2)
	if err != nil || rj["review"].(map[string]any)["state"] != ReviewRejected {
		t.Fatalf("reject = %v %v", rj, err)
	}
}
