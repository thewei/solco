package session

import (
	"context"
	"sync"
	"testing"

	"github.com/thewei/solco/internal/remotedesktop/platform"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// zoomCap is a fitCap whose windows can be zoomed like the helper does: steps count per window, never below zero,
// and a restore (any outcome but gone) hands them back.
type zoomCap struct {
	*fitCap
	zmu   sync.Mutex
	steps map[string]int
	zerr  error
	dirs  []string
}

func (c *zoomCap) ZoomWindow(_ context.Context, id, dir string) (int, error) {
	c.zmu.Lock()
	defer c.zmu.Unlock()
	if c.zerr != nil {
		return 0, c.zerr
	}
	if c.steps == nil {
		c.steps = map[string]int{}
	}
	c.dirs = append(c.dirs, dir)
	switch {
	case dir == protocol.ZoomIn:
		c.steps[id]++
	case c.steps[id] > 0:
		c.steps[id]--
	}
	return c.steps[id], nil
}

func (c *zoomCap) RestoreWindow(ctx context.Context, id string) (protocol.RestoreOutcome, error) {
	c.zmu.Lock()
	delete(c.steps, id)
	c.zmu.Unlock()
	return c.fitCap.RestoreWindow(ctx, id)
}

func (c *zoomCap) zoomOf(id string) int {
	c.zmu.Lock()
	defer c.zmu.Unlock()
	return c.steps[id]
}

func newZoomRig(t *testing.T, mode protocol.Mode) (*fitRig, *zoomCap) {
	t.Helper()
	var zc *zoomCap
	f := newFitRigWith(t, mode, func(fc *fitCap) platform.Capturer {
		zc = &zoomCap{fitCap: fc}
		return zc
	})
	return f, zc
}

func (f *fitRig) openWindow(t *testing.T) {
	t.Helper()
	f.s.OnLinkState(LinkConnected)
	f.s.OnControl(ctl("target.select", "kind", "window", "id", "11"))
	waitFor(t, "window capture", func() bool { return f.fc.startCount() == 1 })
}

func TestZoomCountsStepsAndTellsThePhone(t *testing.T) {
	f, zc := newZoomRig(t, protocol.ModeFullControl)
	f.openWindow(t)
	f.s.OnControl(ctl("window.zoom", "dir", "in"))
	f.s.OnControl(ctl("window.zoom", "dir", "in"))
	waitFor(t, "two steps", func() bool { return f.s.State().Zoom == 2 })
	f.s.OnControl(ctl("window.zoom", "dir", "out"))
	waitFor(t, "one step", func() bool { return f.s.State().Zoom == 1 && zc.zoomOf("11") == 1 })
	if st := f.lastOf("session.state"); st == nil {
		t.Fatal("the phone was not sent the new state")
	}
}

func TestZoomIsGivenBackWhereverTheWindowIsLetGo(t *testing.T) {
	// back (session end), a window switch, a display switch, the window's own end, the Mac's Disconnect: all restore
	for name, leave := range map[string]func(f *fitRig){
		"session end":    func(f *fitRig) { f.s.OnControl(ctl("session.end")) },
		"window switch":  func(f *fitRig) { f.s.OnControl(ctl("target.select", "kind", "window", "id", "12")) },
		"display":        func(f *fitRig) { f.s.OnControl(ctl("target.select", "kind", "display", "id", f.s.State().DisplayID)) },
		"mac disconnect": func(f *fitRig) { f.s.Stop() },
	} {
		f, zc := newZoomRig(t, protocol.ModeFullControl) // never fitted: zoom alone makes the window one to give back
		f.openWindow(t)
		f.s.OnControl(ctl("window.zoom", "dir", "in"))
		waitFor(t, name+": zoomed", func() bool { return f.s.State().Zoom == 1 })
		leave(f)
		waitFor(t, name+": given back", func() bool { return zc.zoomOf("11") == 0 && len(f.fc.restored()) == 1 })
		if st := f.s.State(); st.Zoom != 0 && name != "session end" && name != "mac disconnect" { // a closed session's state is gone with it
			t.Errorf("%s: the state kept zoom %d", name, st.Zoom)
		}
	}
}

func TestAZoomedWindowOfALostLinkWaitsForTheGraceLikeAFittedOne(t *testing.T) {
	f, _ := newZoomRig(t, protocol.ModeFullControl)
	f.openWindow(t)
	f.s.OnControl(ctl("window.zoom", "dir", "in"))
	waitFor(t, "zoomed", func() bool { return f.s.State().Zoom == 1 })
	f.s.OnLinkState(LinkClosed)
	waitFor(t, "ended", func() bool { return len(f.end) == 1 })
	if got := f.deferred(); len(got) != 1 || got[0].id != "11" || got[0].grace != FitGrace {
		t.Fatalf("deferred = %+v", got)
	}
	if got := f.fc.restored(); len(got) != 0 {
		t.Fatalf("restored at once: %v", got)
	}
}

func TestFitOffGivesTheZoomBackToo(t *testing.T) {
	f, zc := newZoomRig(t, protocol.ModeFullControl)
	f.openFitted(t)
	f.s.OnControl(ctl("window.zoom", "dir", "in"))
	waitFor(t, "zoomed", func() bool { return f.s.State().Zoom == 1 })
	f.s.OnControl(ctl("window.fit.off"))
	waitFor(t, "original", func() bool { return !f.s.State().Fitted && f.s.State().Zoom == 0 && zc.zoomOf("11") == 0 })
}

func TestViewOnlyCannotZoomAndAnAppWithoutTheItemSaysSo(t *testing.T) {
	v, zc := newZoomRig(t, protocol.ModeViewOnly)
	v.openWindow(t)
	v.s.OnControl(ctl("window.zoom", "dir", "in"))
	waitFor(t, "view_only", func() bool { return v.lastOf("error") != nil })
	if len(zc.dirs) != 0 {
		t.Fatalf("a view-only session pressed a menu item: %v", zc.dirs)
	}

	f, zc2 := newZoomRig(t, protocol.ModeFullControl)
	zc2.zerr = protocol.Errf(protocol.CodeMenuItemUnavailable, "")
	f.openWindow(t)
	f.s.OnControl(ctl("window.zoom", "dir", "in"))
	waitFor(t, "menu_item_unavailable", func() bool { return f.lastOf("error") != nil })
	if st := f.s.State(); st.Zoom != 0 {
		t.Fatalf("a refused zoom was counted: %+v", st)
	}
}

func TestZoomOnADisplaySessionIsABadRequest(t *testing.T) {
	f, _ := newZoomRig(t, protocol.ModeFullControl)
	f.s.OnLinkState(LinkConnected)
	f.s.OnControl(ctl("window.zoom", "dir", "in"))
	waitFor(t, "bad_request", func() bool { return f.lastOf("error") != nil })
}
