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
