// Package usage reads how much of each agent's plan quota is spent: one
// Fetcher per provider (Codex, Claude Code, Cursor, Zhipu GLM, GitHub
// Copilot, Antigravity), a Service that caches and throttles them, and the
// pure window arithmetic the Usage page draws (pace, levels).
//
// It is a read-only catalog of a vendor's own numbers. Nothing here keeps a
// history, owns a credential store or draws UI: tokens arrive through
// callbacks, secrets never reach a log or an error text, and every request
// goes only to the provider's own host (or to a local program).
package usage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

/**
 * [INPUT]: 各 Provider 的额度来源（Codex app-server 子进程、Anthropic/Cursor/GitHub/智谱 HTTPS 接口、Antigravity 本机 language server）与调用方给的 token 回调
 * [OUTPUT]: ID/Window/Snapshot/Result 数据模型、统一错误（ErrSetup/ErrUnauthorized/ErrUnavailable/ErrCustomEndpoint/RateLimitedError）、Window.Pace/Level 纯函数、Fetcher 接口
 * [POS]: Usage 页的只读数据域：只读取厂商给的额度数字，不存历史、不持有凭据仓库、不画 UI；日志与错误文本不得含 token
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// ID names one provider of quota.
type ID string

const (
	Claude      ID = "claude"
	Codex       ID = "codex"
	Cursor      ID = "cursor"
	Zhipu       ID = "zhipu"
	Copilot     ID = "copilot"
	Antigravity ID = "antigravity"
)

// All is the provider order of the page.
var All = []ID{Claude, Codex, Cursor, Zhipu, Copilot, Antigravity}

// Title is the provider's product name.
func (id ID) Title() string {
	switch id {
	case Claude:
		return "Claude Code"
	case Codex:
		return "Codex"
	case Cursor:
		return "Cursor"
	case Zhipu:
		return "Zhipu GLM"
	case Copilot:
		return "GitHub Copilot"
	case Antigravity:
		return "Antigravity"
	}
	return string(id)
}

// Known reports whether id is one of All.
func (id ID) Known() bool {
	for _, known := range All {
		if known == id {
			return true
		}
	}
	return false
}

// Window is one limit: a share of an allowance spent over a span, with the
// moment it resets.
type Window struct {
	Name    string        // "5-hour", "Weekly", "Included · monthly"
	UsedPct float64       // 0..100
	Span    time.Duration // length of the window; 0 when the vendor does not say
	Resets  time.Time     // zero when unknown
	Amount  string        // optional "$11.60 of $20" / "186 of 300"

	// Used/Max/Unit carry a counted allowance ("124 of 500 requests") for
	// plans measured in requests or calls; zero Max means a plain share.
	Used, Max float64
	Unit      string
	// Group names the set of models that share these windows (Antigravity's
	// "Claude and GPT models"); windows with a Group render as one block.
	Group string
	// Minor marks a sub-limit of the main allowance (a per-model weekly cap,
	// a model-specific limit) that the page shows as a compact line.
	Minor bool
}

// Counted reports a window that states a count, not only a share.
func (w Window) Counted() bool { return w.Max > 0 }

// Level is how worried a bar should look.
type Level int

const (
	LevelOK Level = iota
	LevelWarn
	LevelBad
)

// Level maps the spent share to a bar tone: 80% warns, 95% is critical.
func (w Window) Level() Level { return LevelOf(w.UsedPct) }

// LevelOf is Window.Level for a bare percentage.
func LevelOf(pct float64) Level {
	switch {
	case pct >= 95:
		return LevelBad
	case pct >= 80:
		return LevelWarn
	}
	return LevelOK
}

// Elapsed is how far into its span the window is, 0..1; ok is false when the
// vendor gives no span or no reset time.
func (w Window) Elapsed(now time.Time) (frac float64, ok bool) {
	if w.Span <= 0 || w.Resets.IsZero() {
		return 0, false
	}
	remaining := w.Resets.Sub(now)
	if remaining > w.Span {
		remaining = w.Span
	}
	if remaining < 0 {
		remaining = 0
	}
	return 1 - float64(remaining)/float64(w.Span), true
}

// minPaceElapsed is how far into a window it must be before a rate is
// extrapolated: just after a reset one request is a huge rate.
const minPaceElapsed = 0.15

// PaceState is the verdict of Window.Pace.
type PaceState int

const (
	PaceUnknown  PaceState = iota // too early (or no span) to say
	PaceOn                        // spending slowly enough to last the window
	PaceRunsOut                   // the allowance is gone before the reset at this rate
	PaceExceeded                  // already at the limit
)

// Pace extrapolates the window's rate so far. For PaceRunsOut, In is the time
// until the allowance would be gone.
func (w Window) Pace(now time.Time) (state PaceState, in time.Duration) {
	elapsed, ok := w.Elapsed(now)
	if !ok || w.UsedPct <= 0 {
		return PaceUnknown, 0
	}
	if w.UsedPct >= 100 {
		return PaceExceeded, 0
	}
	if elapsed < minPaceElapsed {
		return PaceUnknown, 0
	}
	spent := time.Duration(elapsed * float64(w.Span))
	// Time to spend what is left at the rate so far.
	left := time.Duration(float64(spent) * (100 - w.UsedPct) / w.UsedPct)
	if left < w.Resets.Sub(now) {
		return PaceRunsOut, left
	}
	return PaceOn, 0
}

// Snapshot is one reading of a provider's limits.
type Snapshot struct {
	Plan    string   // "Max 5×", "Plus", "Pro"
	Windows []Window // in display order
	Notes   []string // facts without a bar: "Extra usage is off", "Credits $0.00"
	At      time.Time
}

// Tightest is the window closest to its limit.
func (s Snapshot) Tightest() (Window, bool) {
	var best Window
	found := false
	for _, w := range s.Windows {
		if !found || w.UsedPct > best.UsedPct {
			best, found = w, true
		}
	}
	return best, found
}

// State is why a provider shows what it shows.
type State int

const (
	StateIdle           State = iota // never read
	StateLive                        // the last read succeeded
	StateSetup                       // no credential found
	StateExpired                     // the credential was refused
	StateRateLimited                 // the vendor asked to come back later
	StateUnavailable                 // the source could not be reached
	StateCustomEndpoint              // the agent talks to a non-vendor endpoint: no plan quota exists
)

// Result is a provider's standing: its last good snapshot (if any) and why
// the latest attempt did or did not refresh it.
type Result struct {
	ID       ID
	Snapshot Snapshot
	Has      bool // Snapshot holds a reading (possibly an old one)
	State    State
	Detail   string    // short, secret-free reason
	RetryAt  time.Time // RateLimited: when to try again
	Checked  time.Time // the latest attempt
}

// Stale reports a result that shows an old reading after a failed refresh.
func (r Result) Stale() bool { return r.Has && r.State != StateLive && r.State != StateIdle }

// Failures every Fetcher reports with these, so the Service and the page tell
// them apart without parsing text.
var (
	// ErrSetup: no credential was found where the provider keeps it.
	ErrSetup = errors.New("no sign-in found")
	// ErrUnauthorized: the vendor refused the credential (expired or revoked).
	ErrUnauthorized = errors.New("sign-in was refused")
	// ErrUnavailable: the source could not be reached (offline, app not running).
	ErrUnavailable = errors.New("source unavailable")
	// ErrCustomEndpoint: the agent is pointed at an endpoint that has no plan quota.
	ErrCustomEndpoint = errors.New("custom endpoint")
)

// RateLimitedError is a vendor's "come back later".
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("rate limited, retry in %s", e.RetryAfter.Round(time.Second))
	}
	return "rate limited"
}

// Fetcher reads one provider's limits.
type Fetcher interface {
	ID() ID
	Fetch(ctx context.Context) (Snapshot, error)
}
