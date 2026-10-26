// Package legacy carries the Shardlane → Solco rebrand (2026-10-26): the
// places the app used to keep its files and the environment variables it used
// to read keep working for the machines that already have them. Nothing here
// is a second store — it only moves what the old name left behind to the new
// name, once, and never overwrites anything.
//
// [INPUT]: the user's home, the MyGo user-data/logs directories, os environment.
// [OUTPUT]: MergeDir, MigrateHome, MigrateAppDir, Env.
// [POS]: called from main before anything reads the new locations; the Hook
// Doctor (internal/agenthooks) handles the provider wiring the old name wrote.
// [PROTOCOL]: delete this package once no machine still has a Shardlane-era
// ~/.shardlane or ~/Library/Application Support/Shardlane.
package legacy

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Name is the brand the app carried before the rebrand.
const (
	oldBrand = "Shardlane"
	newBrand = "Solco"
	oldDot   = ".shardlane"
	newDot   = ".solco"
)

// Env reads SOLCO_<name>, then the pre-rebrand SHARDLANE_<name>.
func Env(name string) string {
	if v := strings.TrimSpace(os.Getenv("SOLCO_" + name)); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("SHARDLANE_" + name))
}

// MergeDir moves what is in from into to: an entry the destination lacks is
// renamed over whole, a directory both have is merged one level down, and a
// file both have stays where it is (the destination wins). It returns how many
// entries were moved; from is removed when nothing is left in it.
func MergeDir(from, to string) (int, error) {
	entries, err := os.ReadDir(from)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	if err := os.MkdirAll(to, 0o755); err != nil {
		return 0, err
	}
	moved := 0
	for _, entry := range entries {
		src, dst := filepath.Join(from, entry.Name()), filepath.Join(to, entry.Name())
		dstInfo, statErr := os.Lstat(dst)
		switch {
		case statErr != nil && errors.Is(statErr, os.ErrNotExist):
			if err := os.Rename(src, dst); err != nil {
				return moved, fmt.Errorf("move %s: %w", src, err)
			}
			moved++
		case statErr != nil:
			return moved, statErr
		case entry.IsDir() && dstInfo.IsDir():
			n, err := MergeDir(src, dst)
			moved += n
			if err != nil {
				return moved, err
			}
		}
	}
	_ = os.Remove(from) // only succeeds when empty
	return moved, nil
}

// MigrateAppDir moves the pre-rebrand sibling of a MyGo app directory (the
// user-data or logs directory; "Solco" ← "Shardlane", "Solco-dev" ←
// "Shardlane-dev") into current.
func MigrateAppDir(current string) {
	base := filepath.Base(current)
	old := strings.Replace(base, newBrand, oldBrand, 1)
	if old == base {
		return
	}
	from := filepath.Join(filepath.Dir(current), old)
	if info, err := os.Lstat(from); err != nil || !info.IsDir() {
		return
	}
	moved, err := MergeDir(from, current)
	if err != nil {
		slog.Warn("rebrand: move app directory failed", "from", from, "to", current, "error", err)
		return
	}
	if moved > 0 {
		slog.Info("rebrand: moved app directory", "from", from, "to", current, "entries", moved)
	}
}

// MigrateHome moves ~/.shardlane into ~/.solco and leaves ~/.shardlane as a
// symlink to it, so the hook scripts and wiring the old name installed (and
// the socket path they were handed) keep resolving until the Hook Doctor
// replaces them.
func MigrateHome(home string) {
	from, to := filepath.Join(home, oldDot), filepath.Join(home, newDot)
	if info, err := os.Lstat(from); err != nil || !info.IsDir() { // missing, or already the symlink
		return
	}
	moved, err := MergeDir(from, to)
	if err != nil {
		slog.Warn("rebrand: move home directory failed", "from", from, "to", to, "error", err)
		return
	}
	if _, err := os.Lstat(from); errors.Is(err, os.ErrNotExist) {
		if err := os.Symlink(newDot, from); err != nil {
			slog.Warn("rebrand: link old home directory failed", "path", from, "error", err)
		}
	}
	if moved > 0 {
		slog.Info("rebrand: moved home directory", "from", from, "to", to, "entries", moved)
	}
}
