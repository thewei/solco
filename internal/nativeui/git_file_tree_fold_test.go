package nativeui

/**
 * [INPUT]: MyGo clicks on changed-file directory rows in the right panel's Changes list
 * [OUTPUT]: Repeated collapse/reopen independent of selected-list index
 * [POS]: Regression for previously non-responsive file-tree disclosures
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/gitworkbench"
)

func TestGitFileTreesRepeatedDirectoryClicks(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	s.git.rootOf[s.git.root] = s.git.root
	s.git.refreshing = true
	s.git.snapshot.Files = []gitworkbench.ChangeFile{
		{Path: "nested/one.go", Status: gitworkbench.StatusModified, Unstaged: true, Additions: 1},
		{Path: "nested/two.go", Status: gitworkbench.StatusModified, Unstaged: true, Additions: 2},
		{Path: "other/main.go", Status: gitworkbench.StatusModified, Unstaged: true, Additions: 3},
	}
	showChangesPanel(s)
	s.rebuildDiffRows()
	tt := ui.NewTester(s.View, 1280, 900)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	for i := 0; i < 3; i++ {
		if err := tt.Click("nested"); err != nil {
			t.Fatalf("collapse iteration %d: %v", i, err)
		}
		tt.Frame()
		if !s.git.collapsedDirs["nested"] {
			t.Fatalf("directory did not close at iteration %d", i)
		}
		if err := tt.Click("nested"); err != nil {
			t.Fatalf("reopen iteration %d: %v", i, err)
		}
		tt.Frame()
		if s.git.collapsedDirs["nested"] {
			t.Fatalf("directory did not reopen at iteration %d", i)
		}
	}
}
