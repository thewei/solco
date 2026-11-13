package macos

import (
	"context"

	"github.com/thewei/solco/internal/remotedesktop/helperipc"
	"github.com/thewei/solco/internal/remotedesktop/platform"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// WindowsSupported: the helper embedded with this app always knows window mode, so a usable helper is enough.
func (b *Backend) WindowsSupported() bool {
	ok, _ := b.Available()
	return ok
}

var (
	_ platform.WindowSupport   = (*Backend)(nil)
	_ platform.Windows         = (*capturer)(nil)
	_ platform.WindowFitter    = (*capturer)(nil)
	_ platform.WindowMenu      = (*capturer)(nil)
	_ platform.WindowZoomer    = (*capturer)(nil)
	_ platform.JournalRestorer = (*Backend)(nil)
)

// WindowsSupported: an older helper that did not announce the three window capabilities offers none
// of it (the session then stays on display mode).
func (c *capturer) WindowsSupported() bool {
	have := map[string]bool{}
	for _, name := range c.conn.Hello.Capabilities {
		have[name] = true
	}
	return have[helperipc.CapWindowCapture] && have[helperipc.CapWindowCatalog] && have[helperipc.CapAppCatalog]
}

func (c *capturer) ListWindows(ctx context.Context) ([]protocol.WindowInfo, error) {
	var out helperipc.WindowsResult
	if err := c.conn.Call(ctx, helperipc.OpWindowsList, nil, &out); err != nil {
		return nil, asProtocolErr(err)
	}
	return out.Windows, nil
}

func (c *capturer) ListApps(ctx context.Context) ([]protocol.AppInfo, error) {
	var out helperipc.AppsResult
	if err := c.conn.Call(ctx, helperipc.OpAppsList, nil, &out); err != nil {
		return nil, asProtocolErr(err)
	}
	return out.Apps, nil
}

func (c *capturer) AppIcon(ctx context.Context, bundleID string) (string, error) {
	var out helperipc.AppIconResult
	if err := c.conn.Call(ctx, helperipc.OpAppIcon, helperipc.BundleArgs{BundleID: bundleID}, &out); err != nil {
		return "", asProtocolErr(err)
	}
	return out.PNG, nil
}

func (c *capturer) OpenApp(ctx context.Context, bundleID string) (string, error) {
	var out helperipc.AppOpenResult
	if err := c.conn.Call(ctx, helperipc.OpAppOpen, helperipc.BundleArgs{BundleID: bundleID}, &out); err != nil {
		return "", asProtocolErr(err)
	}
	return out.WindowID, nil
}

func (c *capturer) RaiseWindow(ctx context.Context, windowID string) error {
	return asProtocolErr(c.conn.Call(ctx, helperipc.OpWindowRaise, helperipc.WindowArgs{WindowID: windowID}, nil))
}

func (c *capturer) WindowThumbnail(ctx context.Context, windowID string, maxWidth int) (string, error) {
	var out helperipc.ThumbResult
	if err := c.conn.Call(ctx, helperipc.OpWindowThumb, helperipc.ThumbArgs{WindowID: windowID, MaxWidth: maxWidth}, &out); err != nil {
		return "", asProtocolErr(err)
	}
	return out.JPEG, nil
}

// FitWindow resizes the window to w×h points; the helper journals the original frame first and reads the
// result back, so a refusing app shows as clamped (or fit_unavailable) rather than as success.
func (c *capturer) FitWindow(ctx context.Context, windowID string, w, h float64) (protocol.Rect, bool, error) {
	var out helperipc.FitResult
	if err := c.conn.Call(ctx, helperipc.OpWindowFit, helperipc.FitArgs{WindowID: windowID, Width: w, Height: h}, &out); err != nil {
		return protocol.Rect{}, false, asProtocolErr(err)
	}
	return out.Applied, out.Clamped, nil
}

func (c *capturer) RestoreWindow(ctx context.Context, windowID string) (protocol.RestoreOutcome, error) {
	var out helperipc.RestoreResult
	if err := c.conn.Call(ctx, helperipc.OpWindowRestore, helperipc.RestoreArgs{WindowID: windowID}, &out); err != nil {
		return "", asProtocolErr(err)
	}
	for _, r := range out.Results {
		if r.WindowID == windowID {
			return protocol.RestoreOutcome(r.Outcome), nil
		}
	}
	return protocol.RestoreNone, nil
}

// RestoreJournal gives back every window the journal still lists (the helper's one-shot `restore-journal`).
func (b *Backend) RestoreJournal(ctx context.Context) error {
	var out helperipc.RestoreResult
	return b.oneShot(ctx, &out, "restore-journal")
}

// WindowMenu reads the menu bar of the app that owns the window; the helper bounds it (items, depth, time).
func (c *capturer) WindowMenu(ctx context.Context, windowID string) ([]protocol.MenuItem, error) {
	var out helperipc.MenuResult
	if err := c.conn.Call(ctx, helperipc.OpWindowMenu, helperipc.WindowArgs{WindowID: windowID}, &out); err != nil {
		return nil, asProtocolErr(err)
	}
	return menuItems(out.Items), nil
}

func (c *capturer) InvokeMenu(ctx context.Context, windowID string, path []int) error {
	return asProtocolErr(c.conn.Call(ctx, helperipc.OpWindowMenuInvoke, helperipc.MenuInvokeArgs{WindowID: windowID, Path: path}, nil))
}

// ZoomWindow presses the app's Zoom In or Zoom Out once; the helper finds the item by its key, journals the step
// first and answers with the net steps.
func (c *capturer) ZoomWindow(ctx context.Context, windowID, dir string) (int, error) {
	var out helperipc.ZoomResult
	if err := c.conn.Call(ctx, helperipc.OpWindowZoom, helperipc.ZoomArgs{WindowID: windowID, Dir: dir}, &out); err != nil {
		return 0, asProtocolErr(err)
	}
	return out.Steps, nil
}

func menuItems(in []helperipc.MenuItem) []protocol.MenuItem {
	out := make([]protocol.MenuItem, 0, len(in))
	for _, m := range in {
		item := protocol.MenuItem{Title: m.Title, Enabled: m.Enabled, Checked: m.Checked, Separator: m.Separator, Shortcut: m.Shortcut}
		if m.Children != nil {
			// a submenu the helper could not read (time, depth) must not look like a leaf the phone would press
			if len(m.Children) == 0 {
				item.Enabled = false
			} else {
				item.Children = menuItems(m.Children)
			}
		}
		out = append(out, item)
	}
	return out
}
