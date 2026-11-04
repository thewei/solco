package daemon

import (
	"context"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
	"github.com/thewei/solco/plugins/solco-automations/internal/history"
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

// inboxEvery is how often the inbox is read for something new to say.
const inboxEvery = 15 * time.Second

// pumpInterval is how often queued Assistant tasks are looked for. Much
// shorter than the cron tick: a task a person just delegated should start now.
const pumpInterval = 3 * time.Second

// personal is the Assistants half of the daemon: the same process, the same
// lock, the one scheduler. It opens the store, settles what a crash left
// behind and then pumps queued tasks into real Herdr Agents.
type personal struct {
	store    *personalagent.Store
	disp     *personalagent.Dispatcher
	cancel   context.CancelFunc
	ctx      context.Context
	watch    *personalagent.Watcher
	beat     *personalagent.Beater
	rules    *personalagent.RuleScheduler
	reviewer *personalagent.Reviewer
	inbox    *personalagent.InboxWatcher
	lastLook time.Time
	polling  atomic.Bool

	notifier *notify.Notifier
	lastRule time.Time
	mu       sync.Mutex
	seen     map[string]string // run id -> last status written to history
}

func startPersonal(n *notify.Notifier) *personal {
	store, err := personalagent.Open(personalagent.DBPath())
	if err != nil {
		log.Printf("assistants: store unavailable, Assistants are paused: %v", err)
		return nil
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
	p.watch = &personalagent.Watcher{Store: store, Fetcher: &personalagent.GitHubFetcher{BaseURL: os.Getenv("SOLCO_ASSISTANTS_GITHUB_API")}}
	p.beat = &personalagent.Beater{Store: store}
	p.inbox = &personalagent.InboxWatcher{Store: store}
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
	if p.polling.CompareAndSwap(false, true) {
		// Watching reads the network: never inside the tick, never twice at once.
		go func() {
			defer p.polling.Store(false)
			if n, err := p.watch.Poll(p.ctx); err != nil && p.ctx.Err() == nil {
				log.Printf("assistants: watch: %v", err)
			} else if n > 0 {
				log.Printf("assistants: %d watched change(s) queued", n)
			}
		}()
	}
	// Heartbeats are a few indexed reads and, at most, one task insert each.
	if r, err := p.beat.Tick(); err != nil {
		log.Printf("assistants: heartbeat: %v", err)
	} else if r.Woke > 0 {
		log.Printf("assistants: %d heartbeat wake(s) queued", r.Woke)
	}
	if time.Since(p.lastLook) >= inboxEvery {
		p.lastLook = time.Now()
		p.lookAtInbox()
	}
	if time.Since(p.lastRule) >= ruleEvery {
		p.lastRule = time.Now()
		p.tickRules()
	}
	if n := p.disp.Pump(p.ctx); n > 0 {
		log.Printf("assistants: started %d task(s)", n)
	}
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
		items = append(items, notify.AttentionItem{
			Title:  attentionTitle(it.Kind) + " · " + it.Title,
			Body:   it.Detail,
			Urgent: it.Priority >= 3,
		})
	}
	p.notifier.Attention(items)
}

func attentionTitle(kind string) string {
	switch kind {
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

// ownedRules is what the old scheduler must leave alone (nil without a store).
func (p *personal) ownedRules() map[string]bool {
	if p == nil {
		return nil
	}
	owned, err := p.store.OwnedLegacy()
	if err != nil {
		log.Printf("assistants: rule ledger: %v", err)
		return nil
	}
	return owned
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
