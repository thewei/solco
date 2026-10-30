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
