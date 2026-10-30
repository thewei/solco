package herdr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// SSH socket bridge: the data plane for saved-machine instances. Herdr's
// `--machine` prefix forwards single API commands over SSH, but not
// interactive terminal attachment or long-lived event streams — both are raw
// socket sessions. A forwarded unix socket makes a remote Herdr server
// addressable through the SAME client path as local ones: every RPC, event
// subscription, and `herdr terminal attach` child speaks to the bridge's
// local endpoint. This mirrors the proven Rust ssh_bridge. The bridge never
// starts or restarts a remote server; unreachable servers surface as errors.
//
// When the user has signed in through Solco (sshcontrol.go) a live OpenSSH
// ControlMaster exists for the target, and the bridge's `ssh -N -L` becomes a
// mux client: it hands the forward to the master and exits at once, so "the
// ssh process is gone" no longer means "the bridge is down" — the forward
// socket answering does.

type sshBridge struct {
	socket   string
	target   string
	forwards []string // "<local>:<remote>" of the API and client sockets, kept to cancel them inside a shared connection
	// shared: ssh handed the forwards to a ControlMaster and exited, so the
	// master — not cmd — owns them from then on.
	shared bool
	cmd    *exec.Cmd
}

// close tears the bridge down: a direct connection dies with its ssh process,
// a shared one must have its forwards cancelled inside the master.
func (b *sshBridge) close() {
	if b.shared {
		for _, forward := range b.forwards {
			cancelForward(b.target, forward)
		}
	} else {
		_ = b.cmd.Process.Kill()
	}
	removeSockets(b.socket)
}

// clientSocketPath is where Herdr keeps a session's client socket: beside the
// API socket, "<stem>-client.sock". `herdr terminal attach` derives it from
// the API socket it is given, so the bridge must put the pair under this rule
// on both ends.
func clientSocketPath(apiSocket string) string {
	return strings.TrimSuffix(apiSocket, ".sock") + "-client.sock"
}

// bridgeForwards are the two `-L` specs of one bridge: the API socket and the
// client socket beside it.
func bridgeForwards(local, remote string) []string {
	return []string{local + ":" + remote, clientSocketPath(local) + ":" + clientSocketPath(remote)}
}

// removeSockets deletes a bridge's local API socket and the client socket
// beside it.
func removeSockets(apiSocket string) {
	_ = os.Remove(apiSocket)
	_ = os.Remove(clientSocketPath(apiSocket))
}

// bridges is process-global: the ssh children must outlive the goroutines
// that spawned them, and they are killed when the shell closes (the app's
// graceful shutdown). A crashed app can leave an `ssh -N` behind; it holds
// one idle TCP connection and its forwarded socket is replaced on rebuild.
// mu guards the maps only; each machine brings its own bridge up under its own
// lock, so a slow or dead host never stalls RPCs to the machine in use.
var bridges = struct {
	mu      sync.Mutex
	byID    map[string]*sshBridge
	locks   map[string]*sync.Mutex
	closing bool
}{byID: make(map[string]*sshBridge), locks: make(map[string]*sync.Mutex)}

// ensureBridge returns the local endpoint of the machine's SSH bridge,
// bringing one up (or replacing a dead one) when needed. The ping probe in
// the bring-up and the live-bridge check is what turns "remote server not
// running" into an error instead of a hang.
func (m *Manager) ensureBridge(machine Machine) (string, error) {
	return m.ensureBridgeCtx(context.Background(), machine)
}

// ConnectMachine brings one saved machine's bridge up and proves its remote
// Herdr answers, without touching any shell state: the cancellable first half
// of a device switch. Cancelling ctx abandons the attempt and kills the ssh it
// started; a failure is a *BridgeError whose Kind names the recovery.
func (m *Manager) ConnectMachine(ctx context.Context, id string) error {
	machine, err := m.machineByID(id)
	if err != nil {
		return err
	}
	_, err = m.ensureBridgeCtx(ctx, machine)
	return err
}

func (m *Manager) ensureBridgeCtx(ctx context.Context, machine Machine) (string, error) {
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("SSH socket bridging is not ported to Windows yet")
	}
	bridges.mu.Lock()
	if bridges.closing {
		bridges.mu.Unlock()
		return "", fmt.Errorf("Solco is shutting down")
	}
	lock := bridges.locks[machine.ID]
	if lock == nil {
		lock = &sync.Mutex{}
		bridges.locks[machine.ID] = lock
	}
	bridges.mu.Unlock()

	lock.Lock()
	defer lock.Unlock()

	bridges.mu.Lock()
	bridge := bridges.byID[machine.ID]
	bridges.mu.Unlock()
	if bridge != nil {
		if _, err := ping(bridge.socket); err == nil {
			return bridge.socket, nil
		}
		bridge.close()
		bridges.mu.Lock()
		delete(bridges.byID, machine.ID)
		bridges.mu.Unlock()
	}
	bridge, err := m.bringUpBridge(ctx, machine)
	if err != nil {
		return "", err
	}
	bridges.mu.Lock()
	defer bridges.mu.Unlock()
	if bridges.closing {
		bridge.close()
		return "", fmt.Errorf("Solco is shutting down")
	}
	bridges.byID[machine.ID] = bridge
	return bridge.socket, nil
}

// bringUpBridge resolves the remote HOME, forwards fresh local unix sockets
// to the remote session's Herdr API and client sockets, and waits for a
// successful Herdr ping through the API forward. Blocking; callers run it off the UI lane. ctx
// bounds the bring-up only — the bridge process it returns outlives it.
func (m *Manager) bringUpBridge(ctx context.Context, machine Machine) (*sshBridge, error) {
	home, err := remoteHome(ctx, machine.Target)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, newBridgeError(ClassifyFailure(err.Error()), err, "%s: %v", machine.Label, err)
	}
	local := bridgeSocketPath(machine.Target, machine.Session)
	forwards := bridgeForwards(local, remoteSocketPath(home, machine.Session))
	// Forwards an earlier run left in a still-live shared connection own the
	// old socket files; cancel them before the files go.
	for _, forward := range forwards {
		cancelForward(machine.Target, forward)
	}
	for _, stale := range []string{local, clientSocketPath(local)} {
		if err := os.Remove(stale); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("%s: clean stale bridge socket: %w", machine.Label, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		return nil, fmt.Errorf("%s: create bridge dir: %w", machine.Label, err)
	}
	args := []string{
		"-N",
		"-o", "BatchMode=yes",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
	}
	args = append(args, reuseArgs(machine.Target)...)
	args = append(args, "-L", forwards[0], "-L", forwards[1], machine.Target)
	var stderr bytes.Buffer
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = nil
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: spawn ssh: %w", machine.Label, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	shared, err := waitForward(ctx, func() error { _, err := ping(local); return err }, exited, 250*time.Millisecond, 10*time.Second)
	if err == nil {
		return &sshBridge{socket: local, target: machine.Target, forwards: forwards, shared: shared, cmd: cmd}, nil
	}
	_ = cmd.Process.Kill()
	if shared {
		for _, forward := range forwards {
			cancelForward(machine.Target, forward)
		}
	}
	removeSockets(local)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	detail := strings.TrimSpace(stderr.String())
	if errors.Is(err, errSSHExited) {
		if detail == "" {
			detail = err.Error()
		}
		return nil, newBridgeError(ClassifyFailure(detail), err, "%s: ssh %s failed: %s", machine.Label, machine.Target, detail)
	}
	if detail != "" {
		err = fmt.Errorf("%w (%s)", err, detail)
	}
	// ssh is up but Herdr did not answer through the forward: the remote
	// session is not running (or not set up) unless ssh itself said otherwise.
	kind := ClassifyFailure(detail)
	if kind == FailureOther {
		kind = FailureServer
	}
	return nil, newBridgeError(kind, err, "%s: remote Herdr at %s did not come up: %v", machine.Label, machine.Target, err)
}

// errSSHExited marks waitForward's "ssh itself failed" outcome, as opposed to
// the forward simply never answering.
var errSSHExited = errors.New("ssh exited")

// waitForward waits until probe succeeds. ssh exiting with success is the
// shared-connection hand-over (reported as shared) and the wait goes on; ssh
// exiting with an error ends the wait at once with errSSHExited; ctx and the
// limit end it too.
func waitForward(ctx context.Context, probe func() error, exited <-chan error, interval, limit time.Duration) (shared bool, err error) {
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		lastErr := probe()
		if lastErr == nil {
			return shared, nil
		}
		select {
		case <-ctx.Done():
			return shared, ctx.Err()
		case exitErr := <-exited:
			if exitErr != nil {
				return shared, fmt.Errorf("%w: %v", errSSHExited, exitErr)
			}
			shared = true
			exited = nil
		case <-deadline.C:
			return shared, lastErr
		case <-tick.C:
		}
	}
}

// CloseBridges kills every live SSH bridge. Called from the shell's close
// path; later ensureBridge calls fail instead of reconnecting.
func CloseBridges() {
	bridges.mu.Lock()
	defer bridges.mu.Unlock()
	bridges.closing = true
	for id, bridge := range bridges.byID {
		bridge.close()
		delete(bridges.byID, id)
	}
}

// CloseBridge tears down one machine's bridge: a device that was disabled or
// forgotten must not keep an ssh connection open. It takes the machine's own
// lock, so a bring-up in flight lands first and is closed with the rest.
func CloseBridge(id string) {
	bridges.mu.Lock()
	lock := bridges.locks[id]
	bridges.mu.Unlock()
	if lock != nil {
		lock.Lock()
		defer lock.Unlock()
	}
	bridges.mu.Lock()
	bridge := bridges.byID[id]
	delete(bridges.byID, id)
	bridges.mu.Unlock()
	if bridge != nil {
		bridge.close()
	}
}

// remoteHome resolves the remote user's HOME over a non-interactive SSH run.
// Without a live shared connection a passphrase-protected key must already be
// in ssh-agent — the rule Herdr's own background connections follow; Solco's
// sign-in (sshcontrol.go) is the one interactive step that lifts it.
func remoteHome(ctx context.Context, target string) (string, error) {
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=8"}
	args = append(args, reuseArgs(target)...)
	args = append(args, target, "echo $HOME")
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		detail := err.Error()
		var exit *exec.ExitError
		if asExitError(err, &exit) {
			detail = strings.TrimSpace(string(exit.Stderr))
		}
		return "", fmt.Errorf("ssh %s failed: %s", target, detail)
	}
	home := strings.TrimSpace(string(out))
	if home == "" {
		return "", fmt.Errorf("remote $HOME resolved empty")
	}
	return home, nil
}

// bridgeSocketPath is the deterministic local endpoint for one (target,
// session) pair; rebuilding a bridge always recreates the same path.
func bridgeSocketPath(target, session string) string {
	return filepath.Join(bridgeDir(), bridgeSlug(target, session)+".sock")
}

func bridgeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return filepath.Join(home, ".solco", "ssh-bridges")
}

func bridgeSlug(target, session string) string {
	raw := target + "-" + normalizeSession(session)
	var builder strings.Builder
	for _, char := range strings.ToLower(raw) {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			builder.WriteRune(char)
		} else {
			builder.WriteRune('_')
		}
	}
	return builder.String()
}

// remoteSocketPath mirrors the local SocketPath layout on the remote HOME.
func remoteSocketPath(home, session string) string {
	session = normalizeSession(session)
	if session == defaultSession {
		return filepath.Join(home, ".config", "herdr", "herdr.sock")
	}
	return filepath.Join(home, ".config", "herdr", "sessions", session, "herdr.sock")
}
