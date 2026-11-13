package mapper

import (
	"math"
	"testing"

	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

var (
	// The development Mac: a Retina panel (1800×1169 points, 2×) with a 1080p display to its left
	// (negative x) and a 4K display to its right, vertically offset.
	builtIn = protocol.DisplayInfo{ID: "1", X: 0, Y: 0, Width: 1800, Height: 1169, PixelWidth: 3600, PixelHeight: 2338, Scale: 2, IsMain: true}
	left    = protocol.DisplayInfo{ID: "2", X: -1920, Y: 0, Width: 1920, Height: 1080, PixelWidth: 1920, PixelHeight: 1080, Scale: 1}
	right   = protocol.DisplayInfo{ID: "3", X: 1800, Y: 84, Width: 1920, Height: 1200, PixelWidth: 3840, PixelHeight: 2400, Scale: 2}
)

func TestToGlobal(t *testing.T) {
	tests := []struct {
		name   string
		d      protocol.DisplayInfo
		nx, ny float64
		wx, wy float64
	}{
		{"origin of main", builtIn, 0, 0, 0, 0},
		{"centre of main", builtIn, 0.5, 0.5, 900, 584.5},
		{"left display keeps its negative origin", left, 0, 0, -1920, 0},
		{"left display centre", left, 0.5, 0.5, -960, 540},
		{"offset display adds origin on both axes", right, 0.25, 0.5, 1800 + 480, 84 + 600},
		{"far edge stays inside the display", builtIn, 1, 1, 1799.5, 1168.5},
		{"overshoot clamps", builtIn, 1.7, -0.3, 1799.5, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			x, y, ok := ToGlobal(tc.d, tc.nx, tc.ny)
			if !ok || math.Abs(x-tc.wx) > 1e-9 || math.Abs(y-tc.wy) > 1e-9 {
				t.Fatalf("ToGlobal = (%v,%v,%v), want (%v,%v)", x, y, ok, tc.wx, tc.wy)
			}
		})
	}
}

func TestToGlobalRejectsDegenerateInput(t *testing.T) {
	if _, _, ok := ToGlobal(protocol.DisplayInfo{}, 0.5, 0.5); ok {
		t.Fatal("zero-size display must be refused")
	}
	if _, _, ok := ToGlobal(builtIn, math.NaN(), 0.5); ok {
		t.Fatal("NaN must be refused")
	}
}

// Retina is invisible to the mapper by design: points are what CGEvent takes, and a display's scale
// only changes its pixel size. The same normalised point lands on the same relative spot.
func TestScaleDoesNotChangeTheTarget(t *testing.T) {
	retina := protocol.DisplayInfo{ID: "a", Width: 1000, Height: 500, PixelWidth: 2000, PixelHeight: 1000, Scale: 2}
	plain := protocol.DisplayInfo{ID: "b", Width: 1000, Height: 500, PixelWidth: 1000, PixelHeight: 500, Scale: 1}
	x1, y1, _ := ToGlobal(retina, 0.3, 0.7)
	x2, y2, _ := ToGlobal(plain, 0.3, 0.7)
	if x1 != x2 || y1 != y2 {
		t.Fatalf("scale leaked into the mapping: (%v,%v) vs (%v,%v)", x1, y1, x2, y2)
	}
}

func TestScrollPointsBounds(t *testing.T) {
	if ScrollPoints(math.NaN()) != 0 || ScrollPoints(1e9) != protocol.MaxScrollDelta || ScrollPoints(-1e9) != -protocol.MaxScrollDelta || ScrollPoints(12) != 12 {
		t.Fatal("scroll clamp wrong")
	}
}

func TestToRectMapsOntoTheWindowPictureAndStaysInside(t *testing.T) {
	r := protocol.Rect{X: 100, Y: 50, Width: 400, Height: 300}
	x, y, ok := ToRect(r, 0.5, 0.5)
	if !ok || x != 300 || y != 200 {
		t.Fatalf("centre = %v,%v,%v", x, y, ok)
	}
	x, y, _ = ToRect(r, 5, -3) // a finger dragged past the picture clamps to it
	if x != 499.5 || y != 50 {
		t.Fatalf("clamped = %v,%v", x, y)
	}
	if _, _, ok := ToRect(protocol.Rect{}, 0.5, 0.5); ok {
		t.Fatal("an empty picture maps nothing")
	}
}

func TestInAnyFindsTheWindowOrItsPopup(t *testing.T) {
	rects := []protocol.Rect{{X: 0, Y: 0, Width: 100, Height: 100}, {X: 90, Y: 40, Width: 60, Height: 30}}
	for _, p := range []struct {
		x, y float64
		in   bool
	}{{50, 50, true}, {120, 50, true}, {120, 80, false}, {100, 10, false}, {-1, 5, false}} {
		if got := InAny(rects, p.x, p.y); got != p.in {
			t.Errorf("InAny(%v,%v) = %v", p.x, p.y, got)
		}
	}
}
