package nativeui

import (
	"context"
	"strings"

	"github.com/egoist/mygo"
)

/**
 * [INPUT]: Files 预览页的右键目标（选中文本/链接）、当前被 WebView 层接管的选中文件、mygo.Menu / Clipboard / Shell.OpenExternal
 * [OUTPUT]: FilesPreviewBridge.ShowContextMenu（原生 NSMenu 弹出）、files:menu 事件（页内动作：selectAll）、filesMenuPlan
 * [POS]: Files 预览页的“原生感”，与 chat_menu.go 同一口径：页面屏蔽浏览器菜单，由 macOS 菜单呈现 Copy / Open·Copy Link / Quick Look·Copy Path / Select All。仍只读：文件只取桥自己的当前选中项（不信任页面传路径），不新增 Herdr 方法
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// filesMenu carries the one action only the page can run (selectAll).
var filesMenu = mygo.NewEvent[string]("files:menu")

// FilesMenuInput is what the page knows about the right-click target. The
// file itself is never sent: the bridge uses the selection it owns.
type FilesMenuInput struct {
	Selection string `json:"selection,omitempty"`
	Link      string `json:"link,omitempty"`
}

// filesMenuPlan lists the menu rows; quickLook says the open file resolved.
func filesMenuPlan(in FilesMenuInput, quickLook bool) []chatMenuEntry {
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
	if len(rows) > 0 {
		rows = append(rows, chatMenuEntry{})
	}
	return append(rows, chatMenuEntry{"selectAll", "Select All"})
}

// ShowContextMenu pops up the Files preview's macOS context menu at the
// pointer; it returns once the menu closes.
func (b *FilesPreviewBridge) ShowContextMenu(ctx context.Context, in FilesMenuInput) error {
	var path string
	b.shell.callOnWindow(func() {
		if p := b.shell.rightPanel.selectedFilePath; b.shell.webPreviewOwns(p) {
			path = p
		}
	})
	file, haveFile := resolveChatPath(path)
	win := mygo.CallerWindow(ctx)
	if win == nil {
		win = b.shell.win
	}
	copyText := func(text string) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { mygo.Clipboard.WriteText(text) }
	}
	actions := map[string]func(*mygo.MenuItem, *mygo.Window){
		"copy":     copyText(in.Selection),
		"copyLink": copyText(strings.TrimSpace(in.Link)),
		"copyPath": copyText(file),
		"openLink": func(*mygo.MenuItem, *mygo.Window) {
			go func() { _ = mygo.Shell.OpenExternal(strings.TrimSpace(in.Link)) }()
		},
		"quickLook": func(*mygo.MenuItem, *mygo.Window) { quickLook(file) },
		"selectAll": func(*mygo.MenuItem, *mygo.Window) { _ = filesMenu.Emit(b.shell.win, "selectAll") },
	}
	rows := filesMenuPlan(in, haveFile)
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
