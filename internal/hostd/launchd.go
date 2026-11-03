package hostd

/**
 * [INPUT]: 依赖 os/exec 的 launchctl、当前可执行文件路径
 * [OUTPUT]: 对外提供 Label、BinaryName、PlistPath、Plist、Locate、Installed、Install/InstallSelf、Uninstall、Status
 * [POS]: 常驻服务的 launchd 用户服务（2026-11-03，E05）：RunAtLoad + KeepAlive，日志落在 ~/Library/Logs/Solco；只写自己的 plist，不要管理员权限；
 *        plist 里的路径是 solco-hostd 当前所在位置（App 内 Contents/Helpers/ 或开发时的构建产物）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Label is the launchd job name.
const Label = "com.whstudio.solco.hostd"

// PlistPath is where the user service definition lives.
func PlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
}

// Plist renders the service definition for a binary and data directory.
func Plist(binary, dataDir, logDir string) string {
	esc := html.EscapeString
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + Label + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + esc(binary) + `</string>
    <string>run</string>
    <string>-data</string>
    <string>` + esc(dataDir) + `</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>3</integer>
  <key>ProcessType</key><string>Background</string>
  <key>StandardErrorPath</key><string>` + esc(filepath.Join(logDir, "hostd.log")) + `</string>
  <key>StandardOutPath</key><string>` + esc(filepath.Join(logDir, "hostd.log")) + `</string>
</dict>
</plist>
`
}

// BinaryName is the service executable's file name.
const BinaryName = "solco-hostd"

// Locate finds the service binary shipped with the running app: Contents/Helpers (local installs) or
// Contents/Resources (release builds) next to Contents/MacOS. Not found outside a bundle.
func Locate(exe string) (string, bool) {
	dir := filepath.Dir(exe)
	if !strings.HasSuffix(dir, filepath.Join("Contents", "MacOS")) {
		return "", false
	}
	for _, sub := range []string{"Helpers", "Resources"} {
		p := filepath.Join(filepath.Dir(dir), sub, BinaryName)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode().Perm()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

// Installed reports whether the service definition exists (cheap: no launchctl).
func Installed() bool {
	_, err := os.Stat(PlistPath())
	return err == nil
}

// InstallSelf installs the service from the running executable (the `solco-hostd install` command).
func InstallSelf(dataDir string) error {
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	return Install(bin, dataDir)
}

// Install writes the plist for a binary and loads it (idempotent: a loaded job is replaced).
func Install(bin, dataDir string) error {
	bin, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, "Library", "Logs", "Solco")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return err
	}
	path := PlistPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(Plist(bin, dataDir, logDir)), 0o644); err != nil {
		return err
	}
	_ = launchctl("bootout", domain()+"/"+Label)
	return launchctl("bootstrap", domain(), path)
}

// Uninstall unloads and removes the service; a missing one is not an error.
func Uninstall() error {
	_ = launchctl("bootout", domain()+"/"+Label)
	if err := os.Remove(PlistPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Status reports "not installed", "installed", or "running".
func Status() string {
	if _, err := os.Stat(PlistPath()); err != nil {
		return "not installed"
	}
	out, err := exec.Command("launchctl", "print", domain()+"/"+Label).CombinedOutput()
	if err == nil && strings.Contains(string(out), "state = running") {
		return "running"
	}
	return "installed"
}

func domain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

func launchctl(args ...string) error {
	if out, err := exec.Command("launchctl", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
