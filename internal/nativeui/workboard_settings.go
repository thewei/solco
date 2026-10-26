package nativeui

/**
 * [INPUT]: 依赖 internal/workboard 的 settings.go（Settings/SetSetting/UnsetSetting/Scope）、workboardWrite 单一写通道、settingsGroup/row/settingSwitch 共享设置组件、board 缓存的 Project 名
 * [OUTPUT]: workboardSettingsSection（Settings 导航项，仅插件可用时出现）、Shell.settingsWorkboard（页面：范围选择 → 过滤 → 按前缀分组的设置行）、loadWorkboardSettings
 * [POS]: herdr-projects 设置的原生页（用户 2026-10-26 显式授权，越过 AGENTS.md「禁止插件 UI」，仅限本插件设置）：只画插件 `show --json` 在当前范围（Everywhere / 某 Project / 某 Project 的协调者）返回的那份设置——JSON 不带选项表：布尔用开关，钉住规格表（workboard.SpecFor）命中的用选择型控件（workboard_settings_controls.go），其余与值不合规者降级文本输入，取值校验与拒绝原文全归插件；跨机器/Web UI/语音/快捷键类设置不收录（产品范围已定不集成）；own 的行给 Reset（unset）；页顶 View 组承载 Native | Board 视图切换（General.WorkboardView，默认 Board）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"log/slog"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/wh-studio/solco/internal/herdr"
	"github.com/wh-studio/solco/internal/workboard"
)

// workboardSettingsSection is the Settings navigation entry; it exists only
// while the plugin gate is open.
var workboardSettingsSection = settingsSection{"/settings/workboard", "Work Board", iconFolder, TileBlue}

// Scopes of the page, in the segmented control's order.
const (
	wbScopeEverywhere = iota
	wbScopeProject
	wbScopeCoordinator
)

// workboardSettingsState is the page's state: whose settings, what the plugin
// last showed, and the text being edited per setting.
type workboardSettingsState struct {
	scope   int
	project string
	filter  string

	view    workboard.SettingsView
	shown   workboard.Scope // the scope view was read for
	loaded  bool
	loading bool
	failed  bool
	drafts  map[string]*string

	// Number controls: the typed draft per setting, which of them wait to be
	// written, and which writes are in flight (until the page re-reads).
	numbers map[string]*float64
	pending map[string]bool
	writing map[string]bool

	// models are the plugin's programs/models/efforts for the choice controls;
	// empty (never read, or unreadable) leaves those rows as text fields.
	models        workboard.ModelsView
	modelsLoading bool
	modelsTried   bool
}

// workboardResetWidth is the slot a row reserves for its Reset button.
const workboardResetWidth = 64

// workboardSettingHidden keeps out what the product does not integrate:
// other machines, the web UI, voice and the plugin's own keys.
func workboardSettingHidden(name string) bool {
	switch name {
	case "board.machines", "board.webUi", "board.keys", "board.keyStyle":
		return true
	}
	return strings.HasPrefix(name, "voice.")
}

func (st *workboardSettingsState) workboardScope() workboard.Scope {
	switch {
	case st.scope == wbScopeProject && st.project != "":
		return workboard.Scope{Repo: st.project}
	case st.scope == wbScopeCoordinator && st.project != "":
		return workboard.Scope{Repo: st.project, Coordinator: true}
	}
	return workboard.Scope{}
}

// workboardSettingsProjects names the Projects a scope can point at.
func (s *Shell) workboardSettingsProjects() []string {
	var names []string
	for _, p := range workboardProjects(s.workboard.board) {
		if p.Name != workboardOtherProject {
			names = append(names, p.Name)
		}
	}
	return names
}

// loadWorkboardSettings reads the open scope's settings off the UI lane. A
// result for a scope the page has since left is dropped.
func (s *Shell) loadWorkboardSettings() {
	st := &s.workboard.settings
	if st.loading || !s.workboardAvailable() {
		return
	}
	st.loading = true
	scope := st.workboardScope()
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	workboardGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), workboardRefreshTimeout)
		defer cancel()
		view, err := client.Settings(ctx, scope)
		s.applyOnWindow(func() {
			st := &s.workboard.settings
			st.loading = false
			if scope != st.workboardScope() {
				st.loaded = false // the scope moved on while reading: read the new one
				return
			}
			st.failed = err != nil
			if err != nil {
				return
			}
			st.view, st.shown, st.loaded = view, scope, true
			st.writing = nil
			if st.drafts == nil {
				st.drafts = map[string]*string{}
			}
		})
	})
}

// setWorkboardSetting writes (or, with unset, removes) one setting in the open scope.
func (s *Shell) setWorkboardSetting(name, value string, unset bool) {
	if !s.workboardAvailable() {
		return
	}
	scope := s.workboard.settings.workboardScope()
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	op := "set"
	if unset {
		op = "unset"
	}
	slog.Info("work board write requested", "op", op, "setting", name, "repo", scope.Repo, "coordinator", scope.Coordinator)
	said := ""
	st := &s.workboard.settings
	if st.writing == nil {
		st.writing = map[string]bool{}
	}
	st.writing[name] = true // the control keeps what was chosen until the page re-reads
	s.workboardWrite("setting-"+op, func(ctx context.Context) error {
		var err error
		if unset {
			said, err = client.UnsetSetting(ctx, scope, name)
		} else {
			said, err = client.SetSetting(ctx, scope, name, value)
		}
		if err != nil {
			s.applyOnWindow(func() { delete(s.workboard.settings.writing, name) })
		}
		return err
	}, func() {
		var lines []string
		for _, l := range strings.Split(said, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				lines = append(lines, l)
			}
		}
		s.pendingToast = strings.Join(lines, " · ")
		s.workboard.settings.loaded = false
	})
}

// settingsGroupTitle is the first part of a setting's name, capitalized.
func settingsGroupTitle(name string) string {
	prefix, _, ok := strings.Cut(name, ".")
	if !ok || prefix == "" {
		return "Other"
	}
	return strings.ToUpper(prefix[:1]) + prefix[1:]
}

// settingsWorkboard is the page.
func (s *Shell) settingsWorkboard(c *ui.Context) {
	if !s.workboardAvailable() {
		emptyState(c, "The work board isn't available", "")
		return
	}
	t := c.Theme()
	st := &s.workboard.settings
	projects := s.workboardSettingsProjects()
	if st.scope != wbScopeEverywhere {
		if len(projects) == 0 {
			st.scope = wbScopeEverywhere
		} else if st.project == "" {
			st.project = projects[0]
		}
	}
	if !st.loaded && !st.loading && !st.failed {
		s.loadWorkboardSettings()
	}
	if !st.modelsTried {
		st.modelsTried = true
		s.loadWorkboardModels()
	}

	settingsGroup(c, "View",
		row("Work board", "Board opens the plugin's own board Tab from every task and project row; Native keeps Solco's pages.", func() {
			idx := 1
			if s.settings.General.WorkboardNative() {
				idx = 0
			}
			if ui.Segmented(c, &idx, "Native", "Board").Label("Work board view").Changed() {
				s.setWorkboardView(idx == 0)
			}
		}))

	scopeRows := []settingsRow{row("Applies to", "", func() {
		choice := st.scope
		options := []string{"Everywhere"}
		if len(projects) > 0 {
			options = append(options, "Project", "Project coordinator")
		}
		if ui.Segmented(c, &choice, options...).Label("Settings scope").Changed() {
			st.scope, st.loaded, st.failed = choice, false, false
		}
	})}
	if st.scope != wbScopeEverywhere {
		scopeRows = append(scopeRows, row("Project", "", func() {
			project := st.project
			if ui.Combobox(c, &project, projects).Label("Project").Changed() {
				st.project, st.loaded, st.failed = project, false, false
			}
		}))
	}
	settingsGroup(c, "Scope", scopeRows...)

	switch {
	case st.failed:
		ui.Text(c, "Couldn't read the settings").TextColor(t.TextMuted)
		if ui.Button(c, "Retry").Clicked() {
			st.failed = false
		}
		return
	case !st.loaded && len(st.view.Settings) == 0:
		ui.Spinner(c)
		return
	}

	if len(st.view.Problems) > 0 {
		settingsCard(c, "Problems", func() {
			for _, p := range st.view.Problems {
				ui.Text(c, clipPreview(p, 400, 3)).FontSize(Typography().BodySmall).Selectable()
			}
		})
	}
	ui.SearchField(c, &st.filter).Label("Filter settings").FillWidth()

	needle := strings.ToLower(strings.TrimSpace(st.filter))
	var titles []string
	groups := map[string][]settingsRow{}
	for _, setting := range st.view.Settings {
		setting := setting
		if workboardSettingHidden(setting.Name) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(setting.Name+" "+setting.ValueText()), needle) {
			continue
		}
		title := settingsGroupTitle(setting.Name)
		if _, seen := groups[title]; !seen {
			titles = append(titles, title)
		}
		groups[title] = append(groups[title], s.workboardSettingRow(c, setting))
	}
	for _, title := range titles {
		settingsGroup(c, title, groups[title]...)
	}
	if len(titles) == 0 {
		ui.Text(c, "No settings match").TextColor(t.TextMuted)
	}
}

// workboardSettingRow is one setting: its name, where its value comes from
// (and the plugin's remark), then a switch or a text field, and Reset when
// the value is the scope's own.
func (s *Shell) workboardSettingRow(c *ui.Context, setting workboard.Setting) settingsRow {
	note := setting.From
	if setting.Note != "" {
		note += " · " + setting.Note
	}
	return row(setting.Name, note, func() {
		key := c.Key("wbs:" + setting.Name)
		ui.Row(key).Gap(Spacing().M).AlignItems(ui.Center).Children(func() {
			s.workboardSettingControl(key, setting)
			// Reset keeps its slot while hidden: the controls must not shift
			// left when a value turns the scope's own.
			if setting.Own {
				if ui.Button(key, "Reset").Width(workboardResetWidth).Clicked() {
					s.setWorkboardSetting(setting.Name, "", true)
				}
			} else {
				ui.Box(key.Key("reset-slot")).Width(workboardResetWidth).Shrink(0)
			}
		})
	})
}

func boolWord(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
