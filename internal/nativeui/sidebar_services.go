package nativeui

/**
 * [INPUT]: 依赖 service_index.go（sidebarServiceIndex/paneServiceActivity/serviceActivityDetail）、brands.go/proc_brands.go（visualMark/procBrandAsset/procBrandTints/procFriendlyName/isShell）、internal/agent.AgentCardModel、internal/history、internal/services.LocalPreviewURL、Shell.scriptPanes（script_actions.go 记录）
 * [OUTPUT]: 对外提供 Shell.servicesSection/serviceItems/serviceRowModels/serviceCardItem/rememberScriptPane、classifyServiceOrigin、cardForPane、groupServiceRows、unclaimedPorts、procServiceMark、serviceMarkView、serviceOriginView
 * [POS]: designs/services-surfaces（2026-11-02 定稿 Option A）的呈现层：侧栏 Agents 下方的 Services 区块，Pane 归因的检测服务两行卡 + 多工作区组标签，点击按 panePrefersChat 跳转；未运行 Scripts 只留 QuickPanel、Project-wide 歧义 daemon 只留浮层 Other listeners；只读投影，无第二监视器，无新 Herdr 方法
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/services"
)

// serviceOrigin classifies who started a pane's service: an agent card
// bound to the pane, a script this shell launched into it, or a manual run
// in the terminal. It is presentation-only attribution over the same
// heuristic facts as the service index — never a second runtime.
type serviceOrigin uint8

const (
	serviceOriginManual serviceOrigin = iota
	serviceOriginAgent
	serviceOriginScript
)

// serviceRowModel is one Services row: the pane-attributed service activity
// plus who started it and where it lives. Rows rebuild from the index on
// every render; nothing here is persisted.
type serviceRowModel struct {
	PaneID string
	Act    paneServiceActivity
	Origin serviceOrigin
	// Provider and OriginLabel name the origin: the agent provider and its
	// display label, or the launching script's name; empty for manual runs.
	Provider    history.AgentID
	OriginLabel string
	Card        *agent.AgentCardModel // the pane's agent card, nil when none
	ProjectID   string
	ProjectName string
	// DirName is the pane working directory's leaf (2026-11-03 user
	// request): the second line's small directory fact, so same-named
	// titles in two projects stay apart (`start · devspace` vs `start ·
	// api`). DirPath is the tooltip's full path; both empty when the pane
	// has no directory.
	DirName string
	DirPath string
	// Title is the row's first line: a script-launched service carries the
	// script definition's own name (2026-11-02 user request), everything
	// else the process's friendly name.
	Title string
}

// serviceGroupModel groups rows by workspace for the section's faint
// labels; a lone group renders without its label.
type serviceGroupModel struct {
	Key  string
	Name string
	Rows []serviceRowModel
}

// classifyServiceOrigin resolves who started the service in a pane: an
// agent card bound to the pane wins (the pane is the agent's), then a
// script this shell launched into the pane (the Run button's persisted
// record), then the argv inference — a manually typed `pnpm run start`
// lands on its project's script name (2026-11-03 title alignment) — and
// only then a manual run with the process name as its title.
func classifyServiceOrigin(cards []agent.AgentCardModel, scriptPanes map[string]string, origins map[string]string, paneID string) (serviceOrigin, history.AgentID, string) {
	if card := cardForPane(cards, paneID); card != nil {
		return serviceOriginAgent, card.Provider, card.ProviderLabel
	}
	if name := scriptPanes[paneID]; name != "" {
		return serviceOriginScript, "", name
	}
	if name := origins[paneID]; name != "" {
		return serviceOriginScript, "", name
	}
	return serviceOriginManual, "", ""
}

// cardForPane finds the agent card bound to a pane, nil when none.
func cardForPane(cards []agent.AgentCardModel, paneID string) *agent.AgentCardModel {
	for i := range cards {
		if cards[i].PaneID == paneID {
			return &cards[i]
		}
	}
	return nil
}

// groupServiceRows partitions rows by workspace in their given order; a
// group takes the first row's project name ("" groups under "Workspace").
func groupServiceRows(rows []serviceRowModel) []serviceGroupModel {
	var groups []serviceGroupModel
	index := map[string]int{}
	for _, row := range rows {
		at, ok := index[row.ProjectID]
		if !ok {
			at = len(groups)
			index[row.ProjectID] = at
			groups = append(groups, serviceGroupModel{Key: row.ProjectID, Name: firstText(row.ProjectName, "Workspace")})
		}
		groups[at].Rows = append(groups[at].Rows, row)
	}
	return groups
}

// unclaimedPorts narrows the root-scoped port snapshot to the ports no
// pane-attributed service already presents — the popover's Other listeners.
func unclaimedPorts(ports []uint16, rows []serviceRowModel) []uint16 {
	claimed := map[uint16]bool{}
	for _, row := range rows {
		for _, port := range row.Act.Ports {
			claimed[port] = true
		}
	}
	out := make([]uint16, 0, len(ports))
	for _, port := range ports {
		if !claimed[port] {
			out = append(out, port)
		}
	}
	return out
}

// procServiceMark resolves one service's colored brand mark: the process's
// brand SVG carrying its brand tint (procBrandTints), ok=false when the
// process has no known mark or is a shell.
func procServiceMark(act paneServiceActivity) (visualMark, bool) {
	if act.Proc == "" || isShell(act.Proc) {
		return visualMark{}, false
	}
	asset, svg := procBrandAsset(act.Proc)
	if svg == nil {
		return visualMark{}, false
	}
	mark := visualMark{svg: svg}
	if hex, ok := procBrandTints[asset]; ok {
		mark.tint, mark.tinted = ui.Hex(hex), true
	}
	return mark, true
}

// serviceRowModels projects the section's rows: the pane-attributed
// detected services only. Script definitions stay in the QuickPanel and
// project-wide daemons in the popover's Other listeners (2026-11-02
// decision); a pane that closed after the scan has nothing to jump to and
// is skipped.
func (s *Shell) serviceRowModels() []serviceRowModel {
	var rows []serviceRowModel
	if len(s.serviceIndex.Panes) == 0 {
		return rows
	}
	cards := s.workbenchCards()
	paneOrder := s.paneOrderMap()
	scriptPanes := s.scriptPaneNames()
	for paneID, act := range s.serviceIndex.Panes {
		pane := paneByID(s.projection, paneID)
		if pane == nil {
			continue
		}
		projectName := ""
		for _, project := range s.projection.Projects {
			if project.ID == pane.ProjectID {
				projectName = project.Label
				break
			}
		}
		origin, provider, label := classifyServiceOrigin(cards, scriptPanes, s.serviceOrigins, paneID)
		title := serviceTitle(act)
		if origin == serviceOriginScript {
			title = s.scriptTitleFor(label, pane.CWD)
		}
		row := serviceRowModel{
			PaneID:      paneID,
			Act:         act,
			Origin:      origin,
			Provider:    provider,
			OriginLabel: label,
			Card:        cardForPane(cards, paneID),
			ProjectID:   pane.ProjectID,
			ProjectName: projectName,
			Title:       title,
		}
		// The pane's own directory is the disambiguator (2026-11-03): the
		// leaf joins the second line in small text, the full path rides
		// the tooltip. Degenerate roots render nothing.
		if pane.CWD != "" && pane.CWD != "/" {
			row.DirPath = pane.CWD
			row.DirName = filepath.Base(pane.CWD)
		}
		rows = append(rows, row)
	}
	slices.SortStableFunc(rows, func(a, b serviceRowModel) int {
		return paneOrder[a.PaneID] - paneOrder[b.PaneID]
	})
	return rows
}

// serviceTitle is the process's friendly name — the default row title.
func serviceTitle(act paneServiceActivity) string {
	return firstText(procFriendlyName(act.Proc), act.Proc, "service")
}

// scriptTitleFor resolves a script-launched service's title from the
// script definitions (2026-11-02 user request): the definition scoped to
// the pane's directory is the authority; a deleted definition falls back
// to the recorded launch name.
func (s *Shell) scriptTitleFor(scriptName, paneCWD string) string {
	if s.scriptStore != nil {
		for _, def := range s.scriptStore.List("") {
			if def.Name == scriptName && (def.ProjectPath == paneCWD || dirContains(def.ProjectPath, paneCWD)) {
				return def.Name
			}
		}
	}
	return scriptName
}

// servicesSection is the sidebar's Services block, directly under Agents
// (2026-11-02 designs/services-surfaces, Option A): the pane-attributed
// detected services in the Agents section's two-line card anatomy —
// colored brand mark, friendly name, ports, who started it, and the
// service square.
func (s *Shell) servicesSection(c *ui.Context) {
	rows := s.serviceRowModels()
	s.sidebarSection(c, sidebarSectionSpec{
		Key:   "section:services",
		Title: "Services",
		Count: len(rows),
	}, &s.servicesOpen, func() {
		s.serviceItems(c, rows)
	})
}

// serviceItems draws the section body: one foldable workspace group per
// project, mirroring the Agents list's group headers (2026-11-03 user
// request) — chevron + workspace label + service count, click toggles,
// context menu folds/unfolds all. The fold reuses the Agents folds'
// persistence (foldedAgents → Session.FoldedAgents) under "svc-group:"
// keys; open is the default. No rows keeps the honest empty line.
func (s *Shell) serviceItems(c *ui.Context, rows []serviceRowModel) {
	if len(rows) == 0 {
		ui.Text(c, "No services running").Padding(Spacing().M, Spacing().L).TextColor(c.Theme().TextMuted)
		return
	}
	groups := groupServiceRows(rows)
	for _, group := range groups {
		foldKey := agentFoldKey("svc-group", group.Key)
		folded := s.foldedAgents[foldKey]
		t := c.Theme()
		ui.Box(c.Key("svc-group-gap:" + group.Key)).Height(5).Shrink(0)
		head := ui.ButtonBase(c.Key("svc-group:"+group.Key)).FillWidth().Height(24).Radius(treeRowRadius).
			Padding(0, agentCardPadX, 0, 2).Gap(4).AlignItems(ui.Center).FocusRing(false).
			Label("Toggle " + group.Name + " services")
		if head.Hovered() {
			head.Background(t.SurfaceHover)
		}
		head.Transition(rowFade)
		head.Children(func() {
			treeChevron(c, true, !folded, t.TextMuted)
			ui.Text(c, group.Name).FontSize(Typography().Micro).
				TextColor(t.TextMuted.Alpha(0.75)).SingleLine().Ellipsis("…").Grow(1).MinWidth(0).PassThrough()
			if folded {
				ui.Text(c, itoa(len(group.Rows))).FontSize(Typography().Micro).
					TextColor(t.TextMuted).SingleLine().Shrink(0).PassThrough()
			}
		})
		if head.Clicked() {
			s.toggleAgentFold(foldKey)
		}
		head.ContextMenu(func(m *ui.Menu) {
			if m.Item("Fold All Groups").Chosen() {
				s.setServiceGroupFolds(groups, true)
			}
			if m.Item("Unfold All Groups").Chosen() {
				s.setServiceGroupFolds(groups, false)
			}
		})
		if folded {
			continue
		}
		for _, row := range group.Rows {
			s.serviceCardItem(c, row)
		}
	}
}

// setServiceGroupFolds folds or unfolds every workspace group of the
// Services list at once (a group header's context menu).
func (s *Shell) setServiceGroupFolds(groups []serviceGroupModel, fold bool) {
	if s.foldedAgents == nil {
		s.foldedAgents = map[string]bool{}
	}
	for _, group := range groups {
		key := agentFoldKey("svc-group", group.Key)
		if fold {
			s.foldedAgents[key] = true
		} else {
			delete(s.foldedAgents, key)
		}
	}
}

// serviceCardItem draws one Services row and its wiring — the Agents
// card's anatomy. Click jumps to the pane's remembered surface (the
// agent's Chat when the pane carries an agent card, else the terminal);
// the context menu offers the pane actions and per-port previews.
func (s *Shell) serviceCardItem(c *ui.Context, row serviceRowModel) {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	name := row.Title
	detail := serviceActivityDetail(row.Act)
	label := "Service " + detail
	selected := row.PaneID == s.selectedPaneID
	var rowEl ui.Element
	ui.Box(c.Key("svc:" + row.PaneID)).FillWidth().Children(func() {
		rowEl = ui.ButtonBase(c).FillWidth().MinHeight(38).Radius(6).
			Padding(0, agentCardPadX).Gap(agentCardGap).AlignItems(ui.Center).Label(label)
		if tint, ok := rowTint(tokens, selected, rowEl.Hovered()); ok {
			rowEl.Background(tint)
		}
		rowEl.Transition(rowFade)
		rowEl.Children(func() {
			serviceMarkView(c, row.Act)
			ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, name).FontSize(Typography().Body).SingleLine().Shrink(0)
					tipped(c, ui.Text(c, servicePortsText(row.Act)).FontSize(Typography().Micro).
						TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0).Grow(1), detail)
				})
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					serviceOriginView(c, row)
				})
			})
			statusIndicatorService(c, ToneSuccess, detail)
		})
	})
	if rowEl.Clicked() {
		if row.Card != nil {
			s.openAgentFromSidebar(*row.Card)
		} else {
			s.selectPane(row.PaneID)
		}
	}
	rowEl.ContextMenu(func(m *ui.Menu) {
		if row.Card != nil {
			if m.Item("Open Chat").Chosen() {
				s.openChat(*row.Card)
			}
		}
		s.paneOverflowItems(m, row.PaneID, name)
		m.Separator()
		for _, port := range row.Act.Ports {
			p := port
			if m.Item(fmt.Sprintf("Preview :%d", p)).Chosen() {
				s.openLocalPreviewURL(services.LocalPreviewURL(p))
			}
		}
	})
}

// serviceMarkView draws the 18pt mark slot: the colored brand mark when the
// process has one, else the quiet server glyph on the raised tile.
func serviceMarkView(c *ui.Context, act paneServiceActivity) {
	t := c.Theme()
	mark, ok := procServiceMark(act)
	ui.Box(c).Size(agentCardMark, agentCardMark).Shrink(0).Children(func() {
		if !ok {
			ui.Box(c).Size(agentCardMark, agentCardMark).Radius(4).
				Background(designTokens(t.Dark).PanelHover).Center().Children(func() {
				ui.Icon(c, iconServer).Size(11, 11).TextColor(t.TextMuted)
			})
			return
		}
		markView(c, mark, agentCardMark, t.TextMuted)
	})
}

// serviceOriginView draws the second line's attribution: a mini agent mark
// plus its label, the process a script launch runs, or the plain "Manual" —
// the answer to "who started this". The pane directory's leaf follows in
// the same small text (2026-11-03 user request): `start · devspace` keeps
// same-named titles apart across projects, and the full path rides the
// tooltip. Both the sidebar card and the popover row share this view.
func serviceOriginView(c *ui.Context, row serviceRowModel) {
	t := c.Theme()
	switch row.Origin {
	case serviceOriginAgent:
		ui.Box(c).Size(12, 12).Shrink(0).Children(func() {
			markView(c, agentMark(row.Provider, t.Dark), 12, t.TextMuted)
		})
		ui.Text(c, firstText(row.OriginLabel, "Agent")).FontSize(Typography().Micro).
			TextColor(t.TextMuted).SingleLine().Shrink(0)
	case serviceOriginScript:
		// The script's own name is the row's title; the chip keeps what
		// actually runs underneath it.
		ui.Icon(c, iconTerminal).Size(11, 11).TextColor(t.TextMuted)
		ui.Text(c, serviceTitle(row.Act)).FontSize(Typography().Micro).
			TextColor(t.TextMuted).SingleLine().Shrink(0).Ellipsis("…")
	default:
		ui.Text(c, "Manual").FontSize(Typography().Micro).
			TextColor(t.TextMuted).SingleLine().Shrink(0)
	}
	if row.DirName != "" {
		tipped(c, ui.Text(c, "· "+row.DirName).FontSize(Typography().Micro).
			TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0).Ellipsis("…"), row.DirPath)
	}
}

// rememberScriptPane records the pane a script or pane-command launch ran
// in — "<instance>/<pane>" → the command's name — so the Services surfaces
// can attribute its service and title; the association persists through
// the session snapshot. Written on the UI lane.
func (s *Shell) rememberScriptPane(instance, paneID, script string) {
	if instance == "" || paneID == "" || script == "" || s.win == nil {
		return
	}
	s.win.Update(func() {
		if s.scriptPanes == nil {
			s.scriptPanes = make(map[string]string)
		}
		s.scriptPanes[instance+"/"+paneID] = script
	})
}

// scriptPaneNames scopes the launch records to the active instance for
// classification.
func (s *Shell) scriptPaneNames() map[string]string {
	prefix := s.activeInstance + "/"
	out := make(map[string]string, len(s.scriptPanes))
	for key, name := range s.scriptPanes {
		if paneID, ok := strings.CutPrefix(key, prefix); ok {
			out[paneID] = name
		}
	}
	return out
}
