package nativeui

import (
	"testing"
)

/**
 * [INPUT]: Shell with a seeded Git surface, settings.Workbench.TreeFolds
 * [OUTPUT]: openAllGitSections fixture helper; fold defaults and per-repository persistence guards
 * [POS]: Regression for the sidebar fold persistence (2026-11-03)
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// openAllGitSections unfolds every section: most fixtures look at rows the
// default (only Branches open) hides.
func openAllGitSections(s *Shell) {
	for k := range gitSectionsClosedByDefault {
		s.setGitFolded(k, false)
	}
}

func TestGitFoldsDefaultToOnlyBranchesOpen(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	for _, name := range []string{"Worktrees", "Remotes", "Tags", "Stashes", "Submodules"} {
		if !s.gitFolded("section/" + name) {
			t.Errorf("%s should start folded", name)
		}
	}
	for _, key := range []string{"section/Branches", "local/feature", "remote/origin"} {
		if s.gitFolded(key) {
			t.Errorf("%s should start open", key)
		}
	}
}

func TestGitFoldsPersistPerRepository(t *testing.T) {
	root := t.TempDir()
	s := gitSurfaceTestShell(t, root)
	s.setGitFolded("section/Branches", true)
	s.setGitFolded("section/Tags", false)
	s.setGitFolded("local/feature", true)
	s.setGitFolded("remote/origin/team", true)
	if !s.gitFolded("section/Branches") || s.gitFolded("section/Tags") || !s.gitFolded("local/feature") || !s.gitFolded("remote/origin/team") {
		t.Fatal("choices must read back")
	}
	saved := s.settings.Workbench.TreeFolds[root]
	if len(saved) != 4 {
		t.Fatalf("saved = %v, want the four deviations", saved)
	}
	// Going back to the default drops the entry; an empty repository vanishes.
	for k, v := range saved {
		s.setGitFolded(k, !v)
	}
	if len(s.settings.Workbench.TreeFolds) != 0 {
		t.Fatalf("defaults must not be stored: %v", s.settings.Workbench.TreeFolds)
	}
	// Another repository is untouched.
	s.setGitFolded("section/Tags", false)
	s.git.root = t.TempDir()
	if !s.gitFolded("section/Tags") {
		t.Fatal("folds belong to their repository")
	}
}

func TestGitSearchFoldsStayInTheSession(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	s.setGitFolded("search/x/local/feature", true)
	if !s.gitFolded("search/x/local/feature") || len(s.settings.Workbench.TreeFolds) != 0 {
		t.Fatal("search folds must not be persisted")
	}
}
