package projectlibrary

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestAddListRemoveRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	item, err := s.Add("solco", "PRD notes.md", []byte("# PRD\nship it"))
	if err != nil {
		t.Fatal(err)
	}
	if item.Mode != Reference || item.Kind != "text" {
		t.Fatalf("a new item is Reference text: %+v", item)
	}
	path, _ := s.Path("solco", item)
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("stored 0600: %v %v", st, err)
	}
	items, _ := s.List("solco")
	if len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("list = %+v", items)
	}
	if err := s.Remove("solco", item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("remove deletes the copy")
	}
	if items, _ := s.List("solco"); len(items) != 0 {
		t.Fatalf("list after remove = %+v", items)
	}
	if err := s.Remove("solco", "nope"); err != ErrNoItem {
		t.Fatalf("remove unknown = %v", err)
	}
}

func TestRefusesCredentialsEmptyAndHuge(t *testing.T) {
	s := New(t.TempDir())
	for name, data := range map[string]string{
		".env":          "A=1",
		"id_rsa":        "x",
		"deploy.pem":    "x",
		"notes.txt":     "-----BEGIN OPENSSH PRIVATE KEY-----\nabc",
		"aws.txt":       "key AKIAABCDEFGHIJKLMNOP",
		"my-secret.txt": "hello",
	} {
		if _, err := s.Add("p", name, []byte(data)); err != ErrCredential {
			t.Errorf("%s: err = %v, want ErrCredential", name, err)
		}
	}
	if _, err := s.Add("p", "a.txt", nil); err != ErrEmpty {
		t.Errorf("empty = %v", err)
	}
	if _, err := s.Add("p", "big.bin", make([]byte, MaxFileBytes+1)); err != ErrTooLarge {
		t.Errorf("huge = %v", err)
	}
	if items, _ := s.List("p"); len(items) != 0 {
		t.Fatalf("refused files must not land: %+v", items)
	}
}

func TestProjectNameCannotLeaveTheRoot(t *testing.T) {
	root := t.TempDir()
	s := New(root)
	for _, bad := range []string{"", "  ", "..", "../x", "/etc"} {
		if _, err := s.Add(bad, "a.txt", []byte("x")); bad == "" || bad == "  " || bad == ".." {
			if err != ErrBadProject {
				t.Errorf("%q: err = %v", bad, err)
			}
		}
	}
	item, err := s.Add("../evil", "a.txt", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.Path("../evil", item)
	if !strings.HasPrefix(p, root+string(os.PathSeparator)) {
		t.Fatalf("path %q escaped %q", p, root)
	}
}

func TestAlwaysIsShortTextOnly(t *testing.T) {
	s := New(t.TempDir())
	short, _ := s.Add("p", "rules.md", []byte("be kind"))
	long, _ := s.Add("p", "long.md", []byte(strings.Repeat("x", MaxAlwaysBytes+1)))
	img, _ := s.Add("p", "shot.png", []byte{0x89, 'P', 'N', 'G', 0, 1, 2})
	if err := s.SetMode("p", short.ID, Always); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMode("p", long.ID, Always); err != ErrAlwaysSize {
		t.Fatalf("long = %v", err)
	}
	if err := s.SetMode("p", img.ID, Always); err != ErrAlwaysSize {
		t.Fatalf("image = %v", err)
	}
	if err := s.SetMode("p", short.ID, Mode("bogus")); err == nil {
		t.Fatal("unknown mode must fail")
	}
}

func TestBlockNamesAlwaysByPathAndIndexTheRest(t *testing.T) {
	s := New(t.TempDir())
	if b, _ := s.Block("p"); b != "" {
		t.Fatalf("empty shelf, no block: %q", b)
	}
	rules, _ := s.Add("p", "house rules.md", []byte("rules"))
	ref, _ := s.Add("p", "api.md", []byte("api"))
	task, _ := s.Add("p", "one-off.md", []byte("only for a task"))
	s.SetMode("p", rules.ID, Always)
	s.SetMode("p", task.ID, Task)
	block, _ := s.Block("p")
	rp, _ := s.Path("p", rules)
	idx, _ := s.IndexPath("p")
	for _, want := range []string{BlockBegin, BlockEnd, "Read these first", "@" + mention(rp), "@" + mention(idx)} {
		if !strings.Contains(block, want) {
			t.Errorf("block missing %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "only for a task") || strings.Contains(block, "rules\n") {
		t.Errorf("a block never carries file contents:\n%s", block)
	}
	raw, _ := os.ReadFile(idx)
	text := string(raw)
	ap, _ := s.Path("p", ref)
	tp, _ := s.Path("p", task)
	if !strings.Contains(text, "## Read first") || !strings.Contains(text, "## Open when needed") || !strings.Contains(text, ap) {
		t.Errorf("index:\n%s", text)
	}
	if strings.Contains(text, tp) {
		t.Errorf("Task items are not indexed:\n%s", text)
	}
}

func TestSpliceKeepsTheUsersWordsAndIsIdempotent(t *testing.T) {
	block := BlockBegin + "\nA\n" + BlockEnd
	mine := "Use tabs.\n\nPrefer small commits."
	once := Splice(mine, block)
	if !strings.HasPrefix(once, block) || !strings.HasSuffix(once, mine) {
		t.Fatalf("added before the user's words: %q", once)
	}
	if twice := Splice(once, block); twice != once {
		t.Fatalf("splice twice changed text:\n%q\n%q", once, twice)
	}
	newer := BlockBegin + "\nB\n" + BlockEnd
	if got := Splice(once, newer); !strings.Contains(got, "B") || strings.Contains(got, "\nA\n") || !strings.HasSuffix(got, mine) {
		t.Fatalf("replace in place: %q", got)
	}
	if got := Splice(once, ""); got != mine {
		t.Fatalf("empty block removes it and nothing else: %q", got)
	}
	if got := Splice("", block); got != block {
		t.Fatalf("empty text: %q", got)
	}
	// Words after the block survive a replacement.
	mixed := block + "\n\nLater words."
	if got := Splice(mixed, newer); !strings.HasSuffix(got, "Later words.") || !strings.HasPrefix(got, newer) {
		t.Fatalf("trailing text: %q", got)
	}
}

func TestTaskRefsListsChosenItems(t *testing.T) {
	s := New(t.TempDir())
	a, _ := s.Add("p", "a b.png", []byte{0x89, 'P', 'N', 'G', 0})
	b, _ := s.Add("p", "b.md", []byte("b"))
	got, _ := s.TaskRefs("p", []string{a.ID})
	ap, _ := s.Path("p", a)
	bp, _ := s.Path("p", b)
	if !strings.Contains(got, "@"+mention(ap)) || strings.Contains(got, bp) {
		t.Fatalf("refs = %q", got)
	}
	if none, _ := s.TaskRefs("p", nil); none != "" {
		t.Fatalf("no ids, no text: %q", none)
	}
}

func TestAddFileCopies(t *testing.T) {
	src := t.TempDir() + "/spec.md"
	os.WriteFile(src, []byte("spec"), 0o644)
	s := New(t.TempDir())
	item, err := s.AddFile("p", src)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(src)
	p, _ := s.Path("p", item)
	if raw, err := os.ReadFile(p); err != nil || string(raw) != "spec" {
		t.Fatalf("the shelf holds a copy: %q %v", raw, err)
	}
	if _, err := s.AddFile("p", t.TempDir()); err == nil {
		t.Fatal("a directory is not a file")
	}
}

func TestMentionQuotesWhitespacePaths(t *testing.T) {
	if got := mention("/Users/a/Library/Application Support/x.md"); got != `"/Users/a/Library/Application Support/x.md"` {
		t.Fatalf("mention = %q", got)
	}
	if got := mention("/tmp/x.md"); got != "/tmp/x.md" {
		t.Fatalf("mention = %q", got)
	}
}

func TestSanitisedProjectNamesDoNotCollide(t *testing.T) {
	s := New(t.TempDir())
	a, _ := s.projectDir("a b")
	b, _ := s.projectDir("a_b")
	if a == b {
		t.Fatalf("\"a b\" and \"a_b\" share %s", a)
	}
	if clean, _ := s.projectDir("plain-name"); filepath.Base(clean) != "plain-name" {
		t.Fatalf("a clean name keeps its folder: %s", clean)
	}
}

func TestAddFileJudgesASymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, ".env")
	os.WriteFile(secret, []byte("TOKEN=1"), 0o600)
	link := filepath.Join(dir, "notes.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skip(err)
	}
	if _, err := New(t.TempDir()).AddFile("p", link); !errors.Is(err, ErrCredential) {
		t.Fatalf("a link to a credential file = %v, want ErrCredential", err)
	}
}

func TestConcurrentAddsKeepEveryItem(t *testing.T) {
	s := New(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.Add("p", fmt.Sprintf("f%d.md", i), []byte("x")); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if items, _ := s.List("p"); len(items) != 20 {
		t.Fatalf("items = %d, want 20", len(items))
	}
}
