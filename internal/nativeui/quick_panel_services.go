package nativeui

/**
 * [INPUT]: selected live Herdr tab root, script definitions and bounded observed-port snapshot, service_index.go 的 Pane 归因服务（sidebar_services.go 行模型）
 * [OUTPUT]: compact Services tab inside the existing Agent activity floating panel: Running（命名服务行 + 归属 + Preview）、Other listeners（无 Pane 归因端口）、Scripts（Run）
 * [POS]: shared Header Agent/Services popover; no extra right rail or runtime owner. 2026-11-02 redesign per designs/services-surfaces: named, attributed rows replace the bare port list; Scripts stay the only launch surface.
 * [PROTOCOL]: update this header and check CLAUDE.md
 */

import (
	"fmt"
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/scripts"
	"github.com/thewei/solco/internal/services"
)

func (s *Shell) quickPanelServices(c *ui.Context) {
	t := c.Theme()
	sp := Spacing()
	root := s.selectedTabCWD()
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(0, sp.S, sp.M).Gap(sp.M).Children(func() {
			if root == "" {
				emptyState(c, "No project selected", "Pick a tab to see its services and scripts.")
				return
			}
			ui.Row(c).FillWidth().Padding(sp.XS, sp.M).Gap(sp.S).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconFolder).Size(14, 14).TextColor(t.TextMuted)
				tipped(c, ui.Text(c, filepath.Base(root)).FontSize(Typography().Caption).FontWeight(WeightSemibold).
					Grow(1).MinWidth(0).SingleLine(), root)
			})
			// Running — the same rows the sidebar's Services section
			// shows: one shared model (serviceRowModels), never a
			// root-filtered subset (2026-11-02 user request).
			scoped := s.serviceRowModels()
			ui.Column(c).FillWidth().Gap(sp.S).Children(func() {
				quickPanelGroupHeader(c, "Running", len(scoped), nil)
				if len(scoped) == 0 {
					ui.Text(c, "No services running").Padding(0, sp.M).
						FontSize(Typography().Caption).TextColor(t.TextMuted)
				}
				for _, row := range scoped {
					s.quickPanelServiceRow(c, row)
				}
			})
			// Other listeners — observed ports that no pane's foreground
			// claims (homeless daemons): honest and unattributed.
			others := unclaimedPorts(s.observedPorts(), scoped)
			ui.Column(c).FillWidth().Gap(sp.S).Children(func() {
				quickPanelGroupHeader(c, "Other listeners", len(others), nil)
				if len(others) == 0 {
					ui.Text(c, "No other ports").Padding(0, sp.M).
						FontSize(Typography().Caption).TextColor(t.TextMuted)
				}
				for _, port := range others {
					p := port
					ui.Row(c).FillWidth().Gap(sp.S).Padding(sp.S, sp.M).Radius(Radius().Control).
						Background(designTokens(t.Dark).Panel).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, iconGlobe).Size(14, 14).TextColor(t.TextMuted)
						ui.Text(c, fmt.Sprintf("localhost:%d", p)).FontSize(Typography().BodySmall).Grow(1)
						if compactButton(c, "Preview").Clicked() {
							s.openLocalPreviewURL(services.LocalPreviewURL(p))
						}
					})
				}
			})
			ui.Divider(c)
			// Scripts have a real Herdr launch path; the button is present
			// only for configured definitions, never for sample commands.
			ui.Column(c).FillWidth().Gap(sp.S).Children(func() {
				var defs []scripts.ScriptDefinition
				if s.scriptStore != nil {
					defs = s.scriptStore.List(root)
				}
				quickPanelGroupHeader(c, "Scripts", len(defs), nil)
				if len(defs) == 0 {
					ui.Text(c, "No scripts").Padding(0, sp.M).
						FontSize(Typography().Caption).TextColor(t.TextMuted)
				}
				for _, def := range defs {
					script := def
					ui.Row(c).FillWidth().Gap(sp.S).Padding(sp.S, sp.M).Radius(Radius().Control).
						Background(designTokens(t.Dark).Panel).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, iconTerminal).Size(14, 14).TextColor(t.TextMuted)
						ui.Column(c).Grow(1).MinWidth(0).Gap(sp.XXS).Children(func() {
							tipped(c, ui.Text(c, script.Name).FontSize(Typography().BodySmall).FontWeight(WeightSemibold).
								SingleLine(), script.Name)
							kind := "Service"
							if script.OneShot {
								kind = "One-shot"
							}
							ui.Text(c, kind).FontSize(Typography().Micro).TextColor(t.TextMuted)
						})
						run := compactButton(c, "Run")
						run.Disabled(s.scriptBusy[script.Name])
						if run.Clicked() {
							s.runScriptCommand(root, script)
						}
					})
				}
			})
		})
	})
}

// quickPanelServiceRow is the popover's named service row: the colored
// brand mark, the row title (the script definition's name for a
// script-launched service) with its ports, who started it, and the
// Preview action for the first port.
func (s *Shell) quickPanelServiceRow(c *ui.Context, row serviceRowModel) {
	t := c.Theme()
	sp := Spacing()
	name := row.Title
	detail := serviceActivityDetail(row.Act)
	ui.Row(c).FillWidth().Gap(sp.S).Padding(sp.S, sp.M).Radius(Radius().Control).
		Background(designTokens(t.Dark).Panel).AlignItems(ui.Center).Children(func() {
		serviceMarkView(c, row.Act)
		ui.Column(c).Grow(1).MinWidth(0).Gap(sp.XXS).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, name).FontSize(Typography().BodySmall).FontWeight(WeightSemibold).SingleLine().Shrink(0)
				tipped(c, ui.Text(c, servicePortsText(row.Act)).FontSize(Typography().Micro).
					TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0).Grow(1), detail)
			})
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				serviceOriginView(c, row)
			})
		})
		statusIndicatorService(c, ToneSuccess, detail)
		if len(row.Act.Ports) > 0 {
			if compactButton(c, "Preview").Clicked() {
				s.openLocalPreviewURL(services.LocalPreviewURL(row.Act.Ports[0]))
			}
		}
	})
}
