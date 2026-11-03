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
	OpShutdown           = "shutdown"
)

// Event names (helper → Go).
const (
	EventDisplaysChanged = "displays.changed"
	EventCaptureStopped  = "capture.stopped"
	EventError           = "error"
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
