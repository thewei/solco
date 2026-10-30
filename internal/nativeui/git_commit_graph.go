package nativeui

/**
 * [INPUT]: Git CommitInfo full parent hashes, date-ordered by the graph reader (parents never precede their children)
 * [OUTPUT]: A stable-lane commit graph — each row says which wires pass, enter the dot, leave it, converge into it and fork out of it — and its painter: hairline wires with rounded elbows at forks and merges, small dots, per-row width
 * [POS]: Pure Git history visual projection; no Git invocation in the render loop
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
)

// gitGraphWire is one wire's lane and color.
type gitGraphWire struct {
	Lane  int
	Color int
}

// gitGraphRow is one commit's slice of the graph. Lanes never move: a lane is
// held by the commit it waits for and freed when that commit arrives, so a
// wire is a straight vertical from where it opens to where it closes and the
// only bends are the elbows at a fork or a merge.
type gitGraphRow struct {
	// Lane and Color are the commit's own lane and wire color.
	Lane  int
	Color int
	// Top: a wire enters the dot from above on Lane (the commit was awaited).
	// Bottom: the first parent leaves the dot downward on Lane.
	Top    bool
	Bottom bool
	// Through are the other lanes whose wire runs the whole row untouched.
	Through []gitGraphWire
	// Converge are other lanes whose wire ends at this commit, entering from
	// above (children that share it as parent).
	Converge []gitGraphWire
	// Diverge are other lanes the commit connects into below: a merge's
	// further parents (a new wire, or one already waiting for that parent).
	Diverge []gitGraphWire
	// Lanes is how many lanes the row touches (highest used lane + 1).
	Lanes int
}

// gitHistoryGraph follows actual parent IDs, not branches inferred from
// subjects. The reader's date order guarantees a parent is listed after all of
// its children, which is what lets one pass assign lanes.
func gitHistoryGraph(commits []gitworkbench.CommitInfo) []gitGraphRow {
	rows := make([]gitGraphRow, len(commits))
	var lanes []string // the commit each lane waits for; "" = free
	var colors []int   // each lane's wire color
	next := 0          // round-robin color for a wire that just opened
	fresh := func() int { next++; return (next - 1) % len(gitLaneColors) }
	freeLane := func(avoid map[int]bool) int {
		for i, hash := range lanes {
			if hash == "" && !avoid[i] {
				return i
			}
		}
		lanes = append(lanes, "")
		colors = append(colors, 0)
		return len(lanes) - 1
	}

	for i, cm := range commits {
		row := gitGraphRow{}
		var waiting []int
		for l, hash := range lanes {
			if hash == cm.Hash {
				waiting = append(waiting, l)
			}
		}
		if len(waiting) > 0 {
			row.Lane, row.Top = waiting[0], true
			row.Color = colors[row.Lane]
		} else {
			row.Lane = freeLane(nil)
			row.Color = fresh()
		}
		for l, hash := range lanes {
			if hash != "" && hash != cm.Hash {
				row.Through = append(row.Through, gitGraphWire{l, colors[l]})
			}
		}
		justFreed := map[int]bool{row.Lane: true}
		for _, l := range waiting {
			if l != row.Lane {
				row.Converge = append(row.Converge, gitGraphWire{l, colors[l]})
			}
			lanes[l] = ""
			justFreed[l] = true
		}
		lanes[row.Lane] = ""

		for p, parent := range cm.Parents {
			if p == 0 {
				// The first parent always continues this lane, even when another
				// wire already waits for it: the wires run on side by side and
				// converge on the parent's own row, as a branch reaches its
				// fork point.
				lanes[row.Lane], colors[row.Lane] = parent, row.Color
				row.Bottom = true
				continue
			}
			if l := slices.Index(lanes, parent); l >= 0 {
				// A merged-in parent already has a wire: connect into it.
				if l != row.Lane {
					row.Diverge = append(row.Diverge, gitGraphWire{l, colors[l]})
				}
				continue
			}
			// A new wire opens for a further parent; a lane freed on this very
			// row is not reused, so no lane reads as an S-bend.
			l := freeLane(justFreed)
			lanes[l], colors[l] = parent, fresh()
			row.Diverge = append(row.Diverge, gitGraphWire{l, colors[l]})
		}
		for len(lanes) > 0 && lanes[len(lanes)-1] == "" {
			lanes, colors = lanes[:len(lanes)-1], colors[:len(colors)-1]
		}

		row.Lanes = row.Lane + 1
		for _, set := range [][]gitGraphWire{row.Through, row.Converge, row.Diverge} {
			for _, w := range set {
				row.Lanes = max(row.Lanes, w.Lane+1)
			}
		}
		rows[i] = row
	}
	return rows
}

var gitLaneColors = []ui.Color{
	ui.Hex("#e9a134"), ui.Hex("#5da6ec"), ui.Hex("#65ba85"),
	ui.Hex("#c68ae5"), ui.Hex("#e7858f"), ui.Hex("#e5ca68"),
}

// gitGraphRowH is the graph glyph's height and, by being the tallest child
// of a padding-free row, the All Commits row pitch: adjacent glyphs tile
// edge to edge, so lane wires run unbroken across rows.
const gitGraphRowH float32 = 24

const (
	gitGraphStep     float32 = 11
	gitGraphInset    float32 = 8 // lane 0's x, and the room after the last lane
	gitGraphMaxLanes         = 12
	// gitGraphWire is the wire's thickness; the dot is a little over twice it.
	gitGraphWireW float32 = 1.25
	gitGraphDotR  float32 = 3
	// gitGraphCorner is the radius of an elbow's rounded corner.
	gitGraphCorner float32 = 6
)

// gitGraphWidth is the width the widest row needs (up to gitGraphMaxLanes).
func gitGraphWidth(rows []gitGraphRow) float32 {
	lanes := 1
	for _, r := range rows {
		lanes = max(lanes, r.Lanes, r.Lane+1)
	}
	return gitGraphWidthFor(min(lanes, gitGraphMaxLanes))
}

func gitGraphWidthFor(lanes int) float32 {
	return gitGraphInset*2 + float32(max(lanes, 1)-1)*gitGraphStep
}

// gitGraphRowWidth is the width one row needs: its own lanes, so a stretch of
// single-lane history keeps its subjects close to the dot and a branch's rows
// step right of it.
func gitGraphRowWidth(r gitGraphRow) float32 {
	return gitGraphWidthFor(min(max(r.Lanes, r.Lane+1), gitGraphMaxLanes))
}

// gitGraphGlyph draws one row of the graph in a column width wide. dotOnly
// draws the commit's dot alone — a filtered list is not a history, so wires
// between rows that are no longer neighbours would lead nowhere.
func gitGraphGlyph(c *ui.Context, graph gitGraphRow, width float32, dotOnly bool) {
	ui.Box(c).Width(width).Height(gitGraphRowH).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		color := func(idx int) ui.Color { return gitLaneColors[idx%len(gitLaneColors)] }
		cy := r.Y + r.H/2
		laneX := func(l int) float32 { return r.X + gitGraphInset + float32(min(l, gitGraphMaxLanes-1))*gitGraphStep }
		dotX := laneX(graph.Lane)
		if dotOnly {
			dotX = r.X + gitGraphInset
		} else {
			for _, w := range graph.Through {
				p.Line(laneX(w.Lane), r.Y, laneX(w.Lane), r.Y+r.H, gitGraphWireW, color(w.Color))
			}
			if graph.Top {
				p.Line(dotX, r.Y, dotX, cy, gitGraphWireW, color(graph.Color))
			}
			if graph.Bottom {
				p.Line(dotX, cy, dotX, r.Y+r.H, gitGraphWireW, color(graph.Color))
			}
			var elbow ui.Path
			for _, w := range graph.Converge {
				// Down its own lane, round the corner, then in to the dot.
				x := laneX(w.Lane)
				s, rad := gitGraphSign(dotX-x), min(gitGraphCorner, abs32(dotX-x)-gitGraphDotR)
				elbow.Reset().MoveTo(x, r.Y).LineTo(x, cy-rad).QuadTo(x, cy, x+s*rad, cy).LineTo(dotX-s*gitGraphDotR, cy)
				p.StrokePath(&elbow, gitGraphWireW, color(w.Color))
			}
			for _, w := range graph.Diverge {
				// Out of the dot, round the corner, then down the lane.
				x := laneX(w.Lane)
				s, rad := gitGraphSign(x-dotX), min(gitGraphCorner, abs32(x-dotX)-gitGraphDotR)
				elbow.Reset().MoveTo(dotX+s*gitGraphDotR, cy).LineTo(x-s*rad, cy).QuadTo(x, cy, x, cy+rad).LineTo(x, r.Y+r.H)
				p.StrokePath(&elbow, gitGraphWireW, color(w.Color))
			}
		}
		p.Fill(ui.Rect{X: dotX - gitGraphDotR, Y: cy - gitGraphDotR, W: gitGraphDotR * 2, H: gitGraphDotR * 2},
			color(graph.Color), gitGraphDotR)
	})
}

func gitGraphSign(v float32) float32 {
	if v < 0 {
		return -1
	}
	return 1
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
