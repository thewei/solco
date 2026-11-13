package remotedesktop

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/platform"
)

// journalBackend is a backend that can also give back every journaled window.
type journalBackend struct {
	*fakeBackend
	restores atomic.Int32
}

func (b *journalBackend) RestoreJournal(context.Context) error { b.restores.Add(1); return nil }

var _ platform.JournalRestorer = (*journalBackend)(nil)

func waitRestores(t *testing.T, b *journalBackend, n int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b.restores.Load() == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("journal restores = %d, want %d", b.restores.Load(), n)
}

func newJournalService() (*Service, *journalBackend) {
	b := &journalBackend{fakeBackend: &fakeBackend{avail: true}}
	return NewService(Config{Backend: b}), b
}

func TestDeferredRestoreFiresAfterTheGraceAndNotBefore(t *testing.T) {
	svc, b := newJournalService()
	svc.restoreLater("11", 80*time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	if b.restores.Load() != 0 {
		t.Fatal("restored before the grace ran out")
	}
	waitRestores(t, b, 1)
}

func TestARestoreWithNoGraceRunsAtOnce(t *testing.T) {
	svc, b := newJournalService()
	svc.restoreLater("11", 0)
	waitRestores(t, b, 1)
}

func TestTheSameWindowChosenAgainCancelsTheDeferredRestore(t *testing.T) {
	svc, b := newJournalService()
	svc.restoreLater("11", 60*time.Millisecond)
	if !svc.claimPending("11") {
		t.Fatal("the window the phone came back to was not handed back to it")
	}
	time.Sleep(120 * time.Millisecond)
	if b.restores.Load() != 0 {
		t.Fatal("a claimed window was restored anyway")
	}
}

func TestAnotherChoiceRestoresTheWaitingWindowNow(t *testing.T) {
	for _, window := range []string{"12", ""} { // another window, or a display
		svc, b := newJournalService()
		svc.restoreLater("11", time.Hour)
		if svc.claimPending(window) {
			t.Fatalf("window %q claimed a fit that was not its own", window)
		}
		waitRestores(t, b, 1)
	}
}

func TestClosingTheServiceSettlesAWaitingRestoreBeforeExit(t *testing.T) {
	svc, b := newJournalService()
	svc.restoreLater("11", time.Hour)
	svc.Close()
	if b.restores.Load() != 1 {
		t.Fatalf("restores after Close = %d, want 1 (synchronous)", b.restores.Load())
	}
	svc.Close() // nothing waits any more
	if b.restores.Load() != 1 {
		t.Fatalf("a second Close restored again: %d", b.restores.Load())
	}
}

func TestNoClaimWhenNothingWaits(t *testing.T) {
	svc, b := newJournalService()
	if svc.claimPending("11") || b.restores.Load() != 0 {
		t.Fatal("claimed or restored with nothing pending")
	}
}
