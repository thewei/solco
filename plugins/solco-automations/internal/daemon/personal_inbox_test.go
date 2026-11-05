package daemon

import (
	"testing"

	"github.com/thewei/solco/plugins/solco-automations/internal/personalagent"
)

// Q05: the toast names the card's reason when it has one, the kind otherwise.
func TestAttentionTitleNamesTheReasonBeforeTheKind(t *testing.T) {
	for _, c := range []struct {
		it   personalagent.InboxItem
		want string
	}{
		{personalagent.InboxItem{Kind: personalagent.InboxStalled, Reason: "permission_prompt"}, "Waiting for your answer"},
		{personalagent.InboxItem{Kind: personalagent.InboxReview, Reason: "review_checks_failed"}, "Checks failed"},
		{personalagent.InboxItem{Kind: personalagent.InboxReview, Reason: "merge_failed"}, "Merge failed"},
		{personalagent.InboxItem{Kind: personalagent.InboxNeedsYou, Reason: "task_failed"}, "Task failed"},
		{personalagent.InboxItem{Kind: personalagent.InboxReview, Reason: "review_ready"}, "Ready for review"},
		{personalagent.InboxItem{Kind: personalagent.InboxApproval}, "Approval needed"},
		{personalagent.InboxItem{Kind: personalagent.InboxNeedsYou, Reason: "something_new"}, "Needs you"},
	} {
		if got := attentionTitle(c.it); got != c.want {
			t.Errorf("%+v = %q, want %q", c.it, got, c.want)
		}
	}
}

// Q06: a toast says which Assistant it came from.
func TestAttentionBodyNamesItsSource(t *testing.T) {
	for _, c := range []struct{ name, detail, want string }{
		{"Ada", "Ready for your review", "From Ada · Ready for your review"},
		{"Ada", "", "From Ada"},
		{"", "Ready for your review", "Ready for your review"},
	} {
		if got := attentionBody(c.name, c.detail); got != c.want {
			t.Errorf("attentionBody(%q,%q) = %q, want %q", c.name, c.detail, got, c.want)
		}
	}
}
