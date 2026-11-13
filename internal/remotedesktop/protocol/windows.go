package protocol

// Window mode: the phone looks at (and controls) one application window instead of the whole desktop.
// Window and app ids are opaque to the phone; titles and names are screen content and only ever travel
// over an approved session's control channel. The JSON tags of the types here are also the helper IPC's.

// Rect is a rectangle in the platform's global point space (the space DisplayInfo and mouse events use).
type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Contains reports whether the point lies inside r (right and bottom edges excluded).
func (r Rect) Contains(x, y float64) bool {
	return x >= r.X && y >= r.Y && x < r.X+r.Width && y < r.Y+r.Height
}

// WindowInfo is one window the phone may choose.
type WindowInfo struct {
	ID       string  `json:"id"`
	App      string  `json:"app"`
	BundleID string  `json:"bundleId,omitempty"`
	Title    string  `json:"title,omitempty"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Width    float64 `json:"width"`
	Height   float64 `json:"height"`
	// OnScreen: on a screen right now. False = minimized, hidden or on another Space.
	OnScreen  bool `json:"onScreen"`
	Minimized bool `json:"minimized,omitempty"`
}

// AppInfo is one installed application the phone may open.
type AppInfo struct {
	BundleID string `json:"bundleId"`
	Name     string `json:"name"`
	Running  bool   `json:"running"`
}

// WindowGeometry is where a window capture sits in the global point space. Source is what the video
// shows (the window and its popups, widened to the video's aspect ratio, so a normalised pointer
// position maps linearly onto it); Windows are the rectangles that really belong to the target, and
// input outside them is not forwarded. OnScreen false = the window is captured on its own (another
// Space, minimized) and cannot be controlled until it is activated.
type WindowGeometry struct {
	CaptureID uint32 `json:"captureId"`
	Source    Rect   `json:"source"`
	Windows   []Rect `json:"windows"`
	OnScreen  bool   `json:"onScreen"`
}

// WindowGoneReason says why a window left the session.
type WindowGoneReason string

const (
	GoneClosed    WindowGoneReason = "closed"
	GoneAppQuit   WindowGoneReason = "app_quit"
	GoneMinimized WindowGoneReason = "minimized"
	GoneHidden    WindowGoneReason = "hidden"
)

// Target kinds: what the video shows.
const (
	TargetDisplay = "display"
	TargetWindow  = "window"
	// TargetNone: a session opened idle that has not chosen anything yet; nothing is captured.
	TargetNone = "none"
)

// WindowTarget names the window a window-mode session shows (in SessionState).
type WindowTarget struct {
	ID       string `json:"id"`
	App      string `json:"app"`
	BundleID string `json:"bundleId,omitempty"`
	Title    string `json:"title,omitempty"`
	OnScreen bool   `json:"onScreen"`
}

// Insets are the parts of the phone's screen the video must not sit under (status bar, home indicator, notch), in points.
type Insets struct {
	Top    float64 `json:"top"`
	Bottom float64 `json:"bottom"`
	Left   float64 `json:"left"`
	Right  float64 `json:"right"`
}

// Viewport is the phone's screen as the video sees it, in points. Scale is pixels per point.
type Viewport struct {
	W      float64 `json:"w"`
	H      float64 `json:"h"`
	Scale  float64 `json:"scale"`
	Insets Insets  `json:"insets"`
}

// Size is what the window is fitted to: the screen without the insets.
func (v Viewport) Size() (w, h float64) {
	return v.W - v.Insets.Left - v.Insets.Right, v.H - v.Insets.Top - v.Insets.Bottom
}

// Valid bounds a viewport the phone sent: a real phone or tablet screen, nothing the Mac could be asked to
// make absurd.
func (v Viewport) Valid() bool {
	w, h := v.Size()
	return w >= MinFitPoints && h >= MinFitPoints && w <= MaxFitPoints && h <= MaxFitPoints && v.Scale > 0 && v.Scale <= 6
}

// WindowFit is window.fit: resize the shown window to the phone's viewport (idempotent: the same viewport
// again changes nothing, a rotation is a new viewport).
type WindowFit struct {
	Type     string   `json:"type"`
	Viewport Viewport `json:"viewport"`
}

// Clamped says the app would not shrink to the phone: its minimum width, and the factor the picture is
// scaled by on the phone (phone width / MinW).
type Clamped struct {
	MinW  float64 `json:"minW"`
	Scale float64 `json:"scale"`
}

// RestoreOutcome says what became of a fitted window when the session let it go.
type RestoreOutcome string

const (
	RestoreDone    RestoreOutcome = "restored"
	RestoreSkipped RestoreOutcome = "skipped" // the user moved or resized it after the fit: left as they put it
	RestoreGone    RestoreOutcome = "gone"
	RestoreNone    RestoreOutcome = "none" // nothing was journaled for the window
)

// WindowRestored is the Mac's report that a fitted window was given back (or was left on purpose).
type WindowRestored struct {
	Type     string         `json:"type"`
	WindowID string         `json:"windowId"`
	Outcome  RestoreOutcome `json:"outcome"`
}

// MenuItem is one entry of the shown app's menu bar. Titles are the app's screen content: they reach the phone
// and are never logged. A separator has no title; Children is a submenu.
type MenuItem struct {
	Title     string     `json:"title"`
	Enabled   bool       `json:"enabled"`
	Checked   bool       `json:"checked,omitempty"`
	Separator bool       `json:"separator,omitempty"`
	Shortcut  string     `json:"shortcut,omitempty"`
	Children  []MenuItem `json:"children,omitempty"`
}

// WindowMenu answers window.menu.get: the menus of the app that owns the shown window (the Apple menu left
// out). The index of a menu or item in these arrays is what window.menu.invoke names.
type WindowMenu struct {
	Type     string     `json:"type"`
	WindowID string     `json:"windowId"`
	Items    []MenuItem `json:"items"`
}

// MenuInvoke is window.menu.invoke: press the item at Path (an index into the menus, then into each submenu).
type MenuInvoke struct {
	Type string `json:"type"`
	Path []int  `json:"path"`
}

// Valid bounds a path the phone sent: a few small indices, at least a menu and one of its items.
func (m MenuInvoke) Valid() bool {
	if len(m.Path) < 2 || len(m.Path) > MaxMenuDepth+1 {
		return false
	}
	for _, i := range m.Path {
		if i < 0 || i >= 1000 {
			return false
		}
	}
	return true
}

// Zoom directions of window.zoom.
const (
	ZoomIn  = "in"
	ZoomOut = "out"
)

// WindowZoom is window.zoom: press the shown app's own Zoom In (⌘+) or Zoom Out (⌘-) once, so its text is drawn
// larger or smaller by the app itself. Zoom out never goes below the size the window had before the phone.
type WindowZoom struct {
	Type string `json:"type"`
	Dir  string `json:"dir"`
}

// Valid is true for the two directions.
func (z WindowZoom) Valid() bool { return z.Dir == ZoomIn || z.Dir == ZoomOut }

// InfoCapabilities lists optional features of this Mac's Remote Desktop (GET /remote-desktop).
type InfoCapabilities struct {
	Windows bool `json:"windows,omitempty"`
}

// Limits of the window-mode messages (they travel the control channel, whose messages stay under 64 KiB).
const (
	MaxWindowsListed  = 60
	MaxAppsListed     = 400
	MaxThumbWidth     = 480
	MaxThumbBytes     = 48 << 10
	MaxIconBytes      = 24 << 10
	MaxBundleIDLength = 256
	MinFitPoints      = 100
	MaxFitPoints      = 4000
	MaxMenuDepth      = 3
	MaxMenuBytes      = 56 << 10
)
