package remotehost

import (
	"context"
	"errors"
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
