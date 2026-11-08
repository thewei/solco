package personalagent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestNotesStartEmptyAndRoundTripBothFiles(t *testing.T) {
	s := notesStore(t)
	a := mustAssistant(t, s, "a1", "Ada")
	n, err := s.ReadNotes(a.ID)
	if err != nil || n.User.Text != "" || n.Assistant.Text != "" || n.User.Rev == "" {
		t.Fatalf("fresh notes: %+v %v", n, err)
	}
	n, err = s.WriteNotes(a.ID, NotesUser, "I work in UTC+8.\n", n.User.Rev)
	if err != nil || n.User.Text != "I work in UTC+8.\n" {
		t.Fatalf("write user: %+v %v", n, err)
	}
	// Either side may write its own file; the folder is private to the user.
	n, err = s.WriteNotes(a.ID, NotesAssistant, "- keeps the digest on Monday\n", n.Assistant.Rev)
	if err != nil || n.Assistant.Text == "" {
		t.Fatalf("write assistant: %+v %v", n, err)
	}
	dir, _ := s.NotesFolder(a.ID)
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("folder mode: %v %v", fi, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, NotesUserFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", fi, err)
	}
	if _, err := s.WriteNotes(a.ID, "both", "x", n.User.Rev); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an unknown target is invalid: %v", err)
	}
}

func TestStaleWriteIsRefusedSoNobodysEditIsLost(t *testing.T) {
	s := notesStore(t)
	a := mustAssistant(t, s, "a1", "Ada")
	read, _ := s.ReadNotes(a.ID)
	// The person edits the Assistant's file while the Assistant is tidying it.
	if _, err := s.WriteNotes(a.ID, NotesAssistant, "- person's edit\n", read.Assistant.Rev); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteNotes(a.ID, NotesAssistant, "- tidy-up from the stale read\n", read.Assistant.Rev); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("the stale tidy-up must be refused: %v", err)
	}
	if _, err := s.WriteNotes(a.ID, NotesAssistant, "x", ""); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("a write that names no rev is refused: %v", err)
	}
	if n, _ := s.ReadNotes(a.ID); n.Assistant.Text != "- person's edit\n" {
		t.Fatalf("the person's edit survived: %q", n.Assistant.Text)
	}
}

func TestAddNoteAppendsOnceAndCountsLimits(t *testing.T) {
	s := notesStore(t)
	a := mustAssistant(t, s, "a1", "Ada")
	ack, err := s.AddAssistantNote(a.ID, "  Prefers  tea ")
	if err != nil || !ack.Added {
		t.Fatalf("first add: %+v %v", ack, err)
	}
	if ack, _ := s.AddAssistantNote(a.ID, "- prefers TEA"); ack.Added {
		t.Fatal("the same line, up to case and spacing, is not added twice")
	}
	if n, _ := s.ReadNotes(a.ID); n.Assistant.Text != "- Prefers tea\n" {
		t.Fatalf("file = %q", n.Assistant.Text)
	}
	if _, err := s.AddAssistantNote(a.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty note: %v", err)
	}
	if _, err := s.AddAssistantNote(a.ID, strings.Repeat("x", maxNoteLine+1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a long note is not a line: %v", err)
	}
	// Concurrent adds (the broker's CLI runs in its own process) lose nothing.
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.AddAssistantNote(a.ID, "fact number "+strings.Repeat("z", i)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	n, _ := s.ReadNotes(a.ID)
	if got := strings.Count(n.Assistant.Text, "\n"); got != 13 {
		t.Fatalf("expected 13 lines, got %d:\n%s", got, n.Assistant.Text)
	}
}

func TestNotesRefuseOversizeAndSymlinks(t *testing.T) {
	s := notesStore(t)
	a := mustAssistant(t, s, "a1", "Ada")
	n, _ := s.ReadNotes(a.ID)
	if _, err := s.WriteNotes(a.ID, NotesUser, strings.Repeat("a", MaxNotesBytes+1), n.User.Rev); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversize write: %v", err)
	}
	dir, _ := s.NotesFolder(a.ID)
	// A symlinked file is never followed: it can neither be read nor written through.
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("do not read"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(dir, NotesUserFile)) // NotesFolder made it, empty
	if err := os.Symlink(outside, filepath.Join(dir, NotesUserFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadNotes(a.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a symlinked file must be refused: %v", err)
	}
	if c, err := s.BuildContext(a.ID, ContextRequest{}); err == nil && strings.Contains(c.Text, "do not read") {
		t.Fatal("a context read through a symlink")
	}
	if b, _ := os.ReadFile(outside); string(b) != "do not read" {
		t.Fatalf("the link target was modified: %q", b)
	}
	// A symlinked Assistant folder is refused too.
	_ = os.Remove(filepath.Join(dir, NotesUserFile))
	other := t.TempDir()
	root := filepath.Dir(dir)
	b := mustAssistant(t, s, "a2", "Bob")
	if err := os.Symlink(other, filepath.Join(root, b.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadNotes(b.ID); err == nil {
		t.Fatal("a symlinked memory folder must be refused")
	}
	if entries, _ := os.ReadDir(other); len(entries) != 0 {
		t.Fatalf("something was written through the folder link: %v", entries)
	}
}

func TestLegacyApprovedMemoryIsCarriedOverOnce(t *testing.T) {
	s := notesStore(t)
	a := mustAssistant(t, s, "a1", "Ada")
	for i, row := range []struct{ content, state string }{
		{"Likes short reports", "approved"},
		{"Candidate nobody approved", "candidate"},
		{"Was forgotten", "forgotten"},
		{"Works in UTC+8", "approved"},
	} {
		if _, err := s.db.Exec(`INSERT INTO memory_items(id,assistant_id,scope,kind,content,source_ref,state,shared_with,revision,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`, "m_"+string(rune('a'+i)), a.ID, "assistant", "fact", row.content, "run:r_1", row.state, "[]", 1, i+1, i+1); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.ReadNotes(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n.Assistant.Text != "- Likes short reports\n- Works in UTC+8\n" {
		t.Fatalf("only approved items are carried over, in order: %q", n.Assistant.Text)
	}
	// Once: tidying the file does not bring the old items back.
	if _, err := s.WriteNotes(a.ID, NotesAssistant, "- Works in UTC+8\n", n.Assistant.Rev); err != nil {
		t.Fatal(err)
	}
	if n, _ = s.ReadNotes(a.ID); n.Assistant.Text != "- Works in UTC+8\n" {
		t.Fatalf("seeded again: %q", n.Assistant.Text)
	}
}

func TestDeletingAnAssistantRemovesItsMemoryFolder(t *testing.T) {
	s := notesStore(t)
	anchorPrimary(t, s)
	a := mustAssistant(t, s, "a1", "Ada")
	if _, err := s.AddAssistantNote(a.ID, "a fact"); err != nil {
		t.Fatal(err)
	}
	dir, _ := s.NotesFolder(a.ID)
	got, _, err := s.ArchiveAssistant("arch", a.ID, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteArchivedAssistant(a.ID, got.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("memory folder survived deletion: %v", err)
	}
}

// "Open in your editor" needs a file to open: the folder call makes both,
// empty and private, and never touches one that already holds text.
func TestNotesFolderMakesBothFilesAndKeepsTheirText(t *testing.T) {
	s := notesStore(t)
	a := mustAssistant(t, s, "a1", "Ada")
	dir, err := s.NotesFolder(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{NotesUserFile, NotesAssistantFile} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil || fi.Mode().Perm() != 0o600 || fi.Size() != 0 {
			t.Fatalf("%s: %v %v", name, fi, err)
		}
	}
	n, _ := s.ReadNotes(a.ID)
	if _, err := s.WriteNotes(a.ID, NotesUser, "I work in UTC+8.\n", n.User.Rev); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NotesFolder(a.ID); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, NotesUserFile)); string(b) != "I work in UTC+8.\n" {
		t.Fatalf("a second open rewrote the file: %q", b)
	}
}
