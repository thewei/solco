// Package mapper turns what a phone touched into where the Mac should act.
//
// The phone sends pointer positions normalised (0…1) against the *video content*, so letterbox
// bars, zoom, pan, rotation and safe areas stay on the phone. Here a normalised position becomes a
// point in the platform's global point space on the display being streamed, using the display
// geometry the backend reported most recently (displays move, change resolution and scale; the
// session refreshes the geometry on every change).
//
// [INPUT]: protocol.DisplayInfo
// [OUTPUT]: ToGlobal, Clamp01, ScrollPoints
// [POS]: pure maths, no I/O — the one place coordinate bugs can live, and fully table-tested
package mapper

import (
	"math"

	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// ToGlobal maps normalised (nx, ny) on display d to a global point. Values outside 0…1 (a finger
// dragged past the video edge) clamp to the display, and the result stays strictly inside the
// display's bounds — a point exactly on the far edge belongs to the neighbouring display and would
// make the cursor jump there.
func ToGlobal(d protocol.DisplayInfo, nx, ny float64) (x, y float64, ok bool) {
	if d.Width <= 0 || d.Height <= 0 || math.IsNaN(nx) || math.IsNaN(ny) {
		return 0, 0, false
	}
	x = d.X + Clamp01(nx)*d.Width
	y = d.Y + Clamp01(ny)*d.Height
	return clampInside(x, d.X, d.Width), clampInside(y, d.Y, d.Height), true
}

// Clamp01 limits v to [0, 1].
func Clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	}
	return v
}

func clampInside(v, origin, size float64) float64 {
	const edge = 0.5 // half a point inside the far edge
	hi := origin + size - edge
	if v > hi {
		return hi
	}
	return v
}

// ScrollPoints bounds one scroll delta (points) so a corrupt or hostile message cannot spin the Mac's
// wheel; NaN becomes 0.
func ScrollPoints(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(-protocol.MaxScrollDelta, math.Min(protocol.MaxScrollDelta, v))
}
