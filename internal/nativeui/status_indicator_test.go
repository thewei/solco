package nativeui

import "testing"

// TestIndicatorShapes pins the one shape rule: working turns, quiet tones are
// a hollow ring, every tone that asks for a look is a solid dot.
func TestIndicatorShapes(t *testing.T) {
	cases := map[StatusTone]indicatorShape{
		ToneWorking:   shapeSpinner,
		ToneMuted:     shapeRing,
		ToneNeutral:   shapeRing,
		ToneAttention: shapeDot,
		ToneSuccess:   shapeDot,
		ToneInfo:      shapeDot,
	}
	for tone, want := range cases {
		if got := indicatorShapeFor(tone); got != want {
			t.Fatalf("indicatorShapeFor(%v) = %v, want %v", tone, got, want)
		}
	}
}

// TestIndicatorToneForStates maps every Agent state to its slot: no Agent
// says nothing, idle is the quiet ring, the rest follow operationalTone.
func TestIndicatorToneForStates(t *testing.T) {
	if _, ok := indicatorToneFor(opUnknown); ok {
		t.Fatalf("unknown state drew a slot")
	}
	for state, want := range map[operationalState]StatusTone{
		opIdle:           ToneMuted,
		opWorking:        ToneWorking,
		opNeedsAttention: ToneAttention,
		opReadyForReview: ToneSuccess,
	} {
		got, ok := indicatorToneFor(state)
		if !ok || got != want {
			t.Fatalf("indicatorToneFor(%v) = %v ok=%v, want %v", state, got, ok, want)
		}
	}
}

// TestAttentionKeepsTheWarningHue (U5): "needs you" is told apart from the
// other solid dots by its outer ring, not by a second orange, so the status
// colour is the one warning role in both appearances.
func TestAttentionKeepsTheWarningHue(t *testing.T) {
	for _, dark := range []bool{true, false} {
		tokens := designTokens(dark)
		if got, want := tokens.StatusColor(ToneAttention, dark), tokens.StatusColor(ToneWarning, dark); got != want {
			t.Fatalf("dark=%v: attention %v != warning %v", dark, got, want)
		}
		if got, want := tokens.StatusColor(ToneWarning, dark), designRoles(dark).Warn; got != want {
			t.Fatalf("dark=%v: warning %v is not the role %v", dark, got, want)
		}
	}
}
