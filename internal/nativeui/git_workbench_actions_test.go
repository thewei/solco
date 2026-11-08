package nativeui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/gitworkbench"
	"github.com/thewei/solco/internal/commandcenter"
	"github.com/thewei/solco/internal/settings"
)

/**
 * [INPUT]: scratch-less headless Git snapshot and MyGo native interaction
 * [OUTPUT]: Clean-state sync actions, Commit modal cancellation, Merge/branch-switch confirmation
 * [POS]: Phase 5 Native Git workbench UX contract, no user Git mutations
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// A clean worktree's one next action — Pull when behind, Push when ahead,
// else Fetch — starts the repository operation on press.
func TestCleanStateSyncButtonsStartOperations(t *testing.T) {
	for _, tc := range []struct {
		label  string
		ahead  int
		behind int
	}{
		{"Fetch", 0, 0},
		{"Pull 1 commit", 0, 1},
		{"Push 1 commit", 1, 0},
	} {
		s := gitSurfaceTestShell(t, t.TempDir())
		s.git.snapshot.Files = nil
		s.git.upstream = gitworkbench.UpstreamStatus{OK: true, Branch: "main", Remote: "origin", Ahead: tc.ahead, Behind: tc.behind}
		showChangesPanel(s)
		tt := ui.NewTester(s.View, 1280, 760)
		tt.Frame()
		if s.git.opBusy {
			t.Fatalf("%s: no repository operation should run before the press", tc.label)
		}
		if err := tt.Click(tc.label); err != nil {
			t.Fatalf("%s: %v; texts=%q", tc.label, err, tt.Texts())
		}
		if !s.git.opBusy {
			t.Fatalf("%s did not start a repository operation", tc.label)
		}
	}
}

func TestCommitAIPromptRequiresPreviewAndExplicitOptIn(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	s.git.snapshot.Files = append(s.git.snapshot.Files, gitworkbench.ChangeFile{Path: ".env", Status: gitworkbench.StatusModified, Additions: 1, Hunks: []gitworkbench.Hunk{{Lines: []gitworkbench.DiffLine{{Kind: gitworkbench.KindAdd, Text: "SECRET_KEY=leak"}}}}})
	showChangesPanel(s)
	s.openCommitSurface()
	s.commitSecrets = commitMapSecrets{commitAPIKeyName: "test-key-123"}
	s.settings.Workbench.CommitAPI = settings.CommitAPI{Endpoint: "https://commit.test/v1", Model: "test-model"}
	s.commitAPIKeySet = true
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	if tester.HasText("Copy Prompt") || s.commitPromptIncludeExcerpt {
		t.Fatal("AI handoff must not start with a ready-to-copy source prompt")
	}
	if err := tester.Click("Draft Commit Message…"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	for _, want := range []string{"Commit message draft", "Include code excerpts", "Generate with AI", "Review Prompt"} {
		if !tester.HasText(want) {
			t.Fatalf("preview lacks %q: %q", want, tester.Texts())
		}
	}
	if s.commitPromptIncludeExcerpt || tester.HasText("Copy Prompt") {
		t.Fatal("AI preview must default to metadata-only and hide raw prompt")
	}
	if err := tester.Click("Review Prompt"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("Copy Prompt") {
		t.Fatal("review disclosure must reveal a copyable, user-inspected prompt")
	}
	meta := gitworkbench.PrepareCommitPrompt(s.git.snapshot, s.selectedCommitPaths(), s.commitPromptIncludeExcerpt)
	if strings.Contains(meta.Text, "SECRET_KEY=leak") || strings.Contains(meta.Text, "fmt.Println") || meta.ExcludedFiles != 1 {
		t.Fatalf("default preview leaked snippets or failed to exclude sensitive path: %+v", meta)
	}
	s.commitPromptIncludeExcerpt = true
	tester.Frame()
	if err := tester.Click("Paste Suggestion…"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("Suggested commit message") || !tester.HasText("Use Suggested Message") {
		t.Fatalf("manual suggestion disclosure missing: %q", tester.Texts())
	}
	optedIn := gitworkbench.PrepareCommitPrompt(s.git.snapshot, s.selectedCommitPaths(), s.commitPromptIncludeExcerpt)
	if !strings.Contains(optedIn.Text, "fmt.Println(\"new\")") || strings.Contains(optedIn.Text, "SECRET_KEY=leak") {
		t.Fatalf("opt-in must never include sensitive-file contents: %+v", optedIn)
	}
	// The suggestion form is nested in a scrollable modal; headless text
	// exposure is tested above, while the state handler is exercised by
	// TestManualAISuggestionRequiresReplacementApproval.
	s.commitAIResponse = "fix: imported suggestion"
	s.applyManualAISuggestion()
	if s.surface.commit.Subject != "fix: imported suggestion" || s.surface.commit.InFlight {
		t.Fatalf("manual suggestion handler failed: %q", s.surface.commit.Subject)
	}
	s.cancelCommitSurface()
	if s.commitPromptPreviewOpen || s.commitPromptIncludeExcerpt || s.commitAIResponse != "" {
		t.Fatal("AI preview flags leaked into a subsequent commit task")
	}
}

func TestCommitIsNativeModalWithDraftGuard(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	showChangesList(s)
	s.openCommitSurface()
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	if !s.commitDialogOpen || s.surface.current() != WorkspaceSurfaceCommit {
		t.Fatal("Commit editor was not opened as a modal task")
	}
	for _, label := range []string{"Commit Changes", "Commit subject", "Draft Commit Message…", "Cancel Commit"} {
		if !tester.HasText(label) {
			t.Fatalf("Commit modal missing %q: %q", label, tester.Texts())
		}
	}
	s.surface.commit.Subject = "fix: keep this draft"
	tester.Frame()
	if err := tester.Click("Cancel Commit"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !s.commitDiscardPrompt || s.surface.current() != WorkspaceSurfaceCommit {
		t.Fatal("Cancel dismissed an edited commit draft without asking")
	}
	if err := tester.Click("Keep Editing"); err != nil {
		t.Fatal(err)
	}
	if s.commitDiscardPrompt || s.surface.commit.Subject != "fix: keep this draft" {
		t.Fatal("Keep Editing should preserve the draft")
	}
	if err := tester.Click("Cancel Commit"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.Click("Discard Draft"); err != nil {
		t.Fatal(err)
	}
	if s.surface.current() != WorkspaceSurfaceTerminal || s.commitDialogOpen || s.surface.commit.Subject != "" {
		t.Fatal("Discard must return to the Terminal Commit came from, without touching repository state")
	}
}

func TestCommitModalGuardsHeaderAndShortcutNavigation(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	showChangesPanel(s)
	s.openCommitSurface()
	s.surface.commit.Subject = "draft: keep"
	for _, action := range []func(){s.showViewTerminal, s.showViewHistory,
		func() { s.runShortcut(shortcutSettings) }, func() { s.runShortcut(shortcutBack) }} {
		s.commitDiscardPrompt = false
		action()
		if s.surface.current() != WorkspaceSurfaceCommit || s.router.Path() != routeWorkspace || !s.commitDiscardPrompt {
			t.Fatal("navigation bypassed edited Commit modal")
		}
	}
	s.commitDiscardPrompt = false
	s.surface.commit.Subject = ""
	s.showViewTerminal()
	if s.surface.current() != WorkspaceSurfaceTerminal || s.commitDialogOpen {
		t.Fatal("clean commit dialog should return to Terminal without draft confirmation")
	}
}

func TestCommitModalEscapeProtectsDraft(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	showChangesPanel(s)
	s.openCommitSurface()
	s.surface.commit.Subject = "feat: draft"
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if s.surface.current() != WorkspaceSurfaceCommit || s.surface.commit.Subject != "feat: draft" || !s.commitDiscardPrompt {
		t.Fatalf("Escape dropped draft or failed to explain dismissal: kind=%s prompt=%v", s.surface.current(), s.commitDiscardPrompt)
	}
}

func TestCommitDraftGuardsCommandCenterAndAgentDestinations(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	showChangesPanel(s)
	s.openCommitSurface()
	s.surface.commit.Subject = "fix: keep"
	s.openCommandCenter(commandcenter.ScopeAll)
	if s.commandCenterOpen || !s.commitDiscardPrompt || s.surface.current() != WorkspaceSurfaceCommit {
		t.Fatal("Command Center bypassed edited Commit modal")
	}
}

func TestCommitDialogDoesNotDismissWhileInFlight(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	showChangesPanel(s)
	s.openCommitSurface()
	s.surface.commit.InFlight = true
	s.requestCancelCommit()
	if !s.commitDialogOpen || s.surface.current() != WorkspaceSurfaceCommit || s.pendingToast == "" {
		t.Fatal("in-flight Commit must refuse to close")
	}
}

func TestGitBranchMergeAsksBeforeMutation(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	showChangesPanel(s)
	s.git.branches = []gitworkbench.Branch{{Name: "main", Current: true}, {Name: "topic"}}
	tester := ui.NewTester(s.View, 1200, 800)
	// The branch row's context-menu entry is pinned in sidebar_tree_test.go;
	// its handler closes the branch pull-down before showing the strategy modal.
	s.openMergeDialog("topic")
	if !s.mergeDialogOpen || s.confirmOpen {
		t.Fatal("branch merge must open the strategy dialog before confirmation")
	}
	tester.Frame()
	if !tester.HasText("Fast-forward only") || !tester.HasText("Create merge commit") {
		t.Fatalf("merge dialog missing strategy choices: %q", tester.Texts())
	}
	if err := tester.Click("Review Merge"); err != nil {
		t.Fatal(err)
	}
	if !s.confirmOpen || s.confirmKind != "git-merge-ff" || s.confirmTarget != "topic" {
		t.Fatalf("FF merge must reach its own confirmation: %q/%q", s.confirmKind, s.confirmTarget)
	}
	s.confirmOpen = false
	s.openMergeDialog("topic")
	s.mergeStrategy = 1
	tester.Frame()
	if err := tester.Click("Review Merge"); err != nil {
		t.Fatal(err)
	}
	if !s.confirmOpen || s.confirmKind != "git-merge-no-ff" || s.confirmTarget != "topic" {
		t.Fatalf("merge-commit strategy must request distinct confirmation: %q/%q", s.confirmKind, s.confirmTarget)
	}
}

func TestDirtyBranchSwitchHasLiveConfirmationHandler(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	s.confirmSwitchBranch("topic")
	if !s.confirmOpen || s.confirmKind != "git-switch-branch" || s.confirmRepoRoot != s.git.root {
		t.Fatalf("dirty switch must bind a routed Git confirmation, got %q root=%q", s.confirmKind, s.confirmRepoRoot)
	}
}

func TestGitConfirmRefusesRepositoryChange(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	s.confirmSwitchBranch("topic")
	if !s.confirmOpen || s.confirmRepoRoot != s.git.root {
		t.Fatal("confirmation should remember original Git root")
	}
	s.git.root = t.TempDir()
	s.submitConfirm(s.confirmKind, s.confirmTarget)
	if s.git.opBusy || s.pendingToast == "" {
		t.Fatal("confirming in a different repository must fail without mutation")
	}
}

func TestMergeDialogRefusesRepositoryChange(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	s.openMergeDialog("topic")
	if !s.mergeDialogOpen || s.mergeRepoRoot != s.git.root {
		t.Fatal("merge selector did not capture its repository root")
	}
	s.git.root = t.TempDir()
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	if s.mergeDialogOpen {
		t.Fatal("merge selector must close if repository context changed")
	}
}
