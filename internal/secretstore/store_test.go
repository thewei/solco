package secretstore

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type fakeLegacy struct {
	mu      sync.Mutex
	items   map[string]string
	gets    map[string]int
	deleted []string
}

func newFakeLegacy(items map[string]string) *fakeLegacy {
	return &fakeLegacy{items: items, gets: map[string]int{}}
}

func (f *fakeLegacy) Get(key string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets[key]++
	v, ok := f.items[key]
	return v, ok
}

func (f *fakeLegacy) Delete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, key)
	delete(f.items, key)
	return nil
}

func open(t *testing.T, path string, legacy Legacy, hw string) *Store {
	t.Helper()
	s := New(legacy)
	s.hwid = func() string { return hw }
	s.Use(path)
	return s
}

func TestRoundTripSurvivesARestartAndIsNotPlaintext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "secrets.enc")
	s := open(t, path, nil, "HW-1")
	if err := s.Set("usage.zhipu.token", "sk-very-secret"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("sk-very-secret")) {
		t.Fatal("the file holds the credential in plaintext")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, want 0600", info.Mode().Perm())
	}
	again := open(t, path, nil, "HW-1")
	if got, ok := again.Get("usage.zhipu.token"); !ok || got != "sk-very-secret" {
		t.Fatalf("after restart = %q, %v", got, ok)
	}
	if err := again.Delete("usage.zhipu.token"); err != nil {
		t.Fatal(err)
	}
	if _, ok := open(t, path, nil, "HW-1").Get("usage.zhipu.token"); ok {
		t.Fatal("a deleted credential came back")
	}
}

func TestAnotherMachineCannotReadTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.enc")
	if err := open(t, path, nil, "HW-1").Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	if _, ok := open(t, path, nil, "HW-2").Get("k"); ok {
		t.Fatal("a different hardware id decrypted the item")
	}
}

func TestItemsAreBoundToTheirKeyName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.enc")
	s := open(t, path, nil, "HW")
	s.Set("a", "one")
	s.Set("b", "two")
	raw, _ := os.ReadFile(path)
	var doc document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc.Items["a"], doc.Items["b"] = doc.Items["b"], doc.Items["a"]
	raw, _ = json.Marshal(doc)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	again := open(t, path, nil, "HW")
	if _, ok := again.Get("a"); ok {
		t.Fatal("a sealed value opened under another key's name")
	}
	if _, ok := again.Get("b"); ok {
		t.Fatal("a sealed value opened under another key's name")
	}
}

func TestCorruptFileIsAnEmptyStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.enc")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := open(t, path, nil, "HW")
	if _, ok := s.Get("k"); ok {
		t.Fatal("garbage produced a credential")
	}
	if err := s.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, ok := open(t, path, nil, "HW").Get("k"); !ok || v != "v" {
		t.Fatalf("the store did not recover: %q %v", v, ok)
	}
}

func TestOldKeychainItemMovesOverOnceAndIsRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.enc")
	old := newFakeLegacy(map[string]string{"git.commitapi.token": "sk-old"})
	s := open(t, path, old, "HW")
	for i := 0; i < 3; i++ {
		if v, ok := s.Get("git.commitapi.token"); !ok || v != "sk-old" {
			t.Fatalf("read %d = %q %v", i, v, ok)
		}
	}
	if old.gets["git.commitapi.token"] != 1 {
		t.Fatalf("the Keychain was asked %d times, want 1", old.gets["git.commitapi.token"])
	}
	if len(old.deleted) != 1 {
		t.Fatalf("the old item was deleted %d times, want 1", len(old.deleted))
	}
	// A restart reads the file and never goes back to the Keychain.
	old.items["git.commitapi.token"] = "resurrected"
	again := open(t, path, old, "HW")
	if v, _ := again.Get("git.commitapi.token"); v != "sk-old" {
		t.Fatalf("after restart = %q", v)
	}
	if old.gets["git.commitapi.token"] != 1 {
		t.Fatal("a restart asked the Keychain again")
	}
}

func TestAMissIsRememberedAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.enc")
	old := newFakeLegacy(map[string]string{})
	s := open(t, path, old, "HW")
	for i := 0; i < 3; i++ {
		if _, ok := s.Get("usage.cursor.signin"); ok {
			t.Fatal("found a credential nobody stored")
		}
	}
	open(t, path, old, "HW").Get("usage.cursor.signin")
	if old.gets["usage.cursor.signin"] != 1 {
		t.Fatalf("the Keychain was asked %d times for a missing key, want 1", old.gets["usage.cursor.signin"])
	}
}

func TestDeleteDoesNotLetTheOldItemBackIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.enc")
	old := newFakeLegacy(map[string]string{"k": "old"})
	s := open(t, path, old, "HW")
	if err := s.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("k"); ok {
		t.Fatal("a deleted credential came back from the Keychain")
	}
	if old.gets["k"] != 0 {
		t.Fatal("the Keychain was read for a key the user just removed")
	}
	if _, still := old.items["k"]; still {
		t.Fatal("the old Keychain item was left behind")
	}
}

func TestSetSupersedesTheOldItem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.enc")
	old := newFakeLegacy(map[string]string{"k": "old"})
	s := open(t, path, old, "HW")
	if err := s.Set("k", "new"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Get("k"); v != "new" {
		t.Fatalf("k = %q, want new", v)
	}
	if old.gets["k"] != 0 || len(old.items) != 0 {
		t.Fatalf("legacy gets=%d items=%v", old.gets["k"], old.items)
	}
}

func TestMemoryOnlyStoreNeverTouchesTheKeychainOrDisk(t *testing.T) {
	old := newFakeLegacy(map[string]string{"k": "old"})
	s := New(old)
	if _, ok := s.Get("k"); ok {
		t.Fatal("a memory-only store read the Keychain")
	}
	if err := s.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Get("k"); v != "v" {
		t.Fatalf("k = %q", v)
	}
	if old.gets["k"] != 0 || len(old.deleted) != 0 {
		t.Fatal("a memory-only store called the Keychain")
	}
}

func TestHardwareIDComesFromIoreg(t *testing.T) {
	if id := hardwareID(); len(id) < 30 {
		t.Fatalf("hardware id = %q, want the IOPlatformUUID", id)
	}
}
