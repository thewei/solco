package personalagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Gap L06: one vocabulary for "the plugin did not answer", shared by the
// desktop bridge and the Host, so a timeout, a missing plugin and a plugin
// that said no are never reported as the same thing.
func TestClassifySeparatesRefusalTimeoutAndUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want Failure
	}{
		{"nil", nil, FailNone},
		{"refusal", &Error{Code: "conflict", Message: "stale"}, FailRefused},
		{"wrapped refusal", fmt.Errorf("x: %w", &Error{Code: "invalid"}), FailRefused},
		{"deadline", fmt.Errorf("personalagent: list: %w", context.DeadlineExceeded), FailTimeout},
		{"canceled", fmt.Errorf("personalagent: list: %w", context.Canceled), FailCanceled},
		{"no client", ErrUnavailable, FailUnavailable},
		{"unreadable", fmt.Errorf("personalagent: list: %w", ErrUnreadable), FailUnreadable},
		{"exit status", fmt.Errorf("personalagent: list: %w", ErrExited), FailExited},
		{"a caller's own error", errors.New("owner_key_foreign: nope"), FailOther},
	} {
		if got := Classify(tc.err); got != tc.want {
			t.Errorf("%s: Classify = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A CLI killed by the caller's deadline reports "signal: killed"; the caller
// must still see that it was the deadline, otherwise a write that may have
// committed is reported as a plain failure.
func TestDeadlineKillIsReportedAsATimeout(t *testing.T) {
	c := NewClientWithExec(func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, errors.New("signal: killed")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := c.Overview(ctx)
	if Classify(err) != FailTimeout || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a deadline kill classified as %v (%v)", Classify(err), err)
	}
}

func TestUnreadableAnswerAndMissingClientAreNamed(t *testing.T) {
	c := NewClientWithExec(func(context.Context, string, ...string) ([]byte, error) { return []byte("not json"), nil })
	if _, err := c.Overview(context.Background()); Classify(err) != FailUnreadable {
		t.Fatalf("garbage answer classified as %v (%v)", Classify(err), err)
	}
	var none *Client
	if _, err := none.Overview(context.Background()); Classify(err) != FailUnavailable {
		t.Fatalf("nil client classified as %v (%v)", Classify(err), err)
	}
}

// A plugin child that leaves a grandchild holding stdout must not keep the
// call (and its goroutine) alive past the deadline.
func TestDeadlineReturnsEvenWhenAGrandchildHoldsStdout(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n(sleep 20) &\nsleep 20\n"
	if err := os.WriteFile(filepath.Join(bin, "solco-automations"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c := NewClient(root, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.Overview(ctx); done <- err }()
	select {
	case err := <-done:
		if Classify(err) != FailTimeout {
			t.Fatalf("classified as %v (%v)", Classify(err), err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("the call outlived its deadline: a grandchild still holds stdout")
	}
}
