package nativeui

// Identity and surface actions live in the title bar (breadcrumb + muted
// path line, icon tabs, header_overflow.go's Actions menu); the shell keeps
// no second header row.

// workspaceTitle is the display name for surfaces without a breadcrumb.
func (s *Shell) workspaceTitle() string {
	if project := s.selectedProject(); project != nil && project.Label != "" {
		return project.Label
	}
	if repo := s.gitRepoName(); repo != "" {
		return repo
	}
	return "Workspace"
}

// openCommitSurface retains the existing stale-state preflight, but moves
// the commit editor into a native modal over the selected file's diff. The
// Commit lands back on the Terminal or Chat it began from (the state
// machine's previous), with the right panel beside it as it was.
func (s *Shell) openCommitSurface() {
	s.gitMore.pushAfterCommit = false
	if s.surface.current() == WorkspaceSurfaceCommit {
		s.commitDialogOpen = true
		return
	}
	s.surface.openCommit()
	s.commitDialogOpen = true
	s.commitDiscardPrompt = false
	s.resetCommitAIFlow()
	snap := s.gitSnapshot()
	if snap == nil {
		s.surface.commit.ErrText = "Changes are still loading."
		return
	}
	// Default selection: all visible changed files (plan §22.2).
	for _, cf := range snap.Files {
		s.surface.commit.Selected[cf.Path] = true
	}
	s.captureCommitPreflight()
}

// requestCancelCommit protects message edits and in-flight Git mutations.
func (s *Shell) requestCancelCommit() {
	if s.surface.commit.InFlight || s.git != nil && s.git.committing {
		s.pendingToast = "Commit in progress; wait for it to finish."
		return
	}
	if s.surface.commit.Subject != "" || s.surface.commit.Body != "" {
		s.commitDiscardPrompt = true
		return
	}
	s.cancelCommitSurface()
}

// holdCommitNavigation intercepts intent to leave the sole Commit editor.
// The caller may navigate only after the modal is cleanly cancelled. An
// edited draft asks for deliberate Discard; no route/surface change occurs.
func (s *Shell) holdCommitNavigation() bool {
	if s.surface.current() != WorkspaceSurfaceCommit {
		return false
	}
	s.commitDialogOpen = true
	s.requestCancelCommit()
	return s.surface.current() == WorkspaceSurfaceCommit
}

// cancelCommitSurface leaves Commit back to the surface it began from.
func (s *Shell) cancelCommitSurface() {
	s.gitMore.pushAfterCommit = false
	s.commitDialogOpen = false
	s.commitDiscardPrompt = false
	s.resetCommitAIFlow()
	s.landAfterCommit(s.surface.cancelCommit())
}

// dropCommitOnBranchChange closes an open Commit when the branch it was captured
// on changed under it: the draft's preflight no longer describes the worktree.
func (s *Shell) dropCommitOnBranchChange() {
	if !s.surface.branchSwitched() {
		return
	}
	s.commitDialogOpen = false
	s.commitDiscardPrompt = false
	s.resetCommitAIFlow()
	s.landAfterCommit(s.surface.current())
}

// landAfterCommit puts the center back on the surface a Commit began from.
// The Terminal canvas is not drawn under a Commit, so its attachments are
// re-synced on the way in (enterTerminalSurface); Chat keeps its binding and
// only re-reads the worktree.
func (s *Shell) landAfterCommit(kind WorkspaceSurfaceKind) {
	if kind == WorkspaceSurfaceTerminal {
		s.enterTerminalSurface()
		return
	}
	s.ensureGitSnapshot(false)
}
