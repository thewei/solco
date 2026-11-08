package personalagent

import (
	"strings"
	"testing"

	"github.com/thewei/solco/plugins/solco-automations/internal/herdr"
)

// Gap L02: when Herdr drops, an Agent stops or a trust question waits, the
// person sees which of those it is and what to do — and Solco never launches
// the same work a second time because it could not tell.

// Herdr going quiet in the middle of a run proves nothing about the Agent: the
// run is parked for reconciliation (which reattaches by label when Herdr
// answers again), never failed and never relaunched.
func TestHerdrLostMidRunParksTheRunAndNeverRelaunches(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	l := &fakeLauncher{workErr: &herdr.APIError{Command: "agent wait", Message: "failed to connect to socket"}}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon"}
	run, _ := d.RunTask(t.Context(), task.ID)
	if run.Status != RunUncertain {
		t.Fatalf("a dropped Herdr failed the run: %s", run.Status)
	}
	tk, _ := s.GetTask(task.ID)
	if tk.State != TaskUncertain || !tk.NeedsYou {
		t.Fatalf("task = %+v", tk)
	}
	if l.provisions != 1 {
		t.Fatalf("provisions = %d: the work was launched again", l.provisions)
	}
	it, ok := byTask(mustInbox(t, s, task.AssistantID), task.ID)
	if !ok || it.Reason != "herdr_lost" || !strings.Contains(it.Detail, "Herdr") || len(it.Actions) != 1 || it.Actions[0] != "open_pane" {
		t.Fatalf("card = %+v", it)
	}
	// It comes back by itself: Herdr answers, the Agent is found, the run runs.
	l.workErr = nil
	l.probeAgent = true
	l.found = map[string]Session{run.ID: {HerdrSession: "default", WorkspaceID: "w1", PaneID: "w1:p1"}}
	if n, err := d.ReconcilePending(t.Context()); err != nil || n != 1 {
		t.Fatalf("reconcile = %d, %v", n, err)
	}
	d.Wait()
	if got, _ := s.GetRun(run.ID); got.Status == RunUncertain || got.Status == RunFailed {
		t.Fatalf("after reconnect the run is %s", got.Status)
	}
}

// Anything that is not "Herdr gave no answer" keeps failing the way it did.
func TestAnAgentErrorWithAHerdrCodeStillFailsTheRun(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	l := &fakeLauncher{workErr: &herdr.APIError{Command: "agent wait", Code: "pane_not_found", Message: "x"}}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon"}
	if run, _ := d.RunTask(t.Context(), task.ID); run.Status != RunFailed {
		t.Fatalf("run = %s", run.Status)
	}
}

// A workspace that is open but holds no Agent is its own card: look at the
// pane, then retry on purpose.
func TestAnAgentThatIsGoneGetsItsOwnCard(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	d := &Dispatcher{Store: s, Launcher: &fakeLauncher{workErr: ErrAgentMissing}, Claimant: "daemon"}
	run, _ := d.RunTask(t.Context(), task.ID)
	if run.Status != RunUncertain {
		t.Fatalf("run = %s", run.Status)
	}
	it, ok := byTask(mustInbox(t, s, task.AssistantID), task.ID)
	if !ok || it.Reason != "agent_missing" || !strings.Contains(it.Detail, "Agent") {
		t.Fatalf("card = %+v", it)
	}
}

// A folder-trust question waits for a person: the card says which Agent asks
// what, not just "a prompt".
func TestABlockedCardNamesTheQuestionTheAgentAsks(t *testing.T) {
	s, _ := openTest(t)
	_, task := newQueuedTask(t, s, KindResearch)
	l := &fakeLauncher{workErr: &BlockedError{Reason: "Codex is asking whether to trust this folder"}}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon"}
	_, _ = d.RunTask(t.Context(), task.ID)
	in, err := s.Inbox(task.AssistantID, InboxOptions{BlockStall: 0})
	if err != nil {
		t.Fatal(err)
	}
	it, ok := byTask(in, task.ID)
	if !ok || it.Reason != "permission_prompt" || !strings.Contains(it.Detail, "Codex is asking whether to trust this folder") {
		t.Fatalf("card = %+v", it)
	}
}
