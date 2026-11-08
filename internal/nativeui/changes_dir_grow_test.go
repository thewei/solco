package nativeui

import "testing"

// Only opening a folder starts the grow window; closing leaves it alone.
func TestToggleChangesDirStartsGrowWindowOnOpen(t *testing.T) {
	s := &Shell{git: &gitService{collapsedDirs: map[string]bool{"a": true}}}
	s.toggleChangesDir("a") // collapsed -> open
	if s.git.collapsedDirs["a"] || s.git.dirOpenedAt.IsZero() {
		t.Fatalf("opening must clear the collapse flag and stamp dirOpenedAt: %+v", s.git)
	}
	s.git.dirOpenedAt = s.git.dirOpenedAt.Add(-changesDirGrowWindow * 2)
	stamp := s.git.dirOpenedAt
	s.toggleChangesDir("a") // open -> collapsed
	if !s.git.collapsedDirs["a"] || !s.git.dirOpenedAt.Equal(stamp) {
		t.Fatal("closing must not restart the grow window")
	}
}
