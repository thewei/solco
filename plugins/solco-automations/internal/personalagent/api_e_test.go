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
