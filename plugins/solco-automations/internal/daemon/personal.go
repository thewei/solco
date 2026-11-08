package daemon

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/cleanup"
	"github.com/thewei/solco/plugins/solco-automations/internal/config"
	"github.com/thewei/solco/plugins/solco-automations/internal/herdr"
	"github.com/thewei/solco/plugins/solco-automations/internal/history"
	"github.com/thewei/solco/plugins/solco-automations/internal/host"
	"github.com/thewei/solco/plugins/solco-automations/internal/notify"
	"github.com/thewei/solco/plugins/solco-automations/internal/personalagent"
)

// ruleEvery is how often the owned rules are evaluated: a few seconds of
// precision is plenty for cron, and the config is a file read.
const ruleEvery = 10 * time.Second

// ruleHistoryKeep / ruleHistoryMax mirror the old history log's retention.
const (
	ruleHistoryKeep = 30 * 24 * time.Hour
	ruleHistoryMax  = 5000
)

// taskWorktreeKeep is how long a finished coding task's checkout stays after
// its branch is merged (Gap L07): long enough to look back at, short enough
// that merged work does not pile up. Only merged, closed-workspace checkouts of
// branches the core recorded ever go; failed work, open workspaces, unmerged
// commits and uncommitted changes are never touched.
const taskWorktreeKeep = 14 * 24 * time.Hour

// tidyWorktrees is cleanup.TidyOwned, replaceable in tests.
var tidyWorktrees = cleanup.TidyOwned

// inboxEvery is how often the inbox is read for something new to say.
const inboxEvery = 15 * time.Second

// reconcileEvery bounds remote Herdr lookups for uncertain Agent starts.
const reconcileEvery = 20 * time.Second

// idleEvery is how often Coordinators are checked for idle sleep: a few Herdr
// reads, so it stays rare.
const idleEvery = 30 * time.Second

// pumpInterval is how often queued Assistant tasks are looked for. Much
// shorter than the cron tick: a task a person just delegated should start now.
const pumpInterval = 3 * time.Second

// personal is the Assistants half of the daemon: the same process, the same
// lock, the one scheduler. It opens the store, settles what a crash left
// behind and then pumps queued tasks into real Herdr Agents.
type personal struct {
	store         *personalagent.Store
	disp          *personalagent.Dispatcher
	cancel        context.CancelFunc
	ctx           context.Context
	beat          *personalagent.Beater
	triggers      *personalagent.TriggerScheduler
	sup           *personalagent.Supervisor
	reports       *personalagent.ReportPump
	routines      *personalagent.RoutineKeeper
	rules         *personalagent.RuleScheduler
	reviewer      *personalagent.Reviewer
	inbox         *personalagent.InboxWatcher
	results       *personalagent.ResultWatcher
	groups        *personalagent.GroupPump
	sleeper       *personalagent.IdleSleeper
	lastIdle      time.Time
	sleeping      atomic.Bool
	lastReconcile time.Time
	reconciling   atomic.Bool
	reporting     atomic.Bool
	lastLook      time.Time
	notifier      *notify.Notifier
	lastRule      time.Time
	mu            sync.Mutex
	seen          map[string]string // run id -> last status written to history
}

func startPersonal(n *notify.Notifier) *personal {
	ownerKey, err := personalagent.OwnerKeyOption(personalagent.OwnerKeyPath())
	if err != nil {
		// An unreadable key never widens anything: grants stay inert, the
		// rest of the core keeps working.
		log.Printf("assistants: %v; grants will not authorise anything", err)
		ownerKey = func(*personalagent.Store) {}
	}
	store, err := personalagent.Open(personalagent.DBPath(), ownerKey)
	if err != nil {
		log.Printf("assistants: store unavailable, Assistants are paused: %v", err)
		return nil
	}
	if n, err := store.RecoverRoutineIntents(); err != nil {
		if !errors.Is(err, config.ErrDefinitionChanged) {
			// The core itself is unavailable or corrupt; never hand any
			// Assistant-owned rules over to the legacy scheduler.
			log.Printf("assistants: routine crash recovery failed, scheduling paused: %v", err)
			_ = store.Close()
			return nil
		}
		// One interrupted draft was edited externally. Its durable
		// intent still reserves the name; other Assistants may continue.
		log.Printf("assistants: interrupted routine needs review: %v", err)
	} else if n > 0 {
		log.Printf("assistants: recovered %d interrupted draft(s); awaiting Owner approval", n)
	}
	if exe, err := os.Executable(); err == nil {
		personalagent.CLI = exe
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &personal{store: store, ctx: ctx, cancel: cancel, notifier: n, seen: map[string]string{}}
	p.disp = &personalagent.Dispatcher{
		Store:       store,
		Launcher:    personalagent.NewHostLauncher(os.Getenv("SOLCO_ASSISTANTS_HERDR_SESSION"), personalagent.WorkDirRoot()),
		Claimant:    "daemon",
		MaxParallel: 2,
		Rules: func(name string) (config.Automation, bool) {
			cfg, err := config.Load()
			if err != nil {
				return config.Automation{}, false
			}
			a := cfg.Find(name)
			if a == nil {
				return config.Automation{}, false
			}
			return *a, true
		},
		OnSettled: p.settled,
		Worktrees: &personalagent.Worktrees{Store: store, Root: personalagent.DefaultWorktreeRoot()},
	}
	p.rules = &personalagent.RuleScheduler{Store: store, Load: config.Load}
	p.beat = &personalagent.Beater{Store: store}
	p.triggers = &personalagent.TriggerScheduler{Store: store}
	p.sup = &personalagent.Supervisor{Store: store}
	// One durable reporting outbox into the existing original OMP pane,
	// through Herdr's normal agent.prompt. Unlike Group/Worker delivery,
	// only the non-waiting pane-fenced acceptance receipt is used here;
	// no second Agent is launched and no OMP reply is fabricated.
	p.reports = &personalagent.ReportPump{
		Store: store, Submit: submitCoordinatorReport, Ready: coordinatorReportReady,
		Blocked: coordinatorReportDialog,
		Verify: func(marker string, acceptedAtMS int64) (personalagent.ReplyEvidence, error) {
			// Read only OMP's provider-owned JSONL. The unique marker links a
			// Herdr-accepted input to an actual final OMP message; if OMP's
			// home or transcript is missing, keep awaiting rather than guessing.
			home, err := os.UserHomeDir()
			if err != nil {
				return personalagent.ReplyEvidence{}, err
			}
			return personalagent.FindOMPReportReplySince(home, marker, acceptedAtMS)
		},
	}
	p.routines = &personalagent.RoutineKeeper{Store: store, Load: config.Load, ApproveChecked: personalagent.ApproveRoutineDefinition}
	p.inbox = &personalagent.InboxWatcher{Store: store}
	p.results = &personalagent.ResultWatcher{Store: store}
	p.groups = &personalagent.GroupPump{Store: store, Submit: submitToAgent, Blocked: paneDialog}
	// A Coordinator nobody has talked to for a while is stopped like the person
	// would (the next message resumes the same conversation).
	p.sleeper = &personalagent.IdleSleeper{
		Store: store, After: personalagent.IdleSleepAfter(),
		LastActive: personalagent.CoordinatorLastActive,
		AtPrompt:   personalagent.ReportReady,
		Close:      personalagent.HerdrPaneClose,
	}
	if n, err := store.RecoverCoordinatorReports(); err != nil {
		log.Printf("assistants: coordinator report recovery: %v", err)
	} else if n > 0 {
		log.Printf("assistants: %d interrupted coordinator report(s) need human reconciliation", n)
	}
	if n, err := store.RecoverGroupDeliveries(); err != nil {
		log.Printf("assistants: group delivery recovery failed: %v", err)
	} else if n > 0 {
		log.Printf("assistants: %d group message delivery(ies) left uncertain by the last shutdown", n)
	}
	p.reviewer = &personalagent.Reviewer{Store: store, Worktrees: p.disp.Worktrees}
	if n, err := p.reviewer.Recover(); err != nil {
		log.Printf("assistants: merge recovery failed: %v", err)
	} else if n > 0 {
		log.Printf("assistants: %d interrupted merge(s) settled from git's own state", n)
	}
	if n, err := store.Recover(); err != nil {
		log.Printf("assistants: recovery failed: %v", err)
	} else if n > 0 {
		log.Printf("assistants: %d run(s) left uncertain by the last shutdown; they wait for a person", n)
	}
	if n := p.disp.Resume(ctx); n > 0 {
		log.Printf("assistants: re-attached to %d running agent(s)", n)
	}
	return p
}

// tick starts what is queued. Safe on a nil receiver (store unavailable).
func (p *personal) tick() {
	if p == nil {
		return
	}
	// Heartbeats are a few indexed reads and, at most, one task insert each.
	if r, err := p.beat.Tick(); err != nil {
		log.Printf("assistants: heartbeat: %v", err)
	} else if r.Woke > 0 {
		log.Printf("assistants: %d heartbeat wake(s) queued", r.Woke)
	}
	// Approved event triggers: each new matching event becomes one task.
	if r, err := p.triggers.Tick(); err != nil {
		log.Printf("assistants: triggers: %v", err)
	} else if r.Fired > 0 {
		log.Printf("assistants: %d trigger task(s) queued", r.Fired)
	}
	// The Goal supervisor: facts from the event log, milestones handed out and
	// verified, completion earned — deterministic reads and a few inserts.
	if r, err := p.sup.Tick(); err != nil {
		log.Printf("assistants: goals: %v", err)
	} else if r.Dispatched+r.Completed > 0 {
		log.Printf("assistants: goals: %d milestone(s) delegated, %d goal(s) completed", r.Dispatched, r.Completed)
	}
	// Coordinator reporting can block on Herdr's AgentSubmit, and must never
	// delay the normal dispatcher/supervisor scheduler. Only one report pump
	// may run at once (including while a submitted prompt is uncertain).
	if p.reports != nil && p.reporting.CompareAndSwap(false, true) {
		go func() {
			defer p.reporting.Store(false)
			if r, err := p.reports.Tick(); err != nil {
				if p.ctx.Err() == nil {
					log.Printf("assistants: coordinator reports: %v", err)
				}
			} else if r.Submitted+r.Uncertain+r.Verified > 0 {
				log.Printf("assistants: coordinator reports: %d accepted by Herdr, %d verified OMP replies, %d need review", r.Submitted, r.Verified, r.Uncertain)
			}
		}()
	}
	// Idle Coordinators go to sleep. Herdr reads, so never inside the tick.
	if p.sleeper != nil && time.Since(p.lastIdle) >= idleEvery && p.sleeping.CompareAndSwap(false, true) {
		p.lastIdle = time.Now()
		go func() {
			defer p.sleeping.Store(false)
			if r, err := p.sleeper.Tick(); err != nil {
				if p.ctx.Err() == nil {
					log.Printf("assistants: idle sleep: %v", err)
				}
			} else if r.Slept > 0 {
				log.Printf("assistants: %d idle Coordinator(s) put to sleep (%d pane(s) would not close)", r.Slept, r.CloseFailed)
			}
		}()
	}
	// A Routine a person approved is enabled here (the answer itself only records the yes).
	if r, err := p.routines.Tick(); err != nil {
		log.Printf("assistants: routines: %v", err)
	} else if r.Enabled+r.Stale+r.Withdrawn > 0 {
		log.Printf("assistants: routines: %d enabled, %d changed since approval, %d withdrawn", r.Enabled, r.Stale, r.Withdrawn)
	}
	if time.Since(p.lastLook) >= inboxEvery {
		p.lastLook = time.Now()
		p.lookAtInbox()
		p.lookAtResults()
	}
	if time.Since(p.lastRule) >= ruleEvery {
		p.lastRule = time.Now()
		p.tickRules()
	}
	// Reattach positively identified Agents from uncertain starts without
	// re-launching them. This lookup can block on Herdr, so keep it off the
	// scheduler lane and allow only one pass at a time.
	if time.Since(p.lastReconcile) >= reconcileEvery && p.reconciling.CompareAndSwap(false, true) {
		p.lastReconcile = time.Now()
		go func() {
			defer p.reconciling.Store(false)
			if n, err := p.disp.ReconcilePending(p.ctx); err != nil && p.ctx.Err() == nil {
				log.Printf("assistants: reconciling uncertain starts: %v", err)
			} else if n > 0 {
				log.Printf("assistants: reattached %d previously uncertain Agent(s)", n)
			}
		}()
	}
	if n := p.disp.Pump(p.ctx); n > 0 {
		log.Printf("assistants: started %d task(s)", n)
	}
	// @-mentions in Groups: a deterministic pass, no model call of its own.
	if r, err := p.groups.Tick(); err != nil {
		log.Printf("assistants: groups: %v", err)
	} else if r.Started+r.Delivered+r.Failed > 0 {
		log.Printf("assistants: groups: %d agent(s) started, %d delivered, %d failed", r.Started, r.Delivered, r.Failed)
	}
}

func coordinatorReportReady(session, paneID string) bool {
	return personalagent.ReportReady(session, paneID)
}

func coordinatorReportDialog(session, paneID string) (string, bool) {
	return personalagent.ReportDialog(session, paneID)
}

func submitCoordinatorReport(session, paneID, text string) error {
	return personalagent.SubmitReport(session, paneID, text)
}

// submitToAgent hands a Group message to the Agent in a pane through Herdr.
// A refusal that typed nothing (the pane is busy or the agent is not ready) is
// retryable; any other failure may have typed the text and is never retried.
// paneDialog says whether the agent in a pane sits on a startup dialog.
func paneDialog(session, paneID string) (string, bool) {
	screen, err := herdr.Client{Session: session}.PaneRead(paneID, 60)
	if err != nil {
		log.Printf("assistants: groups: reading pane for a startup dialog failed: %v", err)
		return "", false
	}
	return host.StartupDialog(screen)
}

func submitToAgent(session, paneID, text string) error {
	client := herdr.Client{Session: session}
	err := client.AgentSubmit(paneID, text)
	switch {
	case err == nil:
		return nil
	case herdr.IdentityRefused(err):
		// An omp started through bun is not the pane's foreground process to
		// Herdr, which then refuses every agent.* prompt (measured
		// 2026-11-07): type into the pane instead, while it is a live idle agent.
		switch verr := client.SubmitViaPane(paneID, text); {
		case verr == nil:
			return nil
		case errors.Is(verr, herdr.ErrNothingTyped):
			return personalagent.ErrSubmitRefused
		default:
			return verr
		}
	case herdr.HasCode(err, herdr.CodePaneBusy), herdr.HasCode(err, herdr.CodeNotReady), herdr.HasCode(err, herdr.CodeAgentGone):
		return personalagent.ErrSubmitRefused
	}
	return err
}

// lookAtInbox toasts what newly needs a person. The words are fixed phrases and
// the item's own title: no prompt, brief or error text ever reaches a toast.
func (p *personal) lookAtInbox() {
	fresh, err := p.inbox.Tick()
	if err != nil {
		log.Printf("assistants: inbox: %v", err)
		return
	}
	items := make([]notify.AttentionItem, 0, len(fresh))
	for _, it := range fresh {
		name := ""
		if a, err := p.store.GetAssistant(it.AssistantID); err == nil {
			name = a.Name
		}
		items = append(items, notify.AttentionItem{
			Title:  attentionTitle(it) + " · " + it.Title,
			Body:   attentionBody(name, it.Detail),
			Urgent: it.Priority >= 3,
		})
	}
	p.notifier.Attention(items)
}

// lookAtResults quietly toasts what finished by itself. The title is the task's
// own; the body only names the Assistant it came from.
func (p *personal) lookAtResults() {
	done, err := p.results.Tick()
	if err != nil {
		log.Printf("assistants: results: %v", err)
		return
	}
	items := make([]notify.AttentionItem, 0, len(done))
	for _, d := range done {
		items = append(items, notify.AttentionItem{Title: "Task finished · " + d.Title, Body: attentionBody(d.Name, "")})
	}
	p.notifier.Finished(items)
}

// attentionBody names where the card came from (Q06): the Assistant, in front
// of the card's own detail. The toast never invents the source from content.
func attentionBody(assistant, detail string) string {
	if assistant == "" {
		return detail
	}
	if detail == "" {
		return "From " + assistant
	}
	return "From " + assistant + " · " + detail
}

func attentionTitle(it personalagent.InboxItem) string {
	// The card's own reason says more than its kind (Q05); an unknown reason
	// falls back to the kind's fixed phrase.
	switch it.Reason {
	case "permission_prompt":
		return "Waiting for your answer"
	case "review_checks_failed":
		return "Checks failed"
	case "merge_failed":
		return "Merge failed"
	case "task_failed":
		return "Task failed"
	}
	switch it.Kind {
	case personalagent.InboxApproval:
		return "Approval needed"
	case personalagent.InboxFailedStart:
		return "Could not start"
	case personalagent.InboxStalled:
		return "Stalled"
	case personalagent.InboxReview:
		return "Ready for review"
	}
	return "Needs you"
}

// close stops launching; agents that are working keep working in Herdr and are
// re-attached at the next start.
func (p *personal) close() {
	if p == nil {
		return
	}
	p.cancel()
	done := make(chan struct{})
	go func() { p.disp.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		// A waiting Agent is Herdr's, not ours: leave it and let the next
		// daemon re-attach through Resume.
	}
	_ = p.store.Close()
}

// busy reports a personal run in flight: the daemon must not hand itself over
// to a newer binary while it still waits on Agents.
func (p *personal) busy() bool { return p != nil && p.disp.Busy() }

// hasRegistrations is the persistence gate for a hook-started scheduler.
// The Owner may use Assistants from the phone long after the last Herdr
// terminal session exits. On a transient DB error, retain the scheduler:
// an unknown registration state is not evidence that there is no work.
func (p *personal) hasRegistrations() bool {
	if p == nil {
		return false
	}
	assistants, err := p.store.ListAssistants()
	if err != nil {
		log.Printf("assistants: cannot confirm registrations: %v", err)
		return true
	}
	return len(assistants) > 0
}

// ownedRules is what the old scheduler must leave alone. A failed read is
// NOT equivalent to no owned rules: dispatch must stop until the authority
// can be read again, rather than risk sending an Assistant's rule twice.
func (p *personal) ownedRules() (map[string]bool, error) {
	if p == nil {
		return nil, nil
	}
	return p.store.LegacyReservations()
}

// handbacks returns the watermarks of rules just given back to the old
// scheduler, once.
func (p *personal) handbacks() map[string]time.Time {
	if p == nil {
		return nil
	}
	h, err := p.store.TakeHandbacks()
	if err != nil {
		log.Printf("assistants: rule handback: %v", err)
		return nil
	}
	return h
}

// tickRules fires the owned rules and keeps the old history log whole about
// the occurrences that did not become runs.
func (p *personal) tickRules() {
	rep, err := p.rules.Tick()
	if err != nil {
		log.Printf("assistants: rules: %v", err)
		return
	}
	var missed []string
	for _, n := range rep.Notes {
		switch n.Status {
		case "missed":
			recordMissed(n.Name, n.Count, n.Detail)
			missed = append(missed, n.Name)
		case "queued":
			recordQueued(n.Name, history.Trigger(n.Trigger))
		case "skipped":
			if n.Detail == "an occurrence is already queued" {
				recordQueueFull(n.Name, history.Trigger(n.Trigger))
			} else if strings.HasPrefix(n.Detail, "paused") {
				recordPassedOver(n.Name, n.Count)
			} else {
				recordSkipped(n.Name, history.Trigger(n.Trigger), n.Detail)
			}
		}
	}
	p.notifier.Tick(missed, nil)
	if rep.Fired > 0 {
		log.Printf("assistants: %d scheduled rule occurrence(s) queued", rep.Fired)
	}
}

// settled writes a rule's run into the old history log (and raises the old
// toasts), so `history --json`, the desktop page and the phone read one story.
func (p *personal) settled(t personalagent.Task, r personalagent.Run) {
	name, isRule := personalagent.LegacyRuleName(t.RuleID)
	if !isRule {
		return
	}
	var st history.Status
	switch r.Status {
	case personalagent.RunClaimed, personalagent.RunStarting:
		st = history.StatusScheduled
	case personalagent.RunRunning:
		st = history.StatusRunning
	case personalagent.RunDone:
		st = history.StatusDone
	case personalagent.RunFailed:
		st = history.StatusFailed
	case personalagent.RunCancelled:
		st = history.StatusCancelled
	case personalagent.RunBlocked:
		// The old runner failed a run that stopped on a dialog it must not answer.
		st = history.StatusFailed
	default:
		return // uncertain: wait for reconciliation to say something true
	}
	p.mu.Lock()
	if p.seen[r.ID] == string(st) {
		p.mu.Unlock()
		return
	}
	p.seen[r.ID] = string(st)
	if st == history.StatusDone || st == history.StatusFailed || st == history.StatusCancelled {
		delete(p.seen, r.ID)
	}
	p.mu.Unlock()
	errMsg := r.Error
	if st != history.StatusFailed && st != history.StatusCancelled {
		errMsg = ""
	}
	var took time.Duration
	if r.FinishedAt > 0 {
		start := r.StartedAt
		if start == 0 {
			start = r.CreatedAt
		}
		took = time.Duration(r.FinishedAt-start) * time.Millisecond
	}
	if err := history.Append(history.Record{
		RunID: r.ID, Automation: name, Trigger: history.TriggerCron, Status: st, At: time.Now(),
		Session: r.HerdrSession, WorkspaceID: r.WorkspaceID, PaneID: r.PaneID, Worktree: r.Branch, Error: errMsg,
	}); err != nil {
		log.Printf("history append failed: %v", err)
	}
	p.notifier.Outcome(name, st, errMsg, took)
}

// prune is the daily chore for the rules' tasks.
func (p *personal) prune() {
	if p == nil {
		return
	}
	if n, err := p.store.PruneRuleHistory(ruleHistoryKeep, ruleHistoryMax); err != nil {
		log.Printf("assistants: rule history prune failed: %v", err)
	} else if n > 0 {
		log.Printf("assistants: pruned %d rule task(s) past retention", n)
	}
}

// tidyTaskWorktrees is the daily chore for the Assistants' own task checkouts.
// The log carries counts only, never a branch, path or repo name.
func (p *personal) tidyTaskWorktrees() {
	if p == nil {
		return
	}
	owned, err := p.store.OwnedTaskBranches()
	if err != nil {
		log.Printf("assistants: task worktree tidy skipped: %v", err)
		return
	}
	if len(owned) == 0 {
		return
	}
	refused := 0
	removed, err := tidyWorktrees(owned, taskWorktreeKeep, time.Now(), func(cleanup.Candidate, error) { refused++ })
	if err != nil {
		log.Printf("assistants: task worktree tidy stopped: %v", err)
	}
	if removed > 0 || refused > 0 {
		log.Printf("assistants: tidied %d task worktree(s) past retention, %d kept (git refused)", removed, refused)
	}
}
