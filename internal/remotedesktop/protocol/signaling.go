package protocol

// Host HTTP signalling bodies (GET/POST /api/v3/remote-desktop…). The phone reaches them over whatever
// route already works (LAN, Tailscale, relay) with its device token; media then flows peer to peer.

// State is the Mac-side readiness a phone sees before it connects.
type State string

const (
	StateReady              State = "ready"               // can accept a session
	StateDisabled           State = "disabled"            // switched off in Settings
	StatePermissionRequired State = "permission_required" // Screen Recording missing
	StateBusy               State = "busy"                // a session is active
	StateUnavailable        State = "unavailable"         // no backend / helper missing
)

// Info answers GET /remote-desktop.
type Info struct {
	Protocol        int              `json:"protocol"`
	State           State            `json:"state"`
	Platform        string           `json:"platform,omitempty"`
	Permissions     PermissionStatus `json:"permissions"`
	RequireApproval bool             `json:"requireApproval"`
	Tiers           []Tier           `json:"tiers"`
}

// Offer is POST /remote-desktop/session. The phone sends a complete (non-trickle) SDP offer with a
// receive-only video transceiver and the two data channels it expects ("control" reliable/ordered,
// "pointer" unordered/maxRetransmits 0).
type Offer struct {
	Protocol  int    `json:"protocol"`
	SDP       string `json:"sdp"`
	Mode      Mode   `json:"mode,omitempty"`      // requested; the Mac user may downgrade
	DisplayID string `json:"displayId,omitempty"` // default: the main display
	Tier      Tier   `json:"tier,omitempty"`      // default: standard
}

// Answer is the reply to an accepted Offer.
type Answer struct {
	SessionID string       `json:"sessionId"`
	SDP       string       `json:"sdp"`
	State     SessionState `json:"state"`
}

// SessionState is what the Mac tells the phone about the running session, in the answer and again on
// the control channel whenever it changes (display switch, quality change, mode change).
type SessionState struct {
	Mode      Mode          `json:"mode"`
	DisplayID string        `json:"displayId"`
	Displays  []DisplayInfo `json:"displays"`
	Tier      Tier          `json:"tier"`
	Width     int           `json:"width"` // encoded video size
	Height    int           `json:"height"`
	FPS       int           `json:"fps"`
	Hardware  bool          `json:"hardware"`
	Cursor    bool          `json:"cursor"` // the Mac's pointer is drawn in the video
}

// DeviceInfo identifies the phone to the authorizer and the session list. ID is the paired device id.
type DeviceInfo struct {
	ID   string
	Name string
}
