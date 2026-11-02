package nativeui

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/remotehost"
)

func trasherFor(t *testing.T, f *trashFixture) *remoteTrasher {
	t.Helper()
	got, ok := f.shell.hostTrasher(f.shell.hist.service).(*remoteTrasher)
	if !ok {
		t.Fatal("a Shell with a Trash and a catalog must offer the phone a Trasher")
	}
	return got
}

func wantTrashCode(t *testing.T, err error, code string, status int) {
	t.Helper()
	var te *remotehost.TrashError
	if !errors.As(err, &te) || te.Code != code || te.Status != status {
		t.Fatalf("err = %v, want %s/%d", err, code, status)
	}
}

// The phone's two reversible steps run on the desktop's own Store: the file
// leaves its provider root and the catalog forgets it, then comes back whole.
func TestRemoteTrashMovesAndPutsBack(t *testing.T) {
	f := newTrashFixture(t)
	trasher := trasherFor(t, f)
	ctx := context.Background()

	entry, err := trasher.Trash(ctx, trashKeyRelease)
	if err != nil || entry == "" {
		t.Fatalf("Trash = %q, %v", entry, err)
	}
	if pathExists(f.paths[trashKeyRelease]) || f.inTrash() != 1 {
		t.Fatal("the session file did not move into the Trash")
	}
	if _, listed := f.listed()[trashKeyRelease]; listed {
		t.Fatal("the catalog still lists a trashed session")
	}
	entries, err := trasher.List(ctx)
	if err != nil || len(entries) != 1 || entries[0].ID != entry || entries[0].Title != "Review the release notes" || entries[0].Agent != "claude-code" {
		t.Fatalf("List = %+v, %v", entries, err)
	}

	if err := trasher.Restore(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if !pathExists(f.paths[trashKeyRelease]) || f.inTrash() != 0 {
		t.Fatal("Put Back did not return the file")
	}
	if _, listed := f.listed()[trashKeyRelease]; !listed {
		t.Fatal("the catalog did not pick the restored session up again")
	}
}

// An Agent running the session, or a file written a moment ago, is refused:
// the same exact-identity rule as the desktop, and nothing moves.
func TestRemoteTrashRefusesASessionInUse(t *testing.T) {
	f := newTrashFixture(t)
	trasher := trasherFor(t, f)
	ctx := context.Background()

	live := selectionFixture("w1", "t1", "p1")
	live.Agents = []herdr.Agent{{
		TerminalID: "term2", PaneID: "p2", ProjectID: "w2", TabID: "t2", Status: "working",
		AgentSession: &herdr.AgentSessionIdentity{Agent: "claude-code", Kind: "id", Value: f.meta(trashKeySpacing).ID},
	}}
	f.shell.projection = live
	f.shell.publishRemoteLiveSessions()
	_, err := trasher.Trash(ctx, trashKeySpacing)
	wantTrashCode(t, err, "session_in_use", http.StatusConflict)
	if !pathExists(f.paths[trashKeySpacing]) || f.inTrash() != 0 {
		t.Fatal("a running session moved")
	}

	now := time.Now()
	if err := os.Chtimes(f.paths[trashKeySettings], now, now); err != nil {
		t.Fatal(err)
	}
	_, err = trasher.Trash(ctx, trashKeySettings)
	wantTrashCode(t, err, "session_in_use", http.StatusConflict)
	if !pathExists(f.paths[trashKeySettings]) {
		t.Fatal("a session written a moment ago moved")
	}
}

func TestRemoteTrashRefusesUnknownThings(t *testing.T) {
	f := newTrashFixture(t)
	trasher := trasherFor(t, f)
	ctx := context.Background()

	_, err := trasher.Trash(ctx, "claude-code:nope")
	wantTrashCode(t, err, "unknown_session", http.StatusNotFound)
	wantTrashCode(t, trasher.Restore(ctx, "1700000000000-aaaaaaaa"), "unknown_entry", http.StatusNotFound)

	entry, err := trasher.Trash(ctx, trashKeyFlaky)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.paths[trashKeyFlaky], []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantTrashCode(t, trasher.Restore(ctx, entry), "session_exists", http.StatusConflict)
	if f.inTrash() != 1 {
		t.Fatal("a refused Put Back lost the Trash entry")
	}
}

func TestHostTrasherAbsentWithoutATrash(t *testing.T) {
	shell := NewShell()
	if got := shell.hostTrasher(shell.hist.service); got != nil {
		t.Fatalf("no Trash must mean no phone Trash, got %T", got)
	}
}
