package nativeui

import (
	"math"
	"testing"

	"github.com/thewei/solco/internal/history"
)

func TestContextUsageCardData(t *testing.T) {
	used := int64(405_000)
	usage := history.SessionUsage{Input: 200, CacheRead: 1800, CacheWrite: 0, Output: 47}
	got := buildContextUsage(&used, usage, 1_000_000)
	if got.Used != 405_000 || got.Window != 1_000_000 || got.Output != 47 {
		t.Fatalf("card = %+v", got)
	}
	if got.CacheHitRate == nil || math.Abs(*got.CacheHitRate-90) > 1e-9 {
		t.Fatalf("hit rate = %v, want 90 (1800 of 2000 input tokens)", got.CacheHitRate)
	}
}

func TestContextUsageWindowPrefersTheLogsOwnReport(t *testing.T) {
	used := int64(10)
	got := buildContextUsage(&used, history.SessionUsage{Window: 258_400}, 1_000_000)
	if got.Window != 258_400 {
		t.Fatalf("window = %d, want the log's 258400", got.Window)
	}
}

func TestContextUsageUnknownWindowAndNoInputStayHonest(t *testing.T) {
	used := int64(132_000)
	got := buildContextUsage(&used, history.SessionUsage{}, 0)
	if got.Window != 0 || got.CacheHitRate != nil || got.Used != 132_000 {
		t.Fatalf("an unknown window must stay 0 and no input means no rate: %+v", got)
	}
}
