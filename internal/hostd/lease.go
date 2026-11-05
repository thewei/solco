package hostd

/**
 * [INPUT]: 平台文件锁（lock_unix.go / lock_windows.go：flock 或 LockFileEx）与 RemoteHost 目录里的两个小文件
 * [OUTPUT]: 对外提供 Lease、TryAcquire、Claim（桌面端礼让协议）、RequestYield/YieldRequested/ClearYield
 * [POS]: Host v3 端口的唯一所有者租约（2026-11-03，E05）：桌面 App 与常驻 solco-hostd 共用同一份证书/设备库/令牌，永不同时监听同一端口；
 *        持有者就是 flock 的持有者，进程崩溃由系统释放，不存在陈旧锁；桌面要接管时写 host.yield，常驻服务看到后关闭监听并释放租约
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	lockFile  = "host.lock"
	yieldFile = "host.yield"
	// YieldFresh is how long a yield request keeps the resident service from re-taking the port: long enough for
	// the desktop to claim it, short enough that a desktop which never came leaves the phone a Host again.
	YieldFresh = 15 * time.Second
)

// ErrHeld means another process owns the Host port right now.
var ErrHeld = errors.New("another Solco Host owns the port")

// Lease is the right to listen on the Host port. Release (or process exit) gives it up.
type Lease struct{ f *os.File }

// TryAcquire takes the lease without waiting; ErrHeld when someone else has it.
func TryAcquire(dir string) (*Lease, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockExclusive(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Lease{f: f}, nil
}

// Release gives the port back. Safe to call twice and on nil.
func (l *Lease) Release() {
	if l == nil || l.f == nil {
		return
	}
	unlockFile(l.f)
	_ = l.f.Close()
	l.f = nil
}

// RequestYield asks the current owner (the resident service) to let go.
func RequestYield(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, yieldFile), []byte(fmt.Sprint(time.Now().UnixMilli())), 0o600)
}

// ClearYield withdraws the request once the desktop owns the port.
func ClearYield(dir string) { _ = os.Remove(filepath.Join(dir, yieldFile)) }

// YieldRequested reports a request younger than YieldFresh.
func YieldRequested(dir string, now time.Time) bool {
	info, err := os.Stat(filepath.Join(dir, yieldFile))
	return err == nil && now.Sub(info.ModTime()) < YieldFresh
}

// Claim is the desktop's way in: take the lease at once when nobody has it, otherwise ask the owner to yield and
// poll until it does or wait runs out (then ErrHeld — the desktop reports it instead of listening twice).
func Claim(dir string, wait time.Duration) (*Lease, error) {
	if l, err := TryAcquire(dir); err == nil || !errors.Is(err, ErrHeld) {
		return l, err
	}
	if err := RequestYield(dir); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		l, err := TryAcquire(dir)
		if err == nil {
			ClearYield(dir)
			return l, nil
		}
		if !errors.Is(err, ErrHeld) {
			return nil, err
		}
		if time.Now().After(deadline) {
			ClearYield(dir)
			return nil, ErrHeld
		}
		time.Sleep(50 * time.Millisecond)
	}
}
