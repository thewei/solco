package nativeui

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/scripts"
)

func svcTestCard(paneID, projectID string) agent.AgentCardModel {
	return agent.AgentCardModel{
		Key:           agent.AgentKey{InstanceID: "main", TerminalID: "term-" + paneID},
		Provider:      history.AgentClaudeCode,
		ProviderLabel: "Claude Code",
		Title:         "Refactor",
		PaneID:        paneID,
		ProjectID:     projectID,
	}
}

func TestClassifyServiceOrigin(t *testing.T) {
	cards := []agent.AgentCardModel{svcTestCard("p1", "w1")}
	scriptPanes := map[string]string{"p3": "dev.up"}

	origin, provider, label := classifyServiceOrigin(cards, scriptPanes, nil, "p1")
	if origin != serviceOriginAgent || provider != history.AgentClaudeCode || label != "Claude Code" {
		t.Fatalf("agent pane origin = %v %v %q", origin, provider, label)
	}
	origin, provider, label = classifyServiceOrigin(cards, scriptPanes, nil, "p3")
	if origin != serviceOriginScript || provider != "" || label != "dev.up" {
		t.Fatalf("script pane origin = %v %v %q", origin, provider, label)
	}
	if origin, _, _ = classifyServiceOrigin(cards, scriptPanes, nil, "p4"); origin != serviceOriginManual {
		t.Fatalf("manual pane origin = %v", origin)
	}
	// The agent card outranks a script record on the same pane.
	if origin, _, _ = classifyServiceOrigin(cards, map[string]string{"p1": "dev.up"}, nil, "p1"); origin != serviceOriginAgent {
		t.Fatalf("card must outrank a script record, got %v", origin)
	}
}

// The 2026-11-03 inferred tier: a pane Solco never launched anything into
// still names its project's script when the foreground argv explains it.
func TestClassifyServiceOriginInferred(t *testing.T) {
	cards := []agent.AgentCardModel{svcTestCard("p1", "w1")}
	records := map[string]string{"p2": "dev.up"}
	origins := map[string]string{"p3": "start", "p1": "daemon", "p2": "other"}

	origin, _, label := classifyServiceOrigin(cards, records, origins, "p3")
	if origin != serviceOriginScript || label != "start" {
		t.Fatalf("inferred origin = %v %q, want script start", origin, label)
	}
	// The Run-button record outranks an inference on the same pane.
	if _, _, label = classifyServiceOrigin(nil, records, origins, "p2"); label != "dev.up" {
		t.Fatalf("record label = %q, want the clicked script", label)
	}
	// The agent card outranks an inference (an omp pane holding a port).
	if origin, _, _ = classifyServiceOrigin(cards, records, origins, "p1"); origin != serviceOriginAgent {
		t.Fatalf("card must outrank an inference, got %v", origin)
	}
	// No inference → the manual fallback.
	if origin, _, _ = classifyServiceOrigin(nil, records, origins, "p9"); origin != serviceOriginManual {
		t.Fatalf("unexplained pane = %v, want manual", origin)
	}
}

func TestGroupServiceRows(t *testing.T) {
	rows := []serviceRowModel{
		{PaneID: "p1", ProjectID: "w1", ProjectName: "solco"},
		{PaneID: "p2", ProjectID: "w2", ProjectName: "api"},
		{PaneID: "p3", ProjectID: "w1", ProjectName: "solco"},
	}
	groups := groupServiceRows(rows)
	if len(groups) != 2 {
		t.Fatalf("groups = %d (%+v)", len(groups), groups)
	}
	if groups[0].Key != "w1" || groups[0].Name != "solco" || len(groups[0].Rows) != 2 ||
		groups[0].Rows[0].PaneID != "p1" || groups[0].Rows[1].PaneID != "p3" {
		t.Fatalf("group w1 = %+v", groups[0])
	}
	if groups[1].Key != "w2" || groups[1].Name != "api" || len(groups[1].Rows) != 1 {
		t.Fatalf("group w2 = %+v", groups[1])
	}
	if g := groupServiceRows([]serviceRowModel{{PaneID: "p9"}}); len(g) != 1 || g[0].Name != "Workspace" {
		t.Fatalf("unnamed group = %+v", g)
	}
}

func TestUnclaimedPorts(t *testing.T) {
	rows := []serviceRowModel{{PaneID: "p1", Act: paneServiceActivity{Proc: "node", Ports: []uint16{3000, 8080}}}}
	got := unclaimedPorts([]uint16{3000, 5173, 8080, 6379}, rows)
	if len(got) != 2 || got[0] != 5173 || got[1] != 6379 {
		t.Fatalf("unclaimed = %v", got)
	}
	if got := unclaimedPorts([]uint16{3000}, rows); len(got) != 0 {
		t.Fatalf("fully claimed ports must yield none, got %v", got)
	}
}

func TestProcServiceMark(t *testing.T) {
	mark, ok := procServiceMark(paneServiceActivity{Proc: "node", Ports: []uint16{3000}})
	if !ok || mark.svg == nil || !mark.tinted {
		t.Fatalf("node mark = %+v ok=%v (want a tinted brand mark)", mark, ok)
	}
	if _, ok := procServiceMark(paneServiceActivity{Proc: "zsh", Ports: []uint16{3000}}); ok {
		t.Fatal("a shell must not carry a service mark")
	}
	if _, ok := procServiceMark(paneServiceActivity{Proc: "totally-unknown-server"}); ok {
		t.Fatal("an unknown process must not carry a service mark")
	}
}

// TestSidebarServicesSection pins the 2026-11-02 section contract
// (designs/services-surfaces, Option A): the Services block renders under
// Agents with one two-line row per pane-attributed service and a Manual
// origin when nothing claims the pane, its header toggles the persisted
// flag, and collapsing it drops the rows.
func TestSidebarServicesSection(t *testing.T) {
	s := NewShell()
	s.settings.General.TabsPlacement = "sidebar" // these assertions read the tree/breadcrumb layout
	s.loading = false
	s.activeInstance = "default"
	s.projection = herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: "Project A"}, {ID: "w2", Label: "Project B"}},
		Tabs:     []herdr.Tab{{ID: "t1", ProjectID: "w1"}, {ID: "t2", ProjectID: "w2"}},
		Panes: []herdr.Pane{
			{ID: "p1", ProjectID: "w1", TabID: "t1", CWD: "/repo-a"},
			{ID: "p2", ProjectID: "w2", TabID: "t2", CWD: "/repo-b"},
		},
	}
	s.serviceIndex = sidebarServiceIndex{
		Panes: map[string]paneServiceActivity{
			"p1": {Proc: "node", Ports: []uint16{3000}},
			"p2": {Proc: "go", Ports: []uint16{8080}},
		},
		Projects: map[string]paneServiceActivity{},
	}

	tester := ui.NewTester(s.View, 1200, 800)
	for _, want := range []string{"Services", ":3000", ":8080", "Manual", "· repo-a", "· repo-b"} {
		if !tester.HasText(want) {
			t.Fatalf("sidebar services section missing %q; texts=%q", want, tester.Texts())
		}
	}

	// The Services header is the shared builder: clicking it toggles the
	// section through the persisted flag (both ways), and a collapsed
	// section draws no rows.
	if err := tester.Click("Services"); err != nil {
		t.Fatalf("services header click: %v", err)
	}
	if s.servicesOpen {
		t.Fatalf("services header click did not collapse the section")
	}
	tester.Frame()
	if tester.HasText(":3000") {
		t.Fatalf("services rows still render while collapsed; texts=%q", tester.Texts())
	}
	if err := tester.Click("Services"); err != nil {
		t.Fatal(err)
	}
	if !s.servicesOpen {
		t.Fatalf("services header click did not reopen the section")
	}
	tester.Frame()
	if !tester.HasText(":3000") || !tester.HasText(":8080") {
		t.Fatalf("services rows did not render again; texts=%q", tester.Texts())
	}

	// Workspace groups fold like the Agents list's (2026-11-03): the
	// header toggle hides only that project's rows, the folded group
	// shows its count, and the fold rides the Agents folds' persistence.
	if err := tester.Click("Toggle Project A services"); err != nil {
		t.Fatalf("workspace group click: %v", err)
	}
	if !s.foldedAgents["svc-group:w1"] {
		t.Fatalf("group click did not fold Project A: %v", s.foldedAgents)
	}
	tester.Frame()
	if tester.HasText(":3000") {
		t.Fatalf("folded Project A rows still render; texts=%q", tester.Texts())
	}
	if !tester.HasText(":8080") {
		t.Fatalf("Project B rows must survive the Project A fold; texts=%q", tester.Texts())
	}
	if !tester.HasText("1") {
		t.Fatalf("folded group lacks its service count; texts=%q", tester.Texts())
	}
	if err := tester.Click("Toggle Project A services"); err != nil {
		t.Fatal(err)
	}
	if s.foldedAgents["svc-group:w1"] {
		t.Fatalf("second click did not unfold Project A")
	}
	tester.Frame()
	if !tester.HasText(":3000") {
		t.Fatalf("unfolded Project A rows did not render; texts=%q", tester.Texts())
	}
}

// TestServiceRowModelsAttribution pins the model behind the section: only
// pane-attributed services become rows (stale panes are skipped), a pane
// with an agent card carries that origin, and script attribution comes
// from the shell's launch record.
func TestServiceRowModelsAttribution(t *testing.T) {
	s := NewShell()
	s.projection = herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: "Project A"}, {ID: "w2", Label: "Project B"}},
		Panes: []herdr.Pane{
			{ID: "p1", ProjectID: "w1", CWD: "/repo-a"},
			{ID: "p2", ProjectID: "w2", CWD: "/repo-b"},
		},
	}
	s.serviceIndex = sidebarServiceIndex{
		Panes: map[string]paneServiceActivity{
			"p1": {Proc: "node", Ports: []uint16{3000}},
			"p2": {Proc: "go", Ports: []uint16{8080}},
			"p9": {Proc: "redis", Ports: []uint16{6379}}, // stale pane: no row
		},
		Projects: map[string]paneServiceActivity{},
	}
	s.activeInstance = "main"
	s.scriptPanes["main/p2"] = "dev.up"

	rows := s.serviceRowModels()
	if len(rows) != 2 {
		t.Fatalf("rows = %d (%+v), want 2 (stale pane skipped)", len(rows), rows)
	}
	byPane := map[string]serviceRowModel{}
	for _, row := range rows {
		byPane[row.PaneID] = row
	}
	p1 := byPane["p1"]
	if p1.Origin != serviceOriginManual || p1.ProjectName != "Project A" || len(p1.Act.Ports) != 1 {
		t.Fatalf("p1 row = %+v", p1)
	}
	p2 := byPane["p2"]
	if p2.Origin != serviceOriginScript || p2.OriginLabel != "dev.up" || p2.ProjectName != "Project B" {
		t.Fatalf("p2 row = %+v", p2)
	}
	if p2.Title != "dev.up" {
		t.Fatalf("script row title = %q, want the script name", p2.Title)
	}
	if p1.Title == "" {
		t.Fatalf("process row title must fall back to the friendly name")
	}
	// The 2026-11-03 directory disambiguator: the pane cwd's leaf rides
	// the row, the full path its tooltip.
	if p1.DirName != "repo-a" || p1.DirPath != "/repo-a" || p2.DirName != "repo-b" || p2.DirPath != "/repo-b" {
		t.Fatalf("dir facts = %+v / %+v", p1, p2)
	}

	// With an agent card bound to p1 the origin names the agent and the
	// row keeps the card for the panePrefersChat jump.
	cards := []agent.AgentCardModel{svcTestCard("p1", "w1")}
	p1Origin, provider, label := classifyServiceOrigin(cards, s.scriptPanes, s.serviceOrigins, "p1")
	if p1Origin != serviceOriginAgent || provider != history.AgentClaudeCode || label != "Claude Code" {
		t.Fatalf("p1 with card = %v %v %q", p1Origin, provider, label)
	}
	if card := cardForPane(cards, "p1"); card == nil || card.PaneID != "p1" {
		t.Fatalf("cardForPane = %+v", card)
	}
}

// TestScriptTitleFor pins the 2026-11-02 title rule: a script-launched
// service's row title resolves from the script definitions scoped to the
// pane's directory, falling back to the recorded launch name when the
// store holds no definition there.
func TestScriptTitleFor(t *testing.T) {
	s := NewShell()
	if got := s.scriptTitleFor("dev.up", "/repo-b"); got != "dev.up" {
		t.Fatalf("nil store title = %q, want the recorded name", got)
	}
	store, err := scripts.NewStore(filepath.Join(t.TempDir(), "scripts.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(scripts.ScriptDefinition{ID: "1", ProjectPath: "/repo-b", Name: "dev.up", Command: "bun run dev"}); err != nil {
		t.Fatal(err)
	}
	s.scriptStore = store
	for _, cwd := range []string{"/repo-b", "/repo-b/sub"} {
		if got := s.scriptTitleFor("dev.up", cwd); got != "dev.up" {
			t.Fatalf("title under %s = %q", cwd, got)
		}
	}
}
