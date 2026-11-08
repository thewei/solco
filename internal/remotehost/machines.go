package remotehost

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

/**
 * [INPUT]: 依赖 Options.Machines（已保存 Herdr SSH 机器的缝，internal/app 提供）、server.go 的 guard/writeError/writeJSON
 * [OUTPUT]: 对外提供 Machines/MachineInfo/MachineError、bootstrap 的设备条目 `{id,kind:"device",label,status}`、POST /api/v3/devices/{id}/connect
 * [POS]: 手机「设备」：桌面 Herdr 里保存的远程机器。设备是 bootstrap.instances[] 的额外条目（id = `machine/<profile>`），连接是一次事务——桥起来并证明远端 Herdr 应答才返回 instance，失败不改变手机当前选择；Host 不收凭据、不收桥（桥归桌面管）、不替桌面登录（需要登录/MFA 只回 device_needs_signin）；状态只来自缓存，bootstrap 从不等 SSH
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const connectTimeout = 40 * time.Second

// Machine status words on the wire.
const (
	MachineConnected   = "connected"    // the desktop's bridge is up: the phone can address it
	MachineOnline      = "online"       // reachable without interaction: Connect will work
	MachineNeedsSignIn = "needs_signin" // auth/host key: only the Mac user can fix it
	MachineUnreachable = "unreachable"  // network or remote Herdr down
	MachineDisabled    = "disabled"     // the profile is switched off on the Mac
	MachineChecking    = "checking"     // no verdict yet
)

// MachineInfo is one saved machine as the phone sees it.
type MachineInfo struct {
	// Key is the instance key ("machine/<profile id>"), the string every route takes as ?instance=.
	Key    string
	Label  string
	Status string
}

// Machines is everything the Host needs from Herdr's saved machines.
type Machines interface {
	// List returns the saved machines with their cached status; it never waits for SSH.
	List() ([]MachineInfo, error)
	// Connect brings the machine's bridge up and proves its Herdr answers; id is the profile id.
	// Refusals are *MachineError.
	Connect(ctx context.Context, id string) error
}

// MachineError is a refusal the Host relays with a stable code and no ssh text.
type MachineError struct {
	// Code: unknown_device | device_disabled | device_needs_signin | device_unreachable | device_herdr_down | device_connect_failed
	Code   string
	Status int
}

func (e *MachineError) Error() string { return e.Code }

// machineEntries is the bootstrap's device rows ([] when the seam is off or fails).
func (s *Server) machineEntries() []MachineInfo {
	if s.opts.Machines == nil {
		return nil
	}
	list, err := s.opts.Machines.List()
	if err != nil {
		slog.Warn("remote host bootstrap: list machines failed", "error", err)
		return nil
	}
	return list
}

type deviceConnectResponse struct {
	Instance string `json:"instance"`
}

func (s *Server) handleDeviceConnect(w http.ResponseWriter, r *http.Request, dev Device) {
	if s.opts.Machines == nil {
		writeError(w, http.StatusNotFound, "devices_unavailable", "")
		return
	}
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), connectTimeout)
	defer cancel()
	if err := s.opts.Machines.Connect(ctx, id); err != nil {
		var me *MachineError
		if errors.As(err, &me) {
			slog.Warn("remote host device connect refused", "device", dev.ID, "code", me.Code)
			writeError(w, me.Status, me.Code, "")
			return
		}
		slog.Warn("remote host device connect failed", "device", dev.ID)
		writeError(w, http.StatusBadGateway, "device_connect_failed", "")
		return
	}
	slog.Info("remote host device connected", "device", dev.ID)
	writeJSON(w, http.StatusOK, deviceConnectResponse{Instance: machineKeyFor(id)})
	s.Poke()
}

// machineKeyFor mirrors herdr.MachineInstanceKey without widening this file's imports.
func machineKeyFor(id string) string { return "machine/" + id }
