//go:build !windows

package feedback

import (
	"errors"
	"os"
	"syscall"
)

// lockExclusive takes an exclusive non-blocking whole-file lock; ErrHeld when another process holds it.
func lockExclusive(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrHeld
		}
		return err
	}
	return nil
}

func unlockFile(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
