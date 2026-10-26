package herdr

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPluginListJSONAsksHerdrForMachineReadableOutput(t *testing.T) {
	fakeHerdr(t, `echo "$@"`)
	out, err := PluginListJSON(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "plugin list --json" {
		t.Fatalf("argv = %q", out)
	}
}

func TestPluginActionArgsPinTheInvokeShape(t *testing.T) {
	got, err := pluginActionArgs("herdr-projects", "open")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"plugin", "action", "invoke", "open", "--plugin", "herdr-projects"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	for _, bad := range [][2]string{{"", "open"}, {"herdr-projects", ""}, {"  ", "  "}} {
		if _, err := pluginActionArgs(bad[0], bad[1]); err == nil {
			t.Fatalf("pluginActionArgs(%q, %q) should be refused", bad[0], bad[1])
		}
	}
}

// A plugin action runs against this machine's plugin set, so a machine
// instance is refused before any CLI call happens.
func TestInvokePluginActionRefusesRemoteInstances(t *testing.T) {
	log := filepath.Join(t.TempDir(), "calls")
	t.Setenv("PLUGIN_CALL_LOG", log)
	fakeHerdr(t, `echo "$@" >> "$PLUGIN_CALL_LOG"`)
	_, err := (&Manager{}).InvokePluginAction(context.Background(), MachineInstanceKey("vm-1"), "herdr-projects", "open")
	if err == nil || !strings.Contains(err.Error(), "this machine's sessions only") {
		t.Fatalf("err = %v, want the local-only refusal", err)
	}
	if _, statErr := os.Stat(log); statErr == nil {
		t.Fatal("a refused remote action must not reach the Herdr CLI")
	}
}
