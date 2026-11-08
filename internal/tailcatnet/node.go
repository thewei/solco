package tailcatnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
	"tailscale.com/wgengine/filter"
)

/**
 * [INPUT]: 依赖固定提交的 github.com/tailscale/tailcat（Server/Client/KeySet 之外的 AllowClient、Listen、PeerKey、DisconnectClient）与 tailscale.com/types/key
 * [OUTPUT]: 对外提供 Port（Tailcat 虚拟端口 7788）、Identity/LoadOrCreateIdentity（<RemoteHost dir>/tailcat.json 0600，Mac 持久节点密钥 + PSK）、Node（Start/Listener/Addr/PeerKey/Disconnect/Close）、PeerKeyText
 * [POS]: 内置 Tailcat 的 Mac 侧唯一适配层：隔离 tailscale.com 依赖，remotehost 只见 Overlay 接口；节点只在 Port 上 Listen（不设 OnTCP/OnTCPForward/AllowProxy，ServedTCPPorts 锁到 Port），AllowClient 由调用方给；身份文件与 TLS 证书/设备库同在 RemoteHost 目录，由 hostd 租约保证同一时刻只有一个所有者在用，GUI↔hostd 切换后地址与密钥不变；Tailcat 地址 = 带 PSK 的 bearer 能力，永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Port is the virtual TCP port the Host's HTTPS answers on inside the tunnel. It is independent of the OS
// listener port: nothing is opened on the Mac's own interfaces for it.
const Port uint16 = 7788

const identityFile = "tailcat.json"

// Identity is the Mac's persistent Tailcat node: the same key and pre-shared key across restarts keep the address
// phones were paired with valid. Rebuilding it (a missing or damaged file) means every phone must pair again.
type Identity struct {
	Version int    `json:"version"`
	NodeKey string `json:"nodeKey"` // key.NodePrivate text
	PSK     string `json:"psk"`     // tailcat.PresharedKey text
}

// LoadOrCreateIdentity reads dir/tailcat.json or creates it (0600, atomic). A file that does not parse is
// replaced, like the TLS certificate: the failure mode is "phones pair again", never a silent half-identity.
func LoadOrCreateIdentity(dir string) (Identity, error) {
	path := filepath.Join(dir, identityFile)
	if raw, err := os.ReadFile(path); err == nil {
		var id Identity
		if json.Unmarshal(raw, &id) == nil && id.Version == 1 && id.valid() {
			return id, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Identity{}, err
	}
	priv := key.NewNode()
	privText, err := priv.MarshalText()
	if err != nil {
		return Identity{}, err
	}
	psk := tailcat.NewPresharedKey()
	pskText, err := psk.MarshalText()
	if err != nil {
		return Identity{}, err
	}
	id := Identity{Version: 1, NodeKey: string(privText), PSK: string(pskText)}
	raw, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return Identity{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Identity{}, err
	}
	if err := writeFileAtomic(path, raw, 0o600); err != nil {
		return Identity{}, err
	}
	return id, nil
}

func (id Identity) valid() bool {
	_, _, err := id.keys()
	return err == nil
}

func (id Identity) keys() (key.NodePrivate, tailcat.PresharedKey, error) {
	var priv key.NodePrivate
	var psk tailcat.PresharedKey
	if err := priv.UnmarshalText([]byte(id.NodeKey)); err != nil || priv.IsZero() {
		return priv, psk, errors.New("tailcat identity: bad node key")
	}
	if err := psk.UnmarshalText([]byte(id.PSK)); err != nil || psk.IsZero() {
		return priv, psk, errors.New("tailcat identity: bad preshared key")
	}
	return priv, psk, nil
}

// Options builds a Node.
type Options struct {
	Identity Identity
	// Allow decides admission of a client node key (text form); it runs on the tunnel's goroutines and must not block long.
	Allow func(peerKey string) bool
	// DERPMapURL, when set, replaces the default public DERP map (self-hosted DERP).
	DERPMapURL string
}

// Node is one running Tailcat server with the Host's HTTPS port listening inside it.
type Node struct {
	mu     sync.Mutex
	srv    *tailcat.Server
	ln     net.Listener
	closed bool
}

// Start brings the server up (DERP discovery makes this take network time: callers run it off their UI lane)
// and listens on Port.
func Start(ctx context.Context, opts Options) (*Node, error) {
	priv, psk, err := opts.Identity.keys()
	if err != nil {
		return nil, err
	}
	srv := &tailcat.Server{
		Key:            priv,
		PresharedKey:   psk,
		Logf:           func(string, ...any) {}, // tunnel logs name peers and endpoints; Solco logs operations, not network facts
		DERPMapURL:     opts.DERPMapURL,
		ServedTCPPorts: []filter.PortRange{{First: Port, Last: Port}},
	}
	if opts.Allow != nil {
		allow := opts.Allow
		srv.AllowClient = func(k key.NodePublic) bool {
			text, err := k.MarshalText()
			if err != nil {
				return false
			}
			return allow(string(text))
		}
	} else {
		srv.AllowClient = func(key.NodePublic) bool { return false } // fail closed
	}
	if err := srv.StartContext(ctx); err != nil {
		_ = srv.Close()
		return nil, fmt.Errorf("tailcat start: %w", err)
	}
	ln, err := srv.Listen(ctx, "tcp", fmt.Sprintf(":%d", Port))
	if err != nil {
		_ = srv.Close()
		return nil, fmt.Errorf("tailcat listen: %w", err)
	}
	return &Node{srv: srv, ln: ln}, nil
}

// Listener is where the Host's HTTPS server accepts tunnel connections.
func (n *Node) Listener() net.Listener { return n.ln }

// Addr is the tailcat address clients dial: a bearer capability (it carries the pre-shared key), never logged.
func (n *Node) Addr() string { return string(n.srv.TailcatAddr()) }

// PeerKey names the authenticated tunnel peer behind an accepted connection's remote address.
func (n *Node) PeerKey(remote net.Addr) (string, bool) {
	k, ok := n.srv.PeerKey(remote)
	if !ok {
		return "", false
	}
	text, err := k.MarshalText()
	if err != nil {
		return "", false
	}
	return string(text), true
}

// Disconnect drops a connected peer; callers first make Allow refuse it.
func (n *Node) Disconnect(peerKey string) bool {
	var k key.NodePublic
	if k.UnmarshalText([]byte(peerKey)) != nil {
		return false
	}
	return n.srv.DisconnectClient(k)
}

// Close stops listening and the tunnel.
func (n *Node) Close() error {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return nil
	}
	n.closed = true
	n.mu.Unlock()
	_ = n.ln.Close()
	return n.srv.Close()
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tailcat-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := io.Writer(tmp).Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
