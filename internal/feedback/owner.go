package feedback

/**
 * [INPUT]: 数据目录（<userData>/Feedback）里的 broker.lock、broker.json、token、feedback.json
 * [OUTPUT]: 对外提供 Run（抢到锁就服务，否则待命重试）、Dir、EnsureToken、Options
 * [POS]: broker 的唯一所有者协议（2026-11-09 决策）：常驻 solco-hostd、Solco.app 与 Agent 侧 shim 都跑同一个 Run，flock 的持有者就是 broker，
 *        进程退出由系统释放，待命者几秒内接手；端口是 loopback 临时端口，写进 broker.json 供客户端发现，状态落在 feedback.json 所以换主不丢 Ask
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	lockName     = "broker.lock"
	endpointName = "broker.json"
	tokenName    = "token"
)

// ErrHeld means another process owns the broker.
var ErrHeld = errors.New("feedback broker is owned by another process")

// Dir is the Feedback directory inside the app's user-data directory.
func Dir(userDataDir string) string { return filepath.Join(userDataDir, "Feedback") }

// EnsureToken returns the shared access token, creating it (mode 0600) on first use.
func EnsureToken(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(dir, tokenName)
	if b, err := os.ReadFile(p); err == nil {
		if t := strings.TrimSpace(string(b)); len(t) >= 32 {
			return t, nil
		}
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	t := hex.EncodeToString(raw[:])
	if err := os.WriteFile(p, []byte(t+"\n"), 0o600); err != nil {
		return "", err
	}
	return t, nil
}

// Options configure Run.
type Options struct {
	Dir          string
	Version      string
	Notifier     Notifier
	Retry        time.Duration // standby poll; default 3 s
	ImportLegacy string        // path of the old decisions.jsonl to replay once; "" = skip
	Log          *slog.Logger
}

type endpointFile struct {
	Addr string `json:"addr"`
	PID  int    `json:"pid"`
}

// Run serves the broker whenever this process can own it and waits in standby otherwise, until ctx ends.
func Run(ctx context.Context, o Options) error {
	if o.Retry <= 0 {
		o.Retry = 3 * time.Second
	}
	log := o.Log
	if log == nil {
		log = slog.Default()
	}
	if err := os.MkdirAll(o.Dir, 0o700); err != nil {
		return err
	}
	for ctx.Err() == nil {
		lease, err := tryLock(o.Dir)
		if err == nil {
			log.Info("feedback broker serving")
			if err := serve(ctx, o, log); err != nil {
				log.Warn("feedback broker stopped", "error", err)
			}
			lease.release()
		} else if !errors.Is(err, ErrHeld) {
			log.Warn("feedback broker lock failed", "error", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(o.Retry):
		}
	}
	return nil
}

func serve(ctx context.Context, o Options, log *slog.Logger) error {
	token, err := EnsureToken(o.Dir)
	if err != nil {
		return err
	}
	store, err := Open(o.Dir)
	if err != nil {
		return err
	}
	if o.ImportLegacy != "" {
		if n, err := store.ImportDecisions(o.Dir, o.ImportLegacy); err != nil {
			log.Warn("feedback legacy import failed", "error", err)
		} else if n > 0 {
			log.Info("feedback legacy history imported", "records", n)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	b := &Broker{Store: store, Token: token, Notifier: o.Notifier, Version: o.Version}
	srv := &http.Server{Handler: NewHandler(b), ReadHeaderTimeout: 10 * time.Second}
	epPath := filepath.Join(o.Dir, endpointName)
	body, _ := json.Marshal(endpointFile{Addr: ln.Addr().String(), PID: os.Getpid()})
	tmp := epPath + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		_ = ln.Close()
		return err
	}
	if err := os.Rename(tmp, epPath); err != nil {
		_ = ln.Close()
		return err
	}
	defer os.Remove(epPath)

	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			store.Tick()
		case <-ctx.Done():
			sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(sctx)
			_ = srv.Close()
			return nil
		case err := <-done:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		}
	}
}

type lease struct{ f *os.File }

func tryLock(dir string) (*lease, error) {
	f, err := os.OpenFile(filepath.Join(dir, lockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockExclusive(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &lease{f: f}, nil
}

func (l *lease) release() {
	if l == nil || l.f == nil {
		return
	}
	unlockFile(l.f)
	_ = l.f.Close()
	l.f = nil
}
