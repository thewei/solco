module github.com/thewei/solco

go 1.27.3

tool github.com/egoist/mygo/cmd/mygo

// LOCAL FRAMEWORK PATCH (F146/F147 + WIX-021 + OSC 7501, user-approved):
// the vendored fork under third_party/mygo is v0.6.4 plus four local deltas
// — terminal Options.LocalDragSelect (scrollback panes select text and get
// a right-click menu while the wheel keeps reporting), field focus
// delegation (AutoFocus on a SearchField/Combobox reaches its inner
// input), multi-display tray flyout anchoring on macOS, and the OSC 7501
// program-status scanner (Options.OnProgramStatus). Remove this replace,
// bump the pin, and delete third_party/mygo once upstream ships all four
// — Solco code needs no changes. See
// third_party/mygo/LOCAL-PATCH.md.
replace github.com/egoist/mygo => ./third_party/mygo

require (
	github.com/coder/websocket v1.8.15
	github.com/egoist/mygo v0.6.4
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
)

require (
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/pion/datachannel v1.6.3 // indirect
	github.com/pion/dtls/v3 v3.1.15 // indirect
	github.com/pion/ice/v4 v4.4.8 // indirect
	github.com/pion/interceptor v0.1.51 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/mdns/v2 v2.2.2 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/rtcp v1.2.20 // indirect
	github.com/pion/rtp v1.10.6 // indirect
	github.com/pion/sctp v1.12.0 // indirect
	github.com/pion/sdp/v3 v3.0.20 // indirect
	github.com/pion/srtp/v3 v3.1.3 // indirect
	github.com/pion/stun/v4 v4.0.1 // indirect
	github.com/pion/transport/v5 v5.1.4 // indirect
	github.com/pion/turn/v5 v5.1.3 // indirect
	github.com/pion/webrtc/v4 v4.2.26 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/image v0.46.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	golang.org/x/time v0.14.0 // indirect
)
