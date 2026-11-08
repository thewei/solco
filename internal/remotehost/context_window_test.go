package remotehost

import (
	"context"
	"testing"
	"time"

	"github.com/thewei/solco/internal/history"
)

func TestContextWindowIsReadOncePerMinutePerSession(t *testing.T) {
	var reads int
	old := readContextWindow
	readContextWindow = func(context.Context, history.AgentID, string, string) int64 { reads++; return 258000 }
	t.Cleanup(func() { readContextWindow = old })

	var cache contextWindowCache
	key := contextWindowKey{provider: history.AgentCodex, path: "/s/a.jsonl", model: "gpt"}
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 5; i++ {
		if got := cache.get(context.Background(), now.Add(time.Duration(i)*time.Second), key); got != 258000 {
			t.Fatalf("window = %d, want 258000", got)
		}
	}
	if reads != 1 {
		t.Fatalf("reads within the minute = %d, want 1", reads)
	}
	cache.get(context.Background(), now.Add(contextWindowTTL+time.Second), key)
	if reads != 2 {
		t.Fatalf("reads after the minute = %d, want 2", reads)
	}
	other := key
	other.path = "/s/b.jsonl"
	cache.get(context.Background(), now, other)
	if reads != 3 {
		t.Fatalf("a second session shares nothing: reads = %d, want 3", reads)
	}
}

func TestContextWindowKeepsTheOldAnswerWhenTheReadTimesOut(t *testing.T) {
	old := readContextWindow
	t.Cleanup(func() { readContextWindow = old })
	var cache contextWindowCache
	key := contextWindowKey{provider: history.AgentPi, path: "/s/a.jsonl", model: "m"}
	now := time.Unix(1_700_000_000, 0)
	readContextWindow = func(context.Context, history.AgentID, string, string) int64 { return 1000 }
	cache.get(context.Background(), now, key)
	readContextWindow = func(ctx context.Context, _ history.AgentID, _, _ string) int64 { <-ctx.Done(); return 0 }
	if got := cache.get(context.Background(), now.Add(2*contextWindowTTL), key); got != 1000 {
		t.Fatalf("timed-out read answered %d, want the cached 1000", got)
	}
}
