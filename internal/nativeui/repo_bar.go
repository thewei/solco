// [INPUT]: Git staged-file row selections.
// [OUTPUT]: Git file click handlers and the shared toggleMenu popover helper.
// [POS]: Changes-list click behavior (the right panel's file rows, via sidePanelSelectChange); the project/branch pull-downs live in sidebar_switchers.
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
package nativeui

import (
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/gitworkbench"
)

// changesClickSide opens a tree row from one of the right panel's Changes
// panes: a folder toggles, a file is picked and its diff shown on that pane's
// side of the change. The center stays where it is.
func (s *Shell) changesClickSide(row gitworkbench.FlatRow, view gdViewSide) {
	if row.Node.Dir {
		s.toggleChangesDir(row.Node.Path)
		return
	}
	s.sidePanelSelectChange(row.Node.Path, view)
}

// toggleMenu flips *open when e was clicked since the last frame. The
// click is consumed by the first pass that asks for it, so later passes
// of the same frame cannot flip it twice. Clicks() cannot be used here:
// the engine resets every element's click count at the end of each pass,
// so a count kept across frames would swallow every click after the
// first (the 2026-10-06 staged-files toggle could collapse but never
// re-expand).
func toggleMenu(open *bool, e ui.Element) {
	if e.Clicked() {
		*open = !*open
	}
}
