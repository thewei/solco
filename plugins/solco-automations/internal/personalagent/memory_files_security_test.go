package personalagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AUD-14: a parent-directory symlink must never redirect private memory
// projection into another Assistant's folder.
func TestMemoryFilesRejectCrossAssistantDirectorySymlink(t *testing.T) {
	f, s := memFiles(t)
	ada := mustAssistant(t, s, "a-ada", "Ada")
	bo := mustAssistant(t, s, "a-bo", "Bo")
	mustSync(t, f, ada.ID)
	mustSync(t, f, bo.ID)
	adaDir, _ := f.Dir(ada.ID)
	boDir, _ := f.Dir(bo.ID)
	if err := os.Remove(filepath.Join(adaDir, "facts")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(boDir, "facts"), filepath.Join(adaDir, "facts")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddMemory("secret-ada", ada.ID, "Ada's private release plan", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Sync(ada.ID); err == nil {
		t.Fatal("must reject a symlinked facts directory")
	}
	mustSync(t, f, bo.ID)
	for _, m := range mustListMemory(t, s, bo.ID) {
		if strings.Contains(m.Content, "private release") {
			t.Fatalf("Ada's memory leaked to Bo: %+v", m)
		}
	}
}

func mustListMemory(t *testing.T, s *Store, id string) []MemoryItem {
	t.Helper()
	items, err := s.ListMemory(id)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// AUD-16: receipt identity for file import includes the owning Assistant.
func TestMemoryFilesSameNewNoteCanBeImportedByTwoAssistants(t *testing.T) {
	f, s := memFiles(t)
	a := mustAssistant(t, s, "a1", "Ada")
	b := mustAssistant(t, s, "b1", "Bo")
	mustSync(t, f, a.ID)
	mustSync(t, f, b.ID)
	for _, id := range []string{a.ID, b.ID} {
		writeMem(t, f, id, "notes/shared.md", "Same note text")
	}
	for _, id := range []string{a.ID, b.ID} {
		rep := mustSync(t, f, id)
		if rep.Imported != 1 {
			t.Fatalf("%s: imported=%d refused=%d", id, rep.Imported, rep.Refused)
		}
		if items := mustListMemory(t, s, id); len(items) != 1 || items[0].State != MemoryCandidate {
			t.Fatalf("%s memory = %+v", id, items)
		}
		if again := mustSync(t, f, id); again.Imported != 0 {
			t.Fatalf("duplicate on retry: %+v", again)
		}
	}
}

// AUD-17: old text is a new edit when its base revision is new (ABA).
func TestMemoryFilesCandidateEditCanReturnToPriorContent(t *testing.T) {
	f, s := memFiles(t)
	a := mustAssistant(t, s, "a1", "Ada")
	m := proposeMem(t, s, "proposal", a.ID, "Apple")
	mustSync(t, f, a.ID)
	rel := "inbox/" + m.ID + ".md"
	writeMem(t, f, a.ID, rel, "Banana")
	if got := mustSync(t, f, a.ID); got.Edited != 1 {
		t.Fatalf("first edit %+v", got)
	}
	banana, _ := s.GetMemory(m.ID)
	if _, _, err := s.CorrectMemory("from-phone", m.ID, "Cherry", banana.Revision); err != nil {
		t.Fatal(err)
	}
	mustSync(t, f, a.ID)
	writeMem(t, f, a.ID, rel, "Banana")
	if got := mustSync(t, f, a.ID); got.Edited != 1 {
		t.Fatalf("ABA edit %+v", got)
	}
	final, _ := s.GetMemory(m.ID)
	if final.Content != "Banana" {
		t.Fatalf("old text cannot be used again: %+v", final)
	}
}

// AUD-15: a file writer cannot impersonate an Owner just by changing Markdown.
// Real Owner edits go through CorrectMemory (Host ScopeManage or trusted UI).
func TestMemoryFilesApprovedEditsRequireExplicitApproval(t *testing.T) {
	f, s := memFiles(t)
	a := mustAssistant(t, s, "a1", "Ada")
	m, _, err := s.AddMemory("original", a.ID, "Verified owner fact", "")
	if err != nil {
		t.Fatal(err)
	}
	mustSync(t, f, a.ID)
	writeMem(t, f, a.ID, "facts/"+m.ID+".md", "Untrusted file mutation")
	rep := mustSync(t, f, a.ID)
	current, err := s.GetMemory(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != MemoryApproved || current.Content != "Verified owner fact" || current.Revision != m.Revision {
		t.Fatalf("unauthenticated edit overwrote approved fact: %+v", current)
	}
	candidates := mustListMemory(t, s, a.ID)
	if rep.Proposed != 1 || len(candidates) != 2 ||
		candidates[1].State != MemoryCandidate || candidates[1].Content != "Untrusted file mutation" {
		t.Fatalf("file edit not retained for review: %+v, %+v", rep, candidates)
	}
	if !strings.Contains(readMem(t, f, a.ID, "facts/"+m.ID+".md"), "Verified owner fact") {
		t.Fatal("approved fact projection should remain unchanged")
	}
}
