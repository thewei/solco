package nativeui

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/agenthooks"
	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/settings"
)

/**
 * [INPUT]: 依赖 agent 集成审计与 agenthooks 直连 hook 诊断（providers_ui 统一状态机）、mygo/ui、ds_components 状态件
 * [OUTPUT]: 对外提供 settingsProviders（Settings→Agents 开关页：可启动 Agent 每家一个开关行 + 状态/修复；开关首次开启即装 Solco 自己的 status hook；OMP 附属独立直播文字 Doctor 行，只有点击 Install/Update 才装）、agentToggleRow/agentEnabled/setAgentEnabled/installAgentWiring/providerRows/facetControls
 * [POS]: 开关为主面（= New Tab "+" 菜单的同一份 kinds 清单，Terminal 除外），Hook Doctor 诊断降为行内状态与修复动作；不可启动的 Agent（与 Aider）保留无开关的健康行；审计只在路由进入/手动刷新/动作完成后发生，绝不随渲染轮询
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// settingsProviders is the Agents settings page: one toggle row per
// launchable agent — the same list the New Tab "+" menu offers — with the
// audit's status line and its single repair action beside each switch.
// Enabling an agent installs Solco's own status hook for it at once.
// Agents Herdr cannot start here (and hook-only extras such as Aider)
// keep their health rows, without a toggle, below. The audit runs on
// route entry, manual refresh and after actions — never from render.
func (s *Shell) settingsProviders(c *ui.Context) {
	if !s.providers.loaded && !s.providers.loading && s.providers.errText == "" {
		s.requestProvidersAudit()
	}
	if s.ompStream.manager != nil && !s.ompStream.loaded && !s.ompStream.loading {
		s.requestOMPStreamDiagnosis()
	}
	s.consumeProvidersToast(c)
	if s.ompStream.pendingToast != "" {
		c.Toast(s.ompStream.pendingToast)
		s.ompStream.pendingToast = ""
	}

	sp := Spacing()
	t := c.Theme()
	if s.providers.errText != "" {
		inlineNotice(c, ToneError, s.providers.errText, func() {
			s.providers.loading = false
			s.providers.errText = ""
			s.requestProvidersAudit()
		})
	}

	ui.Row(c).FillWidth().Gap(sp.M).AlignItems(ui.Center).Children(func() {
		if s.providers.loading {
			ui.Spinner(c).Size(14, 14)
			ui.Text(c, "Checking…").FontSize(Typography().Caption).TextColor(t.TextMuted)
		}
		ui.Box(c).Grow(1)
		if gorexIconButton(c, gorexColorsOf(t.Dark), iconRefresh, "Refresh", "Refresh", false, gorexIconBtn, gorexIconGlyph).Clicked() {
			s.requestProvidersAudit()
			s.requestOMPStreamDiagnosis()
		}
	})

	s.ensureLaunchKinds()
	if s.launchKinds == nil {
		loadingState(c, "Checking…")
		return
	}
	launchable := make(map[history.AgentID]bool, len(s.launchKinds))
	var rows []settingsRow
	for _, kind := range s.launchKinds {
		if kind.ID == app.TerminalKind {
			continue
		}
		id := history.AgentID(kind.ID)
		launchable[id] = true
		rows = append(rows, s.agentToggleRow(c, kind))
		if id == history.AgentOMP && s.ompStream.manager != nil {
			rows = append(rows, s.ompStreamRow(c))
		}
	}
	if len(rows) > 0 {
		settingsGroup(c, "Agents", rows...)
	}
	if s.providers.loaded {
		var diagnosed []settingsRow
		for _, prow := range s.providers.rows {
			if launchable[prow.provider] {
				continue
			}
			diagnosed = append(diagnosed, s.providerRows(c, prow)...)
		}
		if len(diagnosed) > 0 {
			settingsGroup(c, "Other integrations", diagnosed...)
		}
	}
}

// agentEnabled is the Shell-side toggle read (the presentation snapshot;
// the launch gate reads the same settings through its own seam).
func (s *Shell) agentEnabled(id history.AgentID) bool {
	return s.settings.Agents.IsEnabled(string(id))
}

// setAgentEnabled persists the toggle. Enabling an agent installs its
// Solco status hook at once — the toggle-on promise. Disabling only hides
// the agent from the launch surfaces; nothing already installed is removed.
func (s *Shell) setAgentEnabled(id history.AgentID, on bool) {
	before := s.agentEnabled(id)
	s.applySettings(func(v *settings.Settings) error {
		if on {
			delete(v.Agents.Enabled, string(id)) // absent = enabled
			return nil
		}
		if v.Agents.Enabled == nil {
			v.Agents.Enabled = map[string]bool{}
		}
		v.Agents.Enabled[string(id)] = false
		return nil
	})
	if on && !before {
		s.installAgentWiring(id)
		s.trustAutomationFolders(id)
	}
}

// trustAutomationFolders pre-answers Claude Code's "trust this folder" for
// the folder Herdr keeps worktrees in, so an Automations run in a fresh
// worktree never stops on it (user-approved 2026-11-03). One added entry in
// Claude's own config, off the render path; an existing record is never
// touched. Codex has no folder inheritance and is answered at run time by the
// plugin instead.
func (s *Shell) trustAutomationFolders(id history.AgentID) {
	if id != history.AgentClaudeCode || s.providers.hooks == nil {
		return
	}
	s.dispatch(func() {
		changed, err := s.providers.hooks.TrustWorktrees()
		if err != nil {
			slog.Warn("claude worktree trust failed", "error", err)
			return
		}
		if changed {
			slog.Info("claude worktree trust recorded")
		}
	})
}

// installAgentWiring turns on the agent's Solco status hook the moment it
// is enabled. Only Solco's own hook installs here; the Herdr integration
// keeps its manual repair lane on the row.
func (s *Shell) installAgentWiring(id history.AgentID) {
	provider, ok := hookProviderOf(id)
	if !ok {
		return // agents without a Solco hook have nothing to install
	}
	if row, ok := s.providersRowFor(id); ok && row.hook != nil {
		if row.hook.State == agenthooks.StateOK {
			return // already current; nothing to install
		}
		s.runProviderAction(row, facetStatusHook)
		return
	}
	// The audit has not reconciled yet (the first-run guide): install
	// directly off the render path; the next audit reports the result.
	s.dispatch(func() {
		if _, err := s.providers.hooks.Install(provider); err != nil {
			slog.Warn("agent status hook install failed", "agent", id, "error", err)
			return
		}
		s.applyOnWindow(func() { s.requestProvidersAudit() })
	})
}

// providersRowFor finds the audited card for one launchable agent.
func (s *Shell) providersRowFor(id history.AgentID) (providerRow, bool) {
	for _, row := range s.providers.rows {
		if row.provider == id {
			return row, true
		}
	}
	return providerRow{}, false
}

// agentToggleRow is one launchable agent's toggle row, shared by this page
// and the first-run guide: brand tile, status note, optional repair, and
// the switch.
func (s *Shell) agentToggleRow(c *ui.Context, kind app.SessionKind) settingsRow {
	id := history.AgentID(kind.ID)
	on := s.agentEnabled(id)
	note, repair := s.agentHealth(c, id)
	return settingsRow{
		lead:  func() { agentTile(c, id, 24) },
		label: kind.Label,
		note:  note,
		control: func() {
			ui.Row(c).Gap(Spacing().M).AlignItems(ui.Center).Children(func() {
				if repair != nil {
					repair()
				}
				if value, changed := settingSwitch(c, kind.Label, on); changed {
					s.setAgentEnabled(id, value)
				}
			})
		},
	}
}

// agentHealth joins the audit onto one toggle row: a status line plus the
// single repair action of the leading mechanism — our status hook where
// one exists, else the Herdr integration while it is load-bearing.
func (s *Shell) agentHealth(c *ui.Context, id history.AgentID) (string, func()) {
	row, ok := s.providersRowFor(id)
	if !ok {
		return "", nil
	}
	if row.hook != nil {
		hook := row.hook
		return hookNote(hook), s.repairAction(c, row, facetStatusHook)
	}
	if row.integration != nil {
		integration := row.integration
		return healthStateText(integration.State), s.repairAction(c, row, facetIntegration)
	}
	return "", nil
}

// repairAction renders the row's one safe repair action, nil when the
// facet needs nothing or the audit has not reached this agent.
func (s *Shell) repairAction(c *ui.Context, row providerRow, facet providerFacet) func() {
	if row.actionLabel(facet) == "" {
		return nil
	}
	return func() {
		inFlight := s.providers.actionInFlight(row, facet)
		label := row.actionLabel(facet)
		verb, _, _ := strings.Cut(label, " ")
		switch {
		case inFlight:
			ui.Spinner(c).Size(14, 14)
			regularButton(c, verb).Label(label).Disabled(true)
		case regularButton(c, verb).Label(label).Clicked():
			s.runProviderAction(row, facet)
		}
	}
}

// providerRows renders one agent as list rows. Hook-covered agents lead with
// our direct status hook; the Herdr integration becomes the fallback row
// below it — muted while the hook is healthy (nothing to repair), actionable
// again only while the hook cannot do the job. Agents without a status hook
// keep the Herdr integration as their single (and only) mechanism.
func (s *Shell) providerRows(c *ui.Context, row providerRow) []settingsRow {
	lead := func() { agentTile(c, row.provider, 24) }
	blank := func() { ui.Box(c).Width(24).Shrink(0) }
	if row.hook != nil {
		hook := row.hook
		rows := []settingsRow{{
			lead: lead, label: row.name, note: hookNote(hook),
			control: func() {
				s.facetControls(c, row, facetStatusHook, hookStateText(hook.State), hookStateTone(hook.State))
			},
		}}
		if row.integration != nil {
			integration := row.integration
			tone := healthTone(integration.State)
			if row.hookHealthy() {
				// The fallback is not load-bearing; present it as
				// information, never as a warning or a nag.
				tone = ToneMuted
			}
			rows = append(rows, settingsRow{
				lead: blank, attached: true, label: "Herdr integration", note: integrationNote(*integration),
				control: func() {
					s.facetControls(c, row, facetIntegration, healthStateText(integration.State), tone)
				},
			})
		}
		return rows
	}
	if row.integration != nil {
		integration := row.integration
		return []settingsRow{{
			lead: lead, label: row.name, note: integrationNote(*integration),
			control: func() {
				s.facetControls(c, row, facetIntegration, healthStateText(integration.State), healthTone(integration.State))
			},
		}}
	}
	// The integration audit failed or has not reconciled this agent; the
	// inline notice above carries the reason. A row with nothing to report
	// stays a plain name rather than a column of Unknown pills.
	return []settingsRow{{lead: lead, label: row.name}}
}

// facetControls renders one facet's state pill plus its single safe action
// with the in-flight duplicate-disabled state.
func (s *Shell) facetControls(c *ui.Context, row providerRow, facet providerFacet, stateText string, tone StatusTone) {
	ui.Row(c).Gap(Spacing().M).AlignItems(ui.Center).Children(func() {
		statusPill(c, stateText, tone)
		inFlight := s.providers.actionInFlight(row, facet)
		label := row.actionLabel(facet)
		verb, _, _ := strings.Cut(label, " ")
		switch {
		case inFlight:
			ui.Spinner(c).Size(14, 14)
			regularButton(c, verb).Label(label).Disabled(true)
		case label != "":
			if regularButton(c, verb).Label(label).Clicked() {
				s.runProviderAction(row, facet)
			}
		}
	})
}

// hookNote is the short line under an agent's status hook: what the hook
// gives the user, not how it is wired.
func hookNote(hook *agenthooks.Diagnosis) string {
	switch hook.State {
	case agenthooks.StateOK:
		return "Live status is on"
	case agenthooks.StateOutdated:
		return "Update to keep live status working"
	case agenthooks.StateNotInstalled:
		return "Install to show live status"
	case agenthooks.StateNeedsApproval:
		return "Trust it once in Codex (/hooks) to turn live status on"
	case agenthooks.StateError:
		return hook.Detail
	default:
		return "Status comes from Herdr's screen detection"
	}
}

// integrationNote is the version line of a Herdr integration; the mechanism
// behind it stays out of sight.
func integrationNote(row agent.ProviderIntegrationHealth) string {
	if row.Version == "" {
		return ""
	}
	note := row.Version
	if row.LatestVersion != "" {
		note += fmt.Sprintf(" (latest %s)", row.LatestVersion)
	}
	return note
}
