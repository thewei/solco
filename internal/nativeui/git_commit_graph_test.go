package nativeui

/**
 * [INPUT]: Hand-built ancestries (merge, converge, rejoin into a waiting lane, stable lanes) and seeded random DAGs
 * [OUTPUT]: The graph's contract — lanes never move, every wire a row sends down is the wire the next row receives (same lane, same color), a commit's parents are exactly the wires leaving its dot, wire colors follow the lane, and the glyph paints without a panic
 * [POS]: Pure visual graph regression
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
)

func commitsOf(spec ...string) []gitworkbench.CommitInfo {
	// "hash:p1,p2" — parents after the colon.
	var out []gitworkbench.CommitInfo
	for _, s := range spec {
		cm := gitworkbench.CommitInfo{Hash: s}
		for i := 0; i < len(s); i++ {
			if s[i] == ':' {
				cm.Hash = s[:i]
				for _, p := range splitComma(s[i+1:]) {
					cm.Parents = append(cm.Parents, p)
				}
			}
		}
		out = append(out, cm)
	}
	return out
}

func splitComma(s string) []string {
	var parts []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			parts = append(parts, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		parts = append(parts, cur)
	}
	return parts
}

func wires(ws ...gitGraphWire) []gitGraphWire { return ws }

func TestGraphMergeForksAndRejoinsOnStableLanes(t *testing.T) {
	rows := gitHistoryGraph(commitsOf("merge:left,right", "left:base", "right:base", "base"))
	merge, left, right, base := rows[0], rows[1], rows[2], rows[3]
	if merge.Lane != 0 || merge.Top || !merge.Bottom || len(merge.Diverge) != 1 || merge.Diverge[0].Lane != 1 || merge.Lanes != 2 {
		t.Fatalf("merge must keep lane 0 and open lane 1 for its second parent: %+v", merge)
	}
	if left.Lane != 0 || !left.Top || !left.Bottom || len(left.Through) != 1 || left.Through[0].Lane != 1 {
		t.Fatalf("left continues lane 0 with lane 1 passing: %+v", left)
	}
	if right.Lane != 1 || !right.Top || !right.Bottom || len(right.Diverge) != 0 {
		t.Fatalf("right runs on in lane 1 until base: %+v", right)
	}
	if base.Lane != 0 || !base.Top || base.Bottom || base.Lanes != 2 || len(base.Converge) != 1 || base.Converge[0].Lane != 1 {
		t.Fatalf("base takes lane 0 and lane 1 elbows into its dot: %+v", base)
	}
}

func TestGraphChildrenConvergeIntoTheirSharedParent(t *testing.T) {
	rows := gitHistoryGraph(commitsOf("a:p", "b:p", "p"))
	if rows[1].Lane != 1 || rows[1].Top || !rows[1].Bottom || len(rows[1].Through) != 1 {
		t.Fatalf("a second tip opens its own lane and runs on beside lane 0: %+v", rows[1])
	}
	p := rows[2]
	if p.Lane != 0 || !p.Top || len(p.Converge) != 1 || p.Converge[0].Lane != 1 {
		t.Fatalf("the parent must take the left lane and draw lane 1 into its dot: %+v", p)
	}
}

// A merge whose second parent is already awaited by another wire must still
// connect to it (the case a lane-appending projection drops).
func TestGraphMergeIntoAnAlreadyWaitingLane(t *testing.T) {
	rows := gitHistoryGraph(commitsOf("tip:b", "m:a,b", "a:root", "b:root", "root"))
	m := rows[1]
	if m.Lane != 1 || !m.Bottom || len(m.Diverge) != 1 || m.Diverge[0].Lane != 0 {
		t.Fatalf("m's second parent b waits in lane 0; m must connect into it: %+v", m)
	}
}

func TestGraphLanesNeverShift(t *testing.T) {
	// "short" ends while "long" continues: long stays on lane 1.
	rows := gitHistoryGraph(commitsOf("merge:short,long", "short", "long:tip", "tip"))
	if rows[2].Lane != 1 || !rows[2].Top || !rows[2].Bottom || rows[3].Lane != 1 {
		t.Fatalf("a surviving lane must not slide left when its sibling ends: %+v", rows)
	}
	// A lane freed on a row is not reused by a fork on that same row.
	rows = gitHistoryGraph(commitsOf("x:p", "y:p", "p:q,r", "q", "r"))
	for _, w := range rows[2].Diverge {
		if w.Lane == 1 {
			t.Fatalf("lane 1 ended at p; p's fork must not reuse it on the same row: %+v", rows[2])
		}
	}
}

func TestGraphColorsFollowTheWire(t *testing.T) {
	rows := gitHistoryGraph(commitsOf("merge:left,right", "left:base", "right:base", "base"))
	if rows[0].Diverge[0].Color == rows[0].Color {
		t.Fatalf("the second parent's wire must wear a new color: %+v", rows[0])
	}
	if rows[1].Color != rows[0].Color || rows[3].Color != rows[0].Color {
		t.Fatalf("the first-parent line is one color end to end: %+v", rows)
	}
	if rows[2].Color != rows[0].Diverge[0].Color {
		t.Fatalf("right's dot wears its wire's color: %+v", rows[2])
	}
	// A first-parent chain is one wire.
	chain := gitHistoryGraph(commitsOf("m:a,b", "b:c", "c:d", "d:e", "e", "a:f", "f"))
	if chain[1].Color != chain[2].Color || chain[2].Color != chain[3].Color || chain[5].Color == chain[1].Color {
		t.Fatalf("a chain keeps its lane color; the mainline wears its own: %+v", chain)
	}
}

// The rigorous check: whatever a row sends out of its bottom is exactly what
// the next row receives at its top — same lanes, same colors — and a commit
// leaves one wire per parent. Run on seeded random histories.
func TestGraphWiresAreContinuousOnRandomHistories(t *testing.T) {
	for seed := int64(1); seed <= 60; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 40 + rng.Intn(80)
		commits := make([]gitworkbench.CommitInfo, n)
		for i := range commits {
			commits[i].Hash = fmt.Sprintf("c%03d", i)
		}
		for i := range commits {
			// Parents come later in the list (older); sometimes none, one or two.
			for k := rng.Intn(3); k > 0 && i+1 < n; k-- {
				p := commits[min(n-1, i+1+rng.Intn(6))].Hash
				if !slices.Contains(commits[i].Parents, p) {
					commits[i].Parents = append(commits[i].Parents, p)
				}
			}
		}
		rows := gitHistoryGraph(commits)
		out := func(r gitGraphRow) []gitGraphWire {
			ws := slices.Clone(r.Through)
			if r.Bottom {
				ws = append(ws, gitGraphWire{r.Lane, r.Color})
			}
			ws = append(ws, r.Diverge...)
			return ws
		}
		in := func(r gitGraphRow) []gitGraphWire {
			ws := slices.Clone(r.Through)
			if r.Top {
				ws = append(ws, gitGraphWire{r.Lane, r.Color})
			}
			ws = append(ws, r.Converge...)
			return ws
		}
		sortWires := func(ws []gitGraphWire) []gitGraphWire {
			slices.SortFunc(ws, func(a, b gitGraphWire) int { return a.Lane - b.Lane })
			return ws
		}
		for i, r := range rows {
			if got, want := len(r.Diverge)+b2i(r.Bottom), len(dedup(commits[i].Parents)); got != want {
				t.Fatalf("seed %d row %d: %d wires leave the dot for %d parents: %+v", seed, i, got, want, r)
			}
			if i+1 == len(rows) {
				continue
			}
			// Distinct lanes among what one row sends down.
			sent, got := sortWires(out(r)), sortWires(in(rows[i+1]))
			for k := 1; k < len(sent); k++ {
				if sent[k].Lane == sent[k-1].Lane && !(slices.Contains(r.Diverge, sent[k]) || slices.Contains(r.Diverge, sent[k-1])) {
					t.Fatalf("seed %d row %d: two wires share lane %d: %+v", seed, i, sent[k].Lane, r)
				}
			}
			sent, got = dedupWires(sent), dedupWires(got)
			if !slices.Equal(sent, got) {
				t.Fatalf("seed %d: row %d sends %v down but row %d receives %v\nrow %d: %+v\nrow %d: %+v",
					seed, i, sent, i+1, got, i, r, i+1, rows[i+1])
			}
		}
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func dedup(in []string) []string {
	var out []string
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// dedupWires collapses wires that share a lane and color (an elbow that
// merges into a waiting wire overlaps it by design).
func dedupWires(ws []gitGraphWire) []gitGraphWire {
	var out []gitGraphWire
	for _, w := range ws {
		if !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}

// The glyph paints every row shape (dot-only, elbows both ways) in a headless
// frame without trouble.
func TestGraphGlyphPaintsEveryShape(t *testing.T) {
	rows := gitHistoryGraph(commitsOf("m:a,b,c", "a:z", "b:z", "c:z", "z"))
	shell := NewShell()
	view := func(c *ui.Context) {
		ui.Column(c).Children(func() {
			for _, r := range rows {
				gitGraphGlyph(c, r, gitGraphRowWidth(r), false)
			}
			gitGraphGlyph(c, rows[0], gitGraphWidthFor(1), true)
		})
	}
	_ = shell
	tester := ui.NewTester(view, 200, 300)
	tester.Frame()
	if tester.Image() == nil {
		t.Fatal("no frame")
	}
}
