package settings

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestMemoryStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore(Default())
	got, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got.General.Appearance = "dark"
	if err := store.Save(ctx, got); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.General.Appearance != "dark" {
		t.Fatalf("appearance = %q", reloaded.General.Appearance)
	}
}

func TestFileStoreMissingUsesDefaults(t *testing.T) {
	store := NewFileStore(t.TempDir())
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != CurrentSchemaVersion || got.Terminal.FontSize != 13 {
		t.Fatalf("unexpected defaults: %#v", got)
	}
}

func TestFileStoreRoundTripAtomically(t *testing.T) {
	dir := t.TempDir()
	store := NewFileStore(dir)
	value := Default()
	value.Terminal.FontSize = 15
	value.General.RestoreWindow = false

	if err := store.Save(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Terminal.FontSize != 15 || got.General.RestoreWindow {
		t.Fatalf("roundtrip mismatch: %#v", got)
	}
	info, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("settings mode = %o", info.Mode().Perm())
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".settings-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temp files left behind: %v", matches)
	}
}

// TestFileStoreIgnoresTheRetiredHiddenSessionsList pins the migration off
// the old "hide" list: a settings file that still carries it loads fine, the
// list is dropped, and the next save no longer writes it — the sessions it
// named are simply no longer hidden.
func TestFileStoreIgnoresTheRetiredHiddenSessionsList(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"schema_version":` + strconv.Itoa(CurrentSchemaVersion) + `,"session":{"route":"/history","hidden_sessions":["codex:a","pi:b"]}}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewFileStore(dir)
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("a legacy file with hidden_sessions must still load: %v", err)
	}
	if got.Session.Route != "/history" {
		t.Fatalf("the rest of the legacy file was lost: %#v", got.Session)
	}
	if err := store.Save(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hidden_sessions") {
		t.Fatalf("the retired list was written back: %s", data)
	}
}

// TestFileStoreIgnoresTheRetiredFilesPreviewSwitch pins the retirement of
// the "Rich file preview" switch (2026-11-08, Files preview native first): a
// settings file that still carries general.webview_files_preview loads fine,
// the key is dropped, and the next save no longer writes it.
func TestFileStoreIgnoresTheRetiredFilesPreviewSwitch(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"schema_version":` + strconv.Itoa(CurrentSchemaVersion) + `,"general":{"appearance":"dark","webview_files_preview":false}}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewFileStore(dir)
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("a legacy file with webview_files_preview must still load: %v", err)
	}
	if got.General.Appearance != "dark" {
		t.Fatalf("the rest of the legacy file was lost: %#v", got.General)
	}
	if err := store.Save(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "webview_files_preview") {
		t.Fatalf("the retired key was written back: %s", data)
	}
}

func TestTabsInTitleBarByDefault(t *testing.T) {
	var g GeneralSettings
	if !g.TabsInTitleBar() {
		t.Fatal("an absent tabs_placement must mean the title-bar Tab bar")
	}
	g.TabsPlacement = "sidebar"
	if g.TabsInTitleBar() {
		t.Fatal(`"sidebar" must keep the Tabs in the sidebar tree`)
	}
}
