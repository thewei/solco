package vaultlink

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

/**
 * [INPUT]: Spotlight 的 bundle-id 索引（`mdfind`，与 `open -b` 同源；不用 AppleScript 的 `application id`——找不到时它会弹「在哪里？」对话框）、/Applications 与 ~/Applications 的路径探测、`open -b`
 * [OUTPUT]: Install 三态、Client.Installed / Launch、ErrNotInstalled / ErrAlreadyRunning、可注入的 Prober
 * [POS]: 手机端「三级探测」的前两级（2026-11-10，Solco Vault 路线图 B7）：1 已安装（Spotlight bundle 索引，失败退路径，都不行 = 未知，手机不隐藏入口——误隐藏比误显示更糟）；2 运行中（bridge.sock 能连）；3 就绪由 Pending 的 state 给。Launch 只启动应用：`open -b com.whstudio.solcovault`，只传 bundle id，不解锁、不传值；只在「已安装且未运行」时做。结果缓存 30 s，探测从不弹窗
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// BundleID is Solco Vault's bundle identifier (its mygo.config.ts).
const BundleID = "com.whstudio.solcovault"

// Install is what is known of the Solco Vault app on this Mac.
type Install int

const (
	InstallUnknown Install = iota // no way to tell (no GUI session, tools missing)
	InstallYes
	InstallNo
)

// ErrNotInstalled and ErrAlreadyRunning are Launch's two refusals.
var (
	ErrNotInstalled   = errors.New("solco vault is not installed")
	ErrAlreadyRunning = errors.New("solco vault is already running")
)

// Prober is how the client asks the system; tests replace it.
type Prober interface {
	// Lookup asks the system's bundle index whether the app is registered. found=false with a nil
	// error is a definite "not registered"; an error means it could not ask.
	Lookup(ctx context.Context, bundleID string) (found bool, err error)
	// Exists says a path is there (the fallback when Lookup cannot ask).
	Exists(path string) bool
	// Open starts the app by bundle id.
	Open(ctx context.Context, bundleID string) error
}

type systemProber struct{}

func (systemProber) Lookup(ctx context.Context, id string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	// Spotlight's bundle-id index is what Launch Services' `open -b` finds too, and unlike
	// AppleScript's `application id` it never opens a "where is it?" dialog for a missing app.
	out, err := exec.CommandContext(ctx, "mdfind", "kMDItemCFBundleIdentifier == '"+id+"'").Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func (systemProber) Exists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func (systemProber) Open(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "open", "-b", id).Run()
}

type installCache struct {
	mu   sync.Mutex
	at   time.Time
	v    Install
	have bool
}

const installTTL = 30 * time.Second

// appPaths are where the app is looked for when Launch Services cannot answer.
func appPaths() []string {
	paths := []string{"/Applications/Solco Vault.app"}
	if h, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(h, "Applications", "Solco Vault.app"))
	}
	return paths
}

// Installed says whether the app is on this Mac: the bundle index first, then
// the usual folders, else unknown. The answer is cached for 30 seconds.
func (c *Client) Installed() Install {
	c.cache.mu.Lock()
	defer c.cache.mu.Unlock()
	if c.cache.have && time.Since(c.cache.at) < installTTL {
		return c.cache.v
	}
	v := c.installNow()
	c.cache.v, c.cache.at, c.cache.have = v, time.Now(), true
	return v
}

func (c *Client) installNow() Install {
	p := c.prober()
	found, err := p.Lookup(context.Background(), BundleID)
	switch {
	case err == nil && found:
		return InstallYes
	case err == nil:
		for _, path := range appPaths() { // Spotlight may not have indexed a copy that was just dropped in
			if p.Exists(path) {
				return InstallYes
			}
		}
		return InstallNo
	}
	for _, path := range appPaths() {
		if p.Exists(path) {
			return InstallYes
		}
	}
	return InstallUnknown
}

func (c *Client) prober() Prober {
	if c.probe != nil {
		return c.probe
	}
	return systemProber{}
}

// Running says the app's bridge answers (level 2): a connection is enough.
func (c *Client) Running(ctx context.Context) bool {
	_, err := c.call(ctx, request{Op: "pending"})
	return !errors.Is(err, ErrOffline)
}

// Launch starts the app; it never unlocks it and sends it nothing. Only an
// installed app that is not running is started.
func (c *Client) Launch(ctx context.Context) error {
	if c.Installed() == InstallNo {
		return ErrNotInstalled
	}
	if c.Running(ctx) {
		return ErrAlreadyRunning
	}
	if err := c.prober().Open(ctx, BundleID); err != nil {
		return err
	}
	c.cache.mu.Lock()
	c.cache.have = false // it is installed: the next question asks again
	c.cache.mu.Unlock()
	return nil
}
