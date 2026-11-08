package nativeui

import (
	"sync"
)

// WorkspaceSurfaceKind is one mutually exclusive visible center surface of
// the /workspace page (plan §5.1). Terminal/Chat/Detail/Commit are contextual
// surface state inside /workspace — never global Router routes. The Diff
// review that once stood beside them retired with the Git page (2026-11-08,
// G5): the right panel lists the changes and the files, and the item a click
// picks opens in the center as the Detail card (2026-11-08, the panel is a
// list) — one center surface at a time, never a split.
type WorkspaceSurfaceKind string

const (
	WorkspaceSurfaceTerminal WorkspaceSurfaceKind = "terminal"
	WorkspaceSurfaceChat     WorkspaceSurfaceKind = "chat"
	WorkspaceSurfaceDetail   WorkspaceSurfaceKind = "detail"
	WorkspaceSurfaceCommit   WorkspaceSurfaceKind = "commit"
)

// DetailKind names what the Detail card shows: the picked item of the right
// panel's Changes list (the worktree's diff cards, the picked file first) or
// of its Files tree (one file's preview).
type DetailKind string

const (
	DetailChanges DetailKind = "changes"
	DetailFile    DetailKind = "file"
)

// WorkspaceContextKey identifies the runtime context a surface state belongs
// to (plan §5.1). It carries identity strings only — never Herdr runtime
// objects. RepoRoot is the resolved Git repository root of the selected Tab
// ("" when the Tab is not inside a repository).
type WorkspaceContextKey struct {
	InstanceID string
	ProjectID  string
	TabID      string
	RepoRoot   string
}

// DiffSurfaceState is the changed-file diff's own presentation state: the
// file the right panel's Changes page shows, and the one the Commit backdrop
// stands on.
type DiffSurfaceState struct {
	SelectedPath string
}

// CommitSurfaceState is the Commit surface's own presentation state.
type CommitSurfaceState struct {
	Subject       string
	Body          string
	Selected      map[string]bool
	Captured      bool // stale-state fence captured at open
	InFlight      bool
	ErrText       string
	CommittedHash string
	// Amend replaces the last commit; AmendPrefilled guards loading HEAD's
	// message into the fields once per toggle.
	Amend          bool
	AmendPrefilled bool
	// NoVerify skips the repository's commit hooks; SignOff adds a
	// Signed-off-by trailer. Both apply to this one commit only.
	NoVerify bool
	SignOff  bool
}

// workspaceSurfaceState is the UI-independent primary-surface state machine
// for one /workspace center (plan §6 transition table). Pure state: no Herdr
// objects, no Git calls, no IO — transition tests run headless.
type workspaceSurfaceState struct {
	mu sync.Mutex

	kind    WorkspaceSurfaceKind
	context WorkspaceContextKey
	diff    DiffSurfaceState
	commit  CommitSurfaceState
	// previous is the surface a Commit began from: Terminal, Chat or Detail,
	// and where the commit lands again when it is cancelled or finished.
	previous WorkspaceSurfaceKind
	// detail is what the Detail card shows ("" = none open). It outlives a
	// visit to Terminal or Chat — the card's back button keeps it, so the
	// two switch without closing — and ends with Close or a context change.
	// detailFrom is the Terminal or Chat the back button lands on and
	// detailPane the pane that was selected when the card came up: another
	// pane being selected is navigation away from the card.
	detail     DetailKind
	detailFrom WorkspaceSurfaceKind
	detailPane string
}

func (w *workspaceSurfaceState) current() WorkspaceSurfaceKind {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.kind
}

// sameContext reports whether ctx is still the context the state belongs to.
// A repository root that was not found yet when the state began ("") and is
// now is the same Tab finishing its Git lookup, not a navigation: treated as
// one it would drop a Detail card opened in that gap, and land the reader on
// the Terminal at the next projection event (2026-11-08). The caller holds mu.
func (w *workspaceSurfaceState) sameContext(ctx WorkspaceContextKey) bool {
	if w.context == ctx {
		return true
	}
	return w.context.RepoRoot == "" && ctx.RepoRoot != "" &&
		w.context.InstanceID == ctx.InstanceID && w.context.ProjectID == ctx.ProjectID && w.context.TabID == ctx.TabID
}

// resetToTerminal applies the startup and explicit switch-to-Terminal
// transitions. A context change drops prior diff state; whether a context
// change lands on Terminal or a remembered Chat is the Shell's selection
// sync call (2026-10-31 pane-surface memory), not the state machine's.
func (w *workspaceSurfaceState) resetToTerminal(ctx WorkspaceContextKey) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.sameContext(ctx) {
		w.diff = DiffSurfaceState{}
		w.commit = CommitSurfaceState{}
		w.previous = WorkspaceSurfaceTerminal
		w.detail = ""
	}
	w.context = ctx
	w.kind = WorkspaceSurfaceTerminal
}

// openChat switches to the Chat surface for the active context.
func (w *workspaceSurfaceState) openChat(ctx WorkspaceContextKey) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.sameContext(ctx) {
		w.diff = DiffSurfaceState{}
		w.detail = ""
	}
	w.context = ctx
	w.kind = WorkspaceSurfaceChat
}

// openDetail shows kind's card. Opened from the Terminal or Chat, it
// remembers which one the back button lands on and which pane was selected;
// opened again from the card itself, it only swaps what the card shows. A
// Commit is modal: nothing opens over it.
func (w *workspaceSurfaceState) openDetail(kind DetailKind, paneID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch w.kind {
	case WorkspaceSurfaceCommit:
		return
	case WorkspaceSurfaceDetail:
	default:
		w.detailFrom = w.kind
		w.detailPane = paneID
	}
	w.detail = kind
	w.kind = WorkspaceSurfaceDetail
}

// showDetail brings a remembered detail back (the Terminal and Chat headers'
// button, a click on the picked row) and reports whether there was one.
func (w *workspaceSurfaceState) showDetail(paneID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.detail == "" || w.kind == WorkspaceSurfaceCommit {
		return false
	}
	if w.kind != WorkspaceSurfaceDetail {
		w.detailFrom = w.kind
		w.detailPane = paneID
	}
	w.kind = WorkspaceSurfaceDetail
	return true
}

// detailKind is what the card shows, or remembers while Terminal or Chat is
// up; "" when no detail is open.
func (w *workspaceSurfaceState) detailKind() DetailKind {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.detail
}

// detailOrigin is the surface the back button lands on: the Terminal or Chat
// the card was opened from.
func (w *workspaceSurfaceState) detailOrigin() WorkspaceSurfaceKind {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.detailFrom == WorkspaceSurfaceChat {
		return WorkspaceSurfaceChat
	}
	return WorkspaceSurfaceTerminal
}

// detailPaneID is the pane that was selected when the card came up.
func (w *workspaceSurfaceState) detailPaneID() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.detailPane
}

// forgetDetail is Close: the card is dropped and nothing remembers it. The
// caller leaves the card first when it is on screen.
func (w *workspaceSurfaceState) forgetDetail() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.detail = ""
}

// openCommit implements "Commit action → Commit". The preflight capture is
// flagged; the caller fills it right after (must not render a commit that
// silently trusts a stale snapshot).
func (w *workspaceSurfaceState) openCommit() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.kind == WorkspaceSurfaceCommit {
		return
	}
	w.previous = w.kind
	w.kind = WorkspaceSurfaceCommit
	w.commit = CommitSurfaceState{Selected: map[string]bool{}}
}

// leaveCommit returns the surface the Commit began from — Terminal when it
// began from nowhere known, or from a Detail that is gone — and is the one
// exit of cancel, success and a branch switch.
func (w *workspaceSurfaceState) leaveCommit() WorkspaceSurfaceKind {
	w.kind = w.previous
	if w.kind == "" || w.kind == WorkspaceSurfaceCommit || w.kind == WorkspaceSurfaceDetail && w.detail == "" {
		w.kind = WorkspaceSurfaceTerminal
	}
	return w.kind
}

// cancelCommit implements "Commit cancel → the surface it began from".
func (w *workspaceSurfaceState) cancelCommit() WorkspaceSurfaceKind {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.kind != WorkspaceSurfaceCommit {
		return w.kind
	}
	w.commit = CommitSurfaceState{}
	return w.leaveCommit()
}

// commitSucceeded implements "Commit success → the surface it began from"
// (the Changes page re-reads the worktree there).
func (w *workspaceSurfaceState) commitSucceeded() WorkspaceSurfaceKind {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.kind != WorkspaceSurfaceCommit {
		return w.kind
	}
	w.commit.ErrText = ""
	return w.leaveCommit()
}

// branchSwitched drops an open Commit back to where it began — the branch the
// draft was captured on is gone — and reports whether it did.
func (w *workspaceSurfaceState) branchSwitched() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.kind != WorkspaceSurfaceCommit {
		return false
	}
	w.leaveCommit()
	return true
}

// isValid reports whether a stored surface state can be restored for ctx
// (plan §6 "return /workspace → restore only valid prior context; else
// Terminal").
func (w *workspaceSurfaceState) isValid(ctx WorkspaceContextKey) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sameContext(ctx) && w.kind != ""
}
