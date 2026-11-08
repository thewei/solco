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
	// TextMutedOnMaterial is the secondary text tone for text drawn straight
	// on the sidebar's vibrancy material (the desktop shows through it), one
	// step brighter than TextMuted so it keeps its contrast there.
	TextMutedOnMaterial ui.Color
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

// solcoTheme maps the unified role table (design_gen.go) onto the framework
// theme. Roles are the only colour source: a hex written here would be a second
// palette.
func solcoTheme(dark bool) *ui.Theme {
	r := designRoles(dark)
	t := *ui.LightTheme()
	if dark {
		t = *ui.DarkTheme()
	}
	t.Background = r.Canvas
	t.Surface = r.Surface
	t.SurfaceHover = r.SurfaceHover
	t.SurfacePressed = r.SurfacePressed
	t.Border = r.LineStrong
	t.Text = r.Text
	t.TextMuted = r.Muted
	t.Accent = r.Accent
	t.AccentHover = r.AccentHover
	t.AccentPressed = r.AccentPressed
	t.AccentText = r.OnAccent
	t.Warning = r.Warn
	t.Success = r.Ok
	t.Danger = r.Danger
	t.Selection = r.AccentSoft
	t.Focus = r.Focus
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
	r := designRoles(dark)
	tokens := DesignTokens{
		SidebarHover:        r.Hover,
		SidebarSelect:       r.Select,
		SidebarSelectStrong: r.SelectStrong,
		Content:             r.Canvas,
		Panel:               r.Surface,
		PanelHover:          r.SurfaceHover,
		BorderSubtle:        r.LineOnMaterial,
		Text:                r.Text,
		TextMuted:           r.Muted,
		TextMutedOnMaterial: r.MutedOnMaterial,
		Accent:              r.Accent,
		Danger:              r.Danger,
	}
	if !dark {
		tokens.Sidebar = ui.Hex("#efeff1")
		tokens.TreeLine = ui.RGBA(113, 113, 122, 0.28)
		if highContrast {
			tokens.BorderSubtle = ui.RGBA(0, 0, 0, 0.35)
			tokens.TreeLine = ui.RGBA(0, 0, 0, 0.50)
			tokens.TextMuted = ui.Hex("#3f3f46")
			tokens.TextMutedOnMaterial = tokens.TextMuted
			tokens.SidebarSelect = ui.RGBA(0, 0, 0, 0.12)
			tokens.SidebarSelectStrong = ui.RGBA(0, 0, 0, 0.20)
		}
		return tokens
	}
	tokens.Sidebar = ui.Hex("#17171a")
	tokens.TreeLine = ui.RGBA(152, 152, 159, 0.26)
	if highContrast {
		tokens.BorderSubtle = ui.RGBA(255, 255, 255, 0.35)
		tokens.TreeLine = ui.RGBA(255, 255, 255, 0.45)
		tokens.TextMuted = ui.Hex("#d4d4d8")
		tokens.TextMutedOnMaterial = tokens.TextMuted
		tokens.SidebarSelect = ui.RGBA(255, 255, 255, 0.16)
		tokens.SidebarSelectStrong = ui.RGBA(255, 255, 255, 0.26)
	}
	return tokens
}

// sidebarMuted is the secondary text colour for the sidebar's own rows.
func sidebarMuted(t *ui.Theme) ui.Color {
	return designTokens(t.Dark).TextMutedOnMaterial
}
