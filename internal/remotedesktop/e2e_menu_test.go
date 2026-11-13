package remotedesktop

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// P3 on a real window: read the shown app's menu bar through the session, and (when SOLCO_RD_E2E_PRESS names
// "Menu/Item" pairs such as "View/Scientific,View/Basic" for an app like Calculator) press them and read the
// bar again to see the check mark move. Same prerequisites as the fit tests:
//
//	SOLCO_RD_E2E=1 SOLCO_REMOTE_HELPER=<the freshly built helper> SOLCO_RD_E2E_APP=Calculator SOLCO_RD_E2E_PRESS=View/Scientific,View/Basic \
//	  go test ./internal/remotedesktop -run TestRealWindowMenu -v
func (h *fitHarness) menu() protocol.WindowMenu {
	h.t.Helper()
	_ = h.control.SendText(`{"type":"window.menu.get"}`)
	var m protocol.WindowMenu
	if err := json.Unmarshal(h.next("window.menu", 15*time.Second), &m); err != nil {
		h.t.Fatal(err)
	}
	return m
}

func findMenuItem(m protocol.WindowMenu, menu, item string) (path []int, it protocol.MenuItem, ok bool) {
	for i, top := range m.Items {
		if top.Title != menu {
			continue
		}
		for j, c := range top.Children {
			if c.Title == item {
				return []int{i, j}, c, true
			}
		}
	}
	return nil, protocol.MenuItem{}, false
}

func TestRealWindowMenu(t *testing.T) {
	h := openFitted(t)
	m := h.menu()
	if m.WindowID != h.window.ID || len(m.Items) < 2 {
		t.Fatalf("menu = %d items for window %q", len(m.Items), m.WindowID)
	}
	titles := make([]string, 0, len(m.Items))
	for _, i := range m.Items {
		titles = append(titles, i.Title)
	}
	t.Logf("%s menus: %s", h.window.App, strings.Join(titles, " | "))

	press := os.Getenv("SOLCO_RD_E2E_PRESS")
	for _, pair := range strings.Split(press, ",") {
		menu, item, ok := strings.Cut(pair, "/")
		if !ok {
			continue
		}
		path, _, found := findMenuItem(m, menu, item)
		if !found {
			t.Fatalf("no %s ▸ %s", menu, item)
		}
		raw, _ := json.Marshal(path)
		_ = h.control.SendText(`{"type":"window.menu.invoke","path":` + string(raw) + `}`)
		deadline := time.Now().Add(5 * time.Second)
		for {
			m = h.menu()
			if _, it, _ := findMenuItem(m, menu, item); it.Checked {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s ▸ %s was pressed but never became checked", menu, item)
			}
			time.Sleep(300 * time.Millisecond)
		}
		t.Logf("pressed %s ▸ %s (path %s)", menu, item, strconv.Quote(string(raw)))
	}

	// a path past the end is refused, not pressed on a neighbour
	_ = h.control.SendText(`{"type":"window.menu.invoke","path":[0,999]}`)
	var e protocol.ErrorMsg
	if err := json.Unmarshal(h.next("error", 10*time.Second), &e); err != nil || e.Code != protocol.CodeMenuItemUnavailable {
		t.Fatalf("out-of-range press = %+v %v", e, err)
	}
	_ = h.control.SendText(`{"type":"session.end"}`)
	h.wantRestored(10 * time.Second)
}
