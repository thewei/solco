package nativeui

import (
	"testing"
)

// The §6 transition table, headless and pure (GWB-030..039), as it stands
// after the Diff surface retired (2026-11-08, G5): Terminal, Chat, Detail and
// Commit are the surfaces; a Commit lands back on the one it began from, and
// the Detail card — what a click in the right panel's list opens — keeps its
// detail while Terminal or Chat shows.

func ctxA() WorkspaceContextKey {
	return WorkspaceContextKey{InstanceID: "inst1", ProjectID: "p1", TabID: "t1", RepoRoot: "/repo"}
}

func ctxB() WorkspaceContextKey {
	return WorkspaceContextKey{InstanceID: "inst1", ProjectID: "p1", TabID: "t2", RepoRoot: "/repo2"}
}

func TestTransitionStartupIsTerminal(t *testing.T) {
	var w workspaceSurfaceState
	w.resetToTerminal(ctxA())
	if w.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("startup surface = %v", w.current())
	}
}

func TestTransitionSidebarClicksLandTerminal(t *testing.T) {
	var w workspaceSurfaceState
	w.resetToTerminal(ctxA())
	w.openChat(ctxA())
	if w.current() != WorkspaceSurfaceChat {
		t.Fatalf("setup: expected chat, got %v", w.current())
	}
	// Project click / Tab click / Pane click / Agent→Pane all reset to Terminal.
	for _, ctx := range []WorkspaceContextKey{ctxA(), ctxB()} {
		w.resetToTerminal(ctx)
		if w.current() != WorkspaceSurfaceTerminal {
			t.Fatalf("navigation surface = %v", w.current())
		}
	}
}

// A Commit remembers the surface it began from — Terminal or Chat — and a
// cancel lands there again.
func TestTransitionCommitOpenCancelRestoresWhereItBegan(t *testing.T) {
	for _, from := range []WorkspaceSurfaceKind{WorkspaceSurfaceTerminal, WorkspaceSurfaceChat} {
		var w workspaceSurfaceState
		w.resetToTerminal(ctxA())
		if from == WorkspaceSurfaceChat {
			w.openChat(ctxA())
		}
		w.openCommit()
		if w.current() != WorkspaceSurfaceCommit {
			t.Fatalf("from %v: surface = %v", from, w.current())
		}
		if w.previous != from {
			t.Fatalf("from %v: previous = %v", from, w.previous)
		}
		if got := w.cancelCommit(); got != from {
			t.Fatalf("from %v: cancel landed on %v", from, got)
		}
		if w.current() != from {
			t.Fatalf("from %v: surface after cancel = %v", from, w.current())
		}
	}
}

func TestTransitionCommitSuccessLandsWhereItBegan(t *testing.T) {
	for _, from := range []WorkspaceSurfaceKind{WorkspaceSurfaceTerminal, WorkspaceSurfaceChat} {
		var w workspaceSurfaceState
		w.resetToTerminal(ctxA())
		if from == WorkspaceSurfaceChat {
			w.openChat(ctxA())
		}
		w.openCommit()
		if got := w.commitSucceeded(); got != from {
			t.Fatalf("from %v: success surface = %v", from, got)
		}
	}
}

// A Commit that began from nowhere known (a state restored without a
// previous surface) lands on the Terminal, never on Commit itself.
func TestTransitionCommitWithoutAPreviousLandsOnTerminal(t *testing.T) {
	var w workspaceSurfaceState
	w.resetToTerminal(ctxA())
	w.openCommit()
	w.previous = ""
	if got := w.cancelCommit(); got != WorkspaceSurfaceTerminal {
		t.Fatalf("cancel landed on %v", got)
	}
}

func TestTransitionBranchSwitchDropsAnOpenCommit(t *testing.T) {
	var w workspaceSurfaceState
	w.resetToTerminal(ctxA())
	w.openChat(ctxA())
	if w.branchSwitched() {
		t.Fatal("a branch switch with no Commit open has nothing to drop")
	}
	if w.current() != WorkspaceSurfaceChat {
		t.Fatalf("branch switch must keep the surface, got %v", w.current())
	}
	w.openCommit()
	if !w.branchSwitched() {
		t.Fatal("a branch switch must drop an open Commit")
	}
	if w.current() != WorkspaceSurfaceChat {
		t.Fatalf("post-switch surface = %v, want the Chat the Commit began from", w.current())
	}
}

func TestTransitionContextChangeResetsToTerminal(t *testing.T) {
	var w workspaceSurfaceState
	w.resetToTerminal(ctxA())
	w.diff.SelectedPath = "a.go"
	w.openCommit()
	// Tab/repo context changes → Terminal with dropped state.
	w.resetToTerminal(ctxB())
	if w.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("surface = %v", w.current())
	}
	if w.diff.SelectedPath != "" {
		t.Fatalf("stale selected path = %q", w.diff.SelectedPath)
	}
}

func TestTransitionRestoreOnlyValidContext(t *testing.T) {
	var w workspaceSurfaceState
	w.resetToTerminal(ctxA())
	w.openChat(ctxA())
	if !w.isValid(ctxA()) {
		t.Fatal("same context must be valid for restore")
	}
	if w.isValid(ctxB()) {
		t.Fatal("foreign context must not restore")
	}
}

func TestCommitOpenTwiceKeepsState(t *testing.T) {
	var w workspaceSurfaceState
	w.resetToTerminal(ctxA())
	w.openCommit()
	w.commit.Subject = "first"
	w.openCommit()
	if w.commit.Subject != "first" {
		t.Fatalf("reopen wiped draft: %q", w.commit.Subject)
	}
}

func TestTransitionOpenChatSwitchesToChat(t *testing.T) {
	var w workspaceSurfaceState
	w.resetToTerminal(ctxA())
	w.openChat(ctxA())
	if w.current() != WorkspaceSurfaceChat {
		t.Fatalf("surface = %v, want WorkspaceSurfaceChat", w.current())
	}
	if w.previous != WorkspaceSurfaceTerminal {
		t.Fatalf("previous = %v, want WorkspaceSurfaceTerminal", w.previous)
	}
}

func TestTransitionContextChangeFromChatResetsToTerminal(t *testing.T) {
	var w workspaceSurfaceState
	w.resetToTerminal(ctxA())
	w.openChat(ctxA())
	w.resetToTerminal(ctxB())
	if w.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("surface = %v, want WorkspaceSurfaceTerminal", w.current())
	}
}

// The Detail card is the center's third surface (2026-11-08): opened from the
// Terminal or the Chat it remembers which and the pane, going back to either
// keeps the detail, and bringing it up again lands on it.
func TestTransitionDetailOpensFromTerminalOrChatAndKeepsOnBack(t *testing.T) {
	for _, from := range []WorkspaceSurfaceKind{WorkspaceSurfaceTerminal, WorkspaceSurfaceChat} {
		var w workspaceSurfaceState
		w.resetToTerminal(ctxA())
		if from == WorkspaceSurfaceChat {
			w.openChat(ctxA())
		}
		w.openDetail(DetailChanges, "p1")
		if w.current() != WorkspaceSurfaceDetail || w.detailKind() != DetailChanges ||
			w.detailOrigin() != from || w.detailPaneID() != "p1" {
			t.Fatalf("from %v: surface=%v kind=%q origin=%v pane=%q", from, w.current(), w.detailKind(), w.detailOrigin(), w.detailPaneID())
		}

		// Back is the Shell landing the origin; the detail stays remembered.
		if from == WorkspaceSurfaceChat {
			w.openChat(ctxA())
		} else {
			w.resetToTerminal(ctxA())
		}
		if w.current() != from || w.detailKind() != DetailChanges {
			t.Fatalf("from %v: back landed on %v with detail %q, want the origin and the detail kept", from, w.current(), w.detailKind())
		}
		if !w.showDetail("p1") || w.current() != WorkspaceSurfaceDetail {
			t.Fatalf("from %v: the kept detail did not come back", from)
		}
	}
}

// With nothing open there is nothing to bring back.
func TestTransitionShowDetailWithoutOneIsRefused(t *testing.T) {
	var w workspaceSurfaceState
	w.resetToTerminal(ctxA())
	if w.showDetail("p1") || w.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("showDetail with none open = surface %v", w.current())
	}
}

// Picking another kind from the card swaps what it shows and keeps the
// surface its back button lands on; Close forgets it.
func TestTransitionDetailSwapKeepsItsOriginAndCloseForgets(t *testing.T) {
	var w workspaceSurfaceState
	w.resetToTerminal(ctxA())
	w.openChat(ctxA())
	w.openDetail(DetailChanges, "p1")
	w.openDetail(DetailFile, "p1")
	if w.detailKind() != DetailFile || w.detailOrigin() != WorkspaceSurfaceChat {
		t.Fatalf("swap: kind=%q origin=%v, want the file over the Chat", w.detailKind(), w.detailOrigin())
	}
	w.resetToTerminal(ctxA())
	w.forgetDetail()
	if w.detailKind() != "" || w.showDetail("p1") {
		t.Fatal("Close must leave nothing to bring back")
	}
}

// A context change — another project, Tab or repository — is navigation away:
// the remembered detail belonged to the old one.
func TestTransitionContextChangeForgetsTheDetail(t *testing.T) {
	var w workspaceSurfaceState
	w.resetToTerminal(ctxA())
	w.openDetail(DetailChanges, "p1")
	w.resetToTerminal(ctxB())
	if w.current() != WorkspaceSurfaceTerminal || w.detailKind() != "" {
		t.Fatalf("surface=%v detail=%q after a context change", w.current(), w.detailKind())
	}
	w.resetToTerminal(ctxA())
	w.openChat(ctxA())
	w.openDetail(DetailFile, "p1")
	w.openChat(ctxB())
	if w.detailKind() != "" {
		t.Fatalf("a Chat opened in another context kept detail %q", w.detailKind())
	}
}

// A Commit is modal: no card opens over it, and a Commit that began from the
// Detail card lands on the card again — or on the Terminal once it is gone.
func TestTransitionCommitOverDetailReturnsToIt(t *testing.T) {
	var w workspaceSurfaceState
	w.resetToTerminal(ctxA())
	w.openDetail(DetailChanges, "p1")
	w.openCommit()
	w.openDetail(DetailFile, "p1")
	if w.current() != WorkspaceSurfaceCommit || w.detailKind() != DetailChanges {
		t.Fatalf("a card opened over the Commit: surface=%v detail=%q", w.current(), w.detailKind())
	}
	if got := w.cancelCommit(); got != WorkspaceSurfaceDetail {
		t.Fatalf("cancel landed on %v, want the Detail card it began from", got)
	}

	w.openCommit()
	w.forgetDetail()
	if got := w.cancelCommit(); got != WorkspaceSurfaceTerminal {
		t.Fatalf("with the detail gone, cancel landed on %v, want the Terminal", got)
	}
}

// The repository root resolving after the Tab was selected is the same
// context finishing its lookup, not a navigation: the detail, the surface and
// its validity all survive it — while a different Tab, or a root that was
// known and changed, still counts as one.
func TestTransitionLateRepoRootIsNotANavigation(t *testing.T) {
	early := WorkspaceContextKey{InstanceID: "inst1", ProjectID: "p1", TabID: "t1"}
	late := early
	late.RepoRoot = "/repo"

	var w workspaceSurfaceState
	w.resetToTerminal(early)
	w.openDetail(DetailFile, "p1")
	if !w.isValid(late) {
		t.Fatal("a root found late made the stored state invalid")
	}
	w.resetToTerminal(late)
	if w.detailKind() != DetailFile {
		t.Fatalf("a root found late dropped the detail (%q)", w.detailKind())
	}

	moved := late
	moved.RepoRoot = "/other"
	if w.isValid(moved) {
		t.Fatal("another repository in the same Tab must invalidate the state")
	}
	w.resetToTerminal(moved)
	if w.detailKind() != "" {
		t.Fatalf("a repository change kept the detail (%q)", w.detailKind())
	}

	// Leaving the repository (a known root becoming none) is a change too.
	w.resetToTerminal(late)
	w.openDetail(DetailChanges, "p1")
	w.resetToTerminal(early)
	if w.detailKind() != "" {
		t.Fatalf("leaving the repository kept the detail (%q)", w.detailKind())
	}
}
