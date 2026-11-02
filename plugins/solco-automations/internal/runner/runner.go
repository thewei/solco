// Package runner executes one automation end to end and records every state
// transition in the history log. What it means to provision a workspace or get
// an agent to do something lives behind the host seam; what is left here is the
// lifecycle: skip an overlapping run, record what happened, decide whether a
// failure was a failure at all.
package runner

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/history"
	"github.com/DnzzL/herdr-automations/internal/host"
	"github.com/DnzzL/herdr-automations/internal/notify"
)

// Runner runs automations on a host. It holds the in-flight set, which used to
// be a package global that the daemon reached into to decide whether it could
// re-exec itself.
type Runner struct {
	host     host.Host
	notifier *notify.Notifier
	inFlight sync.Map
}

// New returns a Runner that works through h. Its notifier is nil, so it stays
// silent — the board and the CLI's `run` build one this way on purpose (see
// runner.record).
func New(h host.Host) *Runner { return &Runner{host: h} }

// NewWith returns a Runner that also toasts through n.
func NewWith(h host.Host, n *notify.Notifier) *Runner { return &Runner{host: h, notifier: n} }

// Default returns a Runner driving the backend automations.yaml names.
func Default() *Runner {
	b := config.BackendHerdr
	if cfg, err := config.Load(); err == nil {
		b = cfg.Backend
	}
	return New(host.For(b))
}

// Running reports whether this automation is mid-run.
func (r *Runner) Running(name string) bool {
	_, busy := r.inFlight.Load(name)
	return busy
}

// Busy reports whether any automation is mid-run.
func (r *Runner) Busy() bool {
	busy := false
	r.inFlight.Range(func(_, _ any) bool { busy = true; return false })
	return busy
}

// Run executes the automation synchronously.
//
// A draft is refused before anything happens: drafting is the agent's half of
// the workflow, and running one would be running what nobody approved. The
// CLI's `run` refuses earlier with the same words; this is the board's path.
//
// Overlapping runs of the same automation are skipped rather than run twice —
// unless the automation queues overlaps, which the daemon settles before a
// Fire ever reaches here.
func (r *Runner) Run(a config.Automation, trigger history.Trigger) error {
	if a.Draft {
		return fmt.Errorf("%s is a draft — approve it first: herdr-automations approve %s", a.Name, a.Name)
	}
	if _, busy := r.inFlight.LoadOrStore(a.Name, true); busy {
		r.record(history.NewID(a.Name, ""), a.Name, trigger, history.StatusSkipped, host.Session{},
			"previous run still in flight", 0)
		return fmt.Errorf("%s: previous run still in flight, skipped", a.Name)
	}
	defer r.inFlight.Delete(a.Name)

	id := history.NewID(a.Name, "")
	started := time.Now()
	r.record(id, a.Name, trigger, history.StatusScheduled, host.Session{}, "", 0)

	session, err := r.host.Provision(a)
	if err != nil {
		r.record(id, a.Name, trigger, history.StatusFailed,
			host.Session{WorkspaceID: session.WorkspaceID, HerdrSession: session.HerdrSession},
			err.Error(), time.Since(started))
		return err
	}
	r.record(id, a.Name, trigger, history.StatusRunning, session, "", 0)

	timeout := time.Duration(a.TimeoutMinutes) * time.Minute
	if err := r.host.Do(session, a, timeout); err != nil {
		r.record(id, a.Name, trigger, statusFor(err), session, err.Error(), time.Since(started))
		return err
	}
	r.record(id, a.Name, trigger, history.StatusDone, session, "", time.Since(started))
	return nil
}

// statusFor decides how a run that ended in err is remembered. Only a workspace
// closed under the run is not a failure.
func statusFor(err error) history.Status {
	if errors.Is(err, host.ErrCancelled) {
		return history.StatusCancelled
	}
	return history.StatusFailed
}

// record is the only writer of run history, which is what makes it the only
// caller of notify: the two cannot drift apart, and a future status inherits
// its notification for free without runner filtering anything. took is the
// run's wall time on a final status; 0 while the run is still on its way.
func (r *Runner) record(id, name string, trigger history.Trigger, st history.Status, s host.Session, errMsg string, took time.Duration) {
	err := history.Append(history.Record{
		RunID: id, Automation: name, Trigger: trigger, Status: st, At: time.Now(),
		Session: s.HerdrSession, WorkspaceID: s.WorkspaceID, PaneID: s.PaneID, Worktree: s.Branch, Error: errMsg,
	})
	if err != nil {
		log.Printf("history append failed: %v", err)
	}
	r.notifier.Outcome(name, st, errMsg, took)
}
