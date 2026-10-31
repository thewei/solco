package herdr

import (
	"testing"
	"time"
)

func TestRPCTimeoutOutlastsAnAgentWait(t *testing.T) {
	if got := rpcTimeout("ping", nil); got != time.Second {
		t.Fatalf("ping %v", got)
	}
	if got := rpcTimeout("agent.get", map[string]any{"target": "w1:p1"}); got != 10*time.Second {
		t.Fatalf("ordinary %v", got)
	}
	if got := rpcTimeout("agent.wait", map[string]any{"target": "w1:p1", "timeout_ms": int64(60_000)}); got != 70*time.Second {
		t.Fatalf("a 60 s wait must keep the socket open past 60 s, got %v", got)
	}
	if got := rpcTimeout("agent.wait", map[string]any{"target": "w1:p1"}); got != 10*time.Second {
		t.Fatalf("a wait without its own timeout keeps the ordinary budget, got %v", got)
	}
}
