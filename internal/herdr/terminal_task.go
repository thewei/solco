package herdr

/**
 * [INPUT]: macOS `open -a Terminal`、zsh、已解析的 herdr / ssh 绝对路径
 * [OUTPUT]: TerminalTask（RunInTerminal 写出自带退出码标记的脚本并交给 Terminal.app；Result 轮询退出码）与 Manager.StartMachineAdd / StartMachineSignIn / StartMachineSetup
 * [POS]: 必须由用户在交互终端里完成的三步（machine add / 登录 / Set Up）的唯一交接通道：口令、MFA 与确认提示只发生在用户终端里，Solco 只读退出码，不读终端输出、不存凭据
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Herdr documents `machine add` and `--remote` setup as interactive commands:
// they probe the remote, may install or restart its server, and always ask
// before replacing anything. Solco therefore hands them to the user's Terminal
// and only watches how they end.

// TerminalTask is one command handed to the user's Terminal.
type TerminalTask struct {
	dir    string
	marker string
}

// RunInTerminal opens Terminal.app running command under a title line. The
// script records the command's exit status in a marker file, which Result
// reads; nothing the command prints or the user types ever reaches Solco.
func RunInTerminal(title, command string) (*TerminalTask, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("opening a terminal is only supported on macOS; run: %s", command)
	}
	dir, err := os.MkdirTemp("", "solco-terminal-")
	if err != nil {
		return nil, fmt.Errorf("create terminal task: %w", err)
	}
	marker := filepath.Join(dir, "exit")
	script := filepath.Join(dir, "task.command")
	if err := os.WriteFile(script, []byte(terminalScript(title, command, marker)), 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("write terminal script: %w", err)
	}
	if out, err := exec.Command("open", "-a", "Terminal", script).CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("open Terminal: %s", strings.TrimSpace(string(out)))
	}
	return &TerminalTask{dir: dir, marker: marker}, nil
}

// Result reports the command's exit status once it has ended. The marker is
// written atomically, so a present file is always complete.
func (t *TerminalTask) Result() (code int, done bool) {
	body, err := os.ReadFile(t.marker)
	if err != nil {
		return 0, false
	}
	code, err = strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil {
		return 1, true
	}
	return code, true
}

// Cleanup removes the task's script and marker. Safe to call more than once;
// a window still running its script just loses the marker it would write.
func (t *TerminalTask) Cleanup() { _ = os.RemoveAll(t.dir) }

// terminalScript builds the zsh script one task runs: title, the command, then
// the exit-status marker (atomic: write + rename) and a pause so the window
// stays readable.
func terminalScript(title, command, marker string) string {
	q := shellQuote
	return strings.Join([]string{
		"#!/bin/zsh",
		"clear",
		fmt.Sprintf("printf '%%s\\n\\n' %s", q(title)),
		command,
		"rc=$?",
		fmt.Sprintf("printf '%%s' \"$rc\" > %s.tmp && mv %s.tmp %s", q(marker), q(marker), q(marker)),
		"printf '\\n'",
		`if [ "$rc" -eq 0 ]; then`,
		"  printf 'Done — return to Solco. You can close this window.\\n'",
		"else",
		"  printf 'This did not finish (exit %s). Fix what is shown above, then try again in Solco.\\n' \"$rc\"",
		"fi",
		"printf 'Press any key to close…'",
		"read -k 1 -s",
		"printf '\\n'",
		"",
	}, "\n")
}

// machineAddCommand is the exact `herdr machine add` invocation: the label and
// the remote session are passed only when the user chose them, so Herdr's own
// defaults (host or host/session label, session auto-detection) stay Herdr's.
func machineAddCommand(cli, target, label, session string) string {
	parts := []string{shellQuote(cli), "machine", "add"}
	if label = strings.TrimSpace(label); label != "" {
		parts = append(parts, "--label", shellQuote(label))
	}
	if session = strings.TrimSpace(session); session != "" {
		parts = append(parts, "--remote-session", shellQuote(session))
	}
	return strings.Join(append(parts, shellQuote(strings.TrimSpace(target))), " ")
}

// machineSetupCommand is Herdr's standalone setup command for a saved
// machine: `herdr --remote <target>`, plus `--session <name>` when the profile
// pins a named session — the form Herdr itself tells the user to run.
func machineSetupCommand(cli string, machine Machine) string {
	parts := []string{shellQuote(cli), "--remote", shellQuote(machine.Target)}
	if session := strings.TrimSpace(machine.Session); session != "" && normalizeSession(session) != defaultSession {
		parts = append(parts, "--session", shellQuote(session))
	}
	return strings.Join(parts, " ")
}

// StartMachineAdd hands `herdr machine add` to the user's Terminal. Herdr saves
// the profile only when the command succeeds; the caller watches the profile
// list and the task result.
func (m *Manager) StartMachineAdd(target, label, session string) (*TerminalTask, error) {
	if err := ValidateTarget(target); err != nil {
		return nil, err
	}
	cli, err := resolveCLI()
	if err != nil {
		return nil, err
	}
	return RunInTerminal("Adding "+strings.TrimSpace(target)+" to Herdr…", machineAddCommand(cli, target, label, session))
}

// StartMachineSignIn opens Terminal on Solco's own interactive sign-in for one
// target: it authenticates once (passphrase, MFA, host-key prompt) and leaves
// a shared connection behind that Solco's bridge then rides.
func (m *Manager) StartMachineSignIn(target string) (*TerminalTask, error) {
	if err := ValidateTarget(target); err != nil {
		return nil, err
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return nil, fmt.Errorf("ssh not found: %w", err)
	}
	return RunInTerminal("Signing in to "+strings.TrimSpace(target)+" for Solco…", signInShell(sshPath, target))
}

// StartMachineSetup hands Herdr's standalone setup (`herdr --remote …`) for a
// saved machine to the user's Terminal: the one step that may install or start
// the remote server. The command stays open as Herdr's remote client, so the
// caller watches the machine status rather than waiting for it to end.
func (m *Manager) StartMachineSetup(machine Machine) (*TerminalTask, error) {
	if err := ValidateTarget(machine.Target); err != nil {
		return nil, err
	}
	cli, err := resolveCLI()
	if err != nil {
		return nil, err
	}
	return RunInTerminal("Setting up "+machine.Label+" with Herdr…", machineSetupCommand(cli, machine))
}
