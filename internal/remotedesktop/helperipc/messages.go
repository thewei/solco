package helperipc

import (
	"encoding/json"

	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// Command ops (Go → helper). Mirrors `Op` in the helper's Messages.swift.
const (
	OpDisplaysList       = "displays.list"
	OpPermissionsCheck   = "permissions.check"
	OpPermissionsRequest = "permissions.request"
	OpCaptureStart       = "capture.start"
	OpCaptureStop        = "capture.stop"
	OpCaptureKeyframe    = "capture.keyframe"
	OpCaptureTune        = "capture.tune"
	OpInputMouse         = "input.mouse"
	OpInputScroll        = "input.scroll"
	OpInputKey           = "input.key"
	OpInputText          = "input.text"
	OpInputReleaseAll    = "input.release_all"
	OpClipboardRead      = "clipboard.read"
	OpClipboardWrite     = "clipboard.write"
	OpPrivacySet         = "privacy.set"
	OpScreenState        = "screen.state"
	OpScreenWake         = "screen.wake"
	OpScreenUnlock       = "screen.unlock"
	OpWindowsList        = "windows.list"
	OpAppsList           = "apps.list"
	OpAppIcon            = "app.icon"
	OpAppOpen            = "app.open"
	OpWindowRaise        = "window.raise"
	OpWindowThumb        = "window.thumb"
	OpWindowFit          = "window.fit"
	OpWindowRestore      = "window.restore"
	OpWindowMenu         = "window.menu"
	OpWindowMenuInvoke   = "window.menu.invoke"
	OpWindowZoom         = "window.zoom"
	OpShutdown           = "shutdown"
)

// Capabilities the helper announces in its Hello that window mode needs.
const (
	CapWindowCapture = "capture.window"
	CapWindowCatalog = "window.catalog"
	CapAppCatalog    = "app.catalog"
	CapWindowFit     = "window.fit"
	CapWindowMenu    = "window.menu"
)

// Event names (helper → Go).
const (
	EventDisplaysChanged = "displays.changed"
	EventCaptureStopped  = "capture.stopped"
	EventError           = "error"
	EventWindowGeometry  = "window.geometry"
	EventWindowGone      = "window.gone"
)

// Hello is the helper's first message; Token proves it is the process we launched.
type Hello struct {
	T               string   `json:"t"`
	ProtocolVersion int      `json:"protocolVersion"`
	HelperVersion   string   `json:"helperVersion"`
	Token           string   `json:"token"`
	PID             int32    `json:"pid"`
	Capabilities    []string `json:"capabilities"`
}

// HelloAck answers Hello.
type HelloAck struct {
	T               string         `json:"t"`
	ProtocolVersion int            `json:"protocolVersion"`
	OK              bool           `json:"ok"`
	Error           *ProtocolError `json:"error,omitempty"`
}

// ProtocolError is the helper's failure shape; Code values are those of the helper's ErrorCode enum.
type ProtocolError struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

func (e *ProtocolError) Error() string {
	if e.Message == "" {
		return "helper: " + e.Code
	}
	return "helper: " + e.Code + ": " + e.Message
}

// Helper error codes (ProtocolError.Code).
const (
	CodeProtocolMismatch          = "protocol_mismatch"
	CodeBadRequest                = "bad_request"
	CodeUnknownOp                 = "unknown_op"
	CodePermissionScreenRecording = "permission_screen_recording"
	CodePermissionAccessibility   = "permission_accessibility"
	CodeDisplayNotFound           = "display_not_found"
	CodeCaptureFailed             = "capture_failed"
	CodeEncoderUnavailable        = "encoder_unavailable"
	CodeEncoderFailed             = "encoder_failed"
	CodeNotCapturing              = "not_capturing"
	CodeInputFailed               = "input_failed"
	CodeWindowNotFound            = "window_not_found"
	CodeAppNotFound               = "app_not_found"
	CodeAppOpenFailed             = "app_open_failed"
	CodeFitUnavailable            = "fit_unavailable"
	CodeMenuUnavailable           = "menu_unavailable"
	CodeMenuItemUnavailable       = "menu_item_unavailable"
	CodeInternal                  = "internal"
)

type command struct {
	T    string `json:"t"`
	ID   int64  `json:"id"`
	Op   string `json:"op"`
	Args any    `json:"args"`
}

type replyEnvelope struct {
	T      string          `json:"t"`
	ID     int64           `json:"id"`
	OK     bool            `json:"ok"`
	Error  *ProtocolError  `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

type eventEnvelope struct {
	T    string          `json:"t"`
	Ev   string          `json:"ev"`
	Data json.RawMessage `json:"data"`
}

// Event is an unsolicited helper message.
type Event struct {
	Name string
	Data json.RawMessage
}

// DisplaysResult is the result of displays.list and the data of displays.changed.
type DisplaysResult struct {
	Displays []protocol.DisplayInfo `json:"displays"`
}

// CaptureStopped is the data of capture.stopped (Error nil = a normal stop).
type CaptureStopped struct {
	CaptureID uint32         `json:"captureId"`
	Error     *ProtocolError `json:"error,omitempty"`
}

// Args of the input ops. Positions are global points already mapped by package mapper.
type MouseArgs struct {
	Action     string  `json:"action"` // move | down | up
	Button     string  `json:"button,omitempty"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	ClickCount int     `json:"clickCount,omitempty"`
}

type ScrollArgs struct {
	DX float64 `json:"dx"`
	DY float64 `json:"dy"`
}

type KeyArgs struct {
	Code uint16 `json:"code"`
	Down bool   `json:"down"`
}

type TextArgs struct {
	Text string `json:"text"`
}

type TuneArgs struct {
	BitrateKbps *int `json:"bitrateKbps,omitempty"`
}

type PermissionRequestArgs struct {
	Kind protocol.PermissionKind `json:"kind"`
}

// PrivacyArgs blacks the physical screens out (On) or restores them.
type PrivacyArgs struct {
	On bool `json:"on"`
}

// ScreenState answers screen.state and screen.unlock: whether the lock screen is up (after the attempt).
type ScreenState struct {
	Locked bool `json:"locked"`
}

// UnlockArgs carries the Mac login password to the helper for one typing; it is never logged or kept.
type UnlockArgs struct {
	Password string `json:"password"`
}

// WindowsResult is the result of windows.list.
type WindowsResult struct {
	Windows []protocol.WindowInfo `json:"windows"`
}

// AppsResult is the result of apps.list.
type AppsResult struct {
	Apps []protocol.AppInfo `json:"apps"`
}

// BundleArgs names an application (app.icon, app.open).
type BundleArgs struct {
	BundleID string `json:"bundleId"`
}

// AppIconResult is the result of app.icon: a 64 px PNG, base64.
type AppIconResult struct {
	BundleID string `json:"bundleId"`
	PNG      string `json:"png"`
}

// AppOpenResult is the result of app.open: the window to show.
type AppOpenResult struct {
	WindowID string `json:"windowId"`
}

// WindowArgs names a window (window.raise).
type WindowArgs struct {
	WindowID string `json:"windowId"`
}

// ThumbArgs asks for a still of one window.
type ThumbArgs struct {
	WindowID string `json:"windowId"`
	MaxWidth int    `json:"maxWidth"`
}

// ThumbResult is the result of window.thumb: a JPEG, base64.
type ThumbResult struct {
	WindowID string `json:"windowId"`
	JPEG     string `json:"jpeg"`
}

// WindowGone is the data of the window.gone event.
type WindowGone struct {
	WindowID string                    `json:"windowId"`
	Reason   protocol.WindowGoneReason `json:"reason"`
}

// FitArgs is window.fit: resize the window to this many points.
type FitArgs struct {
	WindowID string  `json:"windowId"`
	Width    float64 `json:"width"`
	Height   float64 `json:"height"`
}

// FitResult is what the app allowed (read back); Clamped = it kept the window bigger or smaller than asked.
type FitResult struct {
	WindowID string        `json:"windowId"`
	Applied  protocol.Rect `json:"applied"`
	Clamped  bool          `json:"clamped"`
}

// MenuItem mirrors the helper's menu entry; the tags are the helper's own.
type MenuItem struct {
	Title     string     `json:"title"`
	Enabled   bool       `json:"enabled"`
	Checked   bool       `json:"checked"`
	Separator bool       `json:"separator"`
	Shortcut  string     `json:"shortcut,omitempty"`
	Children  []MenuItem `json:"children,omitempty"`
}

// MenuResult is the result of window.menu.
type MenuResult struct {
	WindowID string     `json:"windowId"`
	Items    []MenuItem `json:"items"`
}

// MenuInvokeArgs is window.menu.invoke.
type MenuInvokeArgs struct {
	WindowID string `json:"windowId"`
	Path     []int  `json:"path"`
}

// ZoomArgs is window.zoom: press the app's Zoom In ("in") or Zoom Out ("out") once.
type ZoomArgs struct {
	WindowID string `json:"windowId"`
	Dir      string `json:"dir"`
}

// ZoomResult is the net number of steps the window is now above the size it had before the phone.
type ZoomResult struct {
	WindowID string `json:"windowId"`
	Steps    int    `json:"steps"`
}

// RestoreArgs is window.restore; no WindowID = every journaled window.
type RestoreArgs struct {
	WindowID string `json:"windowId,omitempty"`
}

// RestoreEntry is the outcome for one window: restored / skipped / gone / none.
type RestoreEntry struct {
	WindowID string `json:"windowId"`
	Outcome  string `json:"outcome"`
}

// RestoreResult is the result of window.restore and of the one-shot `restore-journal`.
type RestoreResult struct {
	Results []RestoreEntry `json:"results"`
}
