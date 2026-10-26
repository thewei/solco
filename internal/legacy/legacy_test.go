package legacy

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMigrateAppDirMovesTheOldSiblingAndKeepsWhatIsAlreadyThere(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "Shardlane", "settings.json"), "old settings")
	write(t, filepath.Join(root, "Shardlane", "SessionTrash", "a", "m.json"), "trashed")
	write(t, filepath.Join(root, "Solco", "lock"), "fresh") // what MyGo wrote on first start
	write(t, filepath.Join(root, "Solco", "settings.json"), "new settings")

	MigrateAppDir(filepath.Join(root, "Solco"))

	if got := read(t, filepath.Join(root, "Solco", "SessionTrash", "a", "m.json")); got != "trashed" {
		t.Fatalf("trash = %q", got)
	}
	if got := read(t, filepath.Join(root, "Solco", "settings.json")); got != "new settings" {
		t.Fatalf("an existing file must not be overwritten, got %q", got)
	}
	if got := read(t, filepath.Join(root, "Solco", "lock")); got != "fresh" {
		t.Fatalf("lock = %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "Shardlane", "settings.json")); err != nil {
		t.Fatal("the conflicting old file stays for the user to look at")
	}
}

func TestMigrateAppDirTakesTheDevSiblingForTheDevName(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "Shardlane-dev", "settings.json"), "dev")
	MigrateAppDir(filepath.Join(root, "Solco-dev"))
	if got := read(t, filepath.Join(root, "Solco-dev", "settings.json")); got != "dev" {
		t.Fatalf("settings = %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "Shardlane-dev")); !os.IsNotExist(err) {
		t.Fatal("an emptied old directory is removed")
	}
}

func TestMigrateAppDirIsANoopWithoutAnOldDirectory(t *testing.T) {
	root := t.TempDir()
	MigrateAppDir(filepath.Join(root, "Solco")) // must not create or fail
	if _, err := os.Stat(filepath.Join(root, "Solco")); !os.IsNotExist(err) {
		t.Fatal("nothing to migrate must create nothing")
	}
	MigrateAppDir(filepath.Join(root, "Other")) // a name without the brand is left alone
}

func TestMigrateHomeMovesTheDirectoryAndLeavesASymlinkForOldWiring(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".shardlane", "hooks", "shardlane-claude-hook.sh"), "old hook")
	write(t, filepath.Join(home, ".shardlane", "config.json"), "cfg")
	write(t, filepath.Join(home, ".solco", "hooks", "events.jsonl"), "spool") // the plugin forwarder got there first

	MigrateHome(home)

	for path, want := range map[string]string{
		filepath.Join(home, ".solco", "hooks", "shardlane-claude-hook.sh"):     "old hook",
		filepath.Join(home, ".solco", "hooks", "events.jsonl"):                 "spool",
		filepath.Join(home, ".solco", "config.json"):                           "cfg",
		filepath.Join(home, ".shardlane", "hooks", "shardlane-claude-hook.sh"): "old hook", // through the link
	} {
		if got := read(t, path); got != want {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}
	if target, err := os.Readlink(filepath.Join(home, ".shardlane")); err != nil || target != ".solco" {
		t.Fatalf("link = %q, %v", target, err)
	}
	MigrateHome(home) // idempotent
}

func TestEnvPrefersTheNewNameThenTheOldOne(t *testing.T) {
	t.Setenv("SHARDLANE_LOG_LEVEL", "debug")
	if got := Env("LOG_LEVEL"); got != "debug" {
		t.Fatalf("old name = %q", got)
	}
	t.Setenv("SOLCO_LOG_LEVEL", "warn")
	if got := Env("LOG_LEVEL"); got != "warn" {
		t.Fatalf("new name must win, got %q", got)
	}
}
