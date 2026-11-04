package main

import (
	"reflect"
	"testing"
)

// AUD-11: a fallback spawned by a Coordinator cannot be tied to the lifetime
// of a TUI session. The installed login service already uses --standby.
func TestAssistantBootstrapDaemonIsResident(t *testing.T) {
	if got, want := assistantDaemonArgs(), []string{"daemon", "--standby"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Assistant daemon args=%v want=%v", got, want)
	}
}
