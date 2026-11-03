package hostd

/**
 * [INPUT]: 依赖 remotehost.New/Options、herdr.Manager（本机 Herdr 的真实 Backend）、automations.Detect 与 herdr.PluginListJSON、personalagent.Client
 * [OUTPUT]: 对外提供 RemoteDir、NewServer（常驻服务用的 Host 工厂）、LoadRemoteSettings（只读读取桌面的 settings.json）
 * [POS]: 常驻服务的「无 GUI Host」装配（2026-11-03，E05）：只接不依赖窗口的缝——Herdr 树/终端观察/输入、Assistants（经插件 CLI，同一个 SQLite 权威）；
 *        依赖桌面窗口状态的能力（Chat 转录、会话目录、Continue、Git/Files、Quota、Automations 管理、Remote Desktop、语音、配对）不接，bootstrap 因此如实不声明，
 *        手机据此显示不可用而不是假装可用；证书/设备库/令牌取自与桌面同一个 RemoteHost 目录
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/thewei/solco/internal/automations"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/personalagent"
	"github.com/thewei/solco/internal/remotehost"
	"github.com/thewei/solco/internal/settings"
)

// RemoteDir is the Host's own directory under the app's user-data directory (certificate, devices, relay secret,
// lease files) — the same one the desktop uses, which is what keeps one pairing identity across both owners.
func RemoteDir(userDataDir string) string { return filepath.Join(userDataDir, "RemoteHost") }

// LoadRemoteSettings reads the saved Remote Access choice without touching the file.
func LoadRemoteSettings(userDataDir string) (settings.RemoteAccessSettings, error) {
	v, err := settings.NewFileStore(userDataDir).Load(context.Background())
	return v.Remote, err
}

// NewServer returns the factory Run uses: a Host over this Mac's Herdr and the Assistants core, nothing that needs
// the desktop window.
func NewServer(userDataDir, version string) func() (Host, error) {
	return func() (Host, error) {
		host, _ := os.Hostname()
		srv, err := remotehost.New(remotehost.Options{
			Config:        remotehost.Config{Mode: remotehost.ModeOff},
			Dir:           RemoteDir(userDataDir),
			Backend:       remotehost.NewManagerBackend(herdr.NewManager()),
			PersonalAgent: newPluginSeam(),
			HostName:      host,
			Version:       version,
		})
		if err != nil {
			return nil, err
		}
		return srv, nil
	}
}

// pluginSeam finds the automations plugin through Herdr's own plugin list, at most every half minute.
type pluginSeam struct {
	mu     sync.Mutex
	at     time.Time
	client *personalagent.Client
}

func newPluginSeam() remotehost.PersonalAgentFunc {
	p := &pluginSeam{}
	return p.get
}

func (p *pluginSeam) get(ctx context.Context) (*personalagent.Client, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Since(p.at) > 30*time.Second {
		p.at, p.client = time.Now(), nil
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if raw, err := herdr.PluginListJSON(cctx); err == nil {
			if a := automations.Detect(raw); a.Supported {
				p.client = personalagent.NewClient(a.Root, []string{"HERDR_SOCKET_PATH=" + herdr.SocketPath(herdr.ParseRef("").Session)})
			}
		}
	}
	return p.client, p.client != nil
}
