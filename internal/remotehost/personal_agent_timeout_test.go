package remotehost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thewei/solco/internal/personalagent"
)

// AUD-13: a disconnected phone must not cancel a previously accepted write,
// yet the Host must never leave an unbounded plugin child running.
func TestDurablePAContextDetachesCancellationButKeepsHardDeadline(t *testing.T) {
	original, cancelRequest := context.WithCancel(context.Background())
	ctx, cancel := durablePAContext(original, 30*time.Millisecond)
	defer cancel()
	cancelRequest()
	if err := ctx.Err(); err != nil {
		t.Fatalf("phone disconnect cancelled durable write: %v", err)
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 60*time.Millisecond {
		t.Fatalf("durable write missing bounded deadline: %s %v", deadline, ok)
	}
	<-ctx.Done()
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("unexpected timeout error: %v", ctx.Err())
	}
}

// A timed-out write may have committed; the phone must be told to retry
// with its original idempotency key, not to create another operation.
func TestPersonalAgentWriteTimeoutIsUncertain(t *testing.T) {
	client := personalagent.NewClientWithExec(func(context.Context, string, ...string) ([]byte, error) {
		return nil, nil
	})
	s := &Server{opts: Options{PersonalAgent: func(context.Context) (*personalagent.Client, bool) { return client, true }}}
	req := httptest.NewRequest(http.MethodPost, "/api/v3/personal-agent/assistants", nil)
	w := httptest.NewRecorder()
	s.paWrite(w, req, Device{}, "test.timeout", func(ctx context.Context, c *personalagent.Client) (any, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > maxPAWriteDuration {
			t.Fatal("plugin context deadline absent")
		}
		return nil, context.DeadlineExceeded
	})
	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "personal_agent_uncertain") || !strings.Contains(w.Body.String(), "same requestId") {
		t.Fatalf("must tell caller to retry idempotently: %s", w.Body.String())
	}
}

// Gap L06: the phone is told the truth about why the core did not answer.
func TestPersonalAgentFailuresAreNamedByKind(t *testing.T) {
	s := &Server{}
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"read timeout", fmt.Errorf("personalagent: list: %w", context.DeadlineExceeded), http.StatusGatewayTimeout, "personal_agent_timeout"},
		{"service down", fmt.Errorf("personalagent: list: %w", personalagent.ErrUnavailable), http.StatusServiceUnavailable, "personal_agent_offline"},
		{"crash", fmt.Errorf("personalagent: list: %w", personalagent.ErrExited), http.StatusBadGateway, "personal_agent_failed"},
	} {
		w := httptest.NewRecorder()
		s.paRefuse(w, Device{}, "test.read", tc.err)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
			t.Errorf("%s: status=%d body=%s, want %d %s", tc.name, w.Code, w.Body.String(), tc.status, tc.code)
		}
	}
}

// A write whose CLI crashed or answered garbage may still have committed: the
// same requestId is the only safe retry, exactly as for a timeout.
func TestPersonalAgentWriteThatMayHaveCommittedIsUncertain(t *testing.T) {
	client := personalagent.NewClientWithExec(func(context.Context, string, ...string) ([]byte, error) { return nil, nil })
	s := &Server{opts: Options{PersonalAgent: func(context.Context) (*personalagent.Client, bool) { return client, true }}}
	for name, err := range map[string]error{
		"crash":      fmt.Errorf("personalagent: add: %w", personalagent.ErrExited),
		"unreadable": fmt.Errorf("personalagent: add: %w", personalagent.ErrUnreadable),
	} {
		w := httptest.NewRecorder()
		s.paWrite(w, httptest.NewRequest(http.MethodPost, "/x", nil), Device{}, "test.write", func(context.Context, *personalagent.Client) (any, error) { return nil, err })
		if w.Code != http.StatusGatewayTimeout || !strings.Contains(w.Body.String(), "personal_agent_uncertain") {
			t.Errorf("%s: status=%d body=%s", name, w.Code, w.Body.String())
		}
	}
	// An error that is not the client's (a handler's own refusal) is a plain failure.
	w := httptest.NewRecorder()
	s.paWrite(w, httptest.NewRequest(http.MethodPost, "/x", nil), Device{}, "test.write", func(context.Context, *personalagent.Client) (any, error) {
		return nil, errors.New("handler refused")
	})
	if w.Code != http.StatusBadGateway {
		t.Errorf("a handler's own error became %d %s", w.Code, w.Body.String())
	}
	// Nothing was sent when the service is down: that is not uncertain.
	w = httptest.NewRecorder()
	s.paWrite(w, httptest.NewRequest(http.MethodPost, "/x", nil), Device{}, "test.write", func(context.Context, *personalagent.Client) (any, error) {
		return nil, personalagent.ErrUnavailable
	})
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "personal_agent_offline") {
		t.Errorf("service down: status=%d body=%s", w.Code, w.Body.String())
	}
}
