package nativeui

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
)

/**
 * [INPUT]: 页面右键目标（选中文本/链接/消息源文本/文件路径）、聊天 pane 的 cwd 与仓库根、mygo.Menu / Clipboard / Shell.OpenExternal
 * [OUTPUT]: 对外提供 ChatBridge.ShowContextMenu（原生 NSMenu 弹出）、chat:menu 事件（页内动作：selectAll/find）、pageNavigationDecision（WebView 导航闸）
 * [POS]: WebView Chat 的“原生感”：页面屏蔽浏览器默认右键菜单（Reload/Inspect），改由 macOS 菜单呈现；主框架永远不被链接导航带走，http(s)/mailto 交给系统浏览器。仅展示层：不读文件内容、不新增 Herdr 方法，Quick Look 只对本机存在的普通文件
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// chatMenu is the page's own menu actions (the ones only the page can run).
var chatMenu = mygo.NewEvent[string]("chat:menu")

// ChatMenuInput is what the page knows about the right-click target. The
// bridge trusts none of it beyond the checks below: links must be http(s) or
// mailto, a path must name an existing regular file under the pane's folders.
type ChatMenuInput struct {
	// Selection is the page's selected text ("" when nothing is selected).
	Selection string `json:"selection,omitempty"`
	// Link is the href of the link under the pointer.
	Link string `json:"link,omitempty"`
	// Message is the Markdown source of the turn under the pointer.
	Message string `json:"message,omitempty"`
	// Path is a file path shown under the pointer (a chip or a changes row).
	Path string `json:"path,omitempty"`
}

// chatMenuEntry is one row of the context menu; an empty ID is a separator.
type chatMenuEntry struct {
	ID    string
	Label string
}

// chatMenuPlan lists the menu rows for a right-click target. quickLook says
// the path resolved to a local file.
func chatMenuPlan(in ChatMenuInput, quickLook bool) []chatMenuEntry {
	var rows []chatMenuEntry
	if in.Selection != "" {
		rows = append(rows, chatMenuEntry{"copy", "Copy"})
	}
	if safeExternalURL(in.Link) {
		rows = append(rows,
			chatMenuEntry{"openLink", "Open Link"},
			chatMenuEntry{"copyLink", "Copy Link"})
	}
	if quickLook {
		rows = append(rows,
			chatMenuEntry{"quickLook", "Quick Look"},
			chatMenuEntry{"copyPath", "Copy Path"})
	}
	if in.Message != "" {
		rows = append(rows, chatMenuEntry{"copyMessage", "Copy Message"})
	}
	if len(rows) > 0 {
		rows = append(rows, chatMenuEntry{})
	}
	return append(rows,
		chatMenuEntry{"selectAll", "Select All"},
		chatMenuEntry{"find", "Find…"})
}

// safeExternalURL reports a link the system may open for the page: http,
// https or mailto, never file:, javascript:, data: or a custom scheme.
func safeExternalURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	switch parsed.Scheme {
	case "http", "https":
		return parsed.Host != ""
	case "mailto":
		return parsed.Opaque != ""
	}
	return false
}

// pageNavigationDecision is the navigation gate of the chat and Files pages. A page
// never navigates away: every main-frame navigation is cancelled, and a
// link the user clicked opens in the system browser when it is safe to.
func pageNavigationDecision(raw string, userInitiated bool) (open bool) {
	return userInitiated && safeExternalURL(raw)
}

// resolveChatPath turns a path shown in the page into an existing regular
// file: absolute as is, relative against each base in turn.
func resolveChatPath(path string, bases ...string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" || strings.ContainsRune(path, 0) {
		return "", false
	}
	var candidates []string
	if filepath.IsAbs(path) {
		candidates = []string{filepath.Clean(path)}
	} else {
		for _, base := range bases {
			if base != "" {
				candidates = append(candidates, filepath.Join(base, path))
			}
		}
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, true
		}
	}
	return "", false
}

// ShowContextMenu pops up the chat's macOS context menu at the pointer. The
// page calls it from its contextmenu handler (editable fields keep the
// system's own editing menu). It returns once the menu closes.
func (b *ChatBridge) ShowContextMenu(ctx context.Context, in ChatMenuInput) error {
	var bases []string
	b.shell.callOnWindow(func() {
		if b.shell.chatReadOnly {
			return
		}
		if a := b.shell.paneAgent(b.shell.chatPaneID); a != nil {
			bases = append(bases, a.CWD)
		}
		if b.shell.git != nil {
			bases = append(bases, b.shell.git.root)
		}
	})
	file, haveFile := resolveChatPath(in.Path, bases...)
	rows := chatMenuPlan(in, haveFile)

	win := mygo.CallerWindow(ctx)
	if win == nil {
		win = b.shell.win
	}
	copyText := func(text string) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { mygo.Clipboard.WriteText(text) }
	}
	page := func(action string) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { _ = chatMenu.Emit(b.shell.win, action) }
	}
	actions := map[string]func(*mygo.MenuItem, *mygo.Window){
		"copy":        copyText(in.Selection),
		"copyLink":    copyText(strings.TrimSpace(in.Link)),
		"copyPath":    copyText(file),
		"copyMessage": copyText(in.Message),
		"openLink": func(*mygo.MenuItem, *mygo.Window) {
			go func() { _ = mygo.Shell.OpenExternal(strings.TrimSpace(in.Link)) }()
		},
		"quickLook": func(*mygo.MenuItem, *mygo.Window) { quickLook(file) },
		"selectAll": page("selectAll"),
		"find":      page("find"),
	}
	items := make([]*mygo.MenuItem, 0, len(rows))
	for _, row := range rows {
		if row.ID == "" {
			items = append(items, mygo.Separator())
			continue
		}
		items = append(items, &mygo.MenuItem{Label: row.Label, Click: actions[row.ID]})
	}
	mygo.NewMenu(items).Popup(win)
	return nil
}

// quickLook shows file in macOS Quick Look (qlmanage's preview panel, closed
// with Escape). The viewer is a short-lived child that owns nothing of ours.
func quickLook(file string) {
	cmd := exec.Command("/usr/bin/qlmanage", "-p", file)
	if err := cmd.Start(); err != nil {
		slog.Warn("quick look failed", "error", err)
		return
	}
	go func() { _ = cmd.Wait() }()
}
