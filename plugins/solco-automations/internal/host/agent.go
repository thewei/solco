package host

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
	"github.com/thewei/solco/plugins/solco-automations/internal/herdr"
)

// agentWork starts an interactive agent, hands it the prompt, and waits for it
// to settle. Every step verifies what actually happened rather than trusting
// the previous call's verdict: a freshly started agent is reported ready before
// it can accept input, and herdr's answers are correspondingly hedged.
type agentWork struct {
	ops   ops
	knobs knobs
	a     config.Automation
}

func (w agentWork) do(s Session, timeout time.Duration) error {
	if err := w.start(s); err != nil {
		return fmt.Errorf("start %s agent: %w", w.a.Agent, err)
	}
	if strings.TrimSpace(w.a.Prompt) == "" {
		// Nothing to hand over (a resumed conversation that already holds
		// this brief): the work is done once the agent is up and not asking.
		return w.ready(s)
	}
	if err := w.submit(s); err != nil {
		return err
	}
	if err := w.await(s, timeout); err != nil {
		return fmt.Errorf("waiting for the agent: %w", err)
	}
	// An agent sitting on a startup dialog looks idle: a run that ends there
	// did not run (Codex opens its hook review after the prompt was typed).
	return w.blockedBy(s)
}

// args assembles what gets forwarded to the agent executable. Model and MCP
// config are sugar over agent_args, and go in front so an explicit agent_args
// entry can still override them.
func (w agentWork) args() []string { return agentArgs(w.a) }

func agentArgs(a config.Automation) []string {
	args := a.AgentArgs
	if a.Model != "" {
		args = append([]string{"--model", a.Model}, args...)
	}
	if a.MCPConfig != "" {
		args = append([]string{"--mcp-config", a.MCPConfig}, args...)
	}
	return args
}

// start launches the agent, retrying while herdr says the pane is not a shell
// yet. Any other error is final — a bad agent kind or a missing binary will
// not fix itself, and retrying only delays the report.
func (w agentWork) start(s Session) error {
	name := agentNameFor(w.a, s.PaneID)
	deadline := time.Now().Add(w.knobs.paneReady)
	for {
		err := w.ops.AgentStart(name, w.a.Agent, s.PaneID, w.args())
		if err != nil && (w.ops.HasCode(err, herdr.CodeKindMismatch) || w.ops.HasCode(err, herdr.CodeNotReady)) {
			return w.settle(s, err)
		}
		if err == nil {
			return nil
		}
		if !w.ops.HasCode(err, herdr.CodePaneBusy) {
			return w.explainStart(s, err)
		}
		if !time.Now().Before(deadline) {
			return err
		}
		log.Printf("pane has no shell yet, retrying agent start in %s", w.knobs.paneReadyPoll)
		time.Sleep(w.knobs.paneReadyPoll)
	}
}

// explainStart adds the agent's own refusal to a start failure. An executable
// that rejects its flags or a broken extension leaves a shell prompt behind and
// herdr only reports a startup timeout; the pane has the real reason.
func (w agentWork) explainStart(s Session, err error) error {
	screen, rerr := w.ops.PaneRead(s.PaneID, 40)
	if rerr != nil {
		return err
	}
	for _, line := range strings.Split(screen, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Error:") || strings.HasPrefix(line, "Failed to load extension") {
			if len(line) > 240 {
				line = line[:240]
			}
			return fmt.Errorf("%w (the agent said: %s)", err, line)
		}
	}
	return err
}

// ready waits for a started agent that has no prompt to take: Herdr names it
// as the expected kind with a real status, and no startup dialog is open. The
// window is agentReady — the same time an agent Herdr misreads at launch gets.
func (w agentWork) ready(s Session) error {
	deadline := time.Now().Add(w.knobs.agentReady)
	for {
		if err := w.blockedBy(s); err != nil {
			return err
		}
		kind, status, err := w.ops.AgentIdentity(s.PaneID)
		if err == nil && kind == w.a.Agent && status != "" && status != "unknown" {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the %s agent never became ready", w.a.Agent)
		}
		time.Sleep(w.knobs.statusPoll)
	}
}

// settle is what a kind mismatch (or a not-ready answer) at start is given instead of being a verdict:
// time for the pane to show which agent it really is. It ends one of three
// ways — the expected agent registers and the run goes on; a startup dialog is
// found, which no amount of waiting answers, so the run stops there with the
// reason; or the window closes and the mismatch is reported as it came.
func (w agentWork) settle(s Session, mismatch error) error {
	deadline := time.Now().Add(w.knobs.agentReady)
	for {
		if err := w.blockedBy(s); err != nil {
			return err
		}
		kind, status, err := w.ops.AgentIdentity(s.PaneID)
		if err == nil && kind == w.a.Agent && status != "" && status != "unknown" {
			log.Printf("%s registered as %s after a kind mismatch, carrying on", s.PaneID, kind)
			return nil
		}
		if !time.Now().Before(deadline) {
			return mismatch
		}
		time.Sleep(w.knobs.statusPoll)
	}
}

// submit gets the prompt in front of the agent and confirms it started working.
//
// A freshly started agent is reported ready before it can actually accept
// input — it may still be connecting MCP servers, especially right after the
// machine wakes. herdr then reports agent_prompt_stalled even though the text
// reached the composer, so each step here checks the status rather than
// trusting the previous call's verdict.
func (w agentWork) submit(s Session) error {
	// Nothing is typed into an agent that is asking a person a question: the
	// first keystroke would be an answer.
	if err := w.blockedBy(s); err != nil {
		return err
	}
	err := w.ops.AgentSubmit(s.PaneID, w.a.Prompt)
	if err == nil {
		return nil
	}
	if herdr.IdentityRefused(err) {
		return w.submitPaneScoped(s)
	}
	if !w.ops.HasCode(err, herdr.CodeStalled) {
		return fmt.Errorf("prompt: %w", err)
	}

	// The text is probably in the composer, just not submitted. Give the agent
	// a moment, then press Enter for it, then fall back to typing it again.
	// Each nudge re-checks the screen: a dialog that came up late would take
	// the Enter as its answer. A transcript that already holds the prompt is a
	// stronger answer than any screen: the Agent got it (and may even have
	// finished inside herdr's window), so nothing is typed again.
	if w.working(s, w.knobs.promptSettle) || promptDelivered(w.a) {
		return nil
	}
	if err := w.blockedBy(s); err != nil {
		return err
	}
	log.Printf("prompt stalled on %s, submitting the pending composer", s.PaneID)
	if err := w.ops.AgentSubmitPending(s.PaneID); err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	if w.working(s, w.knobs.promptSettle) || promptDelivered(w.a) {
		return nil
	}

	if err := w.blockedBy(s); err != nil {
		return err
	}
	log.Printf("still idle on %s, retyping the prompt", s.PaneID)
	if err := w.ops.AgentSubmit(s.PaneID, w.a.Prompt); err != nil && !w.ops.HasCode(err, herdr.CodeStalled) {
		return fmt.Errorf("prompt: %w", err)
	}
	if w.working(s, w.knobs.promptSettleLast) || promptDelivered(w.a) {
		return nil
	}
	return fmt.Errorf("prompt: the agent never started working; it may still be initialising")
}

// submitPaneScoped hands the prompt to a pane Herdr refused to address as an
// agent (IdentityRefused: an omp started through bun is `bun.native` to
// Herdr's foreground check). It is the same fallback Solco's own sender uses,
// narrowed the same way — only for those two refusals.
//
// Typing is only safe while Herdr still lists this very agent in the pane: had
// the agent exited, the pane is a shell and the prompt would run as commands.
// The text is typed once and never retyped (a retype would double a prompt
// that landed late); the only nudge is Enter on the composer.
func (w agentWork) submitPaneScoped(s Session) error {
	kind, status, err := w.ops.AgentIdentity(s.PaneID)
	if err != nil || kind != w.a.Agent || status == "" || status == "unknown" {
		return fmt.Errorf("prompt: Herdr refused the prompt and %s is not a live %s agent, nothing typed", s.PaneID, w.a.Agent)
	}
	log.Printf("agent prompt refused on %s (foreground identity), typing into the pane", s.PaneID)
	if err := w.ops.PaneRun(s.PaneID, w.a.Prompt); err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	if w.working(s, w.knobs.promptSettle) || promptDelivered(w.a) {
		return nil
	}
	if err := w.blockedBy(s); err != nil {
		return err
	}
	if err := w.ops.AgentSubmitPending(s.PaneID); err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	if w.working(s, w.knobs.promptSettleLast) || promptDelivered(w.a) {
		return nil
	}
	return fmt.Errorf("prompt: the agent never started working; it may still be initialising")
}

// working polls until the agent leaves idle, or the window closes.
func (w agentWork) working(s Session, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		status, err := w.ops.AgentStatus(s.PaneID)
		if err == nil && status != "idle" && status != "unknown" {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(w.knobs.statusPoll)
	}
}

// await blocks until the agent settles, the run is cancelled, or the timeout
// runs out.
//
// It waits in slices rather than handing herdr the whole timeout at once. A run
// whose workspace was closed thirteen seconds in used to sit here for the full
// forty-five minutes before anyone was told, holding the automation's in-flight
// slot the whole time and skipping the next occurrence.
func (w agentWork) await(s Session, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	var downSince time.Time
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			if last != nil {
				return last
			}
			return fmt.Errorf("the agent was still working after %s", timeout)
		}
		slice := min(w.knobs.waitSlice, remaining)

		err := w.ops.AgentWait(s.PaneID, slice)
		if err == nil {
			return nil
		}
		if w.ops.HasCode(err, herdr.CodeAgentGone) || w.ops.HasCode(err, herdr.CodeAgentNotFound) {
			return w.agentGoneOutcome(s)
		}
		// The slice expired with the agent still working, which is the normal
		// case. Keep it: if the deadline passes it is the most accurate thing
		// we have to report.
		last = err
		if w.ops.HasCode(err, herdr.CodeServerDown) {
			// Herdr itself is gone, so the wait returns at once: pace the
			// loop, and past the grace give the failure back so the run is
			// parked rather than held (and silent) until its deadline.
			if downSince.IsZero() {
				downSince = time.Now()
			}
			if time.Since(downSince) > w.knobs.herdrDownGrace {
				return err
			}
			time.Sleep(w.knobs.statusPoll)
		} else {
			downSince = time.Time{}
		}
	}
}

// agentGoneOutcome disambiguates herdr's agent_not_running: it means either
// the workspace was closed, or the agent process died inside a workspace that
// is still open. herdr.go documents both under the same code, but only the
// first is a cancellation — the second is a crash, and reporting it as
// "cancelled" hides it behind the one status the plugin deliberately never
// notifies about.
func (w agentWork) agentGoneOutcome(s Session) error {
	_, err := w.ops.AgentStatus(s.PaneID)
	switch {
	case err == nil:
		// The pane is alive and answered, so nothing closed the workspace —
		// the agent itself is what is missing.
		return fmt.Errorf("the agent exited before finishing")
	case w.ops.HasCode(err, herdr.CodeWorkspaceGone), w.ops.HasCode(err, herdr.CodeAgentNotFound):
		// agent_not_found is herdr 0.9.5's word for a pane whose workspace is gone.
		return ErrCancelled
	case w.ops.HasCode(err, herdr.CodeAgentGone):
		// AgentStatus is itself agent-scoped, so a dead agent in a live
		// workspace is exactly as likely to answer with its own
		// agent_not_running as with a status string. Either shape means the
		// same thing: the workspace is fine, the agent is what is gone.
		return fmt.Errorf("the agent exited before finishing")
	default:
		// Unreadable for some other, unrelated reason: not enough to call it
		// a crash, so keep the existing behaviour rather than turning a doubt
		// into a failure.
		log.Printf("could not confirm why %s went silent (%v), recording it as cancelled", s.PaneID, err)
		return ErrCancelled
	}
}
