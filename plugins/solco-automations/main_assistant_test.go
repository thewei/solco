package main

import (
	"reflect"
	"testing"
)

// AUD-11: a fallback spawned by a Coordinator cannot be tied to the lifetime
// of a TUI session. The installed login service already uses --standby.
func TestAssistantMutationsWakeSchedulerButPollingDoesNot(t *testing.T) {
	cases := []struct {
		label string
		args  []string
		want  bool
	}{
		{"create Assistant", []string{"create"}, true},
		{"enable Assistant", []string{"enable", "a-1"}, true},
		{"new Task", []string{"task", "create"}, true},
		{"retry Task", []string{"task", "retry", "t-1"}, true},
		{"publish Task", []string{"task", "publish", "t-1"}, true},
		{"start Coordinator", []string{"coordinator", "start", "a-1"}, true},
		{"create Routine", []string{"routine", "draft"}, true},
		{"resume Routine", []string{"routine", "resume", "routine-name"}, true},
		{"Owner approval", []string{"approval", "decide", "ap-1"}, true},
		{"new Group message", []string{"group", "post"}, true},
		{"Agent Group message", []string{"group", "say"}, true},
		{"Goal plan", []string{"goal", "set-plan", "g-1"}, true},
		{"Heartbeat wake", []string{"heartbeat", "request-wake", "a-1"}, true},
		{"rule adoption", []string{"rule", "adopt"}, true},
		{"Assistant list polling", []string{"list"}, false},
		{"Assistant overview polling", []string{"overview"}, false},
		{"Task list polling", []string{"task", "list"}, false},
		{"Coordinator status", []string{"coordinator", "show", "a-1"}, false},
		{"Routine list polling", []string{"routine", "list"}, false},
		{"Group feed polling", []string{"group", "show", "g-1"}, false},
		{"Approval list polling", []string{"approval", "list"}, false},
		{"Invalid command", []string{"invalid"}, false},
		{"Empty command", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			if got := assistantNeedsDaemon(tc.args); got != tc.want {
				t.Fatalf("assistantNeedsDaemon(%v)=%v want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestAssistantBootstrapDaemonIsResident(t *testing.T) {
	if got, want := assistantDaemonArgs(), []string{"daemon", "--standby"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Assistant daemon args=%v want=%v", got, want)
	}
}
