package herdr

/**
 * [INPUT]: 系统 OpenSSH `ssh`（ControlPath 复用、-O check/exit/cancel）、已保存机器的 Target、~/.solco/ssh-ctl
 * [OUTPUT]: ControlPath/复用参数、MasterLive/CloseMaster、signInShell（在用户终端里交互建立共享连接）、FailureKind 分类与 BridgeError
 * [POS]: Solco 自有数据面（桥与 remoteHome）的登录态层：口令/MFA 只在用户终端里输入一次，之后所有非交互调用骑在同一条共享连接上；Herdr 自己的连接与凭据不碰，Solco 不存任何凭据
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Why Solco owns a shared SSH connection at all: Herdr runs its own ssh
// through a generated config with a private per-attach control socket, so the
// authentication `herdr machine reconnect` performs can never be reused by
// Solco's bridge, and a bridge is BatchMode (it cannot prompt). Passphrase and
// MFA hosts therefore need one interactive sign-in that Solco's calls can ride
// on. The sign-in runs in the user's Terminal (credentials never reach Solco),
// leaves an OpenSSH ControlMaster behind, and every later non-interactive call
// goes through it. No live master simply means ssh dials normally.

// controlPersist bounds how long a shared connection outlives its last open
// channel. An attached Solco keeps a channel open (the event stream), so this
// only decides how long a sign-in survives once Solco has let go of the host.
const controlPersist = "10m"

// failureProbeTimeout bounds the local `ssh -O` control-socket commands; they
// never touch the network, so a stuck one is a bug, not a slow host.
const failureProbeTimeout = 3 * time.Second

// controlDir holds Solco's private OpenSSH control sockets.
func controlDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return filepath.Join(home, ".solco", "ssh-ctl")
}

// ControlPath is the control socket of one target's shared connection. Unix
// socket paths are limited to ~104 bytes on macOS (and ssh appends a 17-byte
// temp suffix), so the socket is named by a short hash of its target.
func ControlPath(target string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(target)))
	return filepath.Join(controlDir(), hex.EncodeToString(sum[:6]))
}

// reuseArgs make a non-interactive ssh call ride the target's live shared
// connection when there is one. ControlMaster=no means such a call never
// creates a master (BatchMode stays in force, so it never prompts either);
// with no live master ssh falls back to dialing normally.
func reuseArgs(target string) []string {
	return []string{"-o", "ControlMaster=no", "-o", "ControlPath=" + ControlPath(target)}
}

// MasterLive reports whether the target has a live shared connection. The
// check talks to the local control socket only — no network round trip.
func MasterLive(target string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), failureProbeTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "ssh", "-O", "check", "-o", "ControlPath="+ControlPath(target), target).Run() == nil
}

// CloseMaster ends the target's shared connection (best effort). Callers use
// it when the last profile for a target goes away, so a sign-in does not
// outlive the device it was for.
func CloseMaster(target string) {
	ctx, cancel := context.WithTimeout(context.Background(), failureProbeTimeout)
	defer cancel()
	_ = exec.CommandContext(ctx, "ssh", "-O", "exit", "-o", "ControlPath="+ControlPath(target), target).Run()
}

// cancelForward removes one forward from a shared connection. A mux client
// hands its -L forward to the master and exits, so the forward outlives the
// bridge unless it is cancelled; without a master this fails fast and harmlessly.
func cancelForward(target, forward string) {
	ctx, cancel := context.WithTimeout(context.Background(), failureProbeTimeout)
	defer cancel()
	_ = exec.CommandContext(ctx, "ssh", "-O", "cancel", "-o", "ControlPath="+ControlPath(target), "-L", forward, target).Run()
}

// signInShell is the shell snippet the Terminal hand-off runs to establish the
// target's shared connection interactively. When the check fails, whatever sits
// at the control path is dead (a killed master leaves its socket behind, which
// would silently disable multiplexing: "already exists"), so it is removed
// before the new master is made. AddKeysToAgent lets a passphrase key also
// serve Herdr's own background connections after the single prompt.
func signInShell(sshPath, target string) string {
	control := ControlPath(target)
	dir := filepath.Dir(control)
	q := shellQuote
	return strings.Join([]string{
		fmt.Sprintf("mkdir -p %s && chmod 700 %s", q(dir), q(dir)),
		fmt.Sprintf("if %s -O check -o ControlPath=%s %s >/dev/null 2>&1; then", q(sshPath), q(control), q(target)),
		"  printf 'Already signed in.\\n'",
		"else",
		fmt.Sprintf("  rm -f %s", q(control)),
		fmt.Sprintf("  %s -o ControlMaster=yes -o ControlPath=%s -o ControlPersist=%s -o ServerAliveInterval=15 -o ServerAliveCountMax=3 -o AddKeysToAgent=yes %s true",
			q(sshPath), q(control), controlPersist, q(target)),
		"fi",
	}, "\n")
}

// ValidateTarget rejects SSH targets that cannot be a destination: empty,
// containing whitespace or control characters, or starting with "-" (which ssh
// would read as an option).
func ValidateTarget(target string) error {
	target = strings.TrimSpace(target)
	switch {
	case target == "":
		return errors.New("SSH target is required")
	case strings.HasPrefix(target, "-"):
		return errors.New("SSH target cannot start with \"-\"")
	case strings.ContainsFunc(target, func(r rune) bool { return r <= ' ' || r == 0x7f }):
		return errors.New("SSH target cannot contain spaces")
	}
	return nil
}

// FailureKind classifies why reaching a machine failed, so the interface can
// offer the right recovery instead of one generic error.
type FailureKind string

const (
	// FailureAuth: the host is there but refused every non-interactive
	// credential — the user must sign in (passphrase/MFA).
	FailureAuth FailureKind = "auth"
	// FailureHostKey: the host's key is unknown or changed; only the user can
	// decide to trust it, in a terminal.
	FailureHostKey FailureKind = "host-key"
	// FailureNetwork: refused, timed out, unresolved or unreachable.
	FailureNetwork FailureKind = "network"
	// FailureServer: SSH works but the remote Herdr session did not answer —
	// not running, or not set up.
	FailureServer FailureKind = "server"
	// FailureOther: anything else; the message is shown as is.
	FailureOther FailureKind = "other"
)

// failurePatterns map OpenSSH's own diagnostics to a kind, first match wins.
// The server pattern precedes the network one because a failed remote connect
// reads "open failed: connect failed: Connection refused".
var failurePatterns = []struct {
	kind    FailureKind
	needles []string
}{
	{FailureHostKey, []string{"host key verification failed", "remote host identification has changed", "no matching host key type"}},
	{FailureAuth, []string{"permission denied", "authentication failed", "no more authentication methods", "too many authentication failures", "auth required", "authentication required"}},
	{FailureServer, []string{"open failed: connect failed"}},
	{FailureNetwork, []string{"connection refused", "connection timed out", "operation timed out", "no route to host", "could not resolve hostname", "network is unreachable", "connection reset", "connection closed", "broken pipe", "host is down"}},
}

// ClassifyFailure reads a failure message (ssh stderr or a Herdr verdict) and
// names the recovery it needs. Unrecognized text is FailureOther.
func ClassifyFailure(message string) FailureKind {
	lower := strings.ToLower(message)
	for _, group := range failurePatterns {
		for _, needle := range group.needles {
			if strings.Contains(lower, needle) {
				return group.kind
			}
		}
	}
	return FailureOther
}

// BridgeError is a failed attempt to reach a machine's remote Herdr over SSH.
// Its text is the human message; Kind says which recovery applies.
type BridgeError struct {
	Kind FailureKind
	msg  string
	err  error
}

func (e *BridgeError) Error() string { return e.msg }
func (e *BridgeError) Unwrap() error { return e.err }

func newBridgeError(kind FailureKind, err error, format string, args ...any) *BridgeError {
	return &BridgeError{Kind: kind, msg: fmt.Sprintf(format, args...), err: err}
}

// FailureOf extracts the failure kind of an error returned by a machine
// connection attempt; errors that did not come from the bridge are classified
// by their text.
func FailureOf(err error) FailureKind {
	var bridge *BridgeError
	if errors.As(err, &bridge) {
		return bridge.Kind
	}
	if err == nil {
		return FailureOther
	}
	return ClassifyFailure(err.Error())
}
