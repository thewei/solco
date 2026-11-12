package settings

import (
	"encoding/json"
	"strings"
	"testing"
)

// The island is off by default and costs the settings file nothing until it is turned on.
func TestIslandIsAbsentFromTheFileUntilTurnedOn(t *testing.T) {
	raw, err := json.Marshal(Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "island") {
		t.Fatalf("default settings carry the island key: %s", raw)
	}
	raw, _ = json.Marshal(Settings{Notifications: NotificationSettings{Island: true}})
	if !strings.Contains(string(raw), `"island":true`) {
		t.Fatalf("a turned-on island is not written: %s", raw)
	}
}

// The four options cost nothing in the file until they are turned on, and each is written under its own key.
func TestIslandOptionsAreAbsentUntilTurnedOn(t *testing.T) {
	raw, _ := json.Marshal(Settings{Notifications: NotificationSettings{Island: true}})
	for _, k := range []string{"island_wings", "island_virtual", "island_quiet", "island_visible_in_shares"} {
		if strings.Contains(string(raw), k) {
			t.Fatalf("a default island carries %s: %s", k, raw)
		}
	}
	raw, _ = json.Marshal(Settings{Notifications: NotificationSettings{IslandWings: true, IslandVirtual: true, IslandQuiet: true, IslandVisibleInShares: true}})
	for _, k := range []string{`"island_wings":true`, `"island_virtual":true`, `"island_quiet":true`, `"island_visible_in_shares":true`} {
		if !strings.Contains(string(raw), k) {
			t.Fatalf("%s is not written: %s", k, raw)
		}
	}
}
