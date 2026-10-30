package nativeui

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/services"
)

func fgInfo(pid int, name string) *herdr.PaneProcessInfo {
	return &herdr.PaneProcessInfo{
		PaneID:              "p1",
		ForegroundProcesses: []herdr.PaneProcessInfoProcess{{PID: pid, Name: name}},
	}
}

func TestForegroundPaneServiceMatchesListener(t *testing.T) {
	listeners := map[int]services.ListenerSnapshot{
		4321: {PID: 4321, Command: "node", Ports: []uint16{3000, 8080}},
		9999: {PID: 9999, Command: "postgres", Ports: []uint16{5432}},
	}

	act, attributed := foregroundPaneService(fgInfo(4321, "node"), listeners)
	if len(act.Ports) != 2 || act.Ports[0] != 3000 || act.Ports[1] != 8080 {
		t.Fatalf("ports = %v", act.Ports)
	}
	if act.Proc != "node" {
		t.Fatalf("proc = %q", act.Proc)
	}
	if len(attributed) != 1 || attributed[0] != 4321 {
		t.Fatalf("attributed = %v", attributed)
	}

	// A shell without listeners stays quiet; foreign PIDs are not consumed.
	act, attributed = foregroundPaneService(fgInfo(7, "zsh"), listeners)
	if len(act.Ports) != 0 || len(attributed) != 0 {
		t.Fatalf("quiet shell = %+v %v", act, attributed)
	}
	if _, attributed = foregroundPaneService(nil, listeners); len(attributed) != 0 {
		t.Fatalf("nil info consumed %v", attributed)
	}
}

// TestComputeSidebarServicesCWDFallback pins the daemon fallback: a
// listener outside every foreground group is attributed to the one Pane
// whose directory contains it, or to the Project when the directory is
// shared; already-attributed PIDs never fall through.
func TestComputeSidebarServicesCWDFallback(t *testing.T) {
	projection := herdr.Projection{
		Panes: []herdr.Pane{
			{ID: "p1", ProjectID: "w1", TabID: "t1", CWD: "/repo"},
			{ID: "p2", ProjectID: "w1", TabID: "t2", CWD: "/repo"},
			{ID: "p3", ProjectID: "w1", TabID: "t3", CWD: "/repo/sub"},
		},
	}
	listeners := map[int]services.ListenerSnapshot{
		100: {PID: 100, Command: "node", Ports: []uint16{3000}, CWD: "/repo/sub"},
		200: {PID: 200, Command: "vite", Ports: []uint16{5173}, CWD: "/repo/other"},
		300: {PID: 300, Command: "nginx", Ports: []uint16{80}, CWD: "/somewhere/else"},
	}
	infos := map[string]*herdr.PaneProcessInfo{
		// p3's shell foreground owns the node listener on :3000.
		"p3": fgInfo(100, "node"),
	}

	index := computeSidebarServices(projection, listeners, infos)

	if act := index.Panes["p3"]; len(act.Ports) != 1 || act.Ports[0] != 3000 || act.Proc != "node" {
		t.Fatalf("p3 = %+v", act)
	}
	// :5173 sits under the shared /repo — two Panes qualify, so it lands on
	// the Project, not on either Pane.
	if _, ok := index.Panes["p1"]; ok {
		t.Fatalf("ambiguous daemon landed on p1: %+v", index.Panes)
	}
	if act := index.Projects["w1"]; len(act.Ports) != 1 || act.Ports[0] != 5173 || act.Proc != "vite" {
		t.Fatalf("project fallback = %+v", act)
	}
	// The foreign nginx listener has no Pane directory above it: dropped.
	for paneID, act := range index.Panes {
		for _, port := range act.Ports {
			if port == 80 {
				t.Fatalf("foreign listener on %v hit %s", act.Ports, paneID)
			}
		}
	}
}

func TestBuildSidebarActivityServiceRollup(t *testing.T) {
	projection := herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: "repo"}},
		Tabs:     []herdr.Tab{{ID: "t1", ProjectID: "w1"}},
		Panes:    []herdr.Pane{{ID: "p1", ProjectID: "w1", TabID: "t1"}},
	}
	svcs := sidebarServiceIndex{
		Panes: map[string]paneServiceActivity{
			"p1": {Proc: "node", Ports: []uint16{3000}},
		},
		Projects: map[string]paneServiceActivity{},
	}

	activity := buildSidebarActivity(projection, svcs, nil)
	for _, id := range []string{"p1", "t1", "w1"} {
		act, ok := activity[id]
		if !ok || !act.On || act.Tone != ToneSuccess {
			t.Fatalf("activity[%s] = %+v (want green)", id, act)
		}
	}
	if detail := activity["w1"].Detail; detail != "node on :3000" {
		t.Fatalf("project detail = %q", detail)
	}
}

// TestBuildSidebarActivityAgentRollup pins the ancestor agent rollup: a
// working agent below turns Tabs/Projects blue (Panes keep their icon dot
// and no corner dot), an idle-only subtree shows the muted alive-dot, and
// an empty subtree stays dark.
func TestBuildSidebarActivityAgentRollup(t *testing.T) {
	projection := herdr.Projection{
		Projects: []herdr.Project{{ID: "w1"}},
		Tabs:     []herdr.Tab{{ID: "t1", ProjectID: "w1"}},
		Panes:    []herdr.Pane{{ID: "p1", ProjectID: "w1", TabID: "t1"}},
		Agents:   []herdr.Agent{{PaneID: "p1", Status: "working"}},
	}

	activity := buildSidebarActivity(projection, sidebarServiceIndex{}, nil)
	if _, on := activity["p1"]; on {
		t.Fatalf("pane row should keep its icon dot, got %+v", activity["p1"])
	}
	for _, id := range []string{"t1", "w1"} {
		if act := activity[id]; !act.On || act.Tone != ToneWorking {
			t.Fatalf("activity[%s] = %+v (want working tone)", id, act)
		}
	}
	if detail := activity["w1"].Detail; detail != "1 agent running" {
		t.Fatalf("agent detail = %q", detail)
	}

	projection.Agents = []herdr.Agent{{PaneID: "p1", Status: "idle"}}
	activity = buildSidebarActivity(projection, sidebarServiceIndex{}, nil)
	if act := activity["w1"]; !act.On || act.Tone != ToneMuted {
		t.Fatalf("idle rollup = %+v (want muted)", act)
	}

	projection.Agents = nil
	activity = buildSidebarActivity(projection, sidebarServiceIndex{}, nil)
	if len(activity) != 0 {
		t.Fatalf("empty subtree lit %v", activity)
	}
}

// TestBuildSidebarActivityServiceBeatsAgent pins the precedence: a running
// service keeps the corner dot green even when an agent below is working,
// and both facts join in the tooltip.
func TestBuildSidebarActivityServiceBeatsAgent(t *testing.T) {
	projection := herdr.Projection{
		Projects: []herdr.Project{{ID: "w1"}},
		Tabs:     []herdr.Tab{{ID: "t1", ProjectID: "w1"}},
		Panes:    []herdr.Pane{{ID: "p1", ProjectID: "w1", TabID: "t1"}},
		Agents:   []herdr.Agent{{PaneID: "p1", Status: "working"}},
	}
	svcs := sidebarServiceIndex{
		Panes:    map[string]paneServiceActivity{"p1": {Proc: "node", Ports: []uint16{3000}}},
		Projects: map[string]paneServiceActivity{},
	}

	for _, id := range []string{"t1", "w1"} {
		if act := buildSidebarActivity(projection, svcs, nil)[id]; act.Tone != ToneSuccess {
			t.Fatalf("activity[%s] = %+v (want green over agent)", id, act)
		}
	}
}

// TestSidebarRendersServiceActivity pins the render contract: with a
// service in the index the sidebar still renders the full tree, the Agents
// section header keeps the shared compact style (its row toggles
// agentsOpen), and a frame with activity never crashes the tree.
func TestSidebarRendersServiceActivity(t *testing.T) {
	s := NewShell()
	s.loading = false
	s.activeInstance = "default"
	s.projection = herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: "Project A", TabCount: 1}},
		Tabs:     []herdr.Tab{{ID: "t1", ProjectID: "w1", Label: "Tab A", PaneCount: 2}},
		Panes: []herdr.Pane{
			{ID: "p1", ProjectID: "w1", TabID: "t1", Label: "Pane A1", CWD: "/repo"},
			{ID: "p2", ProjectID: "w1", TabID: "t1", Label: "Pane A2"},
		},
		Agents: []herdr.Agent{{PaneID: "p1", Status: "working"}},
	}
	s.selectedProjectID = "w1"
	s.selectedTabID = "t1"
	s.selectedPaneID = "p1"
	s.serviceIndex = sidebarServiceIndex{
		Panes:    map[string]paneServiceActivity{"p1": {Proc: "node", Ports: []uint16{3000}}},
		Projects: map[string]paneServiceActivity{},
	}

	tester := ui.NewTester(s.View, 1200, 800)
	for _, want := range []string{"Agents", "Workspace", "Project A", "Tab A", "Pane A1"} {
		if !tester.HasText(want) {
			t.Fatalf("sidebar missing %q; texts=%q", want, tester.Texts())
		}
	}

	// The Agents header is the shared builder: clicking it toggles the
	// section through the persisted flag (both ways).
	if err := tester.Click("Agents"); err != nil {
		t.Fatalf("agents header click: %v", err)
	}
	if s.agentsOpen {
		t.Fatalf("agents header click did not collapse the section")
	}
	if err := tester.Click("Agents"); err != nil {
		t.Fatal(err)
	}
	if !s.agentsOpen {
		t.Fatalf("agents header click did not reopen the section")
	}
	tester.Frame()

	// The Workspace header shares the same builder and toggle; collapsing
	// it drops the tree rows. The selected Project/Tab/Pane keep showing in
	// the titlebar breadcrumb, so the probe is the unselected "Pane A2",
	// which only the tree draws.
	if err := tester.Click("Workspace"); err != nil {
		t.Fatalf("workspace header click: %v", err)
	}
	if s.projectsOpen {
		t.Fatalf("workspace header click did not collapse the section")
	}
	tester.Frame()
	if tester.HasText("Pane A2") {
		t.Fatalf("workspace section still open while collapsed; texts=%q", tester.Texts())
	}
	if err := tester.Click("Workspace"); err != nil {
		t.Fatal(err)
	}
	if !s.projectsOpen {
		t.Fatalf("workspace header click did not reopen the section")
	}
	tester.Frame()
	if !tester.HasText("Pane A2") {
		t.Fatalf("workspace section did not render its rows again; texts=%q", tester.Texts())
	}
}

// TestBuildSidebarActivityProjectDaemonFallback pins the project-level
// fallback dot: a daemon attributed to the Project (ambiguous cwd) lights
// the project row even when no pane or agent below is active.
func TestBuildSidebarActivityProjectDaemonFallback(t *testing.T) {
	projection := herdr.Projection{
		Projects: []herdr.Project{{ID: "w2", Label: "herdr-client", TabCount: 1}},
		Tabs:     []herdr.Tab{{ID: "t3", ProjectID: "w2", Label: "shell", PaneCount: 1}},
		Panes:    []herdr.Pane{{ID: "p4", ProjectID: "w2", TabID: "t3", Label: "zsh"}},
	}
	svcs := sidebarServiceIndex{
		Panes:    map[string]paneServiceActivity{},
		Projects: map[string]paneServiceActivity{"w2": {Proc: "vite", Ports: []uint16{5173}}},
	}

	act := buildSidebarActivity(projection, svcs, nil)["w2"]
	if !act.On || act.Tone != ToneSuccess || act.Detail != "vite on :5173" {
		t.Fatalf("project daemon dot = %+v", act)
	}
}

// TestTabRowMarkShowsRunningServiceBrand pins A5 (2026-10-06 annotation
// round) plus the 2026-10-30 agent-brand extension: a collapsed Tab
// presenting one Pane wears that Pane's identity — the bound Agent's
// brand first (a single-pane Tab renders no pane row, so the Tab row is
// where the Agent's icon shows), then a recognized program's brand for a
// long-lived service. Unknown or absent services and multi-pane Tabs keep
// the plain tab glyph.
func TestTabRowMarkShowsRunningServiceBrand(t *testing.T) {
	s := NewShell()
	s.projection = herdr.Projection{
		Agents: []herdr.Agent{{PaneID: "pa", Kind: "omp", Status: "working"}},
	}
	s.serviceIndex = sidebarServiceIndex{
		Panes: map[string]paneServiceActivity{
			"ps": {Proc: "node", Ports: []uint16{3000}},
			"pu": {Proc: "mcp-feedback-server", Ports: []uint16{3001}},
		},
		Projects: map[string]paneServiceActivity{},
	}

	if mark := s.tabRowMark([]herdr.Pane{{ID: "ps", TabID: "t1"}}, false); mark.svg == nil || mark.svg == iconTab {
		t.Fatalf("service pane tab mark = %+v, want the node brand", mark)
	}
	if mark := s.tabRowMark([]herdr.Pane{{ID: "pu", TabID: "t1"}}, false); mark.svg != iconTab {
		t.Fatalf("unknown service tab mark = %+v, want the plain tab glyph", mark)
	}
	if mark := s.tabRowMark([]herdr.Pane{{ID: "pa", TabID: "t1"}}, false); mark.bmp == nil {
		t.Fatalf("agent pane tab mark = %+v, want the Agent's brand bitmap", mark)
	}
	if mark := s.tabRowMark([]herdr.Pane{{ID: "ps", TabID: "t1"}, {ID: "pu", TabID: "t1"}}, false); mark.svg != iconTab {
		t.Fatalf("multi-pane tab mark = %+v, want the plain tab glyph", mark)
	}
}

// TestTreeRowsCarryTheAgentWorkingSpinner pins the 2026-10-30 loading
// state: a tree row whose pane's agent is mid-flight ends with the same
// trailing spinner the Agents section's cards show (agent_card.go) — a
// single-pane Tab row (the pane's face) and an expanded multi-pane Tab's
// pane row alike — while an idle agent's row stays spinner-free.
func TestTreeRowsCarryTheAgentWorkingSpinner(t *testing.T) {
	s := NewShell()
	s.loading = false
	s.activeInstance = "default"
	s.projection = herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: "Project A", TabCount: 3}},
		Tabs: []herdr.Tab{
			{ID: "t1", ProjectID: "w1", Label: "Working Tab", PaneCount: 1},
			{ID: "t2", ProjectID: "w1", Label: "Idle Tab", PaneCount: 1},
			{ID: "t3", ProjectID: "w1", Label: "Split Tab", PaneCount: 2},
		},
		Panes: []herdr.Pane{
			{ID: "p1", ProjectID: "w1", TabID: "t1", Label: "working pane"},
			{ID: "p2", ProjectID: "w1", TabID: "t2", Label: "idle pane"},
			{ID: "p3", ProjectID: "w1", TabID: "t3", Label: "split working pane"},
			{ID: "p4", ProjectID: "w1", TabID: "t3", Label: "plain pane"},
		},
		Agents: []herdr.Agent{
			{PaneID: "p1", Kind: "omp", Status: "working"},
			{PaneID: "p2", Kind: "omp", Status: "idle"},
			{PaneID: "p3", Kind: "omp", Status: "working"},
		},
	}
	s.selectedProjectID = "w1"
	s.selectedTabID = "t3" // the multi-pane Tab expands by selection
	s.selectedPaneID = "p3"

	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()

	// The trailing spinner sits inside the row, just before its right
	// padding: Find returns the row itself (the ButtonBase carries the
	// label), so the box is anchored to the row's right edge. Each
	// sampled column takes its reference from the same column's quiet
	// strip near the row's bottom edge, so a background gradient never
	// reads as ink.
	inkAfter := func(label string) int {
		el, ok := tester.Find(label)
		if !ok {
			t.Fatalf("row %q missing; texts=%q", label, tester.Texts())
		}
		midY := int(el.Y + el.H/2)
		right := int(el.X + el.W)
		best := 0
		for dx := 25; dx >= 9; dx-- {
			paper := pixelAt(tester, right-dx, midY+13)
			for dy := -7; dy <= 7; dy += 2 {
				if d := rgbDiff(pixelAt(tester, right-dx, midY+dy), paper); d > best {
					best = d
				}
			}
		}
		return best
	}

	if ink := inkAfter("Working Tab"); ink < 40 {
		t.Fatalf("working single-pane tab row shows no spinner ink (max diff %d)", ink)
	}
	if ink := inkAfter("Idle Tab"); ink > 15 {
		t.Fatalf("idle tab row is not spinner-free (max diff %d)", ink)
	}
	if ink := inkAfter("split working pane"); ink < 40 {
		t.Fatalf("working pane row shows no spinner ink (max diff %d)", ink)
	}
	if ink := inkAfter("plain pane"); ink > 15 {
		t.Fatalf("plain pane row is not spinner-free (max diff %d)", ink)
	}
}
