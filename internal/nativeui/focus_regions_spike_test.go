package nativeui

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

/**
 * [INPUT]: MyGo 的 Element.FocusGroup / Focus / Focused 与 Context.Shortcut，一个三区域的最小视图
 * [OUTPUT]: U7（desktop-t3-unify 的可选 F6 spike）的结论，写成会失败的测试：MyGo 的焦点链能否「按区域记住焦点」，以及 F6 能否在区域间跳转并回到各区域上次的位置
 * [POS]: 只验证，不接线；不进入 Shell。结论见 TestFocusRegionsSpike 的注释。行 → 进入实现要先问用户（改的是全局键盘模型）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// focusRegionsRig is three regions (sidebar, centre, panel) of three buttons
// each, the shape of Solco's window. Each region is one FocusGroup, so Tab
// treats it as a single stop; F6 is handled once at the window.
type focusRegionsRig struct {
	regions [3][]string
	last    [3]string // the button each region had the focus on last (what a region "remembers")
	current int       // the region the focus is in now
	target  string    // the button the next frame hands the focus to (F6)
}

func newFocusRegionsRig() *focusRegionsRig {
	r := &focusRegionsRig{}
	for i := range r.regions {
		for j := 1; j <= 3; j++ {
			r.regions[i] = append(r.regions[i], fmt.Sprintf("R%d-%d", i, j))
		}
	}
	return r
}

func (r *focusRegionsRig) view(c *ui.Context) {
	if c.Shortcut(0, ui.KeyF6) {
		next := (r.current + 1) % len(r.regions)
		r.target = r.last[next]
		if r.target == "" {
			r.target = r.regions[next][0]
		}
	}
	ui.Row(c).Fill().Gap(12).Children(func() {
		for i := range r.regions {
			ui.Column(c.Key(fmt.Sprintf("region-%d", i))).Gap(4).FocusGroup(ui.Vertical).Children(func() {
				for _, label := range r.regions[i] {
					b := ui.Button(c.Key(label), label)
					if r.target == label {
						b.Focus()
						r.target = ""
					}
					if b.Focused() {
						r.current, r.last[i] = i, label
					}
				}
			})
		}
	})
}

// TestFocusRegionsSpike is the answer to U7 ("can MyGo's focus chain remember
// the focus per region?"): YES, with two parts native and one part ours.
//
//   - native: a FocusGroup region is one Tab stop; Tab enters it at the element
//     that had the focus last and the arrows move inside it, so "Tab leaves the
//     sidebar, Shift+Tab returns to the row I was on" needs no code of ours.
//   - ours, small: F6 is one window shortcut plus one remembered key per region
//     (read from Focused() each frame) handed back through Focus() for a single
//     frame. MyGo exposes no "the element this group remembers", so the key is
//     tracked by us; nothing else is missing.
//
// Not covered here, and the reason U7 stays a spike: Solco's regions are not
// plain buttons (a Terminal region owns every key, the Chat WebView takes the
// keyboard away from the native tree), so wiring F6 into the Shell means
// deciding which keys those regions give up — a global keyboard decision.
func TestFocusRegionsSpike(t *testing.T) {
	rig := newFocusRegionsRig()
	tester := ui.NewTester(func(c *ui.Context) { rig.view(c) }, 600, 300)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	settleFrames(tester, 3)

	// Tab walks the regions one stop each, entering at the first element.
	tester.Key(0, ui.KeyTab)
	settleFrames(tester, 2)
	if !tester.Focused("R0-1") {
		t.Fatalf("first Tab did not land in region 0; texts=%q", tester.Texts())
	}
	// An arrow moves inside the region; Tab leaves it as one stop.
	tester.Key(0, ui.KeyDown)
	settleFrames(tester, 2)
	if !tester.Focused("R0-2") {
		t.Fatal("Down did not move within the region")
	}
	tester.Key(0, ui.KeyTab)
	settleFrames(tester, 2)
	if !tester.Focused("R1-1") {
		t.Fatal("Tab did not leave region 0 as a single stop")
	}
	// Shift+Tab returns to the element the region had the focus on.
	tester.Key(ui.Shift, ui.KeyTab)
	settleFrames(tester, 2)
	if !tester.Focused("R0-2") {
		t.Fatal("Shift+Tab did not return to the element region 0 remembered")
	}

	// F6 jumps to the next region and, again, back to where each one was.
	tester.Key(0, ui.KeyF6)
	settleFrames(tester, 2)
	if !tester.Focused("R1-1") {
		t.Fatal("F6 did not land in region 1")
	}
	tester.Key(0, ui.KeyDown)
	settleFrames(tester, 2)
	tester.Key(0, ui.KeyF6)
	settleFrames(tester, 2)
	tester.Key(0, ui.KeyF6)
	settleFrames(tester, 2)
	tester.Key(0, ui.KeyF6)
	settleFrames(tester, 2)
	if !tester.Focused("R1-2") {
		t.Fatalf("F6 round the three regions did not return to the element region 1 remembered; texts=%q", tester.Texts())
	}
}
