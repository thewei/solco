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
)
