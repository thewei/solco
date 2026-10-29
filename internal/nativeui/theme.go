package nativeui

import (
	"sync/atomic"

	"github.com/egoist/mygo/ui"
)

// highContrast mirrors Settings → General → Increase contrast for the whole
// process. Colors reach the views through plain functions of (dark) — the
// theme, the design tokens and the two surface palettes — that most call
// sites read without a Shell at hand, so Shell.View publishes the window's
// one preference here before it builds the frame's theme.
var highContrast atomic.Bool

// Oklch accent colors (MyGo 0.2.15 ui.Oklch): the same Apple-blue family
// as the old sRGB hex values — each call was hill-climbed in a probe so
// its sRGB fallback renders within a step of the original hex — but they
// carry a wide-gamut component, so P3 Mac displays show the fuller blue.
var (
	accentLight        = ui.Oklch(0.6260, 0.1960, 254)   // was #0a84ff
	accentHoverLight   = ui.Oklch(0.5800, 0.2020, 254)   // was #0077ed
	accentPressedLight = ui.Oklch(0.5300, 0.1940, 254.8) // was #0067d8
	accentDark         = ui.Oklch(0.6260, 0.1960, 254)   // was #0a84ff
	accentHoverDark    = ui.Oklch(0.6870, 0.1720, 253.2) // was #409cff
	accentPressedDark  = ui.Oklch(0.5600, 0.1880, 255.2) // was #0070df
)

// DesignTokens are Solco's reusable presentation tokens. The surfaces follow
// a Cursor-3-style flat neutral ladder (2026-10-29 reskin): near-black canvas
// under a half-step lighter sidebar, raised panels, hairline borders, and a
// neutral selection fill. Accent stays out of chrome entirely: it is reserved
// for focus rings, primary buttons and links.
type DesignTokens struct {
	// Sidebar is the opaque sidebar fill for windows that cannot show the
	// vibrancy material (non-macOS, headless); with vibrancy the sidebar
	// draws nothing and the material shows through.
	Sidebar       ui.Color
	SidebarHover  ui.Color
	SidebarSelect ui.Color
	// SidebarSelectStrong is the focused selection face: the same neutral
	// fill one step stronger, so keyboard focus never repaints a row in
	// accent blue.
	SidebarSelectStrong ui.Color
	Content             ui.Color
	Panel               ui.Color
	PanelHover          ui.Color
	BorderSubtle        ui.Color
	TreeLine            ui.Color
	Text                ui.Color
	TextMuted           ui.Color
	Accent              ui.Color
	Danger              ui.Color
}

// resolvedDark maps the user appearance preference onto the OS-reported
// scheme. Only an explicit Light/Dark choice overrides the system.
func (s *Shell) resolvedDark(systemDark bool) bool {
	switch s.settings.General.Appearance {
	case "light":
		return false
	case "dark":
		return true
	default:
		return systemDark
	}
}

func solcoTheme(dark bool) *ui.Theme {
	if !dark {
		t := *ui.LightTheme()
		t.Background = ui.Hex("#f7f7f5")
		t.Surface = ui.Hex("#ffffff")
		t.SurfaceHover = ui.Hex("#f0f0f2")
		t.SurfacePressed = ui.Hex("#e8e8ec")
		t.Border = ui.Hex("#e2e2e6")
		t.Text = ui.Hex("#18181b")
		t.TextMuted = ui.Hex("#71717a")
		t.Accent = accentLight
		t.AccentHover = accentHoverLight
		t.AccentPressed = accentPressedLight
		t.AccentText = ui.Hex("#ffffff")
		t.Selection = ui.RGBA(10, 132, 255, 0.18)
		t.Focus = ui.RGBA(10, 132, 255, 0.45)
		t.Radius = Radius().Row
		t.Spacing = 3.5
		return strengthened(&t)
	}

	t := *ui.DarkTheme()
	t.Background = ui.Hex("#141516")
	t.Surface = ui.Hex("#212121")
	t.SurfaceHover = ui.Hex("#2a2a2e")
	t.SurfacePressed = ui.Hex("#333338")
	t.Border = ui.Hex("#333338")
	t.Text = ui.Hex("#ececee")
	t.TextMuted = ui.Hex("#8b8c92")
	t.Accent = accentDark
	t.AccentHover = accentHoverDark
	t.AccentPressed = accentPressedDark
	t.AccentText = ui.Hex("#ffffff")
	t.Warning = ui.Hex("#ffd60a")
	t.Success = ui.Hex("#30d158")
	t.Danger = ui.Hex("#ff453a")
	t.Selection = ui.RGBA(10, 132, 255, 0.28)
	t.Focus = ui.RGBA(10, 132, 255, 0.52)
	t.Radius = Radius().Row
	t.Spacing = 3.5
	return strengthened(&t)
}

// strengthened applies Increase contrast to the framework theme: the outline
// of every control and the secondary text tone, which is what the widgets and
// most of the views read.
func strengthened(t *ui.Theme) *ui.Theme {
	if !highContrast.Load() {
		return t
	}
	if t.Dark {
		t.Border, t.TextMuted = ui.Hex("#8e8e98"), ui.Hex("#d4d4d8")
	} else {
		t.Border, t.TextMuted = ui.Hex("#8b8b94"), ui.Hex("#3f3f46")
	}
	return t
}

// SidebarDensity configures vertical density and breathing room in the sidebar (0.9 P19).
type SidebarDensity string

const (
	DensityCompact     SidebarDensity = "compact"
	DensityDefault     SidebarDensity = "default"
	DensityComfortable SidebarDensity = "comfortable"
)

// sidebarDensity returns the active density preference with fallback to default.
func (s *Shell) sidebarDensity() SidebarDensity {
	switch s.settings.General.Density {
	case "compact":
		return DensityCompact
	case "comfortable":
		return DensityComfortable
	default:
		return DensityDefault
	}
}

// tokens returns the DesignTokens considering both dark theme and high contrast preference.
func (s *Shell) tokens(dark bool) DesignTokens {
	return designTokensWithContrast(dark, s.settings.General.HighContrast)
}

func designTokens(dark bool) DesignTokens {
	return designTokensWithContrast(dark, highContrast.Load())
}

func designTokensWithContrast(dark bool, highContrast bool) DesignTokens {
	if !dark {
		tokens := DesignTokens{
			Sidebar:             ui.Hex("#efeff1"),
			SidebarHover:        ui.RGBA(0, 0, 0, 0.05),
			SidebarSelect:       ui.RGBA(0, 0, 0, 0.06),
			SidebarSelectStrong: ui.RGBA(0, 0, 0, 0.11),
			Content:             ui.Hex("#f7f7f5"),
			Panel:               ui.Hex("#ffffff"),
			PanelHover:          ui.Hex("#f0f0f3"),
			BorderSubtle:        ui.RGBA(0, 0, 0, 0.07),
			TreeLine:            ui.RGBA(113, 113, 122, 0.28),
			Text:                ui.Hex("#18181b"),
			TextMuted:           ui.Hex("#71717a"),
			Accent:              accentLight,
			Danger:              ui.Hex("#ff3b30"),
		}
		if highContrast {
			tokens.BorderSubtle = ui.RGBA(0, 0, 0, 0.35)
			tokens.TreeLine = ui.RGBA(0, 0, 0, 0.50)
			tokens.TextMuted = ui.Hex("#3f3f46")
			tokens.SidebarSelect = ui.RGBA(0, 0, 0, 0.12)
			tokens.SidebarSelectStrong = ui.RGBA(0, 0, 0, 0.20)
		}
		return tokens
	}
	tokens := DesignTokens{
		Sidebar:             ui.Hex("#17171a"),
		SidebarHover:        ui.RGBA(255, 255, 255, 0.05),
		SidebarSelect:       ui.RGBA(255, 255, 255, 0.08),
		SidebarSelectStrong: ui.RGBA(255, 255, 255, 0.14),
		Content:             ui.Hex("#141516"),
		Panel:               ui.Hex("#212121"),
		PanelHover:          ui.Hex("#2a2a2e"),
		BorderSubtle:        ui.RGBA(255, 255, 255, 0.07),
		TreeLine:            ui.RGBA(152, 152, 159, 0.26),
		Text:                ui.Hex("#ececee"),
		TextMuted:           ui.Hex("#8b8c92"),
		Accent:              accentDark,
		Danger:              ui.Hex("#ff453a"),
	}
	if highContrast {
		tokens.BorderSubtle = ui.RGBA(255, 255, 255, 0.35)
		tokens.TreeLine = ui.RGBA(255, 255, 255, 0.45)
		tokens.TextMuted = ui.Hex("#d4d4d8")
		tokens.SidebarSelect = ui.RGBA(255, 255, 255, 0.16)
		tokens.SidebarSelectStrong = ui.RGBA(255, 255, 255, 0.26)
	}
	return tokens
}
