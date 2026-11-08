package personalagent

import (
	"testing"

	"github.com/thewei/solco/plugins/solco-automations/internal/herdr"
)

// Herdr calls a finished turn nobody has looked at "done" and turns it into
// "idle" only once its pane is seen. A Coordinator in a background workspace
// stays "done" for good, yet it is exactly as free to take a prompt: found on a
// real Mac, where a restarted Coordinator never received its queued wake
// because the pump insisted on "idle".
func TestReportReadyAcceptsAFinishedTurnNobodyHasSeen(t *testing.T) {
	ok := herdr.AgentLiveState{PaneID: "w3:p1", Kind: "omp", InteractiveReady: true}
	for _, c := range []struct {
		status string
		ready  bool
		kind   string
		pane   string
		want   bool
	}{
		{"idle", true, "omp", "w3:p1", true},
		{"done", true, "omp", "w3:p1", true},
		{"working", true, "omp", "w3:p1", false},
		{"blocked", true, "omp", "w3:p1", false},
		{"done", false, "omp", "w3:p1", false},
		{"done", true, "claude", "w3:p1", false},
		{"done", true, "omp", "w9:p1", false},
	} {
		s := ok
		s.Status, s.InteractiveReady, s.Kind = c.status, c.ready, c.kind
		if got := reportReadyState(s, c.pane, "w3:p1"); got != c.want {
			t.Errorf("status=%s ready=%v kind=%s pane=%s: got %v, want %v", c.status, c.ready, c.kind, c.pane, got, c.want)
		}
	}
}
