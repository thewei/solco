package nativeui

/**
 * [INPUT]: 依赖 automationsAvailable/refreshAutomations/loadAutomationHistory（automations_ui.go）、internal/automations 的 list/history 形状、internal/history 的只读目录、Shell 的 openPane（侧栏/Header 同一条跳转路径）、copyToClipboard、shared confirm、route_header 的标题栏归属
 * [OUTPUT]: routeAutomations（/automations）、showViewAutomations（toggle 回 Terminal，同 page_files）/goAutomations/automationsPage；工具条（All/Enabled/Drafts/Paused segment、✦ Let an Agent Draft、＋ New）、列表行（状态槽 + 名称 + 副行 + 下次/状态 + 尾 chip）、右侧抽屉（rationale 卡、prompt 卡、事实格、Last 5 runs、Run Now/Pause|Resume/Edit…/Delete…）、两个 sheet（起草 ask 文本、New/Edit 表单）
 * [POS]: com.whstudio.solco.automations 的唯一原生呈现（Option A 管理列表 + 详情抽屉）；调度权威归插件，页面只画 `list --json`/`history --json` 的缓存答案，渲染路径零 IO；「View in Chat」仅当 internal/history 按 agent+cwd+started_at±2min 唯一命中时出现；日志只落 name/run_id/操作/错误/计数，不落 prompt/rationale
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/automations"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

// routeAutomations ("/automations") lives with the other routes in
// router.go; this file owns the page behind it.

// automationsRemoveConfirm is the shared confirm behind Delete….
const automationsRemoveConfirm = "automations-remove"

// The list toolbar's segments, in display order.
const (
	automationsAll = iota
	automationsEnabled
	automationsDrafts
	automationsPaused
)

// automationsDrawerWidth is the detail drawer's fixed width.
const automationsDrawerWidth = 380

// automationsList is the page's presentation state (automationsState.list):
// the plugin's own answer plus what the user is doing on the page. Nothing
// here outlives the gate.
type automationsList struct {
	loading  bool
	loadedAt time.Time
	failed   bool
	errText  string
	result   automations.ListResult
	segment  int
	selected string // the automation the drawer shows, by name
	history  map[string]automations.HistoryResult
	// historyLoading names the drawer's in-flight history read.
	historyLoading string
	newDraft       automationsDraft // ＋ New sheet
	edit           automationsDraft // Edit sheet (original != "")
	askOpen        bool             // ✦ Let an Agent Draft sheet
}

// automationsDraft is the form the New and Edit sheets share.
type automationsDraft struct {
	open      bool
	original  string // edit only: the name `put --original` targets
	name      string
	trigger   int // 0 cron, 1 one-shot
	cronSel   string
	cron      string
	once      string
	repo      string
	agent     string
	prompt    string
	rationale string
	advanced  bool
	overlap   int // 0 skip, 1 queue
	timeout   string
	catchUp   string
	cleanup   string
}

// automationsCronCustom is the preset entry that reveals the free-form cron
// field.
const automationsCronCustom = "Custom…"

// automationsCronPresets are the cron presets the form's Preset field offers.
var automationsCronPresets = []struct {
	label string
	cron  string
}{
	{"Daily 09:00", "0 9 * * *"},
	{"Weekdays 09:00", "0 9 * * 1-5"},
	{"Hourly", "0 * * * *"},
	{"Mondays 09:00", "0 9 * * 1"},
	{automationsCronCustom, ""},
}

// presetCron answers the cron expression a preset label stands for.
func presetCron(label string) string {
	for _, p := range automationsCronPresets {
		if p.label == label {
			return p.cron
		}
	}
	return ""
}

// automationsAskText is what ✦ Let an Agent Draft offers to copy into Chat
// (v1: no direct wiring — the user pastes it to an agent themselves).
const automationsAskText = `Draft an automation for Solco's Automations plugin (com.whstudio.solco.automations, created with herdr-automations add --draft --rationale):
- Goal: what should this automation reliably accomplish?
- Trigger: a cron expression or a one-shot time (RFC3339), and the repository it acts on
- Guardrails: what it must not touch; what timeout_minutes to set; skip or queue when runs overlap
- Cost: the expected duration of one run and the agent/model
Analyze first, then produce a structured prompt; state goal/inputs/guardrails/cost in the rationale. Always create as a draft (--draft); it stays off until I approve it in Solco myself.`

// showViewAutomations toggles the Automations page; like every destination
// switch a pending Commit draft must be explicitly dismissed first.
func (s *Shell) showViewAutomations() {
	if !s.automationsAvailable() {
		return // zero entry while the gate is closed
	}
	if s.holdCommitNavigation() {
		return
	}
	if s.router.Path() == routeAutomations {
		s.landSelectedPane()
		return
	}
	s.router.Push(routeAutomations)
}

// goAutomations is the destination form of showViewAutomations (see
// goFiles): naming Automations while it is open keeps it.
func (s *Shell) goAutomations() {
	if s.router.Path() == routeAutomations {
		return
	}
	s.showViewAutomations()
}

// automationsPage is the Option A management list plus its detail drawer.
// The title bar names the page (route_header.go); the page draws only from
// the cached list — refreshAutomations does the IO in the background.
func (s *Shell) automationsPage(c *ui.Context) {
	if !s.automationsAvailable() {
		s.automationsGateClosed(c)
		return
	}
	s.refreshAutomations(false)
	// Stretch: a Row centres its children across the axis by default, which
	// floated the toolbar and rows to the middle of the page.
	ui.Row(c).FillWidth().Grow(1).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).MinHeight(0).Children(func() {
			s.automationsListBody(c)
		})
		if sel := s.automations.list.automation(s.automations.list.selected); sel != nil {
			s.automationsDrawer(c, *sel)
		}
	})
}

// automationsGateClosed is the install-guidance empty state. The page can
// only be seen here through a restored route — every entry (sidebar tab, Go
// menu, palette) closes with the gate — so it tells the user how to open it.
func (s *Shell) automationsGateClosed(c *ui.Context) {
	var title, detail string
	var commands []string
	switch s.automations.avail.Reason {
	case automations.ReasonDisabled:
		title, detail = "Automations is disabled", "The plugin is installed but not enabled."
		commands = []string{"herdr plugin enable " + automations.PluginID}
	case automations.ReasonVersionUnsupported:
		title = "Automations needs a verified version"
		detail = "The installed plugin is " + s.automations.avail.Version + "; Solco verifies 0.12.x only."
	case automations.ReasonUnreadable:
		title, detail = "Automations is unavailable", "The plugin list could not be read."
	default:
		title, detail = "Install the Automations plugin", "The plugin is not installed on this machine yet."
		commands = []string{
			"herdr plugin link <repo>/plugins/solco-automations",
			"herdr plugin enable " + automations.PluginID,
		}
	}
	emptyState(c, title, detail)
	if len(commands) > 0 {
		t := c.Theme()
		sp := Spacing()
		ui.Column(c).Gap(sp.S).Padding(0, sp.XL).Children(func() {
			for _, cmd := range commands {
				ui.Row(c).FillWidth().Gap(sp.S).AlignItems(ui.Center).Children(func() {
					ui.Text(c, cmd).FontSize(Typography().BodySmall).TextColor(t.TextMuted).
						SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
					if compactButton(c, "Copy").Clicked() {
						s.copyToClipboard(cmd)
					}
				})
			}
		})
	}
}

// automationsListBody is the toolbar over the rows, or the empty/failed
// states.
func (s *Shell) automationsListBody(c *ui.Context) {
	st := &s.automations.list
	sp := Spacing()
	ui.Column(c).FillWidth().Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		ui.Row(c).FillWidth().Padding(pageTopInset(), sp.XL, sp.M).Gap(sp.M).AlignItems(ui.Center).Wrap().Children(func() {
			segmentedControl(c, &st.segment, "All", "Enabled", "Drafts", "Paused")
			ui.Spacer(c)
			if regularButton(c, "✦ Let an Agent Draft").Clicked() {
				s.openAutomationsAsk()
			}
			if regularPrimaryButton(c, "＋ New").Clicked() {
				s.openAutomationsNew()
			}
		})
		if st.result.Paused {
			inlineNotice(c, ToneWarning, "The plugin's daemon is paused: automations will not fire until it resumes.", nil)
		}
		if st.errText != "" {
			inlineNotice(c, ToneError, st.errText, func() {
				st.errText = ""
				s.refreshAutomations(true)
			})
		}
		rows := s.automationsVisible()
		switch {
		case len(st.result.Automations) == 0 && !st.loading && !st.failed:
			emptyState(c, "No automations", "Let an agent draft one, or create a draft yourself.")
		case len(rows) == 0 && !st.loading:
			emptyState(c, "Nothing in this filter", "Try another filter.")
		case len(rows) == 0 && st.loading:
			loadingState(c, "Loading automations…")
		default:
			ui.Column(c).FillWidth().Padding(0, sp.XL, sp.L).Gap(2).Children(func() {
				for _, a := range rows {
					s.automationsRow(c, a)
				}
				if n := len(st.result.Invalid); n > 0 {
					ui.Text(c, fmt.Sprintf("%d more automations.yaml entries failed to load and were skipped by the plugin.", n)).
						FontSize(Typography().Caption).TextColor(c.Theme().TextMuted)
				}
			})
		}
	})
}

// automationsVisible filters the plugin's list by the toolbar's segment.
func (s *Shell) automationsVisible() []automations.Automation {
	st := &s.automations.list
	var out []automations.Automation
	for _, a := range st.result.Automations {
		switch st.segment {
		case automationsEnabled:
			if a.Draft || a.Disabled {
				continue
			}
		case automationsDrafts:
			if !a.Draft {
				continue
			}
		case automationsPaused:
			if !a.Disabled {
				continue
			}
		}
		out = append(out, a)
	}
	return out
}

// automation returns the named automation from the cached list.
func (l *automationsList) automation(name string) *automations.Automation {
	for i := range l.result.Automations {
		if l.result.Automations[i].Name == name {
			return &l.result.Automations[i]
		}
	}
	return nil
}

// automationStatusTone is the row's slot state (contract §5): a running run
// turns, a draft waits for a look, a failed run wants attention, an enabled
// automation is quiet, and a paused or one-shot one is muted.
func automationStatusTone(a automations.Automation) (StatusTone, string) {
	if a.LastRun != nil && a.LastRun.Status == "running" {
		detail := "Running"
		if started, err := time.Parse(time.RFC3339, a.LastRun.StartedAt); err == nil {
			detail = "Running · " + gdRelativeTime(started)
		}
		return ToneWorking, detail
	}
	switch {
	case a.Draft:
		return ToneInfo, "Draft awaiting approval"
	case a.LastRun != nil && a.LastRun.Status == "failed":
		return ToneError, "Last run failed"
	case a.Disabled:
		return ToneMuted, "Paused"
	case a.OnceAt != nil:
		return ToneMuted, "One-shot"
	}
	return ToneNeutral, "Enabled"
}

// automationChips is the row's trailing facts (contract §5): ✓ last run ·
// ✗ timed out / failed · ✦ draft · running ·m.
func automationChips(a automations.Automation) []struct {
	text string
	tone StatusTone
} {
	var chips []struct {
		text string
		tone StatusTone
	}
	if a.Draft {
		chips = append(chips, struct {
			text string
			tone StatusTone
		}{"✦ Draft", ToneInfo})
	}
	if a.LastRun != nil {
		switch a.LastRun.Status {
		case "running":
			minutes := int64(0)
			if started, err := time.Parse(time.RFC3339, a.LastRun.StartedAt); err == nil {
				minutes = int64(timeNow().Sub(started).Minutes())
			}
			chips = append(chips, struct {
				text string
				tone StatusTone
			}{fmt.Sprintf("Running ·%dm", minutes), ToneWorking})
		case "failed":
			text := "✗ Failed"
			if a.TimeoutMinutes > 0 && a.LastRun.DurationSeconds >= int64(a.TimeoutMinutes)*60 {
				text = "✗ Timed out"
			}
			chips = append(chips, struct {
				text string
				tone StatusTone
			}{text, ToneError})
		case "done":
			text := "✓ Last run"
			if started, err := time.Parse(time.RFC3339, a.LastRun.StartedAt); err == nil {
				text = "✓ Last run " + gdRelativeTime(started)
			}
			chips = append(chips, struct {
				text string
				tone StatusTone
			}{text, ToneSuccess})
		}
	}
	return chips
}

// automationsRow is one list row: the status slot, the name over its subline,
// the next run (or pause state) at the right and the trailing chips.
func (s *Shell) automationsRow(c *ui.Context, a automations.Automation) {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	typ := Typography()
	sp := Spacing()
	st := &s.automations.list
	selected := st.selected == a.Name
	row := ui.ButtonBase(c.Key("automation-row:"+a.Name)).FillWidth().MinWidth(0).
		Radius(Radius().Row).Padding(sp.S+2, sp.M).Gap(sp.M).AlignItems(ui.Center).
		Label("Automation " + a.Name)
	if selected {
		row.Background(tokens.SidebarSelect)
	} else if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		tone, detail := automationStatusTone(a)
		statusIndicatorTone(c, tone, detail)
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, a.Name).FontSize(typ.Body).FontWeight(WeightMedium).SingleLine().Ellipsis("…")
			ui.Text(c, automationSubline(a)).FontSize(typ.Caption).TextColor(t.TextMuted).
				SingleLine().Ellipsis("…")
		})
		ui.Column(c).Shrink(0).Gap(2).AlignItems(ui.End).Children(func() {
			ui.Text(c, automationTrailing(a)).FontSize(typ.Caption).TextColor(t.TextMuted).SingleLine()
			ui.Row(c).Gap(sp.XXS).AlignItems(ui.Center).Children(func() {
				for _, chip := range automationChips(a) {
					statusPill(c, chip.text, chip.tone)
				}
			})
		})
	})
	if row.Clicked() {
		s.openAutomationsDrawer(a.Name)
	}
}

// automationSubline is the row's second line: schedule · agent · repo.
func automationSubline(a automations.Automation) string {
	schedule := a.ScheduleText
	if schedule == "" {
		schedule = a.Cron
	}
	if a.OnceAt != nil {
		schedule = "One-shot " + *a.OnceAt
	}
	if schedule == "" {
		schedule = "Unscheduled"
	}
	parts := []string{schedule}
	if a.Agent != "" {
		parts = append(parts, a.Agent)
	}
	if a.Repo != "" {
		parts = append(parts, tildePath(a.Repo))
	}
	return strings.Join(nonEmptyStrings(parts), " · ")
}

// automationTrailing is the row's right-hand run text: when it fires next,
// or why it does not.
func automationTrailing(a automations.Automation) string {
	switch {
	case a.Draft:
		return "Awaiting approval"
	case a.Disabled:
		return "Paused"
	case a.NextRun != "":
		if t, err := time.Parse(time.RFC3339, a.NextRun); err == nil {
			return "Next " + automationNextText(t)
		}
		return "Next " + a.NextRun
	case a.OnceAt != nil:
		return "One-shot"
	}
	return ""
}

// automationNextText names a future fire time in the viewer's clock: today and
// tomorrow by word, anything later by date. (gdRelativeTime only counts back
// from now — a future time would read "just now".)
func automationNextText(t time.Time) string {
	now := timeNow()
	t = t.In(now.Location())
	day := func(v time.Time) string { return v.Format("2006-01-02") }
	switch day(t) {
	case day(now):
		return "today " + t.Format("15:04")
	case day(now.AddDate(0, 0, 1)):
		return "tomorrow " + t.Format("15:04")
	}
	return t.Format("01-02 15:04")
}

// openAutomationsDrawer selects a row and loads its runs for the drawer.
func (s *Shell) openAutomationsDrawer(name string) {
	s.automations.list.selected = name
	s.loadAutomationHistory(name)
}

// automationsDrawer is the right-hand detail: identity, rationale, prompt,
// facts, the last five runs and the row's actions.
func (s *Shell) automationsDrawer(c *ui.Context, a automations.Automation) {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	sp := Spacing()
	ui.Column(c).Width(automationsDrawerWidth).Shrink(0).MinHeight(0).
		Border(1, tokens.BorderSubtle).Radius(Radius().Card).Background(tokens.Panel).
		Margin(0, sp.M, sp.M, 0).Padding(sp.M).Gap(sp.M).Children(func() {
		// Header: name, state chip, close.
		ui.Row(c).FillWidth().Gap(sp.S).AlignItems(ui.Center).Children(func() {
			tone, detail := automationStatusTone(a)
			statusIndicatorTone(c, tone, detail)
			ui.Text(c, a.Name).FontSize(Typography().Section).FontWeight(WeightSemibold).
				SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
			if gorexIconButton(c, gorexColorsOf(t.Dark), iconClose, "Close Drawer", "Close Drawer", false, gorexRowBtn, gorexRowGlyph).Clicked() {
				s.automations.list.selected = ""
			}
		})
		if a.Rationale != "" {
			settingsCard(c, "Rationale", func() {
				ui.Text(c, a.Rationale).FontSize(Typography().BodySmall).TextColor(t.Text)
			})
		}
		settingsCard(c, "Prompt", func() {
			ui.Text(c, fallbackText(a.Prompt, "(empty)")).FontSize(Typography().BodySmall).TextColor(t.Text)
		})
		s.automationsFacts(c, a)
		s.automationsRuns(c, a)
		// Footer actions (contract §5): run now, pause/resume, edit,
		// delete (shared confirm). Writes go through automationsWrite.
		ui.Row(c).FillWidth().Gap(sp.S).AlignItems(ui.Center).Wrap().Children(func() {
			if a.Draft {
				// A draft never fires and the plugin refuses to run it, so run-now
				// and pause mean nothing here: the one forward move is the
				// human's approval.
				if compactPrimaryButton(c, "Approve").Clicked() {
					s.automationsWrite("approve", a.Name, func(ctx context.Context, client *automations.Client) error {
						return client.Approve(ctx, a.Name)
					}, func() { s.pendingToast = "Approved " + a.Name + " — scheduling begins" })
				}
			} else {
				if compactPrimaryButton(c, "▶ Run Now").Clicked() {
					s.automationsWrite("run", a.Name, func(ctx context.Context, client *automations.Client) error {
						return client.Run(ctx, a.Name)
					}, func() { s.pendingToast = "Run Now requested for " + a.Name })
				}
				pauseResume, verb := "Pause", "pause"
				if a.Disabled {
					pauseResume, verb = "Resume", "resume"
				}
				if compactButton(c, pauseResume).Clicked() {
					s.automationsWrite(verb, a.Name, func(ctx context.Context, client *automations.Client) error {
						if verb == "resume" {
							return client.Resume(ctx, a.Name)
						}
						return client.Pause(ctx, a.Name)
					}, nil)
				}
			}
			ui.Spacer(c)
			if compactButton(c, "Edit…").Clicked() {
				s.openAutomationsEdit(a.Name)
			}
			if compactButton(c, "Delete…").Clicked() {
				s.openConfirm(automationsRemoveConfirm, a.Name,
					"Delete “"+a.Name+"”?",
					"The automation and its schedule are deleted; the plugin will no longer run it. Its past runs stay with the plugin.")
			}
		})
	})
}

// automationsFacts is the drawer's fact grid (Trigger/Repository/Mode/agent/On overlap/Cleanup).
func (s *Shell) automationsFacts(c *ui.Context, a automations.Automation) {
	t := c.Theme()
	typ := Typography()
	sp := Spacing()
	trigger := a.ScheduleText
	if trigger == "" {
		trigger = a.Cron
	}
	if a.OnceAt != nil {
		trigger = "One-shot " + *a.OnceAt
	}
	facts := [][2]string{
		{"Trigger", fallbackText(trigger, "Unscheduled")},
		{"Repository", fallbackText(tildePath(a.Repo), "—")},
		{"Mode", fallbackText(a.WorkspaceMode, "—")},
		{"Agent", fallbackText(a.Agent, "—")},
		{"On overlap", overlapLabel(a.OnOverlap)},
		{"Cleanup", cleanupLabel(a.CleanupTTLHours)},
	}
	settingsCard(c, "Details", func() {
		ui.Column(c).FillWidth().Gap(sp.XXS).Children(func() {
			for _, fact := range facts {
				ui.Row(c).FillWidth().Gap(sp.M).AlignItems(ui.Center).Children(func() {
					ui.Text(c, fact[0]).FontSize(typ.BodySmall).TextColor(t.TextMuted).Width(52).Shrink(0)
					tipped(c, ui.Text(c, fact[1]).FontSize(typ.BodySmall).TextColor(t.Text).
						SingleLine().Ellipsis("…").Grow(1).MinWidth(0), fact[1])
				})
			}
		})
	})
}

func overlapLabel(v string) string {
	if v == "queue" {
		return "Queue"
	}
	return "Skip"
}

func cleanupLabel(hours int) string {
	if hours <= 0 {
		return "Off"
	}
	return fmt.Sprintf("Merged after %d hours", hours)
}

// automationsRuns is the drawer's last-five runs card.
func (s *Shell) automationsRuns(c *ui.Context, a automations.Automation) {
	t := c.Theme()
	typ := Typography()
	sp := Spacing()
	settingsCard(c, "Last 5 runs", func() {
		st := &s.automations.list
		runs := st.history[a.Name].Runs
		if st.historyLoading == a.Name && len(runs) == 0 {
			ui.Text(c, "Loading runs…").FontSize(typ.Caption).TextColor(t.TextMuted)
			return
		}
		if len(runs) == 0 {
			ui.Text(c, "No runs yet.").FontSize(typ.Caption).TextColor(t.TextMuted)
			return
		}
		if len(runs) > 5 {
			runs = runs[:5]
		}
		ui.Column(c).FillWidth().Gap(sp.S).Children(func() {
			for _, run := range runs {
				s.automationsRunRow(c, a, run)
			}
		})
	})
}

// automationsRunRow is one run: status, trigger and time, then the two ways
// back into what it did — its pane, and its conversation when the history
// catalog matches it uniquely.
func (s *Shell) automationsRunRow(c *ui.Context, a automations.Automation, run automations.Run) {
	t := c.Theme()
	typ := Typography()
	sp := Spacing()
	ui.Column(c).FillWidth().Gap(sp.XXS).Children(func() {
		ui.Row(c).FillWidth().Gap(sp.S).AlignItems(ui.Center).Children(func() {
			statusPill(c, run.Status, automationRunTone(run.Status))
			detail := run.Trigger
			if started, err := time.Parse(time.RFC3339, run.StartedAt); err == nil && !started.IsZero() {
				detail += " · " + gdRelativeTime(started)
			}
			if run.DurationSeconds > 0 {
				detail += fmt.Sprintf(" · %dm", int(run.DurationSeconds/60))
			}
			ui.Text(c, strings.TrimSpace(detail)).FontSize(typ.Caption).TextColor(t.TextMuted).
				SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		})
		ui.Row(c).FillWidth().Gap(sp.S).AlignItems(ui.Center).Children(func() {
			if run.PaneID != "" && paneByID(s.projection, run.PaneID) != nil {
				if compactButton(c, "Open Pane").Clicked() {
					// The sidebar/Header jump path: select the run's pane in
					// the Workspace; no new Herdr method, no --takeover.
					s.openPane(run.PaneID)
				}
			}
			if key := automationChatSessionKey(a.Repo, run.Agent, run.StartedAt, s.hist.sessions); key != "" {
				if compactButton(c, "View in Chat").Clicked() {
					s.router.Push(routeHistory + "/" + key)
				}
			}
		})
	})
}

// automationRunTone maps the plugin's run vocabulary onto a status tone.
func automationRunTone(status string) StatusTone {
	switch status {
	case "done":
		return ToneSuccess
	case "failed", "invalid":
		return ToneError
	case "running":
		return ToneWorking
	case "queued":
		return ToneAttention
	case "skipped", "missed", "cancelled":
		return ToneMuted
	}
	return ToneNeutral
}

// automationChatSessionKey finds the one history session a run could be:
// same agent, same repository (cwd), started within ±2 minutes of the run.
// Zero or several matches answer "" — the link is omitted, never guessed.
func automationChatSessionKey(repo, agentName, startedAt string, sessions []history.SessionSummary) string {
	started, err := time.Parse(time.RFC3339, startedAt)
	if err != nil || started.IsZero() {
		return ""
	}
	id, ok := history.ParseAgentID(agentName)
	if !ok {
		return ""
	}
	want := automationsRepoAbs(repo)
	if want == "" {
		return ""
	}
	key := ""
	for _, summary := range sessions {
		meta := summary.Meta
		if meta.Agent != id || meta.ProjectPath == "" {
			continue
		}
		if !samePath(automationsRepoAbs(meta.ProjectPath), want) {
			continue
		}
		delta := time.UnixMilli(meta.CreatedAt).Sub(started)
		if delta < -2*time.Minute || delta > 2*time.Minute {
			continue
		}
		if key != "" {
			return "" // several candidates: not unique
		}
		key = meta.Key
	}
	return key
}

// automationsRepoAbs expands a leading ~ the plugin's yaml may carry.
func automationsRepoAbs(repo string) string {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return ""
	}
	if repo == "~" || strings.HasPrefix(repo, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(repo, "~"))
		}
	}
	return repo
}

// openAutomationsAsk shows the ✦ sheet with the ask text.
func (s *Shell) openAutomationsAsk() {
	s.hideQuickPanel()
	s.automations.list.askOpen = true
}

// openAutomationsNew opens the ＋ New sheet; creation is always a draft.
func (s *Shell) openAutomationsNew() {
	s.hideQuickPanel()
	s.automations.list.newDraft = automationsDraft{
		open: true, cronSel: automationsCronPresets[0].label,
	}
}

// openAutomationsEdit prefills the edit sheet from the automation's row.
func (s *Shell) openAutomationsEdit(name string) {
	a := s.automations.list.automation(name)
	if a == nil {
		return
	}
	d := automationsDraft{
		open: true, original: a.Name, name: a.Name, repo: a.Repo,
		agent: a.Agent, prompt: a.Prompt, rationale: a.Rationale, advanced: true,
	}
	if a.OnOverlap == "queue" {
		d.overlap = 1
	}
	if a.OnceAt != nil && *a.OnceAt != "" {
		d.trigger, d.once = 1, *a.OnceAt
	} else if cron := presetCronOf(a.Cron); cron != "" {
		d.cronSel = cron
	} else {
		d.cronSel, d.cron = automationsCronCustom, a.Cron
	}
	if a.TimeoutMinutes > 0 {
		d.timeout = itoa(a.TimeoutMinutes)
	}
	if a.CatchUpMinutes > 0 {
		d.catchUp = itoa(a.CatchUpMinutes)
	}
	if a.CleanupTTLHours > 0 {
		d.cleanup = itoa(a.CleanupTTLHours)
	}
	s.automations.list.edit = d
}

// presetCronOf answers the preset label whose cron matches, or "" when the
// expression is the user's own.
func presetCronOf(cron string) string {
	if cron == "" {
		return automationsCronPresets[0].label
	}
	for _, p := range automationsCronPresets {
		if p.cron != "" && p.cron == cron {
			return p.label
		}
	}
	return ""
}

// automationsDialogs renders the page's three sheets; mounted from
// dialogs.go beside the app's other modals.
func (s *Shell) automationsDialogs(c *ui.Context) {
	s.automationsAskSheet(c)
	s.automationsDraftSheet(c, &s.automations.list.newDraft, "New Automation Draft", "Create Draft", "automations-add")
	s.automationsDraftSheet(c, &s.automations.list.edit, "Edit Automation", "Save", "automations-put")
}

// automationsAskSheet is the ✦ Let an Agent Draft sheet: the suggested ask text
// and a copy button (v1 does not wire Chat itself).
func (s *Shell) automationsAskSheet(c *ui.Context) {
	st := &s.automations.list
	if !st.askOpen {
		return
	}
	ui.Modal(c, &st.askOpen, func() {
		sp := Spacing()
		t := c.Theme()
		ui.Column(c).Width(480).MaxWidth(560).Gap(sp.L).Children(func() {
			modalTitle(c, "Let an Agent Draft")
			ui.Text(c, "Send this text to an agent in Chat and let it draft; the draft lands in the list for your approval.").
				FontSize(Typography().Caption).TextColor(t.TextMuted)
			settingsCard(c, "Ask Text", func() {
				ui.Text(c, automationsAskText).FontSize(Typography().BodySmall)
			})
			ui.Row(c).FillWidth().Gap(sp.M).Justify(ui.End).Children(func() {
				if regularButton(c, "Close").Clicked() {
					st.askOpen = false
				}
				if regularPrimaryButton(c, "Copy").Clicked() {
					s.copyToClipboard(automationsAskText)
					s.pendingToast = "Ask text copied — paste it to an agent in Chat."
					st.askOpen = false
				}
			})
		})
	})
}

// automationsDraftSheet renders the shared New/Edit form (contract §5:
// name/cron(Preset+Custom)/once/repo/agent/prompt/rationale(fold)/Advanced).
func (s *Shell) automationsDraftSheet(c *ui.Context, d *automationsDraft, title, submitLabel, op string) {
	if !d.open {
		return
	}
	ui.Modal(c, &d.open, func() {
		sp := Spacing()
		t := c.Theme()
		ui.Column(c).Width(480).MaxWidth(560).Gap(sp.L).Children(func() {
			modalTitle(c, title)
			textField(c, &d.name).Label("Name").Placeholder("nightly-ci-triage").AutoFocus().FillWidth()
			segmentedControl(c, &d.trigger, "Cron", "One-shot").Label("Trigger")
			if d.trigger == 0 {
				selectField(c, &d.cronSel, cronPresetLabels()).Label("Preset")
				if d.cronSel == automationsCronCustom {
					textField(c, &d.cron).Label("Cron Expression").Placeholder("0 9 * * 1-5").FillWidth()
				} else if cron := presetCron(d.cronSel); cron != "" {
					ui.Text(c, cron).FontSize(Typography().Caption).TextColor(t.TextMuted)
				}
			} else {
				textField(c, &d.once).Label("One-shot Time (RFC3339)").Placeholder("2026-11-03T09:00:00+08:00").FillWidth()
			}
			textField(c, &d.repo).Label("Repository").Placeholder("~/Workspaces/wh-studio/solco").FillWidth()
			textField(c, &d.agent).Label("Agent").Placeholder("claude").FillWidth()
			textArea(c, &d.prompt).Label("Prompt").FillWidth().Height(88)
			if compactButton(c, "Advanced…").Clicked() {
				d.advanced = !d.advanced
			}
			if d.advanced {
				textArea(c, &d.rationale).Label("Rationale (goal / inputs / guardrails / cost)").FillWidth().Height(64)
				segmentedControl(c, &d.overlap, "Skip", "Queue").Label("On Overlap")
				ui.Row(c).FillWidth().Gap(sp.M).Children(func() {
					textField(c, &d.timeout).Label("Timeout (min)").Placeholder("60").Grow(1)
					textField(c, &d.catchUp).Label("Catch-up Window (min)").Placeholder("120").Grow(1)
					textField(c, &d.cleanup).Label("Cleanup TTL (hours)").Placeholder("0").Grow(1)
				})
				ui.Text(c, "0 = the plugin's default.").FontSize(Typography().Caption).TextColor(t.TextMuted)
			}
			ui.Row(c).FillWidth().Gap(sp.M).Justify(ui.End).Children(func() {
				if regularButton(c, "Cancel").Clicked() {
					d.open = false
				}
				if regularPrimaryButton(c, submitLabel).Clicked() && s.automationsDraftReady(d) {
					s.submitAutomationsDraft(d, op)
				}
			})
		})
	})
}

func cronPresetLabels() []string {
	labels := make([]string, 0, len(automationsCronPresets))
	for _, p := range automationsCronPresets {
		labels = append(labels, p.label)
	}
	return labels
}

// automationsDraftReady is whether the form carries what the plugin needs:
// a name, a prompt and one trigger.
func (s *Shell) automationsDraftReady(d *automationsDraft) bool {
	if strings.TrimSpace(d.name) == "" || strings.TrimSpace(d.prompt) == "" {
		return false
	}
	if d.original != "" {
		return true // an edit may clear the trigger; the plugin judges
	}
	if d.trigger == 1 {
		return strings.TrimSpace(d.once) != ""
	}
	if d.cronSel != automationsCronCustom {
		return true
	}
	return strings.TrimSpace(d.cron) != ""
}

// submitAutomationsDraft closes the sheet and writes through the plugin:
// add is always --draft (an agent proposes, a human approves); an edit
// saves as put --original <name>. The draft is snapshotted first — the
// sheet state resets before the background write runs.
func (s *Shell) submitAutomationsDraft(d *automationsDraft, op string) {
	snapshot := *d
	*d = automationsDraft{}
	if !s.automationsAvailable() {
		return
	}
	fields := automations.Fields{
		Repo: snapshot.repo, Agent: snapshot.agent, Prompt: snapshot.prompt,
		Rationale: snapshot.rationale,
	}
	if snapshot.trigger == 1 {
		fields.OnceAt = snapshot.once
	} else if snapshot.cronSel != automationsCronCustom {
		fields.Cron = presetCron(snapshot.cronSel)
	} else {
		fields.Cron = snapshot.cron
	}
	if snapshot.overlap == 1 {
		fields.OnOverlap = "queue"
	}
	fields.TimeoutMinutes = atoiNative(snapshot.timeout)
	fields.CatchUpMinutes = atoiNative(snapshot.catchUp)
	fields.CleanupTTLHours = atoiNative(snapshot.cleanup)
	s.automationsWrite(op, snapshot.name, func(ctx context.Context, client *automations.Client) error {
		if snapshot.original != "" {
			return client.Put(ctx, snapshot.original, fields)
		}
		return client.Add(ctx, snapshot.name, fields)
	}, func() {
		if snapshot.original == "" {
			s.pendingToast = "Draft created — open it in the list to approve."
		} else {
			s.pendingToast = "Saved."
		}
	})
}

// atoiNative parses one numeric field; junk answers 0 (the plugin default).
func atoiNative(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return n
}

// automationsWrite is the single execution channel for automations writes:
// the plugin owns every refusal and its message becomes the toast verbatim
// (bounded), a success refreshes the list.
func (s *Shell) automationsWrite(op, name string, run func(ctx context.Context, client *automations.Client) error, onOK func()) {
	if !s.automationsAvailable() {
		return
	}
	client := newAutomationsClient(s.automations.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	// Logs carry the operation and the automation name — never a prompt or
	// a rationale.
	slog.Info("automations write requested", "op", op, "name", name)
	automationsGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), automationsRefreshTimeout)
		defer cancel()
		err := run(ctx, client)
		s.applyOnWindow(func() {
			if err != nil {
				slog.Warn("automations write failed", "op", op, "name", name, "error", err)
				s.pendingToast = automationsFailureToast(err)
			} else if onOK != nil {
				onOK()
			}
			s.refreshAutomations(true)
		})
	})
}

// removeAutomation is the confirmed Delete….
func (s *Shell) removeAutomation(name string) {
	s.automationsWrite("rm", name, func(ctx context.Context, client *automations.Client) error {
		return client.Remove(ctx, name)
	}, func() {
		if s.automations.list.selected == name {
			s.automations.list.selected = ""
		}
		s.pendingToast = "Deleted " + name
	})
}

// automationsFailureToast shows the plugin's own refusal, bounded.
func automationsFailureToast(err error) string {
	text := err.Error()
	// The CLI wrapper reads "automations: <verb>: <exit>: <plugin message>".
	if parts := strings.SplitN(text, ": ", 4); len(parts) == 4 && parts[0] == "automations" {
		text = parts[3]
	}
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "The plugin refused this operation."
	}
	if r := []rune(text); len(r) > workboardToastMax {
		text = string(r[:workboardToastMax]) + "…"
	}
	return text
}
