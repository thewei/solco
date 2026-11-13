package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// menuCap is a winCap that can read and press a menu bar.
type menuCap struct {
	*winCap
	mu4     sync.Mutex
	items   []protocol.MenuItem
	listErr error
	invErr  error
	pressed [][]int
	asked   []string
}

func (c *menuCap) WindowMenu(_ context.Context, id string) ([]protocol.MenuItem, error) {
	c.mu4.Lock()
	defer c.mu4.Unlock()
	c.asked = append(c.asked, id)
	return c.items, c.listErr
}

func (c *menuCap) InvokeMenu(_ context.Context, _ string, path []int) error {
	c.mu4.Lock()
	defer c.mu4.Unlock()
	c.pressed = append(c.pressed, append([]int(nil), path...))
	return c.invErr
}

func (c *menuCap) presses() [][]int {
	c.mu4.Lock()
	defer c.mu4.Unlock()
	return append([][]int(nil), c.pressed...)
}

func newMenuRig(t *testing.T, mode protocol.Mode) (*rig, *menuCap) {
	t.Helper()
	wc := &winCap{fakeCap: newFakeCap(), windows: []protocol.WindowInfo{
		{ID: "11", App: "Notes", BundleID: "com.apple.Notes", Title: "Groceries", X: 100, Y: 100, Width: 500, Height: 800, OnScreen: true},
	}, openID: "11"}
	mc := &menuCap{winCap: wc, items: []protocol.MenuItem{
		{Title: "File", Enabled: true, Children: []protocol.MenuItem{{Title: "New Note", Enabled: true, Shortcut: "⌘N"}, {Separator: true}}},
	}}
	r := &rig{t: t, cap: wc.fakeCap, link: &fakeLink{}, end: make(chan protocol.ErrorCode, 1)}
	r.s = New(Config{
		ID: "s1", Device: protocol.DeviceInfo{ID: "d1", Name: "Phone"}, Mode: mode, Tier: protocol.TierStandard, Idle: true,
		Capturer: mc, OnEnd: func(c protocol.ErrorCode) { r.end <- c },
	})
	if _, err := r.s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.s.Attach(r.link)
	t.Cleanup(func() { r.s.Close(protocol.CodeSessionEnded) })
	r.s.OnLinkState(LinkConnected)
	r.s.OnControl(ctl("target.select", "kind", "window", "id", "11"))
	waitFor(t, "window capture", func() bool { return wc.startCount() == 1 })
	return r, mc
}

func (r *rig) errorCode() string {
	var e struct{ Code string }
	_ = json.Unmarshal(r.lastOf("error"), &e)
	return e.Code
}

func TestMenuGetAnswersWithTheShownWindowsMenuInViewOnlyToo(t *testing.T) {
	r, mc := newMenuRig(t, protocol.ModeViewOnly)
	r.s.OnControl(ctl("window.menu.get"))
	waitFor(t, "menu", func() bool { return r.has("window.menu") })
	var got protocol.WindowMenu
	if err := json.Unmarshal(r.lastOf("window.menu"), &got); err != nil || got.WindowID != "11" || len(got.Items) != 1 || got.Items[0].Children[0].Shortcut != "⌘N" {
		t.Fatalf("menu = %+v err=%v", got, err)
	}
	if len(mc.asked) != 1 || mc.asked[0] != "11" {
		t.Fatalf("asked for %v", mc.asked)
	}
}

func TestMenuGetErrorsReachThePhone(t *testing.T) {
	r, mc := newMenuRig(t, protocol.ModeFullControl)
	mc.listErr = protocol.Errf(protocol.CodeMenuUnavailable, "")
	r.s.OnControl(ctl("window.menu.get"))
	waitFor(t, "error", func() bool { return r.errorCode() == "menu_unavailable" })
}

func TestMenuInvokePressesThePathInFullControlOnly(t *testing.T) {
	r, mc := newMenuRig(t, protocol.ModeFullControl)
	r.s.OnControl(ctl("window.menu.invoke", "path", []int{0, 0}))
	waitFor(t, "press", func() bool { return len(mc.presses()) == 1 })
	if p := mc.presses()[0]; len(p) != 2 || p[0] != 0 || p[1] != 0 {
		t.Fatalf("pressed %v", p)
	}
	mc.invErr = protocol.Errf(protocol.CodeMenuItemUnavailable, "")
	r.s.OnControl(ctl("window.menu.invoke", "path", []int{0, 1}))
	waitFor(t, "item error", func() bool { return r.errorCode() == "menu_item_unavailable" })

	v, vc := newMenuRig(t, protocol.ModeViewOnly)
	v.s.OnControl(ctl("window.menu.invoke", "path", []int{0, 0}))
	waitFor(t, "view only", func() bool { return v.errorCode() == "view_only" })
	if len(vc.presses()) != 0 {
		t.Fatalf("a view-only session pressed %v", vc.presses())
	}
}

func TestMenuInvokeRejectsBadPathsBeforeTheBackend(t *testing.T) {
	r, mc := newMenuRig(t, protocol.ModeFullControl)
	for _, p := range [][]int{{}, {0}, {-1, 0}, {0, 0, 0, 0, 0}, {0, 5000}} {
		r.s.OnControl(ctl("window.menu.invoke", "path", p))
	}
	waitFor(t, "bad request", func() bool { return r.errorCode() == "bad_request" })
	time.Sleep(40 * time.Millisecond)
	if len(mc.presses()) != 0 {
		t.Fatalf("bad paths reached the backend: %v", mc.presses())
	}
}

func TestMenuNeedsAWindowTarget(t *testing.T) {
	r, _ := newMenuRig(t, protocol.ModeFullControl)
	r.s.OnControl(ctl("target.select", "kind", "display", "id", "1"))
	waitFor(t, "display", func() bool { return r.s.State().Target != protocol.TargetWindow })
	r.s.OnControl(ctl("window.menu.get"))
	waitFor(t, "no window", func() bool { return r.errorCode() == "bad_request" })
}

func TestAHugeMenuIsCutFromTheTailToFitOneMessage(t *testing.T) {
	var items []protocol.MenuItem
	for i := 0; i < 40; i++ {
		var kids []protocol.MenuItem
		for j := 0; j < 8; j++ {
			kids = append(kids, protocol.MenuItem{Title: fmt.Sprintf("a long menu item title number %d-%d with some more words to fill it up", i, j), Enabled: true, Shortcut: "⌥⇧⌘K"})
		}
		items = append(items, protocol.MenuItem{Title: fmt.Sprintf("Menu %d", i), Enabled: true, Children: kids})
	}
	b, ok := marshalMenu("11", items)
	if !ok || len(b) > protocol.MaxMenuBytes {
		t.Fatalf("ok=%v len=%d", ok, len(b))
	}
	var got protocol.WindowMenu
	if err := json.Unmarshal(b, &got); err != nil || len(got.Items) == 0 || got.Items[0].Title != "Menu 0" {
		t.Fatalf("head not kept: %v", err)
	}
}

func TestACutSubmenuIsNotLeftLookingLikeALeaf(t *testing.T) {
	deep := protocol.MenuItem{Title: "A", Enabled: true, Children: []protocol.MenuItem{{Title: "B", Enabled: true, Children: []protocol.MenuItem{{Title: "C", Enabled: true, Children: []protocol.MenuItem{{Title: "D", Enabled: true}}}}}}}
	left := 100
	got := limitMenu([]protocol.MenuItem{deep}, &left, 0)
	c := got[0].Children[0].Children[0]
	if c.Title != "C" || c.Enabled || len(c.Children) != 0 {
		t.Fatalf("the level past the depth limit = %+v, want a disabled C", c)
	}
	left = 2 // A and B fit, B's children do not
	got = limitMenu([]protocol.MenuItem{deep}, &left, 0)
	if b := got[0].Children[0]; b.Enabled || len(b.Children) != 0 {
		t.Fatalf("a submenu cut to nothing = %+v, want it disabled", b)
	}
}
