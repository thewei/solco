package nativeui

import (
	"strings"
	"testing"
)

// A fresh omp/pi pane has no conversation until its transcript file exists;
// queueing then used to surface the ledger's raw "requires request id,
// conversation and text" and the red line outlived the later bind.
func TestEnqueueRefusesCleanlyBeforeTheConversationIsLinked(t *testing.T) {
	s := &Shell{chatPaneID: "p1"}
	s.enqueueChatFollowUp("next step")
	if !strings.HasPrefix(s.chatOutcome, "Cannot queue yet") || strings.Contains(s.chatOutcome, "request id") {
		t.Fatalf("outcome = %q", s.chatOutcome)
	}
}
