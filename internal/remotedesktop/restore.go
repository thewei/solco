package remotedesktop

import (
	"context"
	"sync"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/platform"
)

// pendingRestore is the window a lost link left fitted: it is given back after the grace unless the phone
// comes back and chooses that very window again (see Service.claimPending).
type pendingRestore struct {
	mu     sync.Mutex
	window string
	timer  *time.Timer
}

// journalRestorer is the backend's one-shot "give back every journaled window", when it has one.
func (s *Service) journalRestorer() (platform.JournalRestorer, bool) {
	r, ok := s.cfg.Backend.(platform.JournalRestorer)
	return r, ok
}

// RestoreJournal gives back every window a vanished session or a crashed helper left fitted. Solco calls it
// at start-up (before any session) and the service itself after a grace or a helper failure. It is cheap
// when the journal is empty.
func (s *Service) RestoreJournal(ctx context.Context) {
	r, ok := s.journalRestorer()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := r.RestoreJournal(ctx); err != nil {
		s.log.Warn("remote desktop journal restore failed")
		return
	}
	s.log.Info("remote desktop journal restored")
}

// restoreLater is Config.RestoreLater of a session: after grace (0 = now) every journaled window is given
// back, unless the phone claims the window first.
func (s *Service) restoreLater(window string, grace time.Duration) {
	s.pending.mu.Lock()
	if s.pending.timer != nil {
		s.pending.timer.Stop()
	}
	s.pending.window = window
	if grace <= 0 {
		s.pending.window, s.pending.timer = "", nil
		s.pending.mu.Unlock()
		go s.RestoreJournal(context.Background())
		return
	}
	var t *time.Timer
	t = time.AfterFunc(grace, func() {
		s.pending.mu.Lock()
		if s.pending.timer != t { // claimed or replaced meanwhile
			s.pending.mu.Unlock()
			return
		}
		s.pending.window, s.pending.timer = "", nil
		s.pending.mu.Unlock()
		s.RestoreJournal(context.Background())
	})
	s.pending.timer = t
	s.pending.mu.Unlock()
}

// claimPending is Config.ClaimPending of a session: window is what the new session now shows ("" = a
// display). The deferred restore of that same window is cancelled (true: it arrives still fitted); a
// deferred restore of any other window happens now.
func (s *Service) claimPending(window string) bool {
	s.pending.mu.Lock()
	id, t := s.pending.window, s.pending.timer
	if t == nil {
		s.pending.mu.Unlock()
		return false
	}
	t.Stop()
	s.pending.window, s.pending.timer = "", nil
	s.pending.mu.Unlock()
	if id == window && window != "" {
		return true
	}
	go s.RestoreJournal(context.Background())
	return false
}

// RestoreWindow is the Mac owner's "Restore Window" for session id.
func (s *Service) RestoreWindow(id string) {
	s.mu.Lock()
	act := s.active
	s.mu.Unlock()
	if act != nil && act.ID() == id {
		go act.RestoreWindow()
	}
}

// settlePending ends the wait at app quit: whatever a lost link left fitted goes back before the process exits.
func (s *Service) settlePending() {
	s.pending.mu.Lock()
	waiting := s.pending.timer != nil
	if waiting {
		s.pending.timer.Stop()
	}
	s.pending.window, s.pending.timer = "", nil
	s.pending.mu.Unlock()
	if waiting {
		s.RestoreJournal(context.Background())
	}
}
