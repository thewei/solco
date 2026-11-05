package personalagent

import (
	"context"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/herdr"
	"github.com/thewei/solco/plugins/solco-automations/internal/host"
)

// The three live Herdr gates of the Coordinator report pump. They live here
// (not in the daemon) so the real-Herdr e2e gate exercises exactly what runs
// in production.

// ReportReady: only an interactive-ready, idle OMP in exactly this pane may be
// handed a report. SQLite remembering a successful launch is not proof.
func ReportReady(session, paneID string) bool {
	state, err := (herdr.Client{Session: session}).AgentState(paneID)
	return err == nil && state.PaneID == paneID && state.Kind == "omp" &&
		state.Status == "idle" && state.InteractiveReady
}

// ReportDialog: a pane screen read failure is not permission to type into an
// unknown first-run trust / authentication dialog; automated reports fail closed.
func ReportDialog(session, paneID string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	screen, err := (herdr.Client{Session: session}).PaneReadContext(ctx, paneID, 60)
	if err != nil {
		return "unavailable", true
	}
	return host.StartupDialog(screen)
}

// SubmitReport takes only Herdr's prompt acceptance as its receipt. It must
// not await the brief 'working' status: a very fast OMP reply may complete
// before Herdr samples working and falsely stall. A successful return is not
// evidence of a human-facing model answer.
func SubmitReport(session, paneID, text string) error {
	err := (herdr.Client{Session: session}).AgentPromptAccepted(paneID, text)
	switch {
	case err == nil:
		return nil
	case herdr.HasCode(err, herdr.CodePaneBusy), herdr.HasCode(err, herdr.CodeNotReady), herdr.HasCode(err, herdr.CodeAgentGone):
		return ErrSubmitRefused
	default:
		return err
	}
}
