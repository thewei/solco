package nativeui

/**
 * [INPUT]: Shell.settings/applySettings、launchKinds（ensureLaunchKinds）、page_providers.go 的 agentToggleRow/setAgentEnabled/requestProvidersAudit、controls.go 的按钮、brands.go 的 brandPNG、theme.go 的 designTokens/solcoTheme、settingsService.FreshInstall（NewShell 的首启闸门）
 * [OUTPUT]: routeOnboarding、Shell.onboardingPage（三步全窗首启引导：固定头部（应用标记 + 步骤标题 + 副标题）→ 可滚动内容区 → 固定底部操作栏（圆点步进 + Skip/Back/Continue））；onboardAppearance 的三张主题预览卡（System 半明半暗、Light、Dark，designTokens 固定调色）、onboardAgents 的 Agent 开关组、onboardPhone 的 Mac+手机组件示意、finishOnboarding
 * [POS]: 首启引导拥有整个画布（无侧栏/标题栏/浮层，Shell.View 专用分支）；仅 fresh install 且未完成时进入（NewShell），完成或跳过写 General.Onboarded 并回 Workspace；每步选择即时持久化，跳过不回滚已做的选择；内容超长走滚动条，操作区固定不滚
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/settings"
)

// routeOnboarding is the full-canvas first-run guide. NewShell replaces the
// initial Workspace route with it when the settings store is fresh and the
// guide has never been completed; finishing or skipping it once writes
// General.Onboarded and the guide never returns.
const routeOnboarding = "/onboarding"

// onboardingSteps is the guide's step count; s.onboardStep indexes it.
const onboardingSteps = 3

// onboardingContentWidth bounds the guide's column, the settings page's
// measure — the guide reads as the same product, not a splash screen.
const onboardingContentWidth float32 = 560

// finishOnboarding persists the done flag and lands on Workspace. Skipping
// keeps whatever the user already chose on the way — every step persisted
// the moment it was changed.
func (s *Shell) finishOnboarding() {
	s.applySettings(func(v *settings.Settings) error {
		v.General.Onboarded = true
		return nil
	})
	if s.agentEnabled(history.AgentClaudeCode) {
		s.trustAutomationFolders(history.AgentClaudeCode) // default-on agents never fire the toggle
	}
	s.router.Replace(routeWorkspace)
}

// onboardingPage draws the guide: a fixed header (mark, step title,
// subtitle), a scrollable content region, and a fixed footer (step dots,
// Skip / Back / Continue) — the content scrolls, the controls never do.
func (s *Shell) onboardingPage(c *ui.Context) {
	sp := Spacing()
	ui.Column(c).Fill().Children(func() {
		// Fixed header.
		ui.Column(c).FillWidth().Padding(sp.XL, sp.XL, sp.L, sp.XL).Children(func() {
			ui.Column(c).Grow(1).Center().Gap(sp.S).Children(func() {
				if bmp := brandPNG("solco-128"); bmp != nil {
					ui.Image(c, bmp).Width(44).Height(44).Fit(ui.ScaleDown)
				}
				s.onboardingStepHeader(c)
			})
		})
		// Scrollable content.
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().Padding(sp.S, sp.XL, sp.XL, sp.XL).Children(func() {
				ui.Column(c).Grow(1).Center().Children(func() {
					ui.Column(c).Width(onboardingContentWidth).Gap(sp.XL).Children(func() {
						switch s.onboardStep {
						case 0:
							s.onboardAppearance(c)
						case 1:
							s.onboardAgents(c)
						default:
							s.onboardPhone(c)
						}
					})
				})
			})
		})
		// Fixed footer: the controls stay put while the content scrolls.
		ui.Column(c).FillWidth().Shrink(0).Children(func() {
			ui.Row(c).FillWidth().Height(1).Shrink(0).Background(designTokens(c.Theme().Dark).BorderSubtle).Children(func() {})
			ui.Row(c).FillWidth().Padding(sp.L, sp.XL).Gap(sp.M).AlignItems(ui.Center).Children(func() {
				s.onboardingDots(c)
				ui.Box(c).Grow(1)
				if regularButton(c, "Skip").Clicked() {
					s.finishOnboarding()
					return
				}
				if s.onboardStep > 0 && regularButton(c, "Back").Clicked() {
					s.onboardStep--
					return
				}
				label := "Continue"
				if s.onboardStep == onboardingSteps-1 {
					label = "Get started"
				}
				if regularPrimaryButton(c, label).Clicked() {
					if s.onboardStep == onboardingSteps-1 {
						s.finishOnboarding()
					} else {
						s.onboardStep++
					}
				}
			})
		})
	})
}

// onboardingStepHeader draws the current step's title and its one-line
// promise: what this choice changes, and where to change it later.
func (s *Shell) onboardingStepHeader(c *ui.Context) {
	t := c.Theme()
	title, subtitle := "", ""
	switch s.onboardStep {
	case 0:
		title, subtitle = "Welcome to Solco", "One window for your coding agents: start them in terminal tabs and follow their work. Pick how Solco matches your system's look — you can change it later in Settings → General."
	case 1:
		title, subtitle = "Choose your agents", "Enabled agents appear in the New Tab menu. Turning one on installs Solco's status hook for it, so live state shows in the sidebar and Chat. Everything stays changeable in Settings → Agents."
	default:
		title, subtitle = "Solco on your phone", "Pair an iPhone or Android phone over your own network to follow agents, watch a terminal, continue a session and check quotas. Nothing is shared until you pair one in Settings → Remote Access."
	}
	ui.Text(c, title).FontSize(Typography().Heading).FontWeight(WeightSemibold)
	ui.Text(c, subtitle).FontSize(Typography().BodySmall).TextColor(t.TextMuted).
		FillWidth().MaxWidth(onboardingContentWidth).TextAlign(ui.Center)
}

// onboardAppearance is step 1: three preview cards — System (split light
// and dark), Light, Dark — painted in their fixed palettes so each card
// shows the look it names. A click applies the theme at once.
func (s *Shell) onboardAppearance(c *ui.Context) {
	ui.Row(c).FillWidth().Gap(Spacing().L).Children(func() {
		s.themeCard(c, "System", s.settings.General.Appearance == "system", func() {
			s.splitThemePreview(c)
		})
		s.themeCard(c, "Light", s.settings.General.Appearance == "light", func() {
			miniWindow(c, themePreviewWidth, themePreviewHeight, designTokens(false))
		})
		s.themeCard(c, "Dark", s.settings.General.Appearance == "dark", func() {
			miniWindow(c, themePreviewWidth, themePreviewHeight, designTokens(true))
		})
	})
}

// themePreviewWidth sizes one appearance card's mockup.
const (
	themePreviewWidth  float32 = 148
	themePreviewHeight float32 = 92
)

// themeCard is one selectable preview: the mockup, a radio dot and its
// label. The border carries the selection; the palette never changes with
// the active theme — the card must show the look it names.
func (s *Shell) themeCard(c *ui.Context, label string, selected bool, paint func()) {
	sp := Spacing()
	t := c.Theme()
	border := t.Border
	if selected {
		border = t.Accent
	}
	card := ui.ButtonBase(c).Grow(1).Padding(sp.S).Radius(Radius().Card).
		Border(1.5, border).FocusRing(false).Label(label).Children(func() {
		ui.Column(c).FillWidth().Gap(sp.S).Children(func() {
			paint()
			ui.Row(c).Gap(sp.S).AlignItems(ui.Center).Children(func() {
				dot := ui.Box(c).Width(13).Height(13).Radius(6.5).Shrink(0)
				if selected {
					dot.Background(t.Accent)
				} else {
					dot.Background(t.Background).Border(1.5, t.Border)
				}
				ui.Text(c, label).FontSize(Typography().BodySmall)
			})
		})
	})
	if card.Clicked() {
		s.applySettings(func(v *settings.Settings) error {
			v.General.Appearance = appearanceFromLabel(label)
			return nil
		})
	}
}

// appearanceFromLabel maps a preview card's label onto the stored value.
func appearanceFromLabel(label string) string {
	switch label {
	case "Light":
		return "light"
	case "Dark":
		return "dark"
	default:
		return "system"
	}
}

// splitThemePreview paints the System card: one window torn between the
// light and dark palettes, the look that follows macOS.
func (s *Shell) splitThemePreview(c *ui.Context) {
	ui.Box(c).Width(themePreviewWidth).Height(themePreviewHeight).Shrink(0).
		Radius(Radius().Control).Border(1, designTokens(false).BorderSubtle).Clip().
		Children(func() {
			ui.Row(c).Fill().Children(func() {
				previewPane(c, 0.5, designTokens(false))
				previewPane(c, 0.5, designTokens(true))
			})
		})
}

// previewPane paints one half of the split preview: the background and two
// muted content bars in the pane's fixed palette.
func previewPane(c *ui.Context, barScale float32, k DesignTokens) {
	sp := Spacing()
	ui.Box(c).Grow(1).FillHeight().Background(k.Content).Children(func() {
		ui.Column(c).FillWidth().Padding(sp.M, sp.M, 0, sp.M).Gap(sp.S).Children(func() {
			contentBar(c, 44*barScale+18, k)
			contentBar(c, 30*barScale+12, k)
		})
	})
}

// miniWindow paints a tiny Solco window in a fixed palette: traffic
// lights, a sidebar strip, content bars — the shape users recognize.
func miniWindow(c *ui.Context, w, h float32, k DesignTokens) {
	sp := Spacing()
	ui.Box(c).Width(w).Height(h).Shrink(0).Radius(Radius().Control).
		Background(k.Content).Border(1, k.BorderSubtle).Clip().Children(func() {
		ui.Column(c).Fill().Children(func() {
			ui.Row(c).FillWidth().Padding(sp.S, sp.S, 0, sp.S).Children(func() {
				ui.Row(c).Gap(4).Children(func() {
					trafficLight(c, "#ff5f57")
					trafficLight(c, "#febc2e")
					trafficLight(c, "#28c840")
				})
			})
			ui.Row(c).Fill().Padding(sp.S, sp.S, sp.S, sp.S).Gap(sp.S).Children(func() {
				ui.Box(c).Width(w * 0.3).FillHeight().Radius(4).Background(k.PanelHover)
				ui.Column(c).FillHeight().Gap(6).Padding(0, sp.S, 0, 0).Children(func() {
					contentBar(c, 52, k)
					contentBar(c, 38, k)
					contentBar(c, 46, k)
				})
			})
		})
	})
}

// trafficLight is one macOS window control dot.
func trafficLight(c *ui.Context, color string) {
	ui.Box(c).Width(8).Height(8).Radius(4).Shrink(0).Background(ui.Hex(color))
}

// contentBar is one muted text line of a mockup.
func contentBar(c *ui.Context, width float32, k DesignTokens) {
	ui.Box(c).Width(width).Height(6).Radius(3).Shrink(0).
		Background(k.TextMuted.Alpha(0.35))
}

// onboardAgents is step 2: the same toggle rows as Settings → Agents over
// the launchable kinds, with the audit's status notes. Turning an agent on
// installs its Solco status hook at once; every choice stays changeable
// later in Settings → Agents. A long list scrolls; the footer stays put.
func (s *Shell) onboardAgents(c *ui.Context) {
	if !s.providers.loaded && !s.providers.loading && s.providers.errText == "" {
		s.requestProvidersAudit()
	}
	s.ensureLaunchKinds()
	if s.launchKinds == nil {
		loadingState(c, "Checking…")
		return
	}
	var rows []settingsRow
	for _, kind := range s.launchKinds {
		if kind.ID == app.TerminalKind {
			continue
		}
		rows = append(rows, s.agentToggleRow(c, kind))
	}
	if len(rows) > 0 {
		settingsGroup(c, "", rows...)
	}
}

// onboardPhone is step 3: a Mac and a phone drawn side by side — the only
// introduction the companion gets. Pairing stays a Settings → Remote
// Access action and nothing is configured here.
func (s *Shell) onboardPhone(c *ui.Context) {
	sp := Spacing()
	t := c.Theme()
	k := designTokens(t.Dark)
	ui.Row(c).FillWidth().Gap(sp.XL).AlignItems(ui.Center).Children(func() {
		// The Mac: the same mini window, plus one live agent row.
		ui.Box(c).Width(210).Height(140).Shrink(0).Radius(Radius().Card).
			Background(k.Content).Border(1, k.BorderSubtle).Clip().Children(func() {
			ui.Column(c).Fill().Children(func() {
				ui.Row(c).FillWidth().Padding(sp.S, sp.S, 0, sp.S).Children(func() {
					ui.Row(c).Gap(4).Children(func() {
						trafficLight(c, "#ff5f57")
						trafficLight(c, "#febc2e")
						trafficLight(c, "#28c840")
					})
				})
				ui.Row(c).Fill().Padding(sp.S, sp.S, sp.S, sp.S).Gap(sp.S).Children(func() {
					ui.Box(c).Width(56).FillHeight().Radius(4).Background(k.PanelHover)
					ui.Column(c).FillHeight().Gap(sp.S).Padding(0, sp.S, 0, 0).Children(func() {
						ui.Row(c).Gap(sp.S).AlignItems(ui.Center).Children(func() {
							ui.Box(c).Width(14).Height(14).Radius(7).Shrink(0).Background(k.Accent.Alpha(0.8))
							contentBar(c, 62, k)
							ui.Box(c).Grow(1)
							ui.Box(c).Width(6).Height(6).Radius(3).Shrink(0).Background(t.Success)
						})
						contentBar(c, 96, k)
						contentBar(c, 74, k)
					})
				})
			})
		})
		// The link between them.
		ui.Column(c).Gap(sp.S).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconGlobe).Size(18, 18).TextColor(t.TextMuted)
			ui.Text(c, "your network").FontSize(Typography().Micro).TextColor(t.TextMuted)
		})
		// The phone: a rounded handset with three mini agent rows.
		ui.Box(c).Width(96).Height(176).Shrink(0).Radius(20).
			Background(k.Panel).Border(1.5, k.BorderSubtle).Children(func() {
			ui.Column(c).Fill().Padding(sp.M, sp.S, sp.M, sp.S).Children(func() {
				ui.Row(c).FillWidth().Children(func() {
					ui.Box(c).Grow(1)
					ui.Box(c).Width(30).Height(4).Radius(2).Background(k.TextMuted.Alpha(0.4))
					ui.Box(c).Grow(1)
				})
				ui.Box(c).Height(sp.S)
				for i := range 3 {
					live := i == 0
					ui.Row(c).FillWidth().Padding(sp.XS, 0).Gap(5).AlignItems(ui.Center).Children(func() {
						dot := ui.Box(c).Width(7).Height(7).Radius(3.5).Shrink(0).Background(k.PanelHover)
						if live {
							dot.Background(t.Success)
						}
						contentBar(c, float32(46-i*8), k)
					})
				}
			})
		})
	})
}

// onboardingDots draws the step indicator: one dot per step, the current
// one solid, the finished ones muted-accent.
func (s *Shell) onboardingDots(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(Spacing().S).AlignItems(ui.Center).Children(func() {
		for i := range onboardingSteps {
			dot := ui.Box(c).Width(7).Height(7).Radius(3.5).Shrink(0)
			switch {
			case i == s.onboardStep:
				dot.Background(t.Accent)
			case i < s.onboardStep:
				dot.Background(t.Accent.Alpha(0.4))
			default:
				dot.Background(designTokens(t.Dark).BorderSubtle)
			}
		}
		ui.Box(c).Width(Spacing().S)
		ui.Textf(c, "Step %d of %d", s.onboardStep+1, onboardingSteps).
			FontSize(Typography().Caption).TextColor(t.TextMuted)
	})
}
