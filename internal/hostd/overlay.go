package hostd

/**
 * [INPUT]: 依赖 internal/tailcatnet（内置 Tailcat 节点）与 remotehost.Overlay 接口
 * [OUTPUT]: 对外提供 TailcatOverlay（RemoteHost 目录 → remotehost.OverlayFactory）
 * [POS]: 桌面 App 与常驻服务共用的隧道装配：身份文件 tailcat.json 与 TLS 证书/设备库同在 RemoteHost 目录，由 Host 租约保证同一时刻只有一个所有者在用，GUI↔hostd 接管后地址与密钥不变；remotehost 因此不 import tailscale
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"net"
	"net/netip"

	"github.com/thewei/solco/internal/remotehost"
	"github.com/thewei/solco/internal/tailcatnet"
)

// TailcatOverlay returns the factory remotehost.Options.Overlay takes; dir is RemoteDir(userDataDir).
func TailcatOverlay(dir string) remotehost.OverlayFactory {
	return func(ctx context.Context, allow func(string) bool) (remotehost.Overlay, error) {
		id, err := tailcatnet.LoadOrCreateIdentity(dir)
		if err != nil {
			return nil, err
		}
		n, err := tailcatnet.Start(ctx, tailcatnet.Options{Identity: id, Allow: allow})
		if err != nil {
			return nil, err
		}
		return overlayNode{n}, nil
	}
}

type overlayNode struct{ n *tailcatnet.Node }

func (o overlayNode) Listener() net.Listener { return o.n.Listener() }
func (o overlayNode) Addr() string           { return o.n.Addr() }
func (o overlayNode) Disconnect(k string) bool {
	return o.n.Disconnect(k)
}
func (o overlayNode) Close() error { return o.n.Close() }
func (o overlayNode) PeerKey(remote string) (string, bool) {
	ap, err := netip.ParseAddrPort(remote)
	if err != nil {
		return "", false
	}
	return o.n.PeerKey(net.TCPAddrFromAddrPort(ap))
}
