package protocol

import (
	"encoding/json"
	"fmt"
)

// Control-channel messages: JSON text frames on the "control" (reliable, ordered) and "pointer"
// (unordered, no retransmits) data channels. Pointer coordinates are normalised (0…1) against the
// *video content*, so letterboxing, zoom, pan, rotation and safe areas on the phone never reach the
// Mac; the Mac maps them onto the current display (package mapper).
//
// Ordering: every pointer event carries Seq, monotonic per session across both channels. A pointer
// move that arrives after a newer button event (the unreliable channel overtakes nothing, but the
// reliable one can) is stale and dropped, so a drag's button state and position never disagree.

// Message types, phone → Mac.
const (
	TypePointerMove   = "pointer.move"
	TypePointerButton = "pointer.button"
	TypePointerScroll = "pointer.scroll"
	TypeKey           = "key"
	TypeTextInsert    = "text.insert"
	TypeClipboardSet  = "clipboard.set"
	TypeClipboardGet  = "clipboard.get"
	TypeDisplaySelect = "display.select"
	TypeQualitySet    = "quality.set"
	TypeCursorSet     = "cursor.set"
	TypePrivacySet    = "privacy.set"
	TypeUnlockRequest = "unlock.request"
	TypeKeyframe      = "keyframe.request"
	TypePing          = "ping"
	TypeSessionEnd    = "session.end"
	TypeReleaseAll    = "input.release_all"
	TypeStatsRequest  = "stats.request"
	TypeWindowsList   = "windows.list"
	TypeAppsList      = "apps.list"
	TypeAppIconGet    = "app.icon.get"
	TypeThumbGet      = "window.thumb.get"
	TypeTargetSelect  = "target.select"
	TypeTargetActive  = "target.activate"
	TypeWindowFit     = "window.fit"
	TypeWindowFitOff  = "window.fit.off"
	TypeAppOpen       = "app.open"
	TypeMenuGet       = "window.menu.get"
	TypeMenuInvoke    = "window.menu.invoke"

	// Mac → phone.
	TypeSessionState  = "session.state"
	TypeClipboardText = "clipboard.text"
	TypeError         = "error"
	TypePong          = "pong"
	TypeStats         = "stats"
	TypeWindows       = "windows.snapshot"
	TypeApps          = "apps.snapshot"
	TypeAppIcon       = "app.icon"
	TypeThumb         = "window.thumb"
	TypeAppOpening    = "app.opening"
	TypeAppOpenFailed = "app.open_failed"
	TypeWindowGone    = "window.gone"
	TypeWindowRestore = "window.restored"
	TypeMenu          = "window.menu"
)

// Button names for TypePointerButton.
const (
	ButtonLeft   = "left"
	ButtonRight  = "right"
	ButtonMiddle = "middle"
)

// Limits that keep a misbehaving or hostile peer bounded.
const (
	MaxMessageBytes    = 64 << 10 // one data-channel message
	MaxTextInsertRunes = 4096
	MaxClipboardBytes  = 256 << 10 // text only in v1
	MaxScrollDelta     = 4000      // points per message
)

// Envelope is the first decoding pass: which message is it?
type Envelope struct {
	Type string `json:"type"`
}

// PointerMove / PointerButton / PointerScroll. X, Y are normalised to the video content.
type PointerMove struct {
	Type      string  `json:"type"`
	Seq       uint64  `json:"seq"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	DisplayID string  `json:"displayId,omitempty"`
}

type PointerButton struct {
	Type       string  `json:"type"`
	Seq        uint64  `json:"seq"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Button     string  `json:"button"`
	Down       bool    `json:"down"`
	ClickCount int     `json:"clickCount,omitempty"`
	DisplayID  string  `json:"displayId,omitempty"`
}

// PointerScroll deltas are in points; positive Dy scrolls the view down.
type PointerScroll struct {
	Type string  `json:"type"`
	Seq  uint64  `json:"seq"`
	DX   float64 `json:"dx"`
	DY   float64 `json:"dy"`
}

// KeyEvent is one physical key transition. Code uses W3C KeyboardEvent.code names ("KeyA", "Digit1",
// "Enter", "MetaLeft", "ArrowUp", "F5" …); shortcuts are modifier downs, the key, then ups — never a
// pre-joined string.
type KeyEvent struct {
	Type string `json:"type"`
	Code string `json:"code"`
	Down bool   `json:"down"`
}

// TextInsert types literal Unicode text (committed IME / dictation output) without needing a
// physical key per character.
type TextInsert struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ClipboardSet struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type DisplaySelect struct {
	Type      string `json:"type"`
	DisplayID string `json:"displayId"`
}

// BundleRef names an application (app.open, app.icon.get, app.opening).
type BundleRef struct {
	Type     string `json:"type"`
	BundleID string `json:"bundleId"`
}

// ThumbGet asks for one still picture of a window.
type ThumbGet struct {
	Type     string `json:"type"`
	WindowID string `json:"windowId"`
	MaxWidth int    `json:"maxWidth,omitempty"`
}

// TargetSelect switches what the video shows: a display, or one window (ID is then a window id).
type TargetSelect struct {
	Type string `json:"type"`
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type QualitySet struct {
	Type string `json:"type"`
	Tier Tier   `json:"tier"`
}

// CursorSet shows or hides the Mac's pointer in the video (the phone draws its own in trackpad mode).
type CursorSet struct {
	Type    string `json:"type"`
	Visible bool   `json:"visible"`
}

// PrivacySet blacks the Mac's own screens out (On) or brings them back; the video the phone sees is unchanged.
type PrivacySet struct {
	Type string `json:"type"`
	On   bool   `json:"on"`
}

type Ping struct {
	Type string `json:"type"`
	T    int64  `json:"t"`
}

// Mac → phone.

type SessionStateMsg struct {
	Type string `json:"type"`
	SessionState
}

type ClipboardText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ErrorMsg struct {
	Type    string    `json:"type"`
	Code    ErrorCode `json:"code"`
	Message string    `json:"message,omitempty"`
}

type Pong struct {
	Type string `json:"type"`
	T    int64  `json:"t"`
}

type StatsMsg struct {
	Type string `json:"type"`
	ConnectionStats
}

// WindowsSnapshot answers windows.list.
type WindowsSnapshot struct {
	Type    string       `json:"type"`
	Windows []WindowInfo `json:"windows"`
}

// AppsSnapshot answers apps.list.
type AppsSnapshot struct {
	Type string    `json:"type"`
	Apps []AppInfo `json:"apps"`
}

// AppIcon answers app.icon.get: a 64 px PNG, base64.
type AppIcon struct {
	Type     string `json:"type"`
	BundleID string `json:"bundleId"`
	PNG      string `json:"png"`
}

// WindowThumb answers window.thumb.get: a small JPEG, base64.
type WindowThumb struct {
	Type     string `json:"type"`
	WindowID string `json:"windowId"`
	JPEG     string `json:"jpeg"`
}

// AppOpenFailed says an app.open did not end in a window. Reason is a stable word, never app content.
type AppOpenFailed struct {
	Type     string `json:"type"`
	BundleID string `json:"bundleId"`
	Reason   string `json:"reason"`
}

// WindowGone says the window the video showed is no longer there; the session lives on with nothing captured.
type WindowGone struct {
	Type     string           `json:"type"`
	WindowID string           `json:"windowId"`
	Reason   WindowGoneReason `json:"reason"`
}

// Marshal encodes a Mac → phone message, filling its Type.
func Marshal(msg any) ([]byte, error) {
	switch m := msg.(type) {
	case SessionStateMsg:
		m.Type = TypeSessionState
		return json.Marshal(m)
	case ClipboardText:
		m.Type = TypeClipboardText
		return json.Marshal(m)
	case ErrorMsg:
		m.Type = TypeError
		return json.Marshal(m)
	case Pong:
		m.Type = TypePong
		return json.Marshal(m)
	case WindowsSnapshot:
		m.Type = TypeWindows
		if m.Windows == nil {
			m.Windows = []WindowInfo{}
		}
		return json.Marshal(m)
	case AppsSnapshot:
		m.Type = TypeApps
		if m.Apps == nil {
			m.Apps = []AppInfo{}
		}
		return json.Marshal(m)
	case AppIcon:
		m.Type = TypeAppIcon
		return json.Marshal(m)
	case WindowThumb:
		m.Type = TypeThumb
		return json.Marshal(m)
	case BundleRef:
		m.Type = TypeAppOpening
		return json.Marshal(m)
	case AppOpenFailed:
		m.Type = TypeAppOpenFailed
		return json.Marshal(m)
	case WindowGone:
		m.Type = TypeWindowGone
		return json.Marshal(m)
	case WindowRestored:
		m.Type = TypeWindowRestore
		return json.Marshal(m)
	case WindowMenu:
		m.Type = TypeMenu
		if m.Items == nil {
			m.Items = []MenuItem{}
		}
		return json.Marshal(m)
	case StatsMsg:
		m.Type = TypeStats
		return json.Marshal(m)
	}
	return nil, fmt.Errorf("protocol: cannot marshal %T", msg)
}

// Decode parses one phone → Mac message into its typed struct. Unknown types and malformed bodies are
// errors (the session answers with an error message rather than guessing).
func Decode(data []byte) (any, error) {
	if len(data) > MaxMessageBytes {
		return nil, Errf(CodeBadRequest, "message too large")
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, Errf(CodeBadRequest, "not json")
	}
	var target any
	switch env.Type {
	case TypePointerMove:
		target = &PointerMove{}
	case TypePointerButton:
		target = &PointerButton{}
	case TypePointerScroll:
		target = &PointerScroll{}
	case TypeKey:
		target = &KeyEvent{}
	case TypeTextInsert:
		target = &TextInsert{}
	case TypeClipboardSet:
		target = &ClipboardSet{}
	case TypeDisplaySelect:
		target = &DisplaySelect{}
	case TypeQualitySet:
		target = &QualitySet{}
	case TypeCursorSet:
		target = &CursorSet{}
	case TypePrivacySet:
		target = &PrivacySet{}
	case TypePing:
		target = &Ping{}
	case TypeAppOpen, TypeAppIconGet:
		target = &BundleRef{}
	case TypeThumbGet:
		target = &ThumbGet{}
	case TypeTargetSelect:
		target = &TargetSelect{}
	case TypeWindowFit:
		target = &WindowFit{}
	case TypeMenuInvoke:
		var inv MenuInvoke
		if err := json.Unmarshal(data, &inv); err != nil || !inv.Valid() {
			return nil, Errf(CodeBadRequest, "malformed %s", env.Type)
		}
		return &inv, nil
	case TypeClipboardGet, TypeKeyframe, TypeSessionEnd, TypeReleaseAll, TypeStatsRequest, TypeUnlockRequest, TypeWindowsList, TypeAppsList, TypeTargetActive, TypeWindowFitOff, TypeMenuGet:
		return &env, nil
	default:
		return nil, Errf(CodeBadRequest, "unknown message type")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return nil, Errf(CodeBadRequest, "malformed %s", env.Type)
	}
	return target, nil
}
