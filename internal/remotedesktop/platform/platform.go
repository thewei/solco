// Package platform is the extension seam of Remote Desktop: everything an operating system must do
// for the session — enumerate displays, capture and encode one of them, inject input, move plain
// text through the clipboard, report permissions — behind small interfaces. macOS implements them
// (package macos, over the native Swift helper); Linux and Windows backends are future packages that
// implement the same Backend and are picked by package auto without touching the session, the
// WebRTC transport or the Host.
//
// A Backend produces H.264 as Annex-B access units (SPS/PPS in-band on keyframes), PTS relative to
// the capture start, tagged with the capture generation that made them; input arrives in the
// platform's global point space (package mapper) and as W3C KeyboardEvent.code names.
//
// [INPUT]: protocol
// [OUTPUT]: Backend, Capturer, Input, Clipboard, Frame, Event, ErrUnsupported
// [POS]: interfaces only, no OS code; platform/macos is the only implementation in this release
package platform

import (
	"context"
	"errors"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// ErrUnsupported means this OS has no Remote Desktop backend yet.
var ErrUnsupported = errors.New("remote desktop is not supported on this platform")

// Backend is one operating system's implementation. It is cheap to hold; Open starts the native
// side (for macOS: launches the helper) and so costs a process.
type Backend interface {
	// ID names the backend ("macos", "unsupported", later "linux", "windows").
	ID() string
	// Available reports whether Open can work (helper present and valid, OS APIs there). The reason
	// is for diagnostics and never contains user data.
	Available() (ok bool, reason string)
	// Permissions reads the OS grant state without opening a session.
	Permissions(ctx context.Context) (protocol.PermissionStatus, error)
	// RequestPermission asks the OS to prompt for one permission (a no-op once the user has
	// decided; the app then points them at System Settings) and returns the state afterwards.
	RequestPermission(ctx context.Context, kind protocol.PermissionKind) (protocol.PermissionStatus, error)
	// Open starts the native side for one session.
	Open(ctx context.Context) (Capturer, error)
}

// Capturer is one open native session: capture + encode of one display at a time, input, clipboard.
// Close releases everything (capture, encoder, the native process) and is idempotent.
type Capturer interface {
	Displays(ctx context.Context) ([]protocol.DisplayInfo, error)
	Permissions(ctx context.Context) (protocol.PermissionStatus, error)
	StartCapture(ctx context.Context, cfg protocol.CaptureConfig) (protocol.CaptureStarted, error)
	StopCapture(ctx context.Context) error
	// RequestKeyframe asks for a fresh keyframe (PLI/FIR, a joining viewer, a dropped frame).
	RequestKeyframe()
	// TuneBitrate retargets the encoder without restarting it.
	TuneBitrate(kbps int)

	// Frames yields encoded video; Events yields state changes. Both close when the capturer ends.
	Frames() <-chan Frame
	Events() <-chan Event

	Input() Input
	Clipboard() Clipboard

	// Done closes when the native side is gone (crash, kill, Close); Err says why (nil = requested).
	Done() <-chan struct{}
	Err() error
	Close() error
}

// Screen is the optional capability of a Capturer that can act on the physical screen: black it out, see
// whether the lock screen is up, wake it and type the saved login password into it. The session
// type-asserts it, so a backend without these simply offers none of them.
type Screen interface {
	// SetBlackout blacks every physical display out (on) or restores it; capture is unaffected.
	SetBlackout(on bool) error
	// LockState reports whether the lock screen is up.
	LockState(ctx context.Context) (locked bool, err error)
	// Wake wakes a sleeping display.
	Wake(ctx context.Context) error
	// Unlock types password into the lock screen and reports whether it is still up afterwards.
	Unlock(ctx context.Context, password string) (stillLocked bool, err error)
}

// Windows is the optional capability of a Capturer that can show one application window instead of a
// whole display: list windows and installed apps, open an app, bring a window forward, take a still of
// one. The session type-asserts it; a backend without it offers display mode only. A window capture is
// started through StartCapture with CaptureConfig.WindowID set.
type Windows interface {
	// WindowsSupported reports whether this helper/OS can do window mode at all.
	WindowsSupported() bool
	ListWindows(ctx context.Context) ([]protocol.WindowInfo, error)
	ListApps(ctx context.Context) ([]protocol.AppInfo, error)
	// AppIcon returns the app's 64 px icon as a base64 PNG.
	AppIcon(ctx context.Context, bundleID string) (string, error)
	// OpenApp launches (or re-opens) the app and returns the id of the window to show.
	OpenApp(ctx context.Context, bundleID string) (windowID string, err error)
	// RaiseWindow brings the window (and its app) to the front, un-minimizing it.
	RaiseWindow(ctx context.Context, windowID string) error
	// WindowThumbnail returns a small JPEG still of the window, base64.
	WindowThumbnail(ctx context.Context, windowID string, maxWidth int) (string, error)
}

// WindowFitter is the optional capability of a Capturer that can resize a window to the phone's screen and
// give it back. Restore is idempotent and never overwrites a frame the user changed after the fit.
type WindowFitter interface {
	// FitWindow resizes the window to w×h points and reports what the app allowed.
	FitWindow(ctx context.Context, windowID string, w, h float64) (applied protocol.Rect, clamped bool, err error)
	// RestoreWindow puts the window back; the outcome is restored, skipped, gone or none (nothing journaled).
	RestoreWindow(ctx context.Context, windowID string) (protocol.RestoreOutcome, error)
}

// JournalRestorer is the optional capability of a Backend to give back every window a vanished session
// left fitted (the deferred restore after a lost link, and Solco's start-up), without a session or a helper
// process of its own.
type JournalRestorer interface {
	RestoreJournal(ctx context.Context) error
}

// WindowSupport is the optional capability of a Backend to say, without opening a helper, that its
// Capturers implement Windows.
type WindowSupport interface {
	WindowsSupported() bool
}

// Frame is one encoded access unit.
type Frame struct {
	CaptureID uint32
	Keyframe  bool
	PTS       time.Duration
	Data      []byte
}

// EventKind enumerates Event.
type EventKind int

const (
	// EventDisplaysChanged: the display set or geometry changed; Displays is the fresh list.
	EventDisplaysChanged EventKind = iota + 1
	// EventCaptureStopped: the capture ended on its own (display gone, OS stopped it); Err says why.
	EventCaptureStopped
	// EventError: an asynchronous failure the session should surface (e.g. input refused).
	EventError
	// EventWindowGeometry: a window capture moved or its popups changed; Geometry is the fresh placement.
	EventWindowGeometry
	// EventWindowGone: the captured window left (closed, app quit, minimized, hidden); WindowID and GoneReason say which and why.
	EventWindowGone
)

// Event is an asynchronous backend notification.
type Event struct {
	Kind       EventKind
	Displays   []protocol.DisplayInfo
	CaptureID  uint32
	Err        *protocol.Error
	Geometry   *protocol.WindowGeometry
	WindowID   string
	GoneReason protocol.WindowGoneReason
}

// Input injects pointer and keyboard events. Pointer positions are global points; Key takes W3C
// KeyboardEvent.code names and returns an error for a key the platform cannot press. ReleaseAll must
// lift every button and key this Input pressed and is safe to call at any time.
type Input interface {
	PointerMove(x, y float64) error
	PointerButton(button string, down bool, x, y float64, clickCount int) error
	Scroll(dx, dy float64) error
	Key(code string, down bool) error
	Text(text string) error
	ReleaseAll() error
}

// Clipboard moves plain text between the phone and the Mac's pasteboard, only on explicit request.
type Clipboard interface {
	ReadText(ctx context.Context) (string, error)
	WriteText(ctx context.Context, text string) error
}
