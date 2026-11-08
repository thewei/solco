package nativeui

/**
 * [INPUT]: MyGo Git controls, folder changed paths, fake asynchronous AI suggestion over the configured channel
 * [OUTPUT]: Shared icon-button face and Tab stops, folder staging selection, AI composer handoff
 * [POS]: Git polish regression and no-automatic-commit contract
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/gitworkbench"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/settings"
)

// A button's face, probed by hand: whether hovering fills a point along its
// edge, and whether it leaves a point near a corner bare (a rounded square
// of the shared radius does; a flatter radius paints into the corner). The
// corner probe is the pixel diagonally one in from the corner: at the shared
// 8-point radius (iconButtonRadius) that pixel is entirely outside the
// curve, while a radius of 6 or less covers part of it.
func hoverFaceOf(tt *ui.Tester, label string) (edgeFilled, cornerBare bool) {
	rect, ok := tt.Find(label)
	if !ok {
		return false, false
	}
	at := func() (edge, corner [4]uint8) {
		tt.Frame() // hover takes a pass to be seen, a second (and the clock) to be painted
		tt.Advance(300 * time.Millisecond)
		tt.Frame()
		return pixelAt(tt, int(rect.X)+2, int(rect.Y+rect.H/2)), pixelAt(tt, int(rect.X)+1, int(rect.Y)+1)
	}
	tt.Move(1, 1)
	idleEdge, idleCorner := at()
	tt.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	hotEdge, hotCorner := at()
	return rgbDiff(hotEdge, idleEdge) > 6, rgbDiff(hotCorner, idleCorner) <= 2
}

// The panel's icon buttons wear the shared icon-button face (gorexIconButton)
// rather than a look of their own: hovered, each paints the same rounded
// square as a shared button of its size — the head's Refresh and the
// composer's Commit options chevron at the denser pane-header size.
func TestGitIconButtonsWearTheSharedFace(t *testing.T) {
	for _, dark := range []bool{false, true} {
		reference := ui.NewTester(func(c *ui.Context) {
			ui.Row(c).FillWidth().Padding(20).Background(designTokens(c.Theme().Dark).Content).Children(func() {
				gorexIconButton(c, gorexColorsOf(c.Theme().Dark), iconRefresh, "Reference", "", false, gorexPaneBtn, gorexPaneGlyph)
			})
		}, 200, 80)
		reference.SetDark(dark)
		reference.SetPreferences(ui.Preferences{ReduceMotion: true})
		reference.Frame()
		paneEdge, paneCorner := hoverFaceOf(reference, "Reference")
		if !paneEdge || !paneCorner {
			t.Fatalf("dark=%v: the probe cannot read the shared face (edge=%v corner=%v)", dark, paneEdge, paneCorner)
		}

		s := gitSurfaceTestShell(t, t.TempDir())
		showChangesPanel(s)
		tt := ui.NewTester(s.View, 1280, 760)
		tt.SetDark(dark)
		tt.SetPreferences(ui.Preferences{ReduceMotion: true})
		tt.Frame()
		for _, label := range []string{"Refresh repository", "Commit options"} {
			if edge, corner := hoverFaceOf(tt, label); edge != paneEdge || corner != paneCorner {
				t.Errorf("dark=%v %s: hover fills its edge=%v and leaves the corner bare=%v; the shared face does %v and %v",
					dark, label, edge, corner, paneEdge, paneCorner)
			}
		}
	}
}

// The shared face must not cost the panel its keyboard: the head's project and
// branch chips, the commit subject field and the composer's Commit options
// chevron stay Tab stops — and so do the shared icon buttons, Refresh and Close
// Right Panel among them (2026-11-08: they were mouse-only, ⌘R their only key).
func TestPanelControlsStayTabStops(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	showChangesList(s)
	tt := ui.NewTester(s.View, 1280, 760)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	settleFrames(tt, 3)
	for _, label := range []string{"Refresh repository", "Switch project", "Switch branch", "Commit subject draft", "Commit options", "Close Right Panel"} {
		reached := false
		for i := 0; i < 80 && !reached; i++ {
			tt.Key(0, ui.KeyTab)
			tt.Frame()
			reached = tt.Focused(label)
		}
		if !reached {
			t.Errorf("Tab never reaches %q; the panel's controls are no longer keyboard-reachable", label)
		}
	}
}

// A shared icon button focused by Tab is pressed by Enter, and a pointer press
// leaves the keyboard focus where it was (the Terminal or the commit field it
// sits beside keeps typing).
func TestSharedIconButtonsActivateFromTheKeyboard(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	showChangesList(s)
	tt := ui.NewTester(s.View, 1280, 760)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	settleFrames(tt, 3)
	reached := false
	for i := 0; i < 80 && !reached; i++ {
		tt.Key(0, ui.KeyTab)
		tt.Frame()
		reached = tt.Focused("Close Right Panel")
	}
	if !reached {
		t.Fatal("Tab never reaches Close Right Panel")
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if s.sidePanel.open {
		t.Fatal("Enter on the focused Close Right Panel did not close the panel")
	}
}

func TestGitDirectoryPathsRespectPaneAndPrefixBoundary(t *testing.T) {
	snap := &gitworkbench.ChangesSnapshot{Files: []gitworkbench.ChangeFile{
		{Path: "src/a.go", Unstaged: true},
		{Path: "src/deep/b.go", Unstaged: true, Staged: true},
		{Path: "src/staged.go", Staged: true},
		{Path: "src-old/no.go", Unstaged: true},
		{Path: "third_party/x.go", Untracked: true},
	}}
	for _, tc := range []struct {
		pane gdViewSide
		want string
	}{
		{gdViewUnstaged, "src/a.go,src/deep/b.go"},
		{gdViewStaged, "src/deep/b.go,src/staged.go"},
		{gdViewCombined, ""},
	} {
		got := strings.Join(gitDirectoryPaths(snap, "src", tc.pane), ",")
		if got != tc.want {
			t.Fatalf("directory paths for pane %v = %s want %s", tc.pane, got, tc.want)
		}
	}
}

func TestGitFolderContextMenuSelectsAllChangedDescendants(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	s.git.rootOf[s.git.root] = s.git.root
	s.git.refreshing = true
	s.git.snapshot.Files = []gitworkbench.ChangeFile{
		{Path: "src/a.go", Unstaged: true, Status: gitworkbench.StatusModified},
		{Path: "src/deep/b.go", Unstaged: true, Status: gitworkbench.StatusModified},
		{Path: "other/c.go", Unstaged: true, Status: gitworkbench.StatusModified},
	}
	showChangesPanel(s)
	s.rebuildDiffRows()
	tt := ui.NewTester(s.View, 1280, 800)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	if err := tt.RightClick("src"); err != nil {
		t.Fatal(err)
	}
	menu := strings.Join(tt.Menu(), " | ")
	if !strings.Contains(menu, "Select All Files in Folder") || !strings.Contains(menu, "Stage Folder (2 Files)") {
		t.Fatalf("directory menu incomplete: %q", menu)
	}
	if err := tt.ChooseMenuItem("Select All Files in Folder"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !s.unstagedChoice.Has("src/a.go") || !s.unstagedChoice.Has("src/deep/b.go") || s.unstagedChoice.Has("other/c.go") {
		t.Fatalf("directory selection leaked to unrelated paths")
	}
}

func TestGitQuickAICommitFillsSubjectButNeverCommits(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	s.git.rootOf[s.git.root] = s.git.root
	s.git.refreshing = true
	showChangesList(s)
	s.commitSecrets = commitMapSecrets{commitAPIKeyName: "test-key-123"}
	s.settings.Workbench.CommitAPI = settings.CommitAPI{Endpoint: "https://commit.test/v1", Model: "test-model"}
	s.commitAPIKeySet = true
	s.gitQuickBody = "Keep manually written body"
	queue := make(chan func(), 2)
	s.uiApplyOverride = func(fn func()) { queue <- fn }
	s.commitGenExecutor = func(ctx context.Context, req agent.CommitRequest) (agent.CommitResponse, error) {
		if req.Endpoint != "https://commit.test/v1" || !strings.Contains(req.Prompt, "File: src/main.go") || !strings.Contains(req.Prompt, "Do NOT stage, commit, push") ||
			strings.Contains(req.Prompt, "fmt.Println") {
			return agent.CommitResponse{}, context.Canceled
		}
		return agent.CommitResponse{Message: "feat: refine Git operations\n\nGenerated details"}, nil
	}
	tt := ui.NewTester(s.View, 1200, 800)
	tt.Frame()
	if err := tt.Click("Commit options"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem("Draft Subject with AI"); err != nil {
		t.Fatal(err)
	}
	if !s.commitQuickRunning {
		t.Fatal("AI was not started by split button")
	}
	deadline := time.After(3 * time.Second)
	for s.commitQuickRunning {
		select {
		case apply := <-queue:
			apply()
		case <-deadline:
			t.Fatal("AI completion timeout")
		}
	}
	if s.gitQuickSubject != "feat: refine Git operations" || s.gitQuickBody != "Keep manually written body" || s.git.committing || s.surface.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("AI must fill Subject only and never commit: subject=%q body=%q error=%q mode=%v", s.gitQuickSubject, s.gitQuickBody, s.commitQuickError, s.surface.current())
	}
}

func TestGitQuickAIRejectsStaleDraft(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	s.git.rootOf[s.git.root] = s.git.root
	s.git.refreshing = true
	showChangesPanel(s)
	s.commitSecrets = commitMapSecrets{commitAPIKeyName: "test-key-123"}
	s.settings.Workbench.CommitAPI = settings.CommitAPI{Endpoint: "https://commit.test/v1", Model: "test-model"}
	s.commitAPIKeySet = true
	queue := make(chan func(), 1)
	s.uiApplyOverride = func(fn func()) { queue <- fn }
	s.commitGenExecutor = func(context.Context, agent.CommitRequest) (agent.CommitResponse, error) {
		return agent.CommitResponse{Message: "feat: obsolete"}, nil
	}
	s.startQuickAICommit()
	s.gitQuickSubject = "feat: my newer draft"
	deadline := time.After(3 * time.Second)
	for s.commitQuickRunning {
		select {
		case f := <-queue:
			f()
		case <-deadline:
			t.Fatal("AI timeout")
		}
	}
	if s.gitQuickSubject != "feat: my newer draft" || s.commitQuickError == "" || s.git.committing {
		t.Fatal("stale AI result overwrote user draft")
	}
}

// A Pull the fast-forward-only rule refuses on a diverged branch says so in
// words instead of git's exit line (device E2E 2026-11-03).
func TestPullRefusalOnDivergedBranchSaysSo(t *testing.T) {
	root := t.TempDir()
	shell := gitSurfaceTestShell(t, root)
	shell.uiApplyOverride = func(fn func()) { fn() }
	shell.git.upstream = gitworkbench.UpstreamStatus{OK: true, Branch: "main", Remote: "origin", Ahead: 2, Behind: 1}
	done := make(chan struct{})
	shell.runGitNetworkOp("Pull", func(context.Context, string) (string, error) {
		defer close(done)
		return "", errors.New("git pull: exit status 128")
	})
	<-done
	deadline := time.Now().Add(5 * time.Second)
	for shell.pendingToast == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(shell.pendingToast, "diverged") {
		t.Fatalf("toast = %q, want the diverged wording", shell.pendingToast)
	}
}

// TestEmptyWorkspaceCenterOffersNewWorkspace: the sidebar header's hover "+"
// was the only way to a Workspace; the empty center names it too (device
// E2E 2026-11-03).
func TestEmptyWorkspaceCenterOffersNewWorkspace(t *testing.T) {
	shell := NewShell()
	tt := ui.NewTester(func(c *ui.Context) { shell.workspaceHomeCenter(c) }, 900, 600)
	tt.Frame()
	if _, ok := tt.Find("New Workspace from Folder…"); !ok {
		t.Fatal("the empty workspace center has no way to create a Workspace")
	}
}
