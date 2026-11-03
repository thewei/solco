// Package protocol is the platform-neutral vocabulary of Solco Remote Desktop: what the phone and the
// Mac say to each other (offer/answer signalling, the control-channel messages) and what a native
// capture backend reports (displays, capture sizes, permissions). It has no I/O and no framework
// imports so every layer — the native-helper IPC, the platform backends, the WebRTC transport, the
// session and the Host endpoints — shares one set of types and one set of error codes.
//
// [INPUT]: encoding/json, fmt
// [OUTPUT]: Version, Error/ErrorCode, DisplayInfo, CaptureConfig/CaptureStarted, PermissionStatus, Mode, Tier/TierSpec, Info, Offer, Answer, SessionState, ConnectionStats
// [POS]: the single source of the wire contract; the JSON tags of DisplayInfo/CaptureConfig/PermissionStatus are also the helper IPC's (solco-remote-helper's HelperProtocol)
package protocol

import "fmt"

// Version is the phone ⇄ Mac Remote Desktop protocol version. A phone offering another major
// version is refused with CodeProtocolUnsupported before anything starts.
const Version = 1

// ErrorCode is a stable, machine-readable failure reason. Messages are for logs and never carry
// screen content, typed text, clipboard contents, SDP or tokens.
type ErrorCode string

const (
	CodeDisabled            ErrorCode = "remote_disabled"      // Remote Desktop is off on the Mac
	CodeUnavailable         ErrorCode = "remote_unavailable"   // no backend for this OS / helper missing
	CodeProtocolUnsupported ErrorCode = "protocol_unsupported" // phone speaks another protocol major
	CodePermissionRequired  ErrorCode = "permission_required"  // Screen Recording / Accessibility not granted
	CodeDenied              ErrorCode = "denied"               // the Mac user declined
	CodeApprovalTimeout     ErrorCode = "approval_timeout"     // nobody answered the Mac's prompt in time
	CodeBusy                ErrorCode = "busy"                 // another session is active (one at a time)
	CodeBadRequest          ErrorCode = "bad_request"          // malformed offer / unknown field values
	CodeBadOffer            ErrorCode = "bad_offer"            // SDP could not be accepted
	CodeDisplayNotFound     ErrorCode = "display_not_found"    // the chosen display is gone
	CodeCaptureFailed       ErrorCode = "capture_failed"       // ScreenCaptureKit / encoder failure
	CodeHelperFailed        ErrorCode = "helper_failed"        // native helper died or never answered
	CodeViewOnly            ErrorCode = "view_only"            // an input message arrived in a view-only session
	CodeNotConnected        ErrorCode = "not_connected"        // control sent before the link was up
	CodeSessionEnded        ErrorCode = "session_ended"        // the session is over (Mac stopped it, revoked, link lost)
	CodeInternal            ErrorCode = "internal"
)

// Error is the one error type that crosses layers; the Host maps Code to an HTTP status.
type Error struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message,omitempty"`
}

func (e *Error) Error() string {
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Errf builds an *Error.
func Errf(code ErrorCode, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// CodeOf extracts the code of err when it is (or wraps nothing but) an *Error; otherwise CodeInternal.
func CodeOf(err error) ErrorCode {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return CodeInternal
}

// DisplayInfo describes one display. Geometry is in the platform's global *point* space — on macOS
// the Core Graphics space (origin = top-left of the primary display, y grows downward) that mouse
// events use; Pixel* is the panel's native resolution. The JSON tags are shared with the native
// helper IPC.
type DisplayInfo struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Width       float64 `json:"width"`
	Height      float64 `json:"height"`
	PixelWidth  int     `json:"pixelWidth"`
	PixelHeight int     `json:"pixelHeight"`
	Scale       float64 `json:"scale"`
	IsMain      bool    `json:"isMain"`
}

// CaptureConfig starts one capture generation. CaptureID tags every frame of the generation so
// frames still in flight after a restart are recognised and dropped.
type CaptureConfig struct {
	CaptureID           uint32 `json:"captureId"`
	DisplayID           string `json:"displayId"`
	Width               int    `json:"width"`
	Height              int    `json:"height"`
	FPS                 int    `json:"fps"`
	BitrateKbps         int    `json:"bitrateKbps"`
	KeyframeIntervalSec int    `json:"keyframeIntervalSec"`
}

// CaptureStarted is the helper's answer to a successful start.
type CaptureStarted struct {
	CaptureID       uint32 `json:"captureId"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	HardwareEncoder bool   `json:"hardwareEncoder"`
}

// PermissionStatus is the OS's grant state for the two capabilities Remote Desktop needs.
// Screen Recording gates seeing the desktop; Accessibility gates controlling it. They are
// independent: View Only needs only the first.
type PermissionStatus struct {
	ScreenRecording bool `json:"screenRecording"`
	Accessibility   bool `json:"accessibility"`
}

// PermissionKind names one of the two permissions.
type PermissionKind string

const (
	PermissionScreenRecording PermissionKind = "screen_recording"
	PermissionAccessibility   PermissionKind = "accessibility"
)

// Mode is what a session may do.
type Mode string

const (
	ModeViewOnly    Mode = "view"
	ModeFullControl Mode = "full"
)

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool { return m == ModeViewOnly || m == ModeFullControl }

// ConnectionStats is what the session measures, for the diagnostics panel and the phone's overlay.
// Latency figures are medians over the last few seconds where noted; counters are per session.
type ConnectionStats struct {
	RTTMillis       float64 `json:"rttMs,omitempty"`
	BitrateKbps     int     `json:"bitrateKbps"`
	FPS             float64 `json:"fps"`
	FramesSent      uint64  `json:"framesSent"`
	FramesDropped   uint64  `json:"framesDropped"`
	KeyframesSent   uint64  `json:"keyframesSent"`
	KeyframeRequest uint64  `json:"keyframeRequests"`
	PathType        string  `json:"pathType,omitempty"` // host | srflx | relay | prflx
	UptimeSeconds   int     `json:"uptimeSeconds"`
}
