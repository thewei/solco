package nativeui

// [INPUT]: 依赖 settings_search.go 的索引与匹配、page_settings.go 的 Voice 页、agent_card.go / sidebar.go 的分组 git 摘要、sidebar_tree.go 的 TrailingOnDemand、tabbar.go 的宽度常量
// [OUTPUT]: 2026-10-30 UX 审计第三轮的守卫：设置索引覆盖每个静态 row、搜索命中进入所在页并清空、Voice 独立成页、同仓库的 Agent 摘要只在分组头画一次、Hosts 行 endpoint 按需、选中 Tab 的宽度区间与入视
// [POS]: designs/ux-audit-3 四个议题的回归；视觉对照原型在真机
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

import (
	"os"
	"regexp"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/sshconfig"
)

// TestSettingsIndexCoversEveryRow keeps the search index honest: every
// literal row("…") label in a settings page must be in settingsLabels, so a
// new setting cannot ship unsearchable. Rows built from data (one per
// provider, per plugin setting) are the named exceptions.
func TestSettingsIndexCoversEveryRow(t *testing.T) {
	indexed := map[string]bool{}
	for _, pairs := range settingsLabels {
		for _, pair := range pairs {
			indexed[pair[1]] = true
		}
	}
	dynamic := map[string]bool{"Show in Quota": true, "Platform": true, "Status": true, "Project": true}
	re := regexp.MustCompile(`[^A-Za-z]row\("([^"]+)"`)
	for _, file := range []string{"page_settings.go", "page_settings_git.go", "usage_settings.go", "workboard_settings.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			if !indexed[m[1]] && !dynamic[m[1]] {
				t.Errorf("%s: row %q is missing from settingsLabels", file, m[1])
			}
		}
	}
}

func TestMatchSettings(t *testing.T) {
	s := NewShell()
	entries := s.settingsEntries()
	if got := matchSettings(entries, "  "); got != nil {
		t.Fatalf("an empty query matched %d entries", len(got))
	}
	hits := matchSettings(entries, "ENDPOINT")
	if len(hits) != 1 || hits[0].Path != "/settings/voice" {
		t.Fatalf("endpoint hits = %+v, want the Voice page's row", hits)
	}
	// A group name finds its rows; a page name finds the page and its rows.
	if hits := matchSettings(entries, "behavior"); len(hits) != 4 {
		t.Fatalf("behavior hits = %d, want the 4 Terminal rows", len(hits))
	}
	if hits := matchSettings(entries, "quota"); len(hits) < 2 {
		t.Fatalf("quota hits = %+v, want the page and its providers", hits)
	}
	if hits := matchSettings(entries, "zzzz"); len(hits) != 0 {
		t.Fatalf("nonsense matched %+v", hits)
	}
}

// TestSettingsSearchOpensTheHitsPage drives the sidebar search: typing lists
// the matching settings with their page, a click opens that page and clears
// the box.
func TestSettingsSearchOpensTheHitsPage(t *testing.T) {
	s := NewShell()
	s.loading = false
	s.router.Replace("/settings/general")
	s.settingsQuery = "endpoint"
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	if !tester.HasText("Endpoint") || !tester.HasText("Voice") {
		t.Fatalf("search hit missing; texts=%q", tester.Texts())
	}
	if tester.HasText("Terminal") {
		t.Fatalf("the page list stayed under an active search; texts=%q", tester.Texts())
	}
	if err := tester.Click("Endpoint"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if got := s.router.Path(); got != "/settings/voice" {
		t.Fatalf("route after the click = %q, want /settings/voice", got)
	}
	if s.settingsQuery != "" {
		t.Fatalf("the search box kept %q", s.settingsQuery)
	}
}

func TestVoiceSettingsLiveOnTheirOwnPage(t *testing.T) {
	s := NewShell()
	s.loading = false
	s.router.Replace("/settings/general")
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	if tester.HasText("Voice input") || tester.HasText("API key") {
		t.Fatalf("General still carries the voice group; texts=%q", tester.Texts())
	}
	if !tester.HasText("Voice") {
		t.Fatalf("the Settings list has no Voice page; texts=%q", tester.Texts())
	}
	s.router.Replace("/settings/voice")
	tester = ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	for _, want := range []string{"Endpoint", "API key", "Model"} {
		if !tester.HasText(want) {
			t.Fatalf("Voice page lacks %q; texts=%q", want, tester.Texts())
		}
	}
}

// TestAgentGroupDrawsSharedGitFactsOnce: three agents in one repository show
// the branch and the diff once, on the Workspace label; an agent in another
// repository keeps its own facts on its row.
func TestAgentGroupDrawsSharedGitFactsOnce(t *testing.T) {
	s := NewShell()
	s.loading = false
	s.workbench.markers = agent.NewMarkerStore()
	s.workbench.directory = agent.NewAgentDirectory()
	s.projection.Agents = []herdr.Agent{{PaneID: "p1"}, {PaneID: "p2"}, {PaneID: "p3"}}
	s.projection.Panes = []herdr.Pane{{ID: "p1", CWD: "/r/a"}, {ID: "p2", CWD: "/r/a"}, {ID: "p3", CWD: "/r/worktree"}}
	card := func(term, title, pane string) agent.AgentCardModel {
		return agent.AgentCardModel{Key: agent.AgentKey{TerminalID: term}, Provider: history.AgentPi, Title: title,
			ProjectID: "proj", ProjectName: "solco", PaneID: pane}
	}
	s.workbench.directory.Replace([]agent.AgentCardModel{card("t1", "first", "p1"), card("t2", "second", "p2"), card("t3", "third", "p3")})
	s.git = &gitService{root: "/r", rootOf: map[string]string{"/r/a": "/r", "/r/worktree": "/rw"}, meta: newGitMetaService()}
	s.git.meta.entries["/r"] = gitMetaEntry{branch: "main", adds: 2842, dels: 334, files: 68, resolved: true}
	s.git.meta.entries["/rw"] = gitMetaEntry{branch: "feat/x", adds: 5, resolved: true}

	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	count := map[string]int{}
	for _, text := range tester.Texts() {
		count[text]++
	}
	if count["main"] != 1 || count["+2842"] != 1 || count["68"] != 1 {
		t.Fatalf("shared facts drawn main=%d +2842=%d files=%d times, want once each; texts=%q", count["main"], count["+2842"], count["68"], tester.Texts())
	}
	if count["feat/x"] != 1 || count["+5"] != 1 {
		t.Fatalf("the other repository's agent lost its own facts; texts=%q", tester.Texts())
	}
}

func TestSelectedTabChipGetsMoreRoom(t *testing.T) {
	if !(tabChipSelectedMinWidth > tabChipMinWidth && tabChipSelectedMaxWidth > tabChipMaxWidth) {
		t.Fatalf("selected chip %v–%v is not wider than the rest %v–%v",
			tabChipSelectedMinWidth, tabChipSelectedMaxWidth, tabChipMinWidth, tabChipMaxWidth)
	}
}

// TestHostEndpointShowsOnlyOnTheSelectedRow: the Hosts list gives the name
// the whole row; user@host appears on the selected (or hovered) row only.
func TestHostEndpointShowsOnlyOnTheSelectedRow(t *testing.T) {
	s := NewShell()
	s.loading = false
	entry := sshconfig.Entry{Kind: sshconfig.KindHost, Patterns: []string{"prod"}, File: "/tmp/cfg", Line: 1,
		Lines: []string{"Host prod", "  HostName 1.2.3.4", "  User root"}}
	s.hosts.entries = []sshconfig.Entry{entry}
	s.hosts.loaded = true
	s.router.Replace(routeHosts)
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	if !tester.HasText("prod") || tester.HasText("root@1.2.3.4") {
		t.Fatalf("an unselected row drew its endpoint; texts=%q", tester.Texts())
	}
	s.hosts.selected = hostEntryKey(entry)
	tester.Frame()
	if !tester.HasText("root@1.2.3.4") {
		t.Fatalf("the selected row lost its endpoint; texts=%q", tester.Texts())
	}
}

// TestSelectedTabEndsWholeInView: the ‹ › buttons appear once the strip
// overflows and narrow it, so the selected chip — close × included — must
// still end inside the strip after the reveal settles.
func TestSelectedTabEndsWholeInView(t *testing.T) {
	for _, selected := range []string{"t40", "t20"} {
		s := tabBarShell(40, selected)
		tester := ui.NewTester(s.View, 1100, 800)
		for i := 0; i < 3; i++ {
			tester.Frame()
		}
		closeBtn, ok := tester.Find("Close Tab")
		right, _ := tester.Find("Scroll Tabs Right")
		if !ok || closeBtn.W == 0 || closeBtn.X+closeBtn.W > right.X {
			t.Fatalf("%s: close × %+v is clipped by the strip's edge (› at x %.0f)", selected, closeBtn, right.X)
		}
	}
}

// TestCommandCenterShowsTheMenuAccelerators pins that a palette command which
// has a key shows exactly the key the menu binds, and that Quota — a page the
// Sessions sidebar reaches — is also a palette command.
func TestCommandCenterShowsTheMenuAccelerators(t *testing.T) {
	s := NewShell()
	menu := map[string]string{}
	for _, name := range append([]string{homeDestination.name}, "Changes", "Sessions", "Files", "Hosts") {
		if item := menuItemNamed(menuNamed(t, s.MenuBar(nil), "Go").Submenu, name); item != nil {
			menu[name] = item.Accelerator
		}
	}
	menu["Settings"] = accelSettings
	seen := map[string]bool{}
	for _, a := range s.buildCommandCenterActions() {
		if a.Section != "App" {
			continue
		}
		seen[a.Title] = true
		if accel, ok := menu[a.Title]; ok && a.Shortcut != acceleratorLabel(accel) {
			t.Errorf("%s shows %q, the menu binds %q", a.Title, a.Shortcut, accel)
		}
	}
	if !seen["Quota"] {
		t.Error("the Command Center has no Quota command")
	}
	if got := acceleratorLabel(accelSettings); got != "⌘," {
		t.Errorf("Settings label = %q, want ⌘,", got)
	}
}
