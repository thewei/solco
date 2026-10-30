package nativeui

/**
 * [INPUT]: the Diff review's per-side hunks (gdFile.hunks), gutter clicks with modifiers, the snapshot's Staged/Unstaged flags
 * [OUTPUT]: partial staging in the review — a gutter pick (click, ⇧ range, ⌘ toggle), a slim hunk header with Stage/Unstage Hunk or N Lines, and the Git index write through Runner.ApplyLines
 * [POS]: Fork-style line staging (designs/git-panel); presentation + one Runner call, no second Git runner and no Herdr method
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
)

// gdLinePick is the changed lines the user picked in the gutter: one side of
// one file, as {hunk, line index} pairs of that side's hunks. It is dropped
// whenever the file, its side or the snapshot changes, and after the Git write.
type gdLinePick struct {
	snap     *gitworkbench.ChangesSnapshot // the snapshot the indices belong to
	path     string
	view     gdViewSide
	lines    map[[2]int]bool
	anchor   [2]int
	anchored bool
}

// holds reports whether the pick still belongs to this file's shown side in
// this snapshot (a refresh renumbers hunks, so an older pick is void).
func (p *gdLinePick) holds(snap *gitworkbench.ChangesSnapshot, f *gdFile) bool {
	return p.snap == snap && p.path == f.cf.Path && p.view == f.view
}

// gdStageSide says whether the review can move this file's lines between the
// worktree and the index, and which way: unstage is false for the unstaged
// side (a hunk of index→worktree is staged) and true for the staged side (a
// hunk of HEAD→index is unstaged). Only a tracked text file's modification
// qualifies, shown on one side — opened from its Unstaged or Staged pane, or
// carrying the change on one side only. Commit review never does.
func (s *Shell) gdStageSide(f *gdFile) (unstage, ok bool) {
	if s.git == nil || s.gdReviewingCommit() {
		return false, false
	}
	cf := f.cf
	if cf.Status != gitworkbench.StatusModified || cf.Untracked || cf.Binary || cf.TooLarge || cf.Deferred ||
		(cf.OldPath != "" && cf.OldPath != cf.Path) || len(f.hunks()) == 0 {
		return false, false
	}
	switch f.view {
	case gdViewUnstaged:
		return false, cf.Unstaged && cf.UnstagedHunks != nil
	case gdViewStaged:
		return true, cf.Staged && cf.StagedHunks != nil
	}
	switch {
	case cf.Unstaged && !cf.Staged:
		return false, true
	case cf.Staged && !cf.Unstaged:
		return true, true
	}
	return false, false
}

// gdPicked reports whether a line of the file's shown side is picked.
func (s *Shell) gdPicked(f *gdFile, hunk, index int) bool {
	p := &s.git.gdPick
	return p.holds(s.git.snapshot, f) && p.lines[[2]int{hunk, index}]
}

// gdPickedIn returns the picked line indices of one hunk, nil for none.
func (s *Shell) gdPickedIn(f *gdFile, hunk int) map[int]bool {
	p := &s.git.gdPick
	if !p.holds(s.git.snapshot, f) {
		return nil
	}
	var out map[int]bool
	for k := range p.lines {
		if k[0] == hunk {
			if out == nil {
				out = map[int]bool{}
			}
			out[k[1]] = true
		}
	}
	return out
}

// gdPickLine answers a click on a changed line's number: a plain click picks
// just that line (again, to clear it), ⌘ toggles it among the picked, ⇧ picks
// the changed lines between the last pick and this one in the same hunk.
func (s *Shell) gdPickLine(f *gdFile, hunk, index int, mods ui.Modifiers) {
	p := &s.git.gdPick
	if !p.holds(s.git.snapshot, f) {
		*p = gdLinePick{snap: s.git.snapshot, path: f.cf.Path, view: f.view}
	}
	if p.lines == nil {
		p.lines = map[[2]int]bool{}
	}
	key := [2]int{hunk, index}
	switch {
	case mods&ui.Shift != 0 && p.anchored && p.anchor[0] == hunk:
		s.gdPickRange(f, hunk, p.anchor[1], index)
		return
	case mods&ui.Cmd != 0:
		if p.lines[key] {
			delete(p.lines, key)
		} else {
			p.lines[key] = true
		}
	case len(p.lines) == 1 && p.lines[key]:
		clear(p.lines)
	default:
		clear(p.lines)
		p.lines[key] = true
	}
	p.anchor, p.anchored = key, true
}

// gdPickRange picks exactly the changed lines of one hunk between two line
// indices, keeping the anchor where it is.
func (s *Shell) gdPickRange(f *gdFile, hunk, from, to int) {
	p := &s.git.gdPick
	if !p.holds(s.git.snapshot, f) {
		*p = gdLinePick{snap: s.git.snapshot, path: f.cf.Path, view: f.view}
	}
	if p.lines == nil {
		p.lines = map[[2]int]bool{}
	}
	lo, hi := min(from, to), max(from, to)
	clear(p.lines)
	for i, l := range f.hunks()[hunk].Lines {
		if i >= lo && i <= hi && l.Kind != gitworkbench.KindContext {
			p.lines[[2]int{hunk, i}] = true
		}
	}
}

// gdGutterDrag is a press that began on a changed line's number and may be
// sweeping over the lines below or above it. MyGo keeps other elements from
// hovering during a press, so the lines under the pointer read it through
// PointerPosition instead.
type gdGutterDrag struct {
	active bool
	moved  bool // the sweep reached another line: the release is no click
	hunk   int
	src    int // the line the press began on
	last   int // the line the sweep last reached
}

// gdGutterPick wires one changed line's number: a click picks (⇧ ranges, ⌘
// toggles), and a press dragged over other lines of the hunk picks them all.
func (s *Shell) gdGutterPick(f *gdFile, gutter ui.Element, hunk, index int) {
	d := &s.git.gdDrag
	// The click comes first: it lands on the frame the press is released, when
	// the drag below is just ending.
	if gutter.Clicked() {
		mods := gutter.ClickModifiers()
		if d.moved && d.hunk == hunk && d.src == index {
			d.moved = false
		} else {
			s.gdPickLine(f, hunk, index, mods)
		}
	}
	if _, _, pressing := gutter.Dragged(); pressing {
		if !d.active || d.hunk != hunk || d.src != index {
			*d = gdGutterDrag{active: true, hunk: hunk, src: index, last: index}
		}
	} else if d.active && d.hunk == hunk && d.src == index {
		d.active, d.moved = false, false
		return
	}
	if !d.active || d.hunk != hunk || d.last == index {
		return
	}
	if _, _, over := gutter.PointerPosition(); over {
		d.last, d.moved = index, true
		s.gdPickRange(f, hunk, d.src, index)
		s.git.gdPick.anchor, s.git.gdPick.anchored = [2]int{hunk, d.src}, true
	}
}

// gdChangedLines returns every changed line index of a hunk.
func gdChangedLines(h gitworkbench.Hunk) map[int]bool {
	out := map[int]bool{}
	for i, l := range h.Lines {
		if l.Kind != gitworkbench.KindContext {
			out[i] = true
		}
	}
	return out
}

// gdMoveLines writes the lines picked in one hunk (the whole hunk when sel is
// all of it) into the index, or out of it, through the Git runner.
func (s *Shell) gdMoveLines(f *gdFile, hunk int, sel map[int]bool, unstage bool) {
	hunks := f.hunks()
	if hunk < 0 || hunk >= len(hunks) || len(sel) == 0 {
		return
	}
	h, path, n := hunks[hunk], f.cf.Path, len(sel)
	s.git.gdPick = gdLinePick{}
	verb, past := "Stage", "Staged"
	if unstage {
		verb, past = "Unstage", "Unstaged"
	}
	s.runGitOp(verb+" Lines", func(ctx context.Context, root string) (string, error) {
		if err := s.git.runner.ApplyLines(ctx, root, path, h, sel, unstage); err != nil {
			return "", err
		}
		return past + " " + gdCountOf(n, "line"), nil
	})
}

// gdCountOf spells a count with its noun in the right number.
func gdCountOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// gdHunkHeaderText is the @@ range and the function context of a hunk.
func gdHunkHeaderText(h gitworkbench.Hunk) string {
	text := "@@ -" + gdRange(h.OldStart, h.OldLines) + " +" + gdRange(h.NewStart, h.NewLines) + " @@"
	if h.Section != "" {
		text += " " + h.Section
	}
	return text
}

// gdHunkRow is a hunk's slim header: its range and context, and — while the
// pointer is on it or lines are picked — the action that moves the picked
// lines (else the whole hunk) between the worktree and the index.
func (s *Shell) gdHunkRow(c *ui.Context, pal *gdPalette, f *gdFile, r *gdRow) {
	hunks := f.hunks()
	unstage, ok := s.gdStageSide(f)
	if !ok || int(r.hunk) >= len(hunks) {
		ui.Box(c).Height(0)
		return
	}
	h := hunks[r.hunk]
	picked := s.gdPickedIn(f, int(r.hunk))
	verb := "Stage"
	if unstage {
		verb = "Unstage"
	}
	row := s.gdCard(c, pal).Height(gdHunkRowH).Background(pal.gapBg).AlignItems(ui.Center)
	active := row.Hovered() || len(picked) > 0
	row.Children(func() {
		ui.Text(c, gdHunkHeaderText(h)).Font(gdCodeFont()).FontSize(Typography().BodySmall).TextColor(pal.gapText).
			SingleLine().Grow(1).MinWidth(0).Margin(0, 12).Unselectable()
		if !active {
			return
		}
		ui.Row(c).Shrink(0).Gap(6).AlignItems(ui.Center).Padding(0, 8, 0, 0).Children(func() {
			busy := s.git.opBusy
			if len(picked) > 0 {
				if compactButton(c, "Clear").Clicked() {
					s.git.gdPick = gdLinePick{}
				}
				label := verb + " " + gdCountOf(len(picked), "Line")
				if tipped(c, compactPrimaryButton(c, label).Disabled(busy), label+" in this hunk").Clicked() {
					s.gdMoveLines(f, int(r.hunk), picked, unstage)
				}
				return
			}
			tip := verb + " every change in this hunk. Click line numbers to pick lines (⇧ for a range, ⌘ to add)"
			if tipped(c, compactButton(c, verb+" Hunk").Disabled(busy), tip).Clicked() {
				s.gdMoveLines(f, int(r.hunk), gdChangedLines(h), unstage)
			}
		})
	})
}
