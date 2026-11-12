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
