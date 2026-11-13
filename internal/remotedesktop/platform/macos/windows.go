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
