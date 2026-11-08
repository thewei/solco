package nativeui

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/gitworkbench"
)

/**
 * [INPUT]: cached sequencer state, Git Diff, selected repo and MyGo headless tester
 * [OUTPUT]: visual/interaction regression for conflict detection, disabled Continue and abort confirm
 * [POS]: Git Workbench conflict UI tests; no real repository mutation
 * [PROTOCOL]: update header on change, then check CLAUDE.md
 */

func TestGitConflictBannerAndInspectorFollowActiveRepo(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	s.git.rootOf[s.git.root] = s.git.root
	showChangesPanel(s)
	s.git.sequencerChecked = true
	s.git.sequencer = gitworkbench.SequencerStatus{Kind: gitworkbench.SequencerMerge, Unmerged: 2, UnmergedPaths: []string{"src/main.go", "README.md"}}
	tester := ui.NewTester(s.View, 1280, 800)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	tester.Frame()
	for _, want := range []string{"Merge needs attention", "2 files with conflicts", "Continue Merge", "Abort Merge", "Show conflict files"} {
		if !tester.HasText(want) {
			t.Fatalf("Git conflict UX missing %q: %q", want, tester.Texts())
		}
	}
	if err := tester.Click("Show conflict files"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.Click("Review Conflict: src/main.go"); err != nil {
		t.Fatal(err)
	}
	if s.surface.diff.SelectedPath != "src/main.go" {
		t.Fatalf("review inspector did not navigate to conflicted file: %q", s.surface.diff.SelectedPath)
	}
	// Continue is disabled until resolved files are staged.
	_ = tester.Click("Continue Merge")
	if s.git.opBusy || s.confirmOpen {
		t.Fatal("Continue must not start a mutation while conflicts remain")
	}
	if err := tester.Click("Abort Merge"); err != nil {
		t.Fatal(err)
	}
	if !s.confirmOpen || s.confirmKind != "git-sequencer-abort" || s.confirmTarget != "merge" {
		t.Fatalf("Abort must open explicit confirmation, got %q/%q", s.confirmKind, s.confirmTarget)
	}
}

func TestGitConflictBannerFollowsSequencerKind(t *testing.T) {
	for _, kind := range []gitworkbench.SequencerKind{gitworkbench.SequencerRevert, gitworkbench.SequencerCherryPick} {
		t.Run(string(kind), func(t *testing.T) {
			s := gitSurfaceTestShell(t, t.TempDir())
			showChangesPanel(s)
			s.git.sequencerChecked = true
			s.git.sequencer = gitworkbench.SequencerStatus{Kind: kind}
			tester := ui.NewTester(s.View, 1200, 800)
			tester.Frame()
			want := "Continue " + gitOperationLabel(kind)
			if !tester.HasText(want) {
				t.Fatalf("sequencer %s missing %q: %q", kind, want, tester.Texts())
			}
			if tester.HasText("Abort Merge") {
				t.Fatal("wrong operation affordance leaked into conflict")
			}
		})
	}
}

func TestGitConflictRootResetClearsInspectorState(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	s.git.sequencerChecked = true
	s.git.sequencer = gitworkbench.SequencerStatus{Kind: gitworkbench.SequencerMerge, Unmerged: 2}
	s.resetGitContextState()
	if s.git.sequencerChecked || s.git.sequencer.Active() {
		t.Fatal("sequencer state of previous repository leaked after context reset")
	}
}
