package herdr

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMachineInstanceKeyRoundTrip(t *testing.T) {
	key := MachineInstanceKey("prof-1")
	if key != "machine/prof-1" {
		t.Fatalf("key = %q", key)
	}
	ref := ParseRef(key)
	if !ref.IsRemote() || ref.Machine != "prof-1" || ref.Session != "" {
		t.Fatalf("ParseRef(machine key) = %+v", ref)
	}
	local := ParseRef("work")
	if local.IsRemote() || local.Session != "work" {
		t.Fatalf("ParseRef(local key) = %+v", local)
	}
	if got := ParseRef("").Session; got != defaultSession {
		t.Fatalf("empty key should normalize to default session, got %q", got)
	}
}

func TestGuardLocalInstanceRejectsMachineKeys(t *testing.T) {
	if err := guardLocalInstance(MachineInstanceKey("prof-1"), "starting a Herdr session"); err == nil {
		t.Fatal("expected machine key to be rejected")
	}
	if err := guardLocalInstance("work", "starting a Herdr session"); err != nil {
		t.Fatalf("local session should pass the guard: %v", err)
	}
	if NewManager().EnsureRunning(MachineInstanceKey("prof-1")) == nil {
		t.Fatal("EnsureRunning must reject machine keys")
	}
	if _, err := NewManager().RenameInstance(MachineInstanceKey("prof-1"), "x"); err == nil {
		t.Fatal("RenameInstance must reject machine keys")
	}
	if NewManager().DeleteInstance(MachineInstanceKey("prof-1")) == nil {
		t.Fatal("DeleteInstance must reject machine keys")
	}
}

func TestDecodeMachineListShapes(t *testing.T) {
	// Bare array with aliased field names.
	doc := []map[string]any{{
		"profile_id":     "p1",
		"label":          "Build machine",
		"ssh_target":     "deploy@workbox",
		"remote_session": "agents",
		"enabled":        false,
	}}
	raw, _ := json.Marshal(doc)
	machines := decodeMachineList(raw)
	if len(machines) != 1 {
		t.Fatalf("machines = %d", len(machines))
	}
	m := machines[0]
	if m.ID != "p1" || m.Label != "Build machine" || m.Target != "deploy@workbox" || m.Session != "agents" || m.Enabled {
		t.Fatalf("machine = %+v", m)
	}

	// Wrapped object, missing enabled defaults to enabled, label falls back
	// to the target.
	wrapped := map[string]any{"machines": []any{map[string]any{
		"id":     "p2",
		"target": "gpu-box",
	}}}
	raw, _ = json.Marshal(wrapped)
	machines = decodeMachineList(raw)
	if len(machines) != 1 || machines[0].ID != "p2" || !machines[0].Enabled || machines[0].Label != "gpu-box" {
		t.Fatalf("machines = %+v", machines)
	}

	// Documents without ids are skipped; garbage decodes empty.
	if got := decodeMachineList([]byte(`[{"label":"no id"}]`)); len(got) != 0 {
		t.Fatalf("expected no machines without ids, got %+v", got)
	}
	if got := decodeMachineList([]byte("not json")); len(got) != 0 {
		t.Fatalf("garbage should decode to no machines, got %+v", got)
	}
}

func TestDecodeMachineStatesShapes(t *testing.T) {
	doc := []map[string]any{
		{"profile_id": "p1", "reachable": true},
		{"profile_id": "p2", "reachable": false, "error": "auth required"},
		{"profile_id": "p3", "state": "reachable"},
		{"profile_id": "p4", "state": "attention (! auth)"},
		{"profile_id": "p5"},
	}
	raw, _ := json.Marshal(doc)
	states := decodeMachineStates(raw)
	if len(states) != 5 {
		t.Fatalf("states = %d", len(states))
	}
	if !states["p1"].Reachable || states["p1"].Attention {
		t.Fatalf("p1 = %+v", states["p1"])
	}
	if states["p2"].Reachable || !states["p2"].Attention || states["p2"].Message != "auth required" {
		t.Fatalf("p2 = %+v", states["p2"])
	}
	if !states["p3"].Reachable {
		t.Fatalf("p3 = %+v", states["p3"])
	}
	if !states["p4"].Attention || states["p4"].Reachable {
		t.Fatalf("p4 = %+v", states["p4"])
	}
	if states["p5"] != (MachineState{}) {
		t.Fatalf("unknown fact must stay zero state, p5 = %+v", states["p5"])
	}
}

// The shapes below were captured from `herdr machine status --json` (0.9.5)
// against a loopback sshd fixture: reachable, auth refused, network refused.
func TestDecodeMachineStatesHerdrVerdicts(t *testing.T) {
	raw := []byte(`[
  {"id":"a1","label":"fixture-a","status":"reachable","error":null},
  {"id":"b2","label":"fixture-b","status":"auth required","error":"remote platform detection failed: wilson@127.0.0.1: Permission denied (publickey)."},
  {"id":"c3","label":"fixture-c","status":"error","error":"remote platform detection failed: ssh: connect to host 127.0.0.1 port 22222: Connection refused"},
  {"id":"d4","label":"fixture-d","status":"unreachable","error":"timed out"}
]`)
	states := decodeMachineStates(raw)
	if s := states["a1"]; !s.Reachable || s.Attention || s.Status != "reachable" || s.Message != "" {
		t.Errorf("reachable verdict = %+v", s)
	}
	if s := states["b2"]; s.Reachable || !s.Attention || s.Status != "auth required" || ClassifyFailure(s.Message) != FailureAuth {
		t.Errorf("auth verdict = %+v", s)
	}
	if s := states["c3"]; s.Reachable || !s.Attention || s.Status != "error" || ClassifyFailure(s.Message) != FailureNetwork {
		t.Errorf("network verdict = %+v", s)
	}
	// "unreachable" contains "reachable": it must still read as a failure.
	if s := states["d4"]; s.Reachable || !s.Attention {
		t.Errorf("unreachable verdict = %+v", s)
	}
}

func TestMachineStatusAndEnableDisableArgv(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	fakeHerdr(t, `echo "$@" >> `+shellQuote(log)+`
case "$2" in
  status) echo '[{"id":"abc","label":"box","status":"reachable","error":null}]';;
esac`)
	m := NewManager()
	state, err := m.MachineStatus("abc")
	if err != nil || !state.Reachable {
		t.Fatalf("MachineStatus = %+v, %v", state, err)
	}
	if err := m.EnableMachine("abc"); err != nil {
		t.Fatal(err)
	}
	if err := m.DisableMachine("abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.MachineStates(); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(log)
	want := "machine status abc --json\nmachine enable abc\nmachine disable abc\nmachine status --json\n"
	if string(body) != want {
		t.Fatalf("argv log:\n%s\nwant:\n%s", body, want)
	}
	if _, err := m.MachineStatus("missing"); err == nil {
		t.Fatal("a verdict-less answer must be an error, not a silent zero state")
	}
}

// The CLI exits 1 while any profile is in error (observed on 0.9.5) but still
// prints every verdict; they must not be thrown away with the exit status.
func TestMachineStatusKeepsTheVerdictsOfAnExitingCLI(t *testing.T) {
	fakeHerdr(t, `cat <<'JSON'
[
  {"id":"a1","label":"fine","status":"reachable","error":null},
  {"id":"b2","label":"down","status":"error","error":"remote platform detection failed: ssh: connect to host 127.0.0.1 port 22222: Connection refused"}
]
JSON
exit 1`)
	m := NewManager()
	states, err := m.MachineStates()
	if err != nil || len(states) != 2 || !states["a1"].Reachable || !states["b2"].Attention {
		t.Fatalf("MachineStates = %+v, %v", states, err)
	}
	down, err := m.MachineStatus("b2")
	if err != nil || down.Reachable || !down.Attention || ClassifyFailure(down.Message) != FailureNetwork {
		t.Fatalf("MachineStatus(b2) = %+v, %v", down, err)
	}

	// No verdicts at all is still a failure that says why.
	fakeHerdr(t, `echo "boom" >&2
exit 1`)
	if _, err := m.MachineStates(); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("a verdict-less failure must surface the CLI's message, got %v", err)
	}
}

func TestBridgeSocketPaths(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "solco-bridge-paths-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)

	got := bridgeSocketPath("deploy@workbox", "")
	if want := filepath.Join(home, ".solco", "ssh-bridges", "deploy_workbox_default.sock"); got != want {
		t.Fatalf("bridgeSocketPath = %q, want %q", got, want)
	}
	named := bridgeSocketPath("Deploy@WorkBox", "agents")
	if named == got {
		t.Fatal("different (target, session) pairs must not share a bridge socket")
	}
	// The slug is sanitized: no separators survive.
	if base := filepath.Base(named); base != "deploy_workbox_agents.sock" {
		t.Fatalf("slug = %q", base)
	}
}

// `herdr terminal attach` finds the client socket next to the API socket it is
// given, so a bridge that forwards only the API socket attaches nothing: both
// ends of the bridge must carry the pair.
func TestBridgeForwardsCarryTheClientSocketBesideTheAPISocket(t *testing.T) {
	if got, want := clientSocketPath("/h/.config/herdr/herdr.sock"), "/h/.config/herdr/herdr-client.sock"; got != want {
		t.Fatalf("clientSocketPath(default) = %q, want %q", got, want)
	}
	if got, want := clientSocketPath("/b/deploy_workbox_default.sock"), "/b/deploy_workbox_default-client.sock"; got != want {
		t.Fatalf("clientSocketPath(bridge) = %q, want %q", got, want)
	}
	got := bridgeForwards("/b/deploy_workbox_agents.sock", remoteSocketPath("/Users/deploy", "agents"))
	want := []string{
		"/b/deploy_workbox_agents.sock:/Users/deploy/.config/herdr/sessions/agents/herdr.sock",
		"/b/deploy_workbox_agents-client.sock:/Users/deploy/.config/herdr/sessions/agents/herdr-client.sock",
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("bridgeForwards = %q, want %q", got, want)
	}
}

// A forgotten or disabled device must not keep its ssh connection: CloseBridge
// ends that machine's bridge (process, both sockets, registry entry) and
// leaves every other machine's alone.
func TestCloseBridgeEndsOnlyThatMachinesBridge(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "solco-close-bridge-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	start := func(id string) (socket string, exited chan struct{}) {
		cmd := exec.Command("sleep", "60")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		socket = filepath.Join(dir, id+".sock")
		for _, path := range []string{socket, clientSocketPath(socket)} {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		exited = make(chan struct{})
		go func() { _ = cmd.Wait(); close(exited) }()
		bridges.mu.Lock()
		bridges.byID[id] = &sshBridge{socket: socket, cmd: cmd}
		bridges.mu.Unlock()
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			bridges.mu.Lock()
			delete(bridges.byID, id)
			bridges.mu.Unlock()
		})
		return socket, exited
	}
	forgottenSocket, forgotten := start("forgotten")
	keptSocket, kept := start("kept")

	CloseBridge("forgotten")
	CloseBridge("never-had-one") // nothing to close

	select {
	case <-forgotten:
	case <-time.After(5 * time.Second):
		t.Fatal("the forgotten machine's ssh is still running")
	}
	for _, path := range []string{forgottenSocket, clientSocketPath(forgottenSocket)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s survived CloseBridge", path)
		}
	}
	bridges.mu.Lock()
	_, listed := bridges.byID["forgotten"]
	bridges.mu.Unlock()
	if listed {
		t.Fatal("the closed bridge is still registered, so the next ensureBridge would trust it")
	}
	select {
	case <-kept:
		t.Fatal("another machine's bridge went down with it")
	default:
	}
	if _, err := os.Stat(keptSocket); err != nil {
		t.Fatalf("another machine's socket went with it: %v", err)
	}
}

func TestRemoteSocketPathMirrorsLocalLayout(t *testing.T) {
	if got, want := remoteSocketPath("/Users/deploy", ""), "/Users/deploy/.config/herdr/herdr.sock"; got != want {
		t.Fatalf("remoteSocketPath(default) = %q, want %q", got, want)
	}
	if got, want := remoteSocketPath("/Users/deploy", "agents"), "/Users/deploy/.config/herdr/sessions/agents/herdr.sock"; got != want {
		t.Fatalf("remoteSocketPath(named) = %q, want %q", got, want)
	}
}
