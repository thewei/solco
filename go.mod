module github.com/thewei/solco

go 1.27.3

tool github.com/egoist/mygo/cmd/mygo

// LOCAL FRAMEWORK PATCH (F146/F147 + OSC 7501 + tray, user-approved):
// the vendored fork under third_party/mygo is v0.8.1 plus five local
// deltas — terminal Options.LocalDragSelect (scrollback panes select
// text and get a right-click menu while the wheel keeps reporting),
// multi-display tray flyout anchoring and the tray popover following the
// anchor's space on macOS, the OSC 7501 program-status scanner
// (Options.OnProgramStatus / OnProgramStatusControl), and the page's
// bridge posting through the scheme handler instead of
// WKScriptMessageHandler (JavaScriptCore heap-collector crash). Remove
// this replace, bump the pin, and delete third_party/mygo once upstream
// ships all five — Solco code needs no changes. See
// third_party/mygo/LOCAL-PATCH.md.
replace github.com/egoist/mygo => ./third_party/mygo

// SHARED GIT / FILES DOMAIN (2026-11-07 Solco Git decision): gitworkbench and
// filesview live in the sibling repository solco-gitkit, required at a tagged
// version so CI needs no relative replace. On a machine with the repositories
// side by side, an uncommitted go.work (go work init . && go work edit
// -replace github.com/thewei/solco-gitkit=../solco-gitkit) makes the local copy
// win. solco-gitkit is public and tagged v0.2.0 (go.sum carries it); verify a
// clean resolve with GOWORK=off go build ./...

require (
	github.com/coder/websocket v1.8.15
	github.com/egoist/mygo v0.8.1
	github.com/go-tex/math v0.50.0
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/pion/webrtc/v4 v4.2.26
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	github.com/tailscale/tailcat v0.7.1-0.20261105151022-e00cdde6ff78
	github.com/thewei/solco-gitkit v0.2.0
	golang.org/x/net v0.60.0
	tailscale.com v1.105.0-pre.0.20261013225543-fb79793d99f5
	tuios.dev/sketch v0.4.0
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/akutz/memconn v0.1.0 // indirect
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/creachadair/msync v0.10.1 // indirect
	github.com/creack/pty v1.1.24 // indirect
	github.com/dblohm7/wingoes v0.0.0-20260526185140-fb298caac7ca // indirect
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/fxamacker/cbor/v2 v2.9.3 // indirect
	github.com/gaissmai/bart v0.29.0 // indirect
	github.com/go-json-experiment/json v0.0.0-20260820222146-c27c302e5fc3 // indirect
	github.com/go-opentype/opentype v0.13.0 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/google/btree v1.1.3 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hdevalence/ed25519consensus v0.2.0 // indirect
	github.com/jsimonetti/rtnetlink v1.4.2 // indirect
	github.com/klauspost/compress v1.20.0 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/mdlayher/netlink v1.11.2 // indirect
	github.com/mdlayher/socket v0.7.0 // indirect
	github.com/mitchellh/go-ps v1.0.0 // indirect
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
	github.com/pires/go-proxyproto v0.15.0 // indirect
	github.com/pkg/sftp v1.13.11 // indirect
	github.com/safchain/ethtool v0.7.0 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/tailscale/certstore v0.1.1-0.20260409135935-3638fb84b77d // indirect
	github.com/tailscale/gliderssh v0.3.4-0.20260716005906-1a0f895faf28 // indirect
	github.com/tailscale/go-winio v0.0.0-20231025203758-c4f33415bf55 // indirect
	github.com/tailscale/hujson v0.0.0-20260727124030-b80ff77dac4f // indirect
	github.com/tailscale/peercred v0.0.0-20250107143737-35a0c7bd7edc // indirect
	github.com/tailscale/web-client-prebuilt v0.0.0-20260917222731-e0ed2d0d0fea // indirect
	github.com/tailscale/wireguard-go v0.0.0-20260928213353-e61b6b78110b // indirect
	github.com/u-root/u-root v0.16.0 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go4.org/mem v0.0.0-20240501181205-ae6ca9944745 // indirect
	go4.org/netipx v0.0.0-20260823151212-3075585bcbeb // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/exp v0.0.0-20260908205506-85c1c2202aba // indirect
	golang.org/x/image v0.46.0 // indirect
	golang.org/x/oauth2 v0.37.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/term v0.46.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	golang.org/x/time v0.16.0 // indirect
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2 // indirect
	golang.zx2c4.com/wireguard/windows v1.0.1 // indirect
	gvisor.dev/gvisor v0.0.0-20260915211658-a6f909f08a72 // indirect
)
