package herdr

import (
	"context"
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
