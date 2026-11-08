package nativeui

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/services"
)

// healthShell builds a shell with one project/pane and a green service in
// the published index — the state the scan's apply finds on entry.
func healthShell(t *testing.T) (*Shell, herdr.Pane) {
	t.Helper()
	s := NewShell()
	pane := herdr.Pane{ID: "p1", ProjectID: "w1", CWD: "/repo-a"}
	s.projection = herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: "devspace"}},
		Panes:    []herdr.Pane{pane},
	}
	s.serviceIndex = sidebarServiceIndex{
		Panes: map[string]paneServiceActivity{
			"p1": {Proc: "node", Ports: []uint16{7676}},
		},
		Projects: map[string]paneServiceActivity{},
	}
	return s, pane
}

func healthProbes(port uint16, class services.ProbeClass, code int) map[uint16]services.ProbeResult {
	return map[uint16]services.ProbeResult{port: {Class: class, Code: code}}
}

func serviceTestPort(listener net.Listener) uint16 {
	_, port, _ := strings.Cut(listener.Addr().String(), ":")
	var out uint16
	for _, r := range port {
		if r < '0' || r > '9' {
			break
		}
		out = out*10 + uint16(r-'0')
	}
	return out
}

func TestApplyServiceHealthGreenWhileServing(t *testing.T) {
	s, pane := healthShell(t)
	now := time.Now()
	next := map[string]paneServiceActivity{
		"p1": {Proc: "node", Ports: []uint16{7676}},
	}
	s.applyServiceHealth([]herdr.Pane{pane}, next, healthProbes(7676, services.ProbeHTTP, 200), map[string]bool{"p1": false}, now)

	act := next["p1"]
	if tone := healthToneOf(act); tone != ToneSuccess || act.HealthReason != "" {
		t.Fatalf("serving = %v/%q, want plain green", tone, act.HealthReason)
	}
}

func TestApplyServiceHealthHTTP5xxIsRed(t *testing.T) {
	s, pane := healthShell(t)
	next := map[string]paneServiceActivity{
		"p1": {Proc: "node", Ports: []uint16{7676}},
	}
	s.applyServiceHealth([]herdr.Pane{pane}, next, healthProbes(7676, services.ProbeHTTP, 502), map[string]bool{"p1": false}, time.Now())

	act := next["p1"]
	if tone := healthToneOf(act); tone != ToneError || act.HealthReason != "HTTP 502" {
		t.Fatalf("5xx = %v/%q, want red HTTP 502", tone, act.HealthReason)
	}
}

func TestApplyServiceHealthHungNeedsTwoStrikes(t *testing.T) {
	s, pane := healthShell(t)
	now := time.Now()
	next := func() map[string]paneServiceActivity {
		return map[string]paneServiceActivity{
			"p1": {Proc: "node", Ports: []uint16{7676}},
		}
	}
	first := next()
	s.applyServiceHealth([]herdr.Pane{pane}, first, healthProbes(7676, services.ProbeHung, 0), map[string]bool{"p1": false}, now)
	if tone := healthToneOf(first["p1"]); tone != ToneWarning {
		t.Fatalf("first hung probe = %v, want orange not-responding", tone)
	}

	second := next()
	s.applyServiceHealth([]herdr.Pane{pane}, second, healthProbes(7676, services.ProbeHung, 0), map[string]bool{"p1": false}, now.Add(10*time.Second))
	if tone := healthToneOf(second["p1"]); tone != ToneError {
		t.Fatalf("second hung probe = %v, want red no-response", tone)
	}

	recovered := next()
	s.applyServiceHealth([]herdr.Pane{pane}, recovered, healthProbes(7676, services.ProbeAlive, 0), map[string]bool{"p1": false}, now.Add(20*time.Second))
	if tone := healthToneOf(recovered["p1"]); tone != ToneSuccess {
		t.Fatalf("recovered probe = %v, want green", tone)
	}
}

// The exit story: the scan saw the service last round; this round the
// ports are gone and the shell sits idle — the row turns red and is
// retained (the index dropped it), until the pane gets busy again.
func TestApplyServiceHealthExitRetainsARedRow(t *testing.T) {
	s, pane := healthShell(t)
	now := time.Now()

	// Scan N+1: ports gone, shell idle. The scan publishes the new index
	// (the pane's entry dropped with its ports) beside the health apply —
	// emulate both halves of the publish.
	next := map[string]paneServiceActivity{}
	s.applyServiceHealth([]herdr.Pane{pane}, next, nil, map[string]bool{"p1": true}, now)
	s.serviceIndex = sidebarServiceIndex{
		Panes:    map[string]paneServiceActivity{},
		Projects: map[string]paneServiceActivity{},
	}
	fact := s.serviceHealth["p1"]
	if fact.Tone != ToneError || fact.Reason != "exited" || fact.Proc != "node" || len(fact.Ports) != 1 {
		t.Fatalf("retained fact = %+v, want red exited with the process facts", fact)
	}
	rows := s.serviceRowModels()
	if len(rows) != 1 || rows[0].Act.HealthReason != "exited" || rows[0].DirName != "repo-a" {
		t.Fatalf("retained rows = %+v, want one red exited row", rows)
	}

	// The shell gets busy again (the user ran something else): the stale
	// story is dropped and the row disappears.
	s.applyServiceHealth([]herdr.Pane{pane}, map[string]paneServiceActivity{}, nil, map[string]bool{"p1": false}, now.Add(10*time.Second))
	if _, retained := s.serviceHealth["p1"]; retained {
		t.Fatalf("a busy shell discharges the exited fact: %+v", s.serviceHealth["p1"])
	}
	if rows := s.serviceRowModels(); len(rows) != 0 {
		t.Fatalf("discharged rows = %+v, want none", rows)
	}
}

func TestApplyServiceHealthRestartingIsOrange(t *testing.T) {
	s, pane := healthShell(t)
	now := time.Now()
	// Ports gone but the shell is busy — a watch loop is restarting it.
	s.applyServiceHealth([]herdr.Pane{pane}, map[string]paneServiceActivity{}, nil, map[string]bool{"p1": false}, now)
	if fact := s.serviceHealth["p1"]; fact.Tone != ToneWarning || fact.Reason != "restarting" {
		t.Fatalf("busy exit = %+v, want orange restarting", fact)
	}
}

func TestApplyServiceHealthFlappingIsUnstable(t *testing.T) {
	s, pane := healthShell(t)
	now := time.Now()
	empty := map[string]paneServiceActivity{}
	serving := func() map[string]paneServiceActivity {
		return map[string]paneServiceActivity{
			"p1": {Proc: "node", Ports: []uint16{7676}},
		}
	}
	busy := map[string]bool{"p1": false}

	// Three down transitions inside the window: the next green scan reads
	// as unstable, not plain green.
	s.applyServiceHealth([]herdr.Pane{pane}, serving(), healthProbes(7676, services.ProbeAlive, 0), busy, now)
	s.applyServiceHealth([]herdr.Pane{pane}, empty, nil, busy, now.Add(10*time.Second))
	s.applyServiceHealth([]herdr.Pane{pane}, serving(), healthProbes(7676, services.ProbeAlive, 0), busy, now.Add(20*time.Second))
	s.applyServiceHealth([]herdr.Pane{pane}, empty, nil, busy, now.Add(30*time.Second))
	s.applyServiceHealth([]herdr.Pane{pane}, serving(), healthProbes(7676, services.ProbeAlive, 0), busy, now.Add(40*time.Second))
	s.applyServiceHealth([]herdr.Pane{pane}, empty, nil, busy, now.Add(50*time.Second))
	flapped := serving()
	s.applyServiceHealth([]herdr.Pane{pane}, flapped, healthProbes(7676, services.ProbeAlive, 0), busy, now.Add(60*time.Second))
	if tone := healthToneOf(flapped["p1"]); tone != ToneWarning || flapped["p1"].HealthReason != "unstable" {
		t.Fatalf("flapping = %v/%q, want orange unstable", tone, flapped["p1"].HealthReason)
	}

	// Transitions spread past the window (hours apart) do not count: the
	// final green scan reads plain green.
	s2, pane2 := healthShell(t)
	for i := range 6 {
		at := now.Add(-time.Duration(6-i) * time.Hour)
		if i%2 == 0 {
			s2.applyServiceHealth([]herdr.Pane{pane2}, serving(), healthProbes(7676, services.ProbeAlive, 0), busy, at)
		} else {
			s2.applyServiceHealth([]herdr.Pane{pane2}, empty, nil, busy, at)
		}
	}
	calm := serving()
	s2.applyServiceHealth([]herdr.Pane{pane2}, calm, healthProbes(7676, services.ProbeAlive, 0), busy, now)
	if tone := healthToneOf(calm["p1"]); tone != ToneSuccess {
		t.Fatalf("spread-out transitions = %v, want green", tone)
	}
}

// A pane that closes takes its retained fact with it.
func TestApplyServiceHealthPaneCloseDropsFact(t *testing.T) {
	s, pane := healthShell(t)
	now := time.Now()
	s.applyServiceHealth([]herdr.Pane{pane}, map[string]paneServiceActivity{}, nil, map[string]bool{"p1": true}, now)
	if fact := s.serviceHealth["p1"]; fact.Tone != ToneError {
		t.Fatalf("setup failed: %+v", fact)
	}
	s.applyServiceHealth(nil, map[string]paneServiceActivity{}, nil, nil, now.Add(10*time.Second))
	if _, retained := s.serviceHealth["p1"]; retained {
		t.Fatal("a closed pane must drop its retained fact")
	}
}

// The probe batch classifies a port once and stops sending GETs to
// bare-TCP answers (the sticky kinds table).
func TestServiceProbePortsStickyKinds(t *testing.T) {
	s := NewShell()
	// A bare-TCP listener answering its own protocol.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := serviceTestPort(listener)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Write([]byte("-ERR unknown command\r\n"))
			conn.Close()
		}
	}()

	results := s.serviceProbePorts([]uint16{port})
	if got := results[port]; got.Class != services.ProbeAlive {
		t.Fatalf("first probe = %v, want alive", got.Class)
	}
	s.probeMu.Lock()
	entry, known := s.probeKinds[port]
	s.probeMu.Unlock()
	if !known || !entry.tcp {
		t.Fatalf("kind entry = %+v known=%v, want sticky bare TCP", entry, known)
	}
}
