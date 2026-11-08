package nativeui

import (
	"github.com/thewei/solco/internal/personalagent"
	"testing"
)

func TestAssistantsActivityWatermarkTriggersHintWithoutCounterChange(t *testing.T) {
	base := personalagent.Overview{NeedsYou: 0, Assistants: []personalagent.OverviewRow{{ID: "a_one", Revision: 1, Tasks: 2, Running: 1}}}
	first := assistantsOverviewFingerprint(base)
	base.ActivitySeq = 9
	second := assistantsOverviewFingerprint(base)
	if first == second {
		t.Fatal("fact committed without changing counts must trigger a phone refresh hint")
	}
	if got := assistantsOverviewFingerprint(base); got != second {
		t.Fatal("same fact watermark must not repeat a hint")
	}
	base.ActivitySeq = 10
	if got := assistantsOverviewFingerprint(base); got == second {
		t.Fatal("next fact must trigger another hint")
	}
}
