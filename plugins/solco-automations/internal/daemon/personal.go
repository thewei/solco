package daemon

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/personalagent"
)

// pumpInterval is how often queued Assistant tasks are looked for. Much
// shorter than the cron tick: a task a person just delegated should start now.
const pumpInterval = 3 * time.Second

// personal is the Assistants half of the daemon: the same process, the same
// lock, the one scheduler. It opens the store, settles what a crash left
// behind and then pumps queued tasks into real Herdr Agents.
type personal struct {
	store  *personalagent.Store
	disp   *personalagent.Dispatcher
	cancel context.CancelFunc
	ctx    context.Context
}

func startPersonal() *personal {
	store, err := personalagent.Open(personalagent.DBPath())
	if err != nil {
		log.Printf("assistants: store unavailable, Assistants are paused: %v", err)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &personal{store: store, ctx: ctx, cancel: cancel}
	p.disp = &personalagent.Dispatcher{
		Store:       store,
		Launcher:    personalagent.NewHostLauncher(os.Getenv("SOLCO_ASSISTANTS_HERDR_SESSION"), personalagent.WorkDirRoot()),
		Claimant:    "daemon",
		MaxParallel: 2,
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
	if n := p.disp.Pump(p.ctx); n > 0 {
		log.Printf("assistants: started %d task(s)", n)
	}
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
