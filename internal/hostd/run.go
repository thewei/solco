package hostd

/**
 * [INPUT]: 依赖 lease.go 的租约、config.go 的设置映射、remotehost.Server 的 Reconfigure/Close/SetP2P
 * [OUTPUT]: 对外提供 Host（最小接口）、Options、Run（常驻服务主循环）
 * [POS]: 常驻 Go Host 的状态机（2026-11-03，E05）：设置为 off → 不监听；有租约 → 按设置监听；桌面写了礼让请求 → 关监听、放租约、等桌面拿走；
 *        桌面退出（租约被系统释放）→ 一个 Poll 内重新接管。同一时刻只有一个所有者；手机看到的证书指纹/设备库/令牌始终是同一份
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/thewei/solco/internal/remotehost"
	"github.com/thewei/solco/internal/settings"
)

// retryAfter spaces out start attempts that failed (a stale address, a taken port).
const retryAfter = 5 * time.Second

// Host is the slice of remotehost.Server the loop drives.
type Host interface {
	SetP2P(remotehost.P2PConfig)
	Reconfigure(remotehost.Config, remotehost.RelayConfig) error
	Close()
}

// Options wires one resident loop.
type Options struct {
	// Dir is the RemoteHost directory (certificate, devices, lease files) shared with the desktop app.
	Dir string
	// Settings reads the saved Remote Access choice (the desktop's settings.json), fresh each tick.
	Settings func() settings.RemoteAccessSettings
	// New builds the Host (once per ownership).
	New func() (Host, error)
	// Poll is the loop period (default 500 ms).
	Poll time.Duration
	Now  func() time.Time
	Log  *slog.Logger
}

// Run owns the Host port while the lease is free and the saved choice is on, until ctx ends.
func Run(ctx context.Context, o Options) error {
	if o.Dir == "" || o.Settings == nil || o.New == nil {
		return errors.New("hostd: Dir, Settings and New are required")
	}
	if o.Poll <= 0 {
		o.Poll = 500 * time.Millisecond
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	log := o.Log
	if log == nil {
		log = slog.Default()
	}
	var (
		lease *Lease
		host  Host
		last  remoteKey
		retry time.Time // after a failed start, wait before trying again
	)
	stop := func() {
		if host != nil {
			host.Close()
			host = nil
		}
		lease.Release()
		lease = nil
	}
	defer stop()
	tick := time.NewTicker(o.Poll)
	defer tick.Stop()
	for {
		cfg := o.Settings()
		switch {
		case !ConfigFrom(cfg).Enabled():
			if host != nil {
				log.Info("remote host switched off; releasing the port")
				stop()
			}
		case host != nil && YieldRequested(o.Dir, o.Now()):
			log.Info("desktop asked for the remote host; yielding")
			stop()
		case host == nil:
			if YieldRequested(o.Dir, o.Now()) || o.Now().Before(retry) {
				break
			}
			l, err := TryAcquire(o.Dir)
			if err != nil {
				if !errors.Is(err, ErrHeld) {
					log.Warn("remote host lease failed", "error", err)
				}
				break
			}
			h, err := o.New()
			if err != nil {
				l.Release()
				log.Warn("remote host unavailable", "error", err)
				break
			}
			lease, host, last = l, h, keyOf(cfg)
			if !apply(h, cfg, log) {
				stop()
				retry = o.Now().Add(retryAfter)
			}
		case keyOf(cfg) != last:
			last = keyOf(cfg)
			if !apply(host, cfg, log) {
				stop()
				retry = o.Now().Add(retryAfter)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

// remoteKey makes a saved choice comparable: Enabled is a pointer (nil = derive from Mode), so two reads of the same
// file never compare equal as structs.
type remoteKey struct {
	r       settings.RemoteAccessSettings
	enabled bool
}

func keyOf(r settings.RemoteAccessSettings) remoteKey {
	enabled := r.IsEnabled()
	r.Enabled = nil
	return remoteKey{r: r, enabled: enabled}
}

func apply(h Host, cfg settings.RemoteAccessSettings, log *slog.Logger) bool {
	h.SetP2P(P2PFrom(cfg))
	if err := h.Reconfigure(ConfigFrom(cfg), RelayFrom(cfg)); err != nil {
		log.Warn("remote host reconfigure failed", "error", err)
		return false
	}
	return true
}
