package personalagent

import (
	"context"
	"errors"
	"io/fs"
	"os/exec"
)

// ErrUnavailable means no call was made: there is no client or no plugin
// binary to run. Nothing was sent, so nothing can have been written.
var ErrUnavailable = errors.New("personalagent: unavailable")

// ErrUnreadable means the plugin ran but its answer could not be understood
// (not JSON, or a schema this build does not speak).
var ErrUnreadable = errors.New("personalagent: unreadable answer")

// ErrExited means the CLI ran and ended without a usable answer (a crash or a
// non-zero exit with no JSON).
var ErrExited = errors.New("personalagent: the plugin exited without an answer")

// Failure says what kind of "no" a call ended with. The desktop bridge and the
// Remote Host both speak it, so a timeout, a missing plugin and a plugin that
// refused are never reported as the same thing (Gap L06).
type Failure int

const (
	// FailNone: the call succeeded.
	FailNone Failure = iota
	// FailRefused: the plugin answered and said no (stable code). Retrying the
	// same request gets the same answer.
	FailRefused
	// FailTimeout: the deadline passed before an answer. A write may have
	// committed; a read is safe to repeat.
	FailTimeout
	// FailCanceled: the caller gave up. Same uncertainty as a timeout.
	FailCanceled
	// FailUnavailable: nothing was sent (no plugin, no client). Safe to retry
	// once the plugin is back.
	FailUnavailable
	// FailUnreadable: the plugin answered with something unusable. A write may
	// have committed.
	FailUnreadable
	// FailExited: the CLI ended without a usable answer (crash, exit status).
	// A write may have committed.
	FailExited
	// FailOther: not a client failure at all (a caller's own validation, say).
	FailOther
)

// Classify names how err ended a call.
func Classify(err error) Failure {
	var refusal *Error
	switch {
	case err == nil:
		return FailNone
	case errors.As(err, &refusal):
		return FailRefused
	case errors.Is(err, context.DeadlineExceeded):
		return FailTimeout
	case errors.Is(err, context.Canceled):
		return FailCanceled
	case errors.Is(err, ErrUnavailable), errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		return FailUnavailable
	case errors.Is(err, ErrUnreadable):
		return FailUnreadable
	case errors.Is(err, ErrExited):
		return FailExited
	default:
		return FailOther
	}
}

// WriteOutcomeUnknown reports whether a failed write may nevertheless have
// been applied. Only the same requestId may be sent again; a fresh one could
// do the thing twice.
func (f Failure) WriteOutcomeUnknown() bool {
	switch f {
	case FailTimeout, FailCanceled, FailUnreadable, FailExited:
		return true
	}
	return false
}
