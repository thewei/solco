package personalagent

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
)

// withRoutineMutationLock serializes draft/withdraw/keeper operations across
// CLI and daemon processes. Their transactions span YAML + SQLite; this lock
// prevents another Routine command changing the YAML between a receipt check
// and the resulting SQLite write. The lock order is always routine -> config
// (config.Put/ApproveIf have their own .automations.lock).
// A process crash still requires separate recovery of an unfinished draft.
func withRoutineMutationLock(fn func() error) error {
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		return err
	}
	path := filepath.Join(config.Dir(), ".assistant-routines.lock")
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	lock := os.NewFile(uintptr(fd), path)
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return fn()
}
