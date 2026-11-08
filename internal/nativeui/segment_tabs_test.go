package nativeui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The Changes | Files switch sits in a fixed 236-point track (the panel's
// narrowest card leaves no more). A count beside "Changes" must show whole there
// at any size: segments that each start from nothing share the track equally and
// clipped "Changes 330" to "Chan… 3…" on the real machine (2026-11-08).
func TestSegmentTabsKeepEveryCountWhole(t *testing.T) {
	for _, n := range []int{7, 330, 12345} {
		var want float32
		chosen := 0
		tt := ui.NewTester(func(c *ui.Context) {
			want, _ = c.MeasureText(0, ui.Span{Text: itoa(n), Size: Typography().BodySmall})
			ui.Row(c).Width(sidePanelTabsW).Shrink(0).Children(func() {
				segmentTabs(c, "Right panel page", &chosen,
					[]segmentTab{{"Changes", iconFileDiff}, {"Files", iconFile}},
					func(i int) int {
						if i == 0 {
							return n
						}
						return 0
					})
			})
		}, 400, 80)
		tt.Frame()

		got, ok := tt.Find(itoa(n))
		if !ok {
			t.Fatalf("count %d: no badge drawn: %q", n, tt.Texts())
		}
		if got.W < want-0.5 {
			t.Errorf("count %d is clipped to %.1f of %.1f px inside a %v px switch", n, got.W, want, sidePanelTabsW)
		}
	}
}

// A segment grows from what its content needs, so the one with a count is
// wider than the bare one — and a switch with room to spare still shares it out
// until the track is full.
func TestSegmentTabsShareTheSpareRoom(t *testing.T) {
	const track = 300
	chosen := 0
	var need0, need1 float32
	tt := ui.NewTester(func(c *ui.Context) {
		need0 = segmentTabNeed(c, "Changes", 330)
		need1 = segmentTabNeed(c, "Files", 0)
		ui.Row(c).Width(track).Shrink(0).Children(func() {
			segmentTabs(c, "Right panel page", &chosen,
				[]segmentTab{{"Changes", iconFileDiff}, {"Files", iconFile}},
				func(i int) int {
					if i == 0 {
						return 330
					}
					return 0
				})
		})
	}, 400, 80)
	tt.Frame()

	changes, okC := tt.Access("Changes")
	files, okF := tt.Access("Files")
	if !okC || !okF || !changes.Segment || !files.Segment {
		t.Fatalf("the two segments are not both in the tree: %+v / %+v", changes, files)
	}
	if changes.Bounds.W <= files.Bounds.W {
		t.Errorf("the segment with a count (%.1f) must be wider than the bare one (%.1f)", changes.Bounds.W, files.Bounds.W)
	}
	if changes.Bounds.W < need0 || files.Bounds.W < need1 {
		t.Errorf("a segment is narrower than its content: changes %.1f < %.1f or files %.1f < %.1f",
			changes.Bounds.W, need0, files.Bounds.W, need1)
	}
	// The track's own padding, gap and hairline take a few points.
	if filled := changes.Bounds.W + files.Bounds.W; filled < track-12 {
		t.Errorf("the segments leave the track empty: %.1f + %.1f = %.1f of %d", changes.Bounds.W, files.Bounds.W, filled, track)
	}
}
