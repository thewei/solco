// Package daemon is the long-running scheduler started by the plugin's startup
// hook. Every tick it re-reads automations.yaml, asks the schedule module what
// should happen now, and does it. It re-executes itself when the plugin binary
// is upgraded underneath it.
//
// Deciding is schedule's job; this package is the part with the side effects.
package daemon

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/cleanup"
	"github.com/thewei/solco/plugins/solco-automations/internal/config"
	"github.com/thewei/solco/plugins/solco-automations/internal/herdr"
	"github.com/thewei/solco/plugins/solco-automations/internal/history"
	"github.com/thewei/solco/plugins/solco-automations/internal/host"
	"github.com/thewei/solco/plugins/solco-automations/internal/hostpath"
	"github.com/thewei/solco/plugins/solco-automations/internal/notify"
	"github.com/thewei/solco/plugins/solco-automations/internal/personalagent"
	"github.com/thewei/solco/plugins/solco-automations/internal/runner"
	"github.com/thewei/solco/plugins/solco-automations/internal/schedule"
)

// tickInterval is how often the wall clock is consulted. Short enough that a
// run resumes within a minute of the machine waking up.
const tickInterval = 30 * time.Second

// maintainInterval is how often the daily chores run: history retention and
// the per-automation cleanup TTL. Both are day-scale; running them at every
// tick would only re-read the same state.
const maintainInterval = 24 * time.Hour

// Run is the scheduler. With standby, a lock already held is waited out
// rather than reported: the service and the TUI's startup hook can both start
// one, and whichever comes second takes over when the first one stops.
func Run(standby bool) error {
	log.SetPrefix("[solco-automations] ")

	release, err := acquireLock()
	for err != nil && standby && errors.Is(err, ErrAlreadyRunning) {
		log.Printf("%v; standing by", err)
		time.Sleep(tickInterval)
		release, err = acquireLock()
	}
	if err != nil {
		return err
	}
	defer release()

	log.Printf("daemon starting, config=%s", config.Path())
	state := loadState()
	// Resolve the path once: a reinstall moves the running image away, but the
	// new build lands here.
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	binary := binaryStamp(exe)
	// The backend is settled once: a run must be awaited where it was
	// started. Editing it in the file restarts the daemon (see the loop).
	backend := config.BackendHerdr
	if cfg, err := config.Load(); err == nil {
		backend = cfg.Backend
	}
	resolved := host.Resolve(backend)
	log.Printf("backend %s (%s)", backend, resolved)
	// Under hc no outside process can raise a toast in the app; the panel
	// announces failures from history instead (ADR 0011).
	var n *notify.Notifier
	if resolved == config.BackendHerdr {
		n = notify.New()
	}
	runs := runner.NewWith(host.For(resolved), n)

	maintain() // start clean: retention and TTLs first, then the schedule
	lastMaintain := time.Now()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	tick := time.NewTicker(tickInterval)
	defer tick.Stop()

	assistants := startPersonal(n)
	defer assistants.close()
	pump := time.NewTicker(pumpInterval)
	defer pump.Stop()

	evaluateWith(state, runs, n, assistants) // don't wait a full tick to notice what is already due

	for {
		select {
		case <-pump.C:
			assistants.tick()
		case <-tick.C:
			// A run in flight records how it ended first, as in restart. Only a
			// daemon a TUI session started, running TUI automations, ends with
			// the last session (ADR 0010): hc needs no session, and the login
			// service owns its own lifetime (launchd would only relaunch it).
			if shouldExitWithoutSessions(standby, resolved, runs.Busy(), assistants.busy(), assistants.hasRegistrations(), herdr.Client{}.RunningSessions) {
				log.Printf("no Herdr session is running, shutting down")
				return nil
			}
			stamp := binaryStamp(exe)
			installed := ""
			if standby {
				installed = hostpath.InstalledBinary()
			}
			if shouldRelaunch(standby, stamp, exe, installed) && !runs.Busy() && !assistants.busy() {
				log.Printf("running %s, installed is %s: leaving for the service to relaunch", exe, installed)
				return nil
			}
			if stamp != binary && stamp != "" {
				restart(runs, exe, "binary changed")
			}
			if cfg, err := config.Load(); err == nil && cfg.Backend != backend {
				restart(runs, exe, "backend changed")
			}
			if time.Since(lastMaintain) >= maintainInterval {
				lastMaintain = time.Now()
				maintain()
				assistants.prune()
			}
			evaluateWith(state, runs, n, assistants)
		case s := <-sigs:
			log.Printf("received %v, shutting down", s)
			return nil
		}
	}
}

// shouldRelaunch reports whether the login service should leave so launchd
// relaunches it through the install-cli shim: a plugin update installed a new
// checkout (and kept the old one, so the running binary is still there), or
// moved the running binary away. A Herdr that cannot say where the plugin is
// changes nothing. A daemon a TUI session started has no one to relaunch it;
// it is re-executed in place instead (#8).
func shouldRelaunch(standby bool, stamp, exe, installed string) bool {
	if !standby {
		return false
	}
	return stamp == "" || (installed != "" && filepath.Clean(installed) != filepath.Clean(exe))
}

// shouldExitWithoutSessions preserves the old startup-hook lifecycle only
// for a scheduler with no persistent Assistant registrations. Once a user has
// any Assistant, work can arrive later from a phone or a timed wake even when
// Herdr currently reports zero sessions. Such a scheduler must stay resident.
func shouldExitWithoutSessions(standby bool, resolved config.Backend, runsBusy, assistantBusy, hasRegisteredAssistants bool, sessions func() ([]string, error)) bool {
	if standby || resolved != config.BackendHerdr || runsBusy || assistantBusy || hasRegisteredAssistants {
		return false
	}
	return noSessionLeft(sessions)
}

// noSessionLeft decides when the daemon is done. It outlives the session that
// started it — every run names its own, and a session already up never re-runs
// its startup hook, so dying with the first would leave the others without a
// scheduler. With no session at all there is nothing left to run in, and the
// next one to start brings a fresh daemon. A Herdr that cannot answer is not
// one that is gone.
func noSessionLeft(list func() ([]string, error)) bool {
	sessions, err := list()
	return err == nil && len(sessions) == 0
}

// evaluate asks the schedule what should happen now, and does it.
func evaluate(state *scheduleState, runs *runner.Runner, n *notify.Notifier) {
	evaluateWith(state, runs, n, nil)
}

// evaluateWith is evaluate for a daemon that also runs Assistants: the rules
// the Assistants ledger owns are dispatched there, so this scheduler must not
// see them (one occurrence, one dispatcher). A rule just rolled back is
// resumed from the watermark the ledger hands over, not from an older one.
func evaluateWith(state *scheduleState, runs *runner.Runner, n *notify.Notifier, own *personal) {
	cfg, err := config.Load()
	if err != nil {
		log.Printf("config error, leaving the schedule untouched: %v", err)
		return
	}
	dirty := false
	for name, at := range own.handbacks() {
		state.LastOccurrence[name] = at
		dirty = true
		log.Printf("%s: handed back to the legacy scheduler", name)
	}
	if owned := own.ownedRules(); len(owned) > 0 {
		cfg = personalagent.FilterOwned(cfg, owned)
	}
	changedInvalid, invalid := reportInvalid(cfg, state)
	dirty = dirty || changedInvalid

	res := schedule.Plan(cfg, schedule.State{LastOccurrence: state.LastOccurrence}, time.Now())
	state.LastOccurrence = res.State.LastOccurrence
	dirty = dirty || res.Changed

	// Catch-ups first: one queued occurrence per automation, held while its
	// previous run is still working, fired the moment the runner is free.
	for name := range state.Queued {
		a := cfg.Find(name)
		if a == nil || a.Disabled || a.Draft {
			// Gone, paused, or unapproved while it waited: the promise dies
			// with the automation rather than firing something withdrawn.
			delete(state.Queued, name)
			dirty = true
			continue
		}
		if runs.Running(name) {
			continue
		}
		delete(state.Queued, name)
		dirty = true
		log.Printf("%s: running the queued occurrence", name)
		go func(a config.Automation) {
			if err := runs.Run(a, history.TriggerCron); err != nil {
				log.Printf("run %s: %v", a.Name, err)
			}
		}(*a)
	}

	var missed []string
	for _, d := range res.Decisions {
		switch d.Kind {
		case schedule.Register:
			log.Printf("%s: scheduled, next run %s", d.Automation.Name, d.At.Format(time.RFC1123))
		case schedule.Draft:
			log.Printf("%s: not scheduled (%s)", d.Automation.Name, d.Reason)
		case schedule.Missed:
			recordMissed(d.Automation.Name, d.Count, d.Reason)
			missed = append(missed, d.Automation.Name)
			log.Printf("%s: missed (%s)", d.Automation.Name, d.Reason)
		case schedule.Paused:
			recordPassedOver(d.Automation.Name, d.Count)
			log.Printf("%s: passed over, automations are paused", d.Automation.Name)
		case schedule.Fire:
			a := d.Automation
			if a.OverlapQueues() && (runs.Running(a.Name) || state.Queued[a.Name]) {
				// The occurrence came due under a still-working run. Queue
				// keeps one; the rest drop, exactly as skip would drop them.
				if state.Queued[a.Name] {
					recordQueueFull(a.Name, d.Trigger)
					log.Printf("%s: skipped, an occurrence is already queued", a.Name)
					continue
				}
				state.Queued[a.Name] = true
				dirty = true
				recordQueued(a.Name, d.Trigger)
				log.Printf("%s: previous run still in flight, occurrence queued", a.Name)
				continue
			}
			if d.Trigger == history.TriggerCatchup {
				log.Printf("%s: running %s late", a.Name,
					time.Since(d.At).Round(time.Minute))
			}
			go func(a config.Automation, trigger history.Trigger) {
				if err := runs.Run(a, trigger); err != nil {
					log.Printf("run %s: %v", a.Name, err)
				}
			}(a, d.Trigger)
		}
	}
	n.Tick(missed, invalid)

	if dirty {
		if err := state.save(); err != nil {
			log.Printf("saving schedule state: %v", err)
		}
	}
}

// recordQueued notes the promise that an occurrence will run once the
// in-flight run of the same automation finishes — the queue half of
// on_overlap: queue.
func recordQueued(name string, trigger history.Trigger) {
	err := history.Append(history.Record{
		RunID:      history.NewID(name, "queued"),
		Automation: name, Trigger: trigger, Status: history.StatusQueued,
		At: time.Now(), Error: "previous run still in flight",
	})
	if err != nil {
		log.Printf("history append failed: %v", err)
	}
}

// recordSkipped notes an occurrence passed over because the previous run of
// the same automation was still working.
func recordSkipped(name string, trigger history.Trigger, why string) {
	err := history.Append(history.Record{
		RunID:      history.NewID(name, "skipped"),
		Automation: name, Trigger: trigger, Status: history.StatusSkipped,
		At: time.Now(), Error: why,
	})
	if err != nil {
		log.Printf("history append failed: %v", err)
	}
}

// recordQueueFull notes an occurrence dropped because one is already queued:
// queue never stacks.
func recordQueueFull(name string, trigger history.Trigger) {
	err := history.Append(history.Record{
		RunID:      history.NewID(name, "queue-full"),
		Automation: name, Trigger: trigger, Status: history.StatusSkipped,
		At: time.Now(), Error: "an occurrence is already queued",
	})
	if err != nil {
		log.Printf("history append failed: %v", err)
	}
}

// maintain runs the day-scale chores: history retention, and the cleanup TTL
// of every automation that opted in. Nothing here is a scheduler — only what
// the config already promised. Every line it logs names operations and
// counts; run content never crosses the log.
func maintain() {
	if removed, err := history.Prune(time.Now()); err != nil {
		log.Printf("history prune failed: %v", err)
	} else if removed > 0 {
		log.Printf("history: pruned %d records past retention", removed)
	}
	cfg, err := config.Load()
	if err != nil {
		log.Printf("cleanup pass skipped, config error: %v", err)
		return
	}
	plan, err := cleanup.ScanWithTTL(cfg, 0, time.Now())
	if err != nil {
		log.Printf("cleanup pass skipped: %v", err)
		return
	}
	if len(plan.Removable) == 0 {
		return
	}
	removed, err := plan.Apply(func(c cleanup.Candidate) {
		log.Printf("cleanup: removed %s in %s", c.Branch, c.Repo)
	})
	if err != nil {
		log.Printf("cleanup stopped early: %v", err)
	}
	log.Printf("cleanup: removed %d worktree(s) past their ttl", removed)
}

// reportInvalid records each broken entry once, and returns the automations
// newly reported this tick so evaluate can fold them into one Tick toast.
// Re-recording an unfixed typo every 30s would bury the log and the board
// under the same line all day; going quiet about it entirely is how a
// "missing" automation stays a mystery.
func reportInvalid(cfg *config.Config, state *scheduleState) (changed bool, newlyInvalid []string) {
	if state.Invalid == nil {
		state.Invalid = map[string]bool{}
	}
	seen := map[string]bool{}
	for _, d := range cfg.Invalid {
		line := d.String()
		seen[line] = true
		if state.Invalid[line] {
			continue
		}
		state.Invalid[line] = true
		changed = true
		log.Printf("not scheduled — %s", line)
		name := d.Name
		if name == "" {
			name = "(unnamed)"
		}
		newlyInvalid = append(newlyInvalid, name)
		err := history.Append(history.Record{
			RunID:      history.NewID(name, "invalid"),
			Automation: name, Status: history.StatusInvalid,
			At: time.Now(), Error: line,
		})
		if err != nil {
			log.Printf("history append failed: %v", err)
		}
	}
	// Forget the ones that were fixed, so breaking them again is reported again.
	for line := range state.Invalid {
		if !seen[line] {
			delete(state.Invalid, line)
			changed = true
		}
	}
	return changed, newlyInvalid
}

// recordPassedOver notes an occurrence that came due while every automation
// was paused. Skipped, not missed: nothing went wrong, so nothing is toasted.
func recordPassedOver(name string, count int) {
	detail := "paused"
	if count > 1 {
		detail = fmt.Sprintf("%d occurrences: paused", count)
	}
	err := history.Append(history.Record{
		RunID:      history.NewID(name, "paused"),
		Automation: name, Trigger: history.TriggerCron, Status: history.StatusSkipped,
		At: time.Now(), Error: detail,
	})
	if err != nil {
		log.Printf("history append failed: %v", err)
	}
}

func recordMissed(name string, count int, why string) {
	detail := why
	if count > 1 {
		detail = fmt.Sprintf("%d occurrences: %s", count, why)
	}
	err := history.Append(history.Record{
		RunID:      history.NewID(name, "missed"),
		Automation: name, Trigger: history.TriggerCron, Status: history.StatusMissed,
		At: time.Now(), Error: detail,
	})
	if err != nil {
		log.Printf("history append failed: %v", err)
	}
}

// restart re-executes the daemon so a plugin upgrade takes effect without
// waiting for the Herdr server to be restarted.
// execve is syscall.Exec, swappable so a test can make the re-exec fail.
var execve = syscall.Exec

func restart(runs *runner.Runner, exe, why string) {
	if runs.Busy() {
		return // let the in-flight run finish; we'll notice again next tick
	}
	log.Printf("%s, re-executing %s", why, exe)
	// No release here: the lock's descriptor is close-on-exec, so a successful
	// exec drops it for the new image, and a failed one leaves it held.
	if err := execve(exe, os.Args, os.Environ()); err != nil {
		log.Printf("re-exec failed, continuing with the old build: %v", err)
	}
}

func binaryStamp(exe string) string {
	st, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d-%d", st.ModTime().UnixNano(), st.Size())
}
