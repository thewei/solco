package nativeui

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thewei/solco/internal/chatattach"
)

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
	if filepath.Dir(path) != filepath.Join(cache, filepath.Base(dir), chatattach.DirName) {
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
