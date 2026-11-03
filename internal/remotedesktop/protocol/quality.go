package protocol

// Tier is a user-facing quality level. Text must stay readable (code, terminals), so the tiers are
// defined by resolution first; 60 fps is an opt-in performance mode, not a first-version tier.
type Tier string

const (
	TierSmooth   Tier = "smooth"   // 720p / 30
	TierStandard Tier = "standard" // 1080p / 30
	TierHigh     Tier = "high"     // 1440p / 30
)

const (
	// DefaultTier is what a session starts with when the phone names none.
	DefaultTier = TierStandard
	// KeyframeIntervalSec is the periodic keyframe cadence; PLI/FIR requests fill the gaps.
	KeyframeIntervalSec = 4
	maxEncodeWidth      = 4096
)

// TierSpec is the encoder target of a Tier.
type TierSpec struct {
	Height      int
	FPS         int
	BitrateKbps int
}

// Specs lists the tiers in ascending quality.
var Specs = map[Tier]TierSpec{
	TierSmooth:   {Height: 720, FPS: 30, BitrateKbps: 2500},
	TierStandard: {Height: 1080, FPS: 30, BitrateKbps: 5000},
	TierHigh:     {Height: 1440, FPS: 30, BitrateKbps: 9000},
}

// Tiers is the stable display order.
var Tiers = []Tier{TierSmooth, TierStandard, TierHigh}

// Valid reports whether t is a known tier.
func (t Tier) Valid() bool { _, ok := Specs[t]; return ok }

// Size is the encoded frame size for display d at this tier: the display's aspect ratio is kept (never
// stretched), the size never exceeds the panel's own pixels (no upscaling), and both sides are even
// (H.264 4:2:0).
func (s TierSpec) Size(d DisplayInfo) (w, h int) {
	pw, ph := d.PixelWidth, d.PixelHeight
	if pw <= 0 || ph <= 0 {
		pw, ph = int(d.Width), int(d.Height)
	}
	if pw <= 0 || ph <= 0 {
		return 0, 0
	}
	h = s.Height
	if ph < h {
		h = ph
	}
	w = int(float64(h)*float64(pw)/float64(ph) + 0.5)
	if w > maxEncodeWidth {
		w = maxEncodeWidth
		h = int(float64(w)*float64(ph)/float64(pw) + 0.5)
	}
	return w &^ 1, h &^ 1
}
