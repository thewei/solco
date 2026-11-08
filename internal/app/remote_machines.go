package app

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/remotehost"
)

/**
 * [INPUT]: 依赖 herdr.Manager（ListMachines/MachineStates/ConnectMachine/BridgedMachineKeys、FailureOf）、remotehost.Machines 契约
 * [OUTPUT]: 对外提供 RemoteMachines（实现 remotehost.Machines：手机「设备」的列表与连接）
 * [POS]: 手机 Host 与 Herdr 已保存机器之间的薄适配——状态来自一份 30 秒缓存（`herdr machine status` 要走 SSH，慢，后台刷新，List 永不等它），桥已起 = connected；Connect 就是桌面切设备事务的前半（ConnectMachine），失败按 FailureKind 译成稳定 code（登录/MFA/主机密钥 → device_needs_signin），ssh 的文字永不出本包；不收凭据、不收桥
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const machineStateTTL = 30 * time.Second

type RemoteMachines struct {
	manager *herdr.Manager

	mu         sync.Mutex
	states     map[string]herdr.MachineState
	checkedAt  time.Time
	refreshing bool
	now        func() time.Time
}

func NewRemoteMachines(m *herdr.Manager) *RemoteMachines {
	return &RemoteMachines{manager: m, now: time.Now}
}

func (r *RemoteMachines) List() ([]remotehost.MachineInfo, error) {
	machines, err := r.manager.ListMachines()
	if err != nil {
		return nil, err
	}
	r.refreshIfStale()
	bridged := r.manager.BridgedMachineKeys()
	r.mu.Lock()
	states := r.states
	r.mu.Unlock()
	out := make([]remotehost.MachineInfo, 0, len(machines))
	for _, m := range machines {
		label := m.Label
		if label == "" {
			label = m.Target
		}
		key := herdr.MachineInstanceKey(m.ID)
		out = append(out, remotehost.MachineInfo{Key: key, Label: label, Status: machineStatus(m, slices.Contains(bridged, key), states)})
	}
	return out, nil
}

// machineStatus is the one place a saved machine becomes a status word.
func machineStatus(m herdr.Machine, bridged bool, states map[string]herdr.MachineState) string {
	switch {
	case !m.Enabled:
		return remotehost.MachineDisabled
	case bridged:
		return remotehost.MachineConnected
	}
	state, ok := states[m.ID]
	switch {
	case !ok:
		return remotehost.MachineChecking
	case state.Reachable:
		return remotehost.MachineOnline
	}
	if kind := herdr.ClassifyFailure(state.Status + " " + state.Message); kind == herdr.FailureAuth || kind == herdr.FailureHostKey {
		return remotehost.MachineNeedsSignIn
	}
	return remotehost.MachineUnreachable
}

// refreshIfStale starts one background `machine status` when the cache is old; callers never wait.
func (r *RemoteMachines) refreshIfStale() {
	r.mu.Lock()
	if r.refreshing || (r.states != nil && r.now().Sub(r.checkedAt) < machineStateTTL) {
		r.mu.Unlock()
		return
	}
	r.refreshing = true
	r.mu.Unlock()
	go func() {
		states, err := r.manager.MachineStates()
		r.mu.Lock()
		defer r.mu.Unlock()
		r.refreshing = false
		if err == nil {
			r.states, r.checkedAt = states, r.now()
		}
	}()
}

func (r *RemoteMachines) Connect(ctx context.Context, id string) error {
	machines, err := r.manager.ListMachines()
	if err != nil {
		return err
	}
	idx := slices.IndexFunc(machines, func(m herdr.Machine) bool { return m.ID == id })
	switch {
	case idx < 0:
		return &remotehost.MachineError{Code: "unknown_device", Status: http.StatusNotFound}
	case !machines[idx].Enabled:
		return &remotehost.MachineError{Code: "device_disabled", Status: http.StatusConflict}
	}
	err = r.manager.ConnectMachine(ctx, id)
	if err == nil {
		r.mu.Lock()
		r.checkedAt = time.Time{} // the next List re-checks; the bridge already says connected
		r.mu.Unlock()
		return nil
	}
	switch herdr.FailureOf(err) {
	case herdr.FailureAuth, herdr.FailureHostKey:
		return &remotehost.MachineError{Code: "device_needs_signin", Status: http.StatusConflict}
	case herdr.FailureNetwork:
		return &remotehost.MachineError{Code: "device_unreachable", Status: http.StatusBadGateway}
	case herdr.FailureServer:
		return &remotehost.MachineError{Code: "device_herdr_down", Status: http.StatusBadGateway}
	}
	return &remotehost.MachineError{Code: "device_connect_failed", Status: http.StatusBadGateway}
}
