package nativeui

import (
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/diagnostics"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/settings"
)

/**
 * [INPUT]: 依赖 github.com/egoist/mygo, github.com/egoist/mygo/ui, settings, diagnostics
 * [OUTPUT]: 对外提供 Shell.settingsLayout/settingsGeneral/settingsTerminal/settingsDiagnostics 与 Herdr 运行时行 herdrRuntimeRow（版本检查/就地升级）
 * [POS]: nativeui 设置页面宿主与分组行容器（settingsGroup），呈现并持久化用户外观与运行时偏好
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

var appearanceOptions = []string{"System", "Light", "Dark"}

func appearanceIndex(value string) int {
	switch value {
	case "light":
		return 1
	case "dark":
		return 2
	default:
		return 0
	}
}

func appearanceFromIndex(index int) string {
	if index < 0 || index >= len(appearanceOptions) {
		return "system"
	}
	return strings.ToLower(appearanceOptions[index])
}

// settingsSection is one entry of the Settings navigation.
type settingsSection struct {
	path  string
	label string
	icon  *ui.SVG
	tone  TileTone
}

// settingsSections is the Settings inner-page navigation; it renders in the
// app sidebar while the section content owns the route area. The title bar
// names the open section from the same table (route_header.go).
var settingsSections = []settingsSection{
	{"/settings/general", "General", iconSettings, TileGray},
	{"/settings/terminal", "Terminal", iconTerminal, TileGreen},
	{"/settings/voice", "Voice", iconMic, TilePink},
	{"/settings/providers", "Agents", iconAgent, TilePurple},
	{"/settings/assistants-archive", "Archived Assistants", iconSparkles, TileGray},
	{"/settings/git", "Git", iconBranch, TileOrange},
	{usageSettingsPath, "Quota", iconQuota, TileTeal},
	{"/settings/notifications", "Notifications", iconChat, TileBlue},
	{remoteSettingsPath, "Remote Access", iconGlobe, TileIndigo},
	{remoteDesktopSettingsPath, "Remote Desktop", iconDesktop, TileIndigo},
	{"/settings/diagnostics", "Diagnostics", iconInfo, TileBlue},
	{"/settings/about", "About", iconAbout, TileGray},
}

// settingsNav is the Settings sidebar body: the section navigation that used
// to be the page's own inner column (inner-page style). The title bar names
// the page, so the list starts at its first section.
func (s *Shell) settingsNav(c *ui.Context) {
	if s.settingsSearch(c) {
		return
	}
	for _, section := range settingsSections {
		section := section
		selected := s.router.Path() == section.path
		btn := navTileButton(c, section.icon, section.tone, section.label, selected)
		if section.path == "/settings/about" && s.hasUpdate() {
			btn.DrawOver(func(p *ui.Painter, r ui.Rect) {
				dotX := r.X + r.W - 14
				dotY := r.Y + r.H/2 - 3
				dotSize := float32(6.5)
				p.Fill(ui.Rect{X: dotX - 1, Y: dotY - 1, W: dotSize + 2, H: dotSize + 2}, c.Theme().Surface, (dotSize+2)/2)
				p.Fill(ui.Rect{X: dotX, Y: dotY, W: dotSize, H: dotSize}, c.Theme().Danger, dotSize/2)
			})
		}
		if btn.Clicked() && !selected {
			s.router.Push(section.path)
		}
	}
}

// settingsPage is the one layout owner for Settings. The title bar names the
// open section, so the page is the scroll viewport and each section renders
// only controls.
const settingsContentMaxWidth float32 = 760

func settingsPage(c *ui.Context, body func()) {
	ui.Column(c).Grow(1).MinWidth(0).Children(func() {
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			contentColumn(c, formColumnWidth, func() {
				ui.Column(c).FillWidth().Padding(pageTopInset(), Spacing().XL, Spacing().XL).Gap(Spacing().XL).Children(body)
			})
		})
	})
}

func (s *Shell) settingsLayout(c *ui.Context, r *ui.Route) {
	ui.Column(c).Grow(1).MinWidth(0).Children(func() {
		r.View(c, func(r *ui.Route) {
			switch {
			case r.Match("/general") || r.Match("/"):
				r.Title("Settings · General")
				settingsPage(c, func() { s.settingsGeneral(c) })
			case r.Match("/terminal"):
				r.Title("Settings · Terminal")
				settingsPage(c, func() { s.settingsTerminal(c) })
			case r.Match("/voice"):
				r.Title("Settings · Voice")
				settingsPage(c, func() { s.settingsVoice(c) })
			case r.Match("/providers"):
				r.Title("Settings · Agents")
				settingsPage(c, func() { s.settingsProviders(c) })
			case r.Match("/assistants-archive"):
				r.Title("Settings · Archived Assistants")
				settingsPage(c, func() { s.settingsArchivedAssistants(c) })
			case r.Match("/git"):
				r.Title("Settings · Git")
				settingsPage(c, func() { s.settingsGit(c) })
			case r.Match("/quota"), r.Match("/usage"):
				r.Title("Settings · Quota")
				settingsPage(c, func() { s.settingsUsage(c) })
			case r.Match("/notifications"):
				r.Title("Settings · Notifications")
				settingsPage(c, func() { s.settingsNotifications(c) })
			case r.Match("/remote"):
				r.Title("Settings · Remote Access")
				settingsPage(c, func() { s.settingsRemote(c) })
			case r.Match("/remote-desktop"):
				r.Title("Settings · Remote Desktop")
				settingsPage(c, func() { s.settingsRemoteDesktop(c) })
			case r.Match("/diagnostics"):
				r.Title("Settings · Diagnostics")
				settingsPage(c, func() { s.settingsDiagnostics(c) })
			case r.Match("/about"):
				r.Title("Settings · About")
				settingsPage(c, func() { s.settingsAbout(c) })
			default:
				r.Title("Settings · General")
				// Unknown sections stay usable through the canonical fallback.
				settingsPage(c, func() { s.settingsGeneral(c) })
			}
		})
	})
}

// settingSwitch is a labeled on/off preference: it reports whether the user
// flipped it this frame and the new value.
func settingSwitch(c *ui.Context, label string, on bool) (bool, bool) {
	value := on
	changed := ui.Switch(c, &value).Label(label).Changed()
	return value, changed
}

// settingCombobox is a labeled combobox preference: like settingSwitch,
// it edits a draft and reports the new value with changed.
func settingCombobox(c *ui.Context, label string, draft *string, choices []string) (string, bool) {
	value := *draft
	changed := comboField(c, &value, choices).Label(label).Changed()
	if changed {
		*draft = value
	}
	return value, changed
}

// settingText is a labeled text preference committed with Return; while it
// is not being edited it mirrors the stored value.
func (s *Shell) settingText(c *ui.Context, label, placeholder string, draft *string, stored string, password bool, commit func(v *settings.Settings, text string)) {
	field := textField(c, draft).Width(240).Label(label).Placeholder(placeholder)
	if password {
		field.Password()
	}
	if field.Submitted() {
		text := strings.TrimSpace(*draft)
		s.applySettings(func(v *settings.Settings) error {
			commit(v, text)
			return nil
		})
	}
	if !field.Focused() {
		*draft = stored
	}
}

func (s *Shell) settingsGeneral(c *ui.Context) {
	t := c.Theme()
	settingsGroup(c, "Appearance",
		row("Theme", "", func() {
			chosen := appearanceIndex(s.settings.General.Appearance)
			if segmentedControl(c, &chosen, appearanceOptions...).Label("Theme").Changed() {
				s.applySettings(func(v *settings.Settings) error {
					v.General.Appearance = appearanceFromIndex(chosen)
					return nil
				})
			}
		}),
		row("Sidebar density", "", func() {
			densityOpts := []string{"Compact", "Default", "Comfortable"}
			idx := 1
			switch s.settings.General.Density {
			case "compact":
				idx = 0
			case "comfortable":
				idx = 2
			}
			if segmentedControl(c, &idx, densityOpts...).Label("Sidebar density").Changed() {
				s.applySettings(func(v *settings.Settings) error {
					switch idx {
					case 0:
						v.General.Density = "compact"
					case 2:
						v.General.Density = "comfortable"
					default:
						v.General.Density = "default"
					}
					return nil
				})
			}
		}),
		row("Increase contrast", "", func() {
			if hc, changed := settingSwitch(c, "Increase contrast", s.settings.General.HighContrast); changed {
				s.applySettings(func(v *settings.Settings) error {
					v.General.HighContrast = hc
					return nil
				})
			}
		}),
		row("Serif chat text", "Sets the assistant's reading text in Chat in a serif face. Code, tool rows and the interface stay sans.", func() {
			if on, changed := settingSwitch(c, "Serif chat text", s.settings.General.ChatSerif); changed {
				s.applySettings(func(v *settings.Settings) error {
					v.General.ChatSerif = on
					return nil
				})
			}
		}),
		row("Chat outline (TOC)", "Shows a tick rail beside the conversation that jumps between your messages. It appears once a chat has three or more of them. Default off.", func() {
			if on, changed := settingSwitch(c, "Chat outline (TOC)", s.settings.General.ChatOutline); changed {
				s.applySettings(func(v *settings.Settings) error {
					v.General.ChatOutline = on
					return nil
				})
			}
		}),
		row("Window opacity", "", func() {
			pct := math.Round(s.settings.General.EffectiveOpacity() * 100)
			ui.Row(c).AlignItems(ui.Center).Gap(Spacing().M).Children(func() {
				if ui.Slider(c, &pct, 50, 100).Step(1).Width(160).Label("Window opacity").Changed() {
					s.applySettings(func(v *settings.Settings) error {
						v.General.Opacity = pct / 100
						return nil
					})
				}
				ui.Text(c, fmt.Sprintf("%d%%", int(pct))).Width(36).FontSize(Typography().Body).TextColor(t.TextMuted)
			})
		}),
	)
	settingsGroup(c, "Agents",
		row("Default Agent view", "What an Agent pane opens in the first time. A pane you switch remembers its own view.", func() {
			chosen := 0
			if s.settings.General.AgentChatFirst() {
				chosen = 1
			}
			if segmentedControl(c, &chosen, "TUI", "Chat").Label("Default Agent view").Changed() {
				s.applySettings(func(v *settings.Settings) error {
					v.General.AgentView = map[int]string{0: "", 1: "chat"}[chosen]
					return nil
				})
			}
		}),
	)
	settingsGroup(c, "Window",
		row("Show Tabs in Sidebar", "On lists each Workspace's Tabs in the sidebar tree. Off (default) puts them in a Tab bar across the top of the window, like a browser.", func() {
			if on, changed := settingSwitch(c, "Show Tabs in Sidebar", !s.settings.General.TabsInTitleBar()); changed {
				s.applySettings(func(v *settings.Settings) error {
					v.General.TabsPlacement = map[bool]string{true: "sidebar", false: ""}[on]
					return nil
				})
			}
		}),
		row("Restore window position", "", func() {
			if restore, changed := settingSwitch(c, "Restore window position", s.settings.General.RestoreWindow); changed {
				s.applySettings(func(v *settings.Settings) error {
					v.General.RestoreWindow = restore
					return nil
				})
			}
		}),
	)
}

func (s *Shell) settingsTerminal(c *ui.Context) {
	settingsGroup(c, "Font",
		row("Font family", "", func() {
			s.settingText(c, "Font family", "", &s.settingsFontDraft, s.settings.Terminal.FontFamily, false,
				func(v *settings.Settings, text string) { v.Terminal.FontFamily = text })
		}),
		row("Font size", "", func() {
			size := s.settings.Terminal.FontSize
			if ui.NumberInput(c, &size, 6, 32, 1).Label("Font size").Changed() {
				s.applySettings(func(v *settings.Settings) error {
					v.Terminal.FontSize = size
					return nil
				})
			}
		}),
		row("Line height", "", func() {
			height := s.settings.Terminal.LineHeight
			if ui.NumberInput(c, &height, 1.0, 2.5, 0.02).Label("Line height").Changed() {
				s.applySettings(func(v *settings.Settings) error {
					v.Terminal.LineHeight = height
					return nil
				})
			}
		}),
	)
	settingsGroup(c, "Behavior",
		row("Scrollback (MB)", "", func() {
			megabytes := float64(s.settings.Terminal.Scrollback) / (1024 * 1024)
			if ui.NumberInput(c, &megabytes, 1, 1024, 1).Label("Scrollback").Changed() {
				s.applySettings(func(v *settings.Settings) error {
					v.Terminal.Scrollback = int(megabytes) * 1024 * 1024
					return nil
				})
			}
		}),
		row("Use Option as Alt", "", func() {
			if alt, changed := settingSwitch(c, "Use Option as Alt", s.settings.Terminal.OptionAsAlt); changed {
				s.applySettings(func(v *settings.Settings) error {
					v.Terminal.OptionAsAlt = alt
					return nil
				})
			}
		}),
		row("Transparent background", "", func() {
			if transparent, changed := settingSwitch(c, "Transparent background", s.settings.Terminal.Transparent); changed {
				s.applySettings(func(v *settings.Settings) error {
					v.Terminal.Transparent = transparent
					return nil
				})
			}
		}),
		row("Theme", "", func() {
			if theme, changed := settingCombobox(c, "Theme", &s.settingsThemeDraft, terminalThemeChoices()); changed {
				name := theme
				if name == terminalThemeDefault {
					name = ""
				}
				s.applySettings(func(v *settings.Settings) error {
					v.Terminal.ThemeName = name
					return nil
				})
			}
		}),
	)
	ui.Text(c, "Changes apply to terminals opened afterwards.").
		FontSize(Typography().Caption).TextColor(c.Theme().TextMuted).Padding(0, 0, 0, Spacing().XS)
}

// herdrRuntimeRow is the Settings runtime row: the connected Herdr identity
// in user language, a live check, and the in-place update when the runtime
// predates the supported protocol. F173 holds here too: the wire protocol
// itself stays in the diagnostics export, never on the panel.
func (s *Shell) herdrRuntimeRow(c *ui.Context, knownVersion string) {
	t := c.Theme()
	version, protocol := knownVersion, int(s.projection.Protocol)
	if s.herdrProbe != nil {
		version, protocol = s.herdrProbe.Version, s.herdrProbe.Protocol
	}
	ui.Row(c).Gap(Spacing().S).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fallbackText(version, "connected")).FontSize(Typography().Body).TextColor(t.TextMuted)
		if protocol > 0 {
			if protocol >= herdr.MinSupportedProtocol {
				statusPill(c, "Compatible", ToneSuccess)
			} else {
				statusPill(c, "Update available", ToneWarning)
			}
		}
		ui.Box(c).Grow(1)
		switch {
		case s.herdrUpdating:
			ui.Spinner(c).Size(16, 16)
			ui.Text(c, "Updating Herdr…").FontSize(Typography().Caption).TextColor(t.Text)
		case s.herdrProbing:
			ui.Spinner(c).Size(16, 16)
		default:
			if _, local := s.herdrUpgradeTarget(); local {
				if regularButton(c, "Check").Clicked() {
					s.checkHerdrRuntime()
				}
			}
			if protocol > 0 && protocol < herdr.MinSupportedProtocol {
				if regularPrimaryButton(c, "Update Herdr").Clicked() {
					s.updateHerdrFromSettings()
				}
			}
		}
	})
	if s.herdrProbeErr != "" {
		ui.Text(c, s.herdrProbeErr).FontSize(Typography().Caption).TextColor(t.TextMuted)
	}
	if s.herdrUpdateErr != "" {
		if s.herdrUpdateNotWrbl {
			ui.Row(c).Gap(Spacing().S).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Updating Herdr needs the administrator permission, and it wasn't granted. Update it from a terminal instead, then press Check.").FontSize(Typography().Caption).TextColor(t.TextMuted)
				if regularButton(c, "Copy Update Command").Clicked() {
					s.copyToClipboard(herdr.UpdateAsRootCommand)
				}
			})
		} else {
			ui.Text(c, s.herdrUpdateErr).FontSize(Typography().Caption).TextColor(t.TextMuted)
		}
	}
}

// systemName is the platform's product name for GOOS.
func systemName(goos string) string {
	if goos == "darwin" {
		return "macOS"
	}
	return goos
}

// diagLogLevels are the log filter values and the labels shown for them.
var (
	diagLogLevelValues = []string{"ALL", "INFO", "WARN", "ERROR", "DEBUG"}
	diagLogLevelLabels = []string{"All", "Info", "Warning", "Error", "Debug"}
)

// settingsDiagnostics renders the diagnostics snapshot, the sanitized export
// and the recent log viewer.
func (s *Shell) settingsDiagnostics(c *ui.Context) {
	t := c.Theme()
	sp := Spacing()
	home, _ := os.UserHomeDir()
	snap := diagnostics.BuildSnapshot(s.projection.Version, s.projection.Protocol, s.activeInstance, home)

	settingsGroup(c, "About",
		row("Solco", "", func() { valueText(c, snap.SolcoVersion) }),
		row("System", "", func() { valueText(c, fmt.Sprintf("%s · %s", systemName(snap.OS), snap.Arch)) }),
		row("Herdr", "", func() { s.herdrRuntimeRow(c, snap.HerdrVersion) }),
		row("Session", "", func() { valueText(c, fallbackText(snap.ActiveInstance, "none")) }),
		row("Log file", compactSearchPath(snap.LogPath), func() {
			if snap.LogPath != "" && regularButton(c, "Reveal in Finder").Clicked() {
				mygo.Shell.ShowItemInFolder(snap.LogPath)
			}
		}),
	)

	settingsGroup(c, "Compatibility",
		row("Rich chat view", "Turn off to use the classic chat view.", func() {
			if useWeb, changed := settingSwitch(c, "Rich chat view", s.settings.General.WebViewChat); changed {
				if useWeb {
					s.chatWebviewBroken = false // re-arm a latched creation failure
				}
				s.applySettings(func(v *settings.Settings) error {
					v.General.WebViewChat = useWeb
					return nil
				})
			}
		}),
	)

	ui.Row(c).Gap(sp.M).AlignItems(ui.Center).Children(func() {
		if regularButton(c, "Copy Diagnostics").Clicked() {
			logs, _ := diagnostics.ReadRecentLogs(500)
			exported, err := diagnostics.BuildExportArchive(snap, logs, home)
			if err == nil {
				s.copyToClipboard(exported)
				s.diagExportStatus = "Copied to the clipboard. Secrets are redacted."
			} else {
				s.diagExportStatus = "Couldn't copy diagnostics: " + err.Error()
			}
		}
		if s.diagExportStatus != "" {
			ui.Text(c, s.diagExportStatus).FontSize(Typography().Caption).TextColor(t.TextMuted)
		}
	})

	settingsCard(c, "Logs", func() {
		rawLogs, _ := diagnostics.ReadRecentLogs(5000)
		filtered := diagnostics.FilterLogs(rawLogs, s.diagLogLevelFilter, s.diagSearchQuery)

		ui.Row(c).FillWidth().Gap(sp.S).AlignItems(ui.Center).Children(func() {
			levelIdx := 0
			for i, lvl := range diagLogLevelValues {
				if lvl == s.diagLogLevelFilter {
					levelIdx = i
					break
				}
			}
			if segmentedControl(c, &levelIdx, diagLogLevelLabels...).Label("Log level").Changed() {
				s.diagLogLevelFilter = diagLogLevelValues[levelIdx]
			}

			ui.Box(c).Grow(1)
			ui.Text(c, fmt.Sprintf("%d of %d entries", len(filtered), len(rawLogs))).
				FontSize(Typography().Caption).TextColor(t.TextMuted)
		})

		ui.Scroll(c).Height(300).Background(designTokens(t.Dark).Content).
			Radius(Radius().Control).Padding(sp.S).Children(func() {
			if len(filtered) == 0 {
				ui.Text(c, "No matching entries").FontSize(Typography().Caption).TextColor(t.TextMuted)
				return
			}
			ui.Column(c).FillWidth().Gap(2).Children(func() {
				displayLimit := len(filtered)
				if displayLimit > 100 {
					displayLimit = 100
				}
				for i := 0; i < displayLimit; i++ {
					entry := filtered[i]
					ui.Row(c).FillWidth().Gap(sp.S).AlignItems(ui.Center).Children(func() {
						tone := ToneMuted
						switch entry.Level {
						case "ERROR":
							tone = ToneError
						case "WARN":
							tone = ToneWarning
						case "INFO":
							tone = ToneInfo
						}
						statusPill(c, entry.Level, tone)
						msgText := entry.Message
						if msgText == "" {
							msgText = entry.Raw
						}
						ui.Text(c, msgText).FontSize(Typography().Caption).SingleLine().Grow(1)
					})
				}
			})
		})
	})
}
