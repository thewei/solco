package hostd

/**
 * [INPUT]: 依赖 internal/settings 的 RemoteAccessSettings 与 internal/remotehost 的配置类型
 * [OUTPUT]: 对外提供 ConfigFrom、RelayFrom、P2PFrom（保存的选择 → Host 配置；桌面与常驻服务共用，永不放宽）
 * [POS]: 两个所有者读同一份设置、得出同一个监听策略的单一出处（原在 nativeui/remote_settings.go）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"strings"

	"github.com/thewei/solco/internal/remotehost"
	"github.com/thewei/solco/internal/settings"
)

// RelayFrom maps the saved choice to the Host's relay config.
func RelayFrom(r settings.RemoteAccessSettings) remotehost.RelayConfig {
	return remotehost.RelayConfig{Enabled: r.RelayEnabled, URL: strings.TrimSpace(r.RelayURL), RegistrationKey: r.RelayKey}
}

// P2PFrom maps the saved choice to the Host's P2P config (on unless the user switched it off; empty ICE text =
// the built-in STUN list).
func P2PFrom(r settings.RemoteAccessSettings) remotehost.P2PConfig {
	return remotehost.P2PConfig{
		Enabled:    !r.P2PDisabled,
		ICEServers: remotehost.ParseICEServers(r.P2PICE, r.P2PTurnUser, r.P2PTurnPass),
	}
}

// ConfigFrom maps the saved choice to the Host's listener config; an unreadable mode is off, never wider.
func ConfigFrom(r settings.RemoteAccessSettings) remotehost.Config {
	mode, err := remotehost.ParseMode(r.Mode)
	if err != nil {
		mode = remotehost.ModeOff
	}
	port := r.Port
	if port == 0 {
		port = remotehost.DefaultPort
	}
	return remotehost.Config{Mode: mode, Port: port, Address: r.Address}
}
