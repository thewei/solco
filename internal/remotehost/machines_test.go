package remotehost

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type fakeMachines struct {
	mu      sync.Mutex
	list    []MachineInfo
	err     error
	connect []string
}

func (f *fakeMachines) List() ([]MachineInfo, error) { return f.list, nil }

func (f *fakeMachines) Connect(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connect = append(f.connect, id)
	return f.err
}

func sampleMachines() *fakeMachines {
	return &fakeMachines{list: []MachineInfo{
		{Key: "machine/abc", Label: "omarchy", Status: MachineConnected},
		{Key: "machine/def", Label: "build box", Status: MachineNeedsSignIn},
	}}
}

func machineHarness(t *testing.T) (*harness, *fakeMachines) {
	t.Helper()
	h := newHarness(t)
	m := sampleMachines()
	h.srv.opts.Machines = m
	h.back.instances = []string{"default", "machine/abc"}
	return h, m
}

func TestBootstrapListsDevicesWithStatusAndCapability(t *testing.T) {
	h, _ := machineHarness(t)
	body := string(bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	for _, want := range []string{`"devices":true`, `{"id":"default"}`, `{"id":"machine/abc","kind":"device","label":"omarchy","status":"connected"}`, `{"id":"machine/def","kind":"device","label":"build box","status":"needs_signin"}`} {
		if !strings.Contains(body, want) {
			t.Fatalf("bootstrap lacks %s: %s", want, body)
		}
	}
	if strings.Count(body, `"id":"machine/abc"`) != 1 {
		t.Fatalf("a bridged device is listed twice: %s", body)
	}
}

func TestBootstrapWithoutMachinesHasNoDevices(t *testing.T) {
	h := newHarness(t)
	body := string(bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	if strings.Contains(body, "devices") || strings.Contains(body, "machine/") {
		t.Fatalf("bootstrap advertises devices: %s", body)
	}
}

func TestDeviceConnectAnswersTheInstance(t *testing.T) {
	h, m := machineHarness(t)
	resp := h.launch("/api/v3/devices/def/connect", h.token, ``)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got := strings.TrimSpace(string(bodyOf(t, resp))); got != `{"instance":"machine/def"}` {
		t.Fatalf("body %s", got)
	}
	if len(m.connect) != 1 || m.connect[0] != "def" {
		t.Fatalf("connected %v", m.connect)
	}
}

func TestDeviceConnectRefusalsAreStableCodes(t *testing.T) {
	h, m := machineHarness(t)
	for code, status := range map[string]int{"device_needs_signin": 409, "device_unreachable": 502, "device_disabled": 409, "unknown_device": 404} {
		m.err = &MachineError{Code: code, Status: status}
		resp := h.launch("/api/v3/devices/def/connect", h.token, ``)
		if resp.StatusCode != status || errCode(t, resp) != code {
			t.Fatalf("%s: status %d", code, resp.StatusCode)
		}
	}
	m.err = context.DeadlineExceeded
	if resp := h.launch("/api/v3/devices/def/connect", h.token, ``); resp.StatusCode != http.StatusBadGateway || errCode(t, resp) != "device_connect_failed" {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestDeviceConnectNeedsAuthAndTheSeam(t *testing.T) {
	h, m := machineHarness(t)
	if resp := h.launch("/api/v3/devices/def/connect", "", ``); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if len(m.connect) != 0 {
		t.Fatal("an unauthenticated connect reached the bridge")
	}
	h.srv.opts.Machines = nil
	if resp := h.launch("/api/v3/devices/def/connect", h.token, ``); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "devices_unavailable" {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

// A device's Workspace path belongs to another computer: nothing that reads this Mac's disk may serve it.
func TestDeviceInstanceNeverReachesTheMacsDisk(t *testing.T) {
	h, _ := machineHarness(t)
	h.srv.opts.Git = sampleGit()
	h.srv.opts.GitWrite = &fakeGitWriter{}
	h.srv.opts.Files = true
	for _, path := range []string{
		"/api/v3/workspaces/w1/git?instance=machine/abc",
		"/api/v3/workspaces/w1/git/diff?path=a.go&instance=machine/abc",
		"/api/v3/workspaces/w1/files?instance=machine/abc",
		"/api/v3/panes/w1:p1/chat?instance=machine/abc",
		"/api/v3/panes/w1:p1/chat/catalog?instance=machine/abc",
	} {
		resp := h.get(path, h.token, nil)
		if resp.StatusCode != http.StatusConflict || errCode(t, resp) != "instance_remote" {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
	}
	for _, path := range []string{
		"/api/v3/workspaces/w1/git/stage?instance=machine/abc",
		"/api/v3/panes/w1:p1/chat/send?instance=machine/abc",
	} {
		resp := h.launch(path, h.token, `{"paths":["a.go"],"text":"hi","requestId":"req_0123456789"}`)
		if resp.StatusCode != http.StatusConflict || errCode(t, resp) != "instance_remote" {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
	}
}

func TestDeviceInstanceServesTreeAndPeek(t *testing.T) {
	h, _ := machineHarness(t)
	if resp := h.get("/api/v3/tree?instance=machine/abc", h.token, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("tree status %d", resp.StatusCode)
	}
	if resp := h.get("/api/v3/tree?instance=machine/zzz", h.token, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unbridged device must be unknown, got %d", resp.StatusCode)
	}
}

func TestDeviceLaunchIsTerminalInAnExistingWorkspaceOnly(t *testing.T) {
	h, _ := machineHarness(t)
	l := &fakeLauncher{pane: "w1:p2"}
	h.srv.opts.Launcher = l
	agent := `{"requestId":"req_0123456789","workspaceId":"w1","kind":"claude-code"}`
	typed := `{"requestId":"req_0123456789","path":"/home/x","kind":"terminal"}`
	for _, body := range []string{agent, typed} {
		resp := h.launch("/api/v3/launch?instance=machine/abc", h.token, body)
		if resp.StatusCode != http.StatusConflict || errCode(t, resp) != "instance_remote" {
			t.Fatalf("%s: status %d", body, resp.StatusCode)
		}
	}
	ok := `{"requestId":"req_0123456789","workspaceId":"w1","kind":"terminal"}`
	if resp := h.launch("/api/v3/launch?instance=machine/abc", h.token, ok); resp.StatusCode != http.StatusOK {
		t.Fatalf("terminal launch status %d", resp.StatusCode)
	}
}
