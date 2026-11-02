package app

import (
	"testing"

	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/remotehost"
)

func TestMachineStatusWords(t *testing.T) {
	m := herdr.Machine{ID: "a", Enabled: true}
	cases := []struct {
		name    string
		machine herdr.Machine
		bridged bool
		states  map[string]herdr.MachineState
		want    string
	}{
		{"disabled wins", herdr.Machine{ID: "a"}, true, nil, remotehost.MachineDisabled},
		{"bridge up", m, true, nil, remotehost.MachineConnected},
		{"no verdict yet", m, false, nil, remotehost.MachineChecking},
		{"reachable", m, false, map[string]herdr.MachineState{"a": {Reachable: true, Status: "reachable"}}, remotehost.MachineOnline},
		{"auth required", m, false, map[string]herdr.MachineState{"a": {Attention: true, Status: "auth required"}}, remotehost.MachineNeedsSignIn},
		{"host key", m, false, map[string]herdr.MachineState{"a": {Attention: true, Status: "error", Message: "Host key verification failed."}}, remotehost.MachineNeedsSignIn},
		{"network", m, false, map[string]herdr.MachineState{"a": {Attention: true, Status: "unreachable", Message: "Connection timed out"}}, remotehost.MachineUnreachable},
	}
	for _, c := range cases {
		if got := machineStatus(c.machine, c.bridged, c.states); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
