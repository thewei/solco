package nativeui

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveChatAttachmentExtensionAndMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "att")
	now := time.Now()
	path, err := saveChatAttachment(dir, []byte("png"), "image/png", "", now)
	if err != nil || !strings.HasSuffix(path, ".png") {
		t.Fatalf("path=%q err=%v", path, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	if path, _ := saveChatAttachment(dir, []byte("x"), "", "Report.PDF", now); !strings.HasSuffix(path, ".pdf") {
		t.Fatalf("name ext: %q", path)
	}
	if path, _ := saveChatAttachment(dir, []byte("x"), "application/x-weird", "a.b/../c", now); !strings.HasSuffix(path, ".bin") {
		t.Fatalf("fallback ext: %q", path)
	}
	if _, err := saveChatAttachment(dir, nil, "image/png", "", now); err == nil {
		t.Fatal("empty attachment accepted")
	}
}

func TestPruneChatAttachments(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := filepath.Join(dir, "old.png")
	fresh := filepath.Join(dir, "fresh.png")
	for _, p := range []string{old, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(old, now.Add(-15*24*time.Hour), now.Add(-15*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	pruneChatAttachments(dir, now)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("15-day-old file kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh file removed")
	}
}

func TestBridgeSaveAttachmentRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir()) // the cache dir lives under HOME
	t.Setenv("XDG_CACHE_HOME", "")
	bridge := NewChatBridge(NewShell(WithUserDataDir(dir)))
	png := []byte("\x89PNG pretend")
	path, err := bridge.SaveAttachment(context.Background(), base64.StdEncoding.EncodeToString(png), "image/png", "")
	if err != nil {
		t.Fatal(err)
	}
	cache, _ := os.UserCacheDir()
	if filepath.Dir(path) != filepath.Join(cache, filepath.Base(dir), chatAttachDir) {
		t.Fatalf("saved outside the cache ChatAttachments: %s", path)
	}
	if strings.Contains(path, " ") {
		t.Fatalf("an @path mention is split at whitespace: %q", path)
	}
	if got, _ := os.ReadFile(path); string(got) != string(png) {
		t.Fatalf("bytes changed: %q", got)
	}
	if _, err := bridge.SaveAttachment(context.Background(), "@@not base64@@", "image/png", ""); err == nil {
		t.Fatal("malformed base64 accepted")
	}
	catalog, err := bridge.Catalog(context.Background())
	if err != nil || catalog.Commands == nil || catalog.Skills == nil || catalog.Models == nil || catalog.CanSwitchModel {
		t.Fatalf("unbound chat must list nothing: %+v err=%v", catalog, err)
	}
}
