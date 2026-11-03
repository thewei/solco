// Package notify turns run and tick outcomes into Herdr toasts. One job: decide
// what deserves to interrupt somebody, and never let a toast fail a run.
package notify

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/herdr"
	"github.com/thewei/solco/plugins/solco-automations/internal/history"
)

// bodyLimit bounds a toast body to one short line. hwf recovers a delegated
// command's exit status by scraping a pane, so a "detail" can be a whole
// screen of terminal output.
const bodyLimit = 200

// Sink is the one Herdr call this package makes, behind an interface so the
// tests need no running Herdr.
type Sink interface {
	NotificationShow(title, body string, sound herdr.Sound) error
}

// Notifier turns run outcomes into toasts. A nil *Notifier says nothing, which
// is what a Runner has until the daemon gives it one.
type Notifier struct {
	sink       Sink
	complained sync.Once
}

// New returns a Notifier that toasts through the real Herdr, in every running
// session.
func New() *Notifier {
	return &Notifier{sink: everywhere{
		list: herdr.Client{}.RunningSessions,
		show: func(session, title, body string, sound herdr.Sound) error {
			return herdr.Client{Session: session}.NotificationShow(title, body, sound)
		},
	}}
}

// everywhere toasts in every running Herdr session. A toast lands only in the
// session it is sent to, and the session a run happens in — a dedicated one,
// say — is often the one nobody is looking at (ADR 0010).
type everywhere struct {
	list func() ([]string, error)
	// show toasts in one session; "" means wherever the environment points.
	show func(session, title, body string, sound herdr.Sound) error
}

func (e everywhere) NotificationShow(title, body string, sound herdr.Sound) error {
	sessions, err := e.list()
	if err != nil || len(sessions) == 0 {
		// One toast somewhere beats none: a herdr that cannot list sessions
		// can still show one where it always did.
		return e.show("", title, body, sound)
	}
	var errs []error
	for _, s := range sessions {
		if err := e.show(s, title, body, sound); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// With returns a Notifier that toasts through sink — the tests' seam.
func With(s Sink) *Notifier { return &Notifier{sink: s} }

// Outcome toasts how a run ended: failures with their reason and a sound,
// completions with a quiet success line (✓ <name> done · <duration>) so a
// finished run is news too. Everything else — still scheduled, running,
// skipped, cancelled — stays silent.
func (n *Notifier) Outcome(name string, st history.Status, detail string, took time.Duration) {
	if n == nil {
		return
	}
	switch st {
	case history.StatusFailed:
		n.show(name+" failed", oneLine(detail), herdr.SoundRequest)
	case history.StatusDone:
		n.show(fmt.Sprintf("✓ %s done · %s", name, shortDuration(took)), "", herdr.SoundNone)
	}
}

// Attention toasts what a person is wanted for (an approval, a failed start, a
// stall, a review). Titles carry the item's own title only — never a prompt or
// an error text. More than three at once collapse into one line.
func (n *Notifier) Attention(items []AttentionItem) {
	if n == nil || len(items) == 0 {
		return
	}
	if len(items) > 3 {
		n.show(fmt.Sprintf("%d things need you", len(items)), "", herdr.SoundRequest)
		return
	}
	for _, it := range items {
		sound := herdr.SoundNone
		if it.Urgent {
			sound = herdr.SoundRequest
		}
		n.show(it.Title, oneLine(it.Body), sound)
	}
}

// AttentionItem is one line of Attention.
type AttentionItem struct {
	Title, Body string
	Urgent      bool
}

// shortDuration renders a run's wall time the way a toast wants it:
// 42s, 38m, 1h23m.
func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	d = d.Round(time.Minute)
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%dh%dm", int(d/time.Hour), int(d/time.Minute)%60)
}

// Tick toasts one line for everything a scheduler tick found wrong, instead of
// one toast per item: a laptop waking from a weekend can find twenty
// automations at once, and twenty stacked panels is not a notification.
//
// Nothing here escalates a cron running every minute into a storm of toasts —
// missed and invalid are both already deduplicated by the caller before they
// reach here, so the real ceiling is one toast per broken thing per tick. A
// broken automation that fires every minute forever is a pause, not a feature.
func (n *Notifier) Tick(missed, invalid []string) {
	if n == nil {
		return
	}
	n.group(missed, "%s missed a run", "%d automations did not run")
	n.group(invalid, "%s is not scheduled", "%d automations are not scheduled")
}

func (n *Notifier) group(names []string, singular, plural string) {
	if len(names) == 0 {
		return
	}
	if len(names) == 1 {
		n.show(fmt.Sprintf(singular, names[0]), "", herdr.SoundNone)
		return
	}
	title := fmt.Sprintf(plural, len(names))
	shown := names
	suffix := ""
	if len(shown) > 3 {
		shown = shown[:3]
		suffix = fmt.Sprintf(", and %d more", len(names)-3)
	}
	n.show(title, strings.Join(shown, ", ")+suffix, herdr.SoundNone)
}

// show degrades silently: a toast is worth trying, never worth breaking a run
// or a tick over. The error is logged once per process, then dropped — there
// is no latch, so notifications resume as soon as Herdr does.
func (n *Notifier) show(title, body string, sound herdr.Sound) {
	if err := n.sink.NotificationShow(title, body, sound); err != nil {
		n.complained.Do(func() {
			log.Printf("notification failed (will keep trying silently): %v", err)
		})
	}
}

// oneLine reduces detail to its first line, truncated on a rune boundary so a
// multi-byte character split mid-sequence does not turn into mojibake.
func oneLine(detail string) string {
	if i := strings.IndexByte(detail, '\n'); i >= 0 {
		detail = detail[:i]
	}
	r := []rune(detail)
	if len(r) <= bodyLimit {
		return string(r)
	}
	return string(r[:bodyLimit-1]) + "…"
}
