package personalagent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
)

// L03: one consistent copy of everything the Assistants own. The database is
// copied with SQLite's own VACUUM INTO (a transactionally consistent snapshot
// taken while the daemon keeps running — memory, approvals, receipts, runs and
// event watermarks are all rows in it), and automations.yaml, where the
// Routines' definitions live, is copied beside it. Restoring is putting both
// files back while the daemon is stopped; a newer binary migrates the file
// forward on its first open. A backup never overwrites an earlier one.
func (s *Store) Backup(dir string) error {
	if dir == "" {
		return ErrInvalid
	}
	if ents, err := os.ReadDir(dir); err == nil && len(ents) > 0 {
		return fmt.Errorf("%w: %s already holds a backup", ErrInvalid, dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dst := filepath.Join(dir, "assistants.sqlite")
	if _, err := s.db.Exec(`VACUUM INTO ?`, dst); err != nil {
		return wrapBusy(err)
	}
	if err := os.Chmod(dst, 0o600); err != nil {
		return err
	}
	if err := copyPrivate(config.Path(), filepath.Join(dir, "automations.yaml")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func copyPrivate(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
