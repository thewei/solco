package macos

import (
	"testing"

	"github.com/thewei/solco/internal/remotedesktop/helperipc"
)

func TestMenuItemsMarkAnUnreadSubmenuDisabled(t *testing.T) {
	got := menuItems([]helperipc.MenuItem{
		{Title: "Open", Enabled: true},
		{Title: "Share", Enabled: true, Children: []helperipc.MenuItem{}},
		{Title: "Find", Enabled: true, Children: []helperipc.MenuItem{{Title: "Next", Enabled: true, Shortcut: "⌘G"}}},
	})
	if !got[0].Enabled || got[0].Children != nil {
		t.Fatalf("leaf = %+v", got[0])
	}
	if got[1].Enabled || got[1].Children != nil {
		t.Fatalf("an unread submenu must not look like a leaf: %+v", got[1])
	}
	if !got[2].Enabled || len(got[2].Children) != 1 || got[2].Children[0].Shortcut != "⌘G" {
		t.Fatalf("submenu = %+v", got[2])
	}
}
