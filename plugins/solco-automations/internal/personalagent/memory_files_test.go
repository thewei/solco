package personalagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func memFiles(t *testing.T, opts ...Option) (*MemoryFiles, *Store) {
	t.Helper()
	s, _ := openTest(t, opts...)
	return &MemoryFiles{Store: s, Root: t.TempDir()}, s
}

func mustSync(t *testing.T, f *MemoryFiles, id string) SyncReport {
	t.Helper()
	r, err := f.Sync(id)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	return r
}

func readMem(t *testing.T, f *MemoryFiles, id, rel string) string {
	t.Helper()
	d, _ := f.Dir(id)
	b, err := os.ReadFile(filepath.Join(d, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func writeMem(t *testing.T, f *MemoryFiles, id, rel, text string) {
	t.Helper()
	d, _ := f.Dir(id)
	if err := os.WriteFile(filepath.Join(d, rel), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// U5-1: facts and candidates are projected as Markdown with private modes, an
// untouched folder is stable, and forgetting removes the file.
func TestMemoryFolderProjection(t *testing.T) {
	f, s := memFiles(t)
	a := mustAssistant(t, s, "a1", "Ada")
	fact, _, err := s.AddMemory("m1", a.ID, "Prefers short answers", "")
	if err != nil {
		t.Fatal(err)
	}
	cand := proposeMem(t, s, "m2", a.ID, "Works from Shanghai")
	r := mustSync(t, f, a.ID)
	if r.Written != 2 {
		t.Fatalf("written = %d", r.Written)
	}
	if got := readMem(t, f, a.ID, "facts/"+fact.ID+".md"); !strings.Contains(got, "state: approved") || !strings.Contains(got, "Prefers short answers") {
		t.Fatalf("fact file: %q", got)
	}
	if got := readMem(t, f, a.ID, "inbox/"+cand.ID+".md"); !strings.Contains(got, "state: candidate") {
		t.Fatalf("candidate file: %q", got)
	}
	d, _ := f.Dir(a.ID)
	for rel, want := range map[string]os.FileMode{"": 0o700, "facts": 0o700, "facts/" + fact.ID + ".md": 0o600} {
		fi, err := os.Stat(filepath.Join(d, rel))
		if err != nil || fi.Mode().Perm() != want {
			t.Fatalf("%q mode = %v err=%v, want %v", rel, fi.Mode().Perm(), err, want)
		}
	}
	if r := mustSync(t, f, a.ID); r != (SyncReport{}) {
		t.Fatalf("second sync should change nothing: %+v", r)
	}
	// Approving moves the file from inbox/ to facts/.
	if _, _, err := s.ApproveMemory("m2-ok", cand.ID, cand.Revision); err != nil {
		t.Fatal(err)
	}
	mustSync(t, f, a.ID)
	if _, err := os.Stat(filepath.Join(d, "inbox", cand.ID+".md")); !os.IsNotExist(err) {
		t.Fatal("inbox file should be gone after approval")
	}
	readMem(t, f, a.ID, "facts/"+cand.ID+".md")
	// Forgetting removes the file and every version's content.
	cur, _ := s.GetMemory(fact.ID)
	if _, _, err := s.ForgetMemory("m1-f", fact.ID, cur.Revision); err != nil {
		t.Fatal(err)
	}
	mustSync(t, f, a.ID)
	if _, err := os.Stat(filepath.Join(d, "facts", fact.ID+".md")); !os.IsNotExist(err) {
		t.Fatal("forgotten file should be gone")
	}
	hist, err := s.MemoryHistory(fact.ID)
	if err != nil || len(hist) != 2 {
		t.Fatalf("history = %+v err=%v", hist, err)
	}
	for _, v := range hist {
		if v.Content != "" {
			t.Fatalf("forgotten item kept content in version %d", v.Revision)
		}
	}
	if !strings.Contains(readMem(t, f, a.ID, "INDEX.md"), cand.ID) || strings.Contains(readMem(t, f, a.ID, "INDEX.md"), "Prefers short answers") {
		t.Fatal("INDEX should list live items only")
	}
}

// U5-1: modifying an approved fact through the filesystem is untrusted:
// it creates a correction candidate instead of silently rewriting an approved
// fact. The authoritative fact and its version history remain untouched.
func TestMemoryFileEditOfApprovedFactNeedsReview(t *testing.T) {
	f, s := memFiles(t)
	a := mustAssistant(t, s, "a1", "Ada")
	m, _, _ := s.AddMemory("m1", a.ID, "Prefers short answers", "")
	mustSync(t, f, a.ID)
	writeMem(t, f, a.ID, "facts/"+m.ID+".md", "---\nid: junk\n---\nPrefers detailed answers\n")
	r := mustSync(t, f, a.ID)
	if r.Proposed != 1 || r.Edited != 0 {
		t.Fatalf("report %+v", r)
	}
	got, _ := s.GetMemory(m.ID)
	if got.Content != "Prefers short answers" || got.Revision != 1 || got.State != MemoryApproved {
		t.Fatalf("approved fact changed without Owner approval: %+v", got)
	}
	hist, _ := s.MemoryHistory(m.ID)
	if len(hist) != 1 || hist[0].Author != MemByUser || hist[0].Content != "Prefers short answers" {
		t.Fatalf("history = %+v", hist)
	}
	list, _ := s.ListMemory(a.ID)
	if len(list) != 2 || list[1].State != MemoryCandidate || list[1].Kind != "correction" ||
		list[1].Content != "Prefers detailed answers" || list[1].SourceRef != "edit:"+m.ID+"@1" {
		t.Fatalf("file correction was not held for review: %+v", list)
	}
	if !strings.Contains(readMem(t, f, a.ID, "facts/"+m.ID+".md"), "id: "+m.ID) {
		t.Fatal("front matter not restored")
	}
	if r := mustSync(t, f, a.ID); r != (SyncReport{}) {
		t.Fatalf("settled folder changed again: %+v", r)
	}
}

// U5-1: an edit made on an outdated file never overwrites the newer text: both
// survive, the newer one as the item and the file's as a conflict candidate.
func TestMemoryFileConflictKeepsBoth(t *testing.T) {
	f, s := memFiles(t)
	a := mustAssistant(t, s, "a1", "Ada")
	m, _, _ := s.AddMemory("m1", a.ID, "Meets on Mondays", "")
	mustSync(t, f, a.ID)
	// The phone changes it in the core; the person edits the stale file before the next sync.
	if _, _, err := s.CorrectMemory("m1-c", m.ID, "Meets on Tuesdays", m.Revision); err != nil {
		t.Fatal(err)
	}
	writeMem(t, f, a.ID, "facts/"+m.ID+".md", "---\nid: "+m.ID+"\n---\nMeets on Wednesdays\n")
	r := mustSync(t, f, a.ID)
	if r.Conflicts != 1 || r.Edited != 0 {
		t.Fatalf("report %+v", r)
	}
	got, _ := s.GetMemory(m.ID)
	if got.Content != "Meets on Tuesdays" {
		t.Fatalf("the newer text was overwritten: %q", got.Content)
	}
	if !strings.Contains(readMem(t, f, a.ID, "facts/"+m.ID+".md"), "Meets on Tuesdays") {
		t.Fatal("file should show the core's text again")
	}
	list, _ := s.ListMemory(a.ID)
	var found bool
	for _, it := range list {
		if it.Kind == "conflict" && it.State == MemoryCandidate && it.Content == "Meets on Wednesdays" && strings.HasPrefix(it.SourceRef, "conflict:"+m.ID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no conflict candidate in %+v", list)
	}
	rev, _ := s.ReviewMemory(a.ID)
	if len(rev.Conflicts) != 1 {
		t.Fatalf("review should list the conflict: %+v", rev)
	}
	// A second sync neither repeats the conflict nor loses it.
	mustSync(t, f, a.ID)
	if again, _ := s.ReviewMemory(a.ID); len(again.Conflicts) != 1 {
		t.Fatalf("conflict duplicated: %+v", again.Conflicts)
	}
}

// U5-1: what a file may not say is refused and the file is restored from the core.
func TestMemoryFileRefusals(t *testing.T) {
	f, s := memFiles(t)
	a := mustAssistant(t, s, "a1", "Ada")
	m, _, _ := s.AddMemory("m1", a.ID, "Likes tea", "")
	mustSync(t, f, a.ID)
	rel := "facts/" + m.ID + ".md"
	for name, text := range map[string]string{
		"credential": "---\nid: x\n---\napi_key = sk-abcdefghijklmnopqrstuvwxyz123456\n",
		"empty":      "---\nid: x\n---\n",
		"too big":    strings.Repeat("x", memoryFileMax+10),
	} {
		writeMem(t, f, a.ID, rel, text)
		r := mustSync(t, f, a.ID)
		if r.Refused != 1 || r.Edited != 0 {
			t.Fatalf("%s: %+v", name, r)
		}
		if got, _ := s.GetMemory(m.ID); got.Content != "Likes tea" || got.Revision != 1 {
			t.Fatalf("%s: item changed to %+v", name, got)
		}
		if !strings.Contains(readMem(t, f, a.ID, rel), "Likes tea") {
			t.Fatalf("%s: file not restored", name)
		}
	}
	// Deleting a file is not forgetting.
	d, _ := f.Dir(a.ID)
	_ = os.Remove(filepath.Join(d, rel))
	mustSync(t, f, a.ID)
	readMem(t, f, a.ID, rel)
	if got, _ := s.GetMemory(m.ID); got.State != MemoryApproved {
		t.Fatalf("state = %s", got.State)
	}
}

// U5-1: a new file is a candidate (never a fact), a symlink is never read, and
// another Assistant's folder never contains this one's items.
func TestMemoryFileImportAndIsolation(t *testing.T) {
	f, s := memFiles(t)
	a := mustAssistant(t, s, "a1", "Ada")
	b := mustAssistant(t, s, "b1", "Bo")
	s.AddMemory("ma", a.ID, "Ada only", "")
	s.AddMemory("mb", b.ID, "Bo only", "")
	mustSync(t, f, a.ID)
	mustSync(t, f, b.ID)
	writeMem(t, f, a.ID, "notes/ideas.md", "# Ideas\nWrite a book\n")
	outside := filepath.Join(t.TempDir(), "secret.md")
	_ = os.WriteFile(outside, []byte("outside the folder"), 0o600)
	d, _ := f.Dir(a.ID)
	_ = os.Symlink(outside, filepath.Join(d, "notes", "link.md"))
	r := mustSync(t, f, a.ID)
	if r.Imported != 1 || r.Refused != 1 {
		t.Fatalf("report %+v", r)
	}
	list, _ := s.ListMemory(a.ID)
	var cand MemoryItem
	for _, it := range list {
		if it.SourceRef == "file:notes/ideas.md" {
			cand = it
		}
	}
	if cand.ID == "" || cand.State != MemoryCandidate || cand.Kind != "note" || !strings.Contains(cand.Content, "Write a book") {
		t.Fatalf("imported = %+v", cand)
	}
	if _, err := os.Stat(filepath.Join(d, "notes", "ideas.md")); !os.IsNotExist(err) {
		t.Fatal("the dropped file should have moved to inbox/")
	}
	readMem(t, f, a.ID, "inbox/"+cand.ID+".md")
	if vis, _ := s.VisibleMemory(a.ID); len(vis) != 1 {
		t.Fatalf("a candidate must not be visible context: %+v", vis)
	}
	// B's folder knows nothing of A.
	bd, _ := f.Dir(b.ID)
	entries, _ := filepath.Glob(filepath.Join(bd, "*", "*.md"))
	for _, e := range entries {
		if raw, _ := os.ReadFile(e); strings.Contains(string(raw), "Ada") {
			t.Fatalf("%s leaks Ada's memory", e)
		}
	}
	if _, err := f.Dir("../escape"); err == nil {
		t.Fatal("a path-like assistant id must be refused")
	}
}

// U5-1: review lists duplicates and candidates nobody answered, and changes nothing.
func TestMemoryReview(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	clock := now
	s, _ := openTest(t, WithClock(func() time.Time { return clock }))
	a := mustAssistant(t, s, "a1", "Ada")
	s.AddMemory("m1", a.ID, "Uses  Vim", "")
	s.AddMemory("m2", a.ID, "uses vim", "")
	old := proposeMem(t, s, "m3", a.ID, "Old guess")
	clock = now.Add(15 * 24 * time.Hour)
	fresh := proposeMem(t, s, "m4", a.ID, "New guess")
	r, err := s.ReviewMemory(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Duplicates) != 1 || len(r.Duplicates[0]) != 2 {
		t.Fatalf("duplicates = %+v", r.Duplicates)
	}
	if len(r.StaleCandidates) != 1 || r.StaleCandidates[0] != old.ID || r.Candidates != 2 || r.Approved != 2 {
		t.Fatalf("review = %+v (fresh %s)", r, fresh.ID)
	}
}

// U5-1: the bounds hold for every writer.
func TestMemoryBounds(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	if _, _, err := s.AddMemory("m1", a.ID, strings.Repeat("x", MaxMemoryText+1), ""); err == nil {
		t.Fatal("an oversized item must be refused")
	}
	if _, _, err := s.AddMemory("m2", a.ID, "password: hunter2 token=ghp_abcdefghijklmnopqrstuvwxyz0123456789", ""); err == nil {
		t.Fatal("a credential must be refused")
	}
	m, _, err := s.AddMemory("m3", a.ID, "fine", "")
	if err != nil || m.State != MemoryApproved || m.SourceRef != "user" {
		t.Fatalf("add = %+v err=%v", m, err)
	}
	if _, rep, _ := s.AddMemory("m3", a.ID, "fine", ""); !rep {
		t.Fatal("same request id must replay")
	}
}
