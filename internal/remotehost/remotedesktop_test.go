package remotehost

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

type fakeRemoteDesktop struct {
	mu      sync.Mutex
	on      bool
	err     error
	offers  []protocol.Offer
	devices []protocol.DeviceInfo
	dropped []string
}

func (f *fakeRemoteDesktop) Enabled() bool { return f.on }
func (f *fakeRemoteDesktop) Info(context.Context) protocol.Info {
	return protocol.Info{
		Protocol: protocol.Version, State: protocol.StateReady, Platform: "macos",
		Permissions:     protocol.PermissionStatus{ScreenRecording: true, Accessibility: true},
		RequireApproval: true, Tiers: protocol.Tiers, Capabilities: &protocol.InfoCapabilities{Windows: true},
	}
}
func (f *fakeRemoteDesktop) Open(_ context.Context, d protocol.DeviceInfo, o protocol.Offer) (protocol.Answer, error) {
	f.mu.Lock()
	f.offers = append(f.offers, o)
	f.devices = append(f.devices, d)
	f.mu.Unlock()
	if f.err != nil {
		return protocol.Answer{}, f.err
	}
	return protocol.Answer{SessionID: "rd_0123456789ab", SDP: "v=0 answer", State: protocol.SessionState{
		Mode: protocol.ModeFullControl, DisplayID: "1", Tier: protocol.TierStandard, Width: 1728, Height: 1080, FPS: 30, Hardware: true, Cursor: true,
		Displays: []protocol.DisplayInfo{{ID: "1", Name: "Built-in Retina Display", Width: 1440, Height: 900, PixelWidth: 2880, PixelHeight: 1800, Scale: 2, IsMain: true}},
	}}, nil
}
func (f *fakeRemoteDesktop) DisconnectDevice(id string) {
	f.mu.Lock()
	f.dropped = append(f.dropped, id)
	f.mu.Unlock()
}

const offerIdleJSON = `{"protocol":1,"sdp":"v=0 offer","mode":"view","idle":true,"tier":"standard"}`

const offerJSON = `{"protocol":1,"sdp":"v=0 offer","mode":"full","displayId":"1","tier":"standard"}`

func TestRemoteDesktopGoldenAndGuards(t *testing.T) {
	h := newHarness(t)
	rd := &fakeRemoteDesktop{}
	h.srv.opts.RemoteDesktop = rd

	// Off in Settings: no capability, no route.
	if got := bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)); containsStr(string(got), "remoteDesktop") {
		t.Fatalf("capability advertised while off: %s", got)
	}
	golden(t, "error-remote-desktop-disabled.json", bodyOf(t, h.get("/api/v3/remote-desktop", h.token, nil)))
	if got := h.launch("/api/v3/remote-desktop/session", h.token, offerJSON).StatusCode; got != http.StatusNotFound {
		t.Fatalf("session while off = %d", got)
	}

	rd.on = true
	golden(t, "bootstrap-remote-desktop.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	golden(t, "remote-desktop-info.json", bodyOf(t, h.get("/api/v3/remote-desktop", h.token, nil)))
	golden(t, "remote-desktop-offer.json", []byte(offerJSON))
	golden(t, "remote-desktop-offer-idle.json", []byte(offerIdleJSON))
	resp := h.launch("/api/v3/remote-desktop/session", h.token, offerJSON)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session = %d", resp.StatusCode)
	}
	golden(t, "remote-desktop-answer.json", bodyOf(t, h.launch("/api/v3/remote-desktop/session", h.token, offerJSON)))
	if len(rd.offers) != 2 || rd.offers[0].DisplayID != "1" || rd.devices[0].ID != h.devID || rd.devices[0].Name != "Test Phone" {
		t.Fatalf("offer/device not forwarded: %+v %+v", rd.offers, rd.devices)
	}

	for name, tc := range map[string]struct {
		code   protocol.ErrorCode
		status int
	}{
		"busy": {protocol.CodeBusy, 409}, "denied": {protocol.CodeDenied, 403}, "approval-timeout": {protocol.CodeApprovalTimeout, 408},
		"permission-required": {protocol.CodePermissionRequired, 409}, "bad-offer": {protocol.CodeBadOffer, 400},
		"protocol-unsupported": {protocol.CodeProtocolUnsupported, 400}, "unavailable": {protocol.CodeUnavailable, 503}, "helper-failed": {protocol.CodeHelperFailed, 502},
	} {
		rd.err = protocol.Errf(tc.code, "secret helper detail /Users/x")
		resp := h.launch("/api/v3/remote-desktop/session", h.token, offerJSON)
		if resp.StatusCode != tc.status {
			t.Errorf("%s: status %d, want %d", name, resp.StatusCode, tc.status)
		}
		body := bodyOf(t, resp)
		if containsStr(string(body), "secret") || containsStr(string(body), "/Users") {
			t.Errorf("%s: internal detail leaked: %s", name, body)
		}
		golden(t, "error-remote-desktop-"+name+".json", body)
	}
	rd.err = nil
	if got := h.launch("/api/v3/remote-desktop/session", h.token, "nope").StatusCode; got != http.StatusBadRequest {
		t.Fatalf("garbage body = %d", got)
	}
	if got := h.launch("/api/v3/remote-desktop/session", "", offerJSON).StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("no token = %d", got)
	}

	if h.launch("/api/v3/remote-desktop/session", h.token, offerIdleJSON).StatusCode != http.StatusOK {
		t.Fatal("idle offer refused")
	}
	if !rd.offers[len(rd.offers)-1].Idle {
		t.Fatalf("the idle flag did not reach the service: %+v", rd.offers)
	}

	// Revoke ends the desktop session too.
	if err := h.srv.Devices().Revoke(h.devID); err != nil {
		t.Fatal(err)
	}
	if len(rd.dropped) == 0 || rd.dropped[0] != h.devID {
		t.Fatalf("revoke did not disconnect the desktop session: %v", rd.dropped)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// The control channel's wire (data-channel JSON) is pinned beside the HTTP signalling: the phone's
// builders and Zod parsers are tested against these exact bytes.
func TestRemoteDesktopControlGolden(t *testing.T) {
	phoneToMac := map[string]string{
		"pointer-move":          `{"type":"pointer.move","seq":7,"x":0.5,"y":0.25,"displayId":"1"}`,
		"pointer-button":        `{"type":"pointer.button","seq":8,"x":0.5,"y":0.25,"button":"left","down":true,"clickCount":2,"displayId":"1"}`,
		"pointer-scroll":        `{"type":"pointer.scroll","seq":9,"dx":0,"dy":-120}`,
		"key":                   `{"type":"key","code":"KeyC","down":true}`,
		"text-insert":           `{"type":"text.insert","text":"你好, world"}`,
		"clipboard-set":         `{"type":"clipboard.set","text":"copied on the phone"}`,
		"clipboard-get":         `{"type":"clipboard.get"}`,
		"display-select":        `{"type":"display.select","displayId":"2"}`,
		"quality-set":           `{"type":"quality.set","tier":"high"}`,
		"cursor-set":            `{"type":"cursor.set","visible":false}`,
		"privacy-set":           `{"type":"privacy.set","on":true}`,
		"unlock-request":        `{"type":"unlock.request"}`,
		"keyframe":              `{"type":"keyframe.request"}`,
		"ping":                  `{"type":"ping","t":1700000000000}`,
		"session-end":           `{"type":"session.end"}`,
		"release-all":           `{"type":"input.release_all"}`,
		"stats-request":         `{"type":"stats.request"}`,
		"windows-list":          `{"type":"windows.list"}`,
		"apps-list":             `{"type":"apps.list"}`,
		"app-icon-get":          `{"type":"app.icon.get","bundleId":"com.apple.Notes"}`,
		"window-thumb-get":      `{"type":"window.thumb.get","windowId":"4210","maxWidth":320}`,
		"target-select-window":  `{"type":"target.select","kind":"window","id":"4210"}`,
		"target-select-display": `{"type":"target.select","kind":"display","id":"1"}`,
		"target-activate":       `{"type":"target.activate"}`,
		"app-open":              `{"type":"app.open","bundleId":"com.apple.Notes"}`,
		"window-fit":            `{"type":"window.fit","viewport":{"w":390,"h":844,"scale":3,"insets":{"top":47,"bottom":34,"left":0,"right":0}}}`,
		"window-fit-off":        `{"type":"window.fit.off"}`,
		"window-menu-get":       `{"type":"window.menu.get"}`,
		"window-menu-invoke":    `{"type":"window.menu.invoke","path":[2,5]}`,
		"window-zoom-in":        `{"type":"window.zoom","dir":"in"}`,
		"window-zoom-out":       `{"type":"window.zoom","dir":"out"}`,
	}
	for name, raw := range phoneToMac {
		if _, err := protocol.Decode([]byte(raw)); err != nil {
			t.Fatalf("%s: the Mac refuses its own golden: %v", name, err)
		}
		golden(t, "remote-desktop-ctl-"+name+".json", []byte(raw))
	}

	st := protocol.SessionState{
		Mode: protocol.ModeFullControl, DisplayID: "1", Tier: protocol.TierStandard, Width: 1728, Height: 1080, FPS: 30, Hardware: true, Cursor: true,
		Displays: []protocol.DisplayInfo{
			{ID: "1", Name: "Built-in Retina Display", X: 0, Y: 0, Width: 1440, Height: 900, PixelWidth: 2880, PixelHeight: 1800, Scale: 2, IsMain: true},
			{ID: "2", Name: "DELL U2723QE", X: 1440, Y: -200, Width: 1920, Height: 1080, PixelWidth: 3840, PixelHeight: 2160, Scale: 2},
		},
	}
	locked := st
	locked.Blackout, locked.Locked, locked.CanUnlock = true, true, true
	inWindow := st
	inWindow.Target = protocol.TargetWindow
	inWindow.Window = &protocol.WindowTarget{ID: "4210", App: "Notes", BundleID: "com.apple.Notes", Title: "Shopping list", OnScreen: true}
	idle := st
	idle.Target = protocol.TargetNone
	fitted := inWindow
	fitted.Width, fitted.Height, fitted.Fitted = 552, 1080, true
	clamped := fitted
	clamped.Clamped = &protocol.Clamped{MinW: 520, Scale: 0.75}
	zoomed := clamped
	zoomed.Zoom = 2
	macToPhone := map[string]any{
		"session-state-window":    protocol.SessionStateMsg{SessionState: inWindow},
		"session-state-idle":      protocol.SessionStateMsg{SessionState: idle},
		"session-state-fitted":    protocol.SessionStateMsg{SessionState: fitted},
		"session-state-clamped":   protocol.SessionStateMsg{SessionState: clamped},
		"session-state-zoomed":    protocol.SessionStateMsg{SessionState: zoomed},
		"window-restored":         protocol.WindowRestored{WindowID: "4210", Outcome: protocol.RestoreDone},
		"window-restored-skipped": protocol.WindowRestored{WindowID: "4210", Outcome: protocol.RestoreSkipped},
		"error-fit-unavailable":   protocol.ErrorMsg{Code: protocol.CodeFitUnavailable},
		"error-menu-unavailable":  protocol.ErrorMsg{Code: protocol.CodeMenuUnavailable, WindowID: "4210"},
		"error-menu-item":         protocol.ErrorMsg{Code: protocol.CodeMenuItemUnavailable, WindowID: "4210"},
		"window-menu": protocol.WindowMenu{WindowID: "4210", Items: []protocol.MenuItem{
			{Title: "File", Enabled: true, Children: []protocol.MenuItem{
				{Title: "New Note", Enabled: true, Shortcut: "⌘N"},
				{Separator: true},
				{Title: "Share", Enabled: true, Children: []protocol.MenuItem{{Title: "Mail", Enabled: false}}},
			}},
			{Title: "View", Enabled: true, Children: []protocol.MenuItem{{Title: "Show Toolbar", Enabled: true, Checked: true, Shortcut: "⌥⌘T"}}},
		}},
		"windows-snapshot": protocol.WindowsSnapshot{Windows: []protocol.WindowInfo{
			{ID: "4210", App: "Notes", BundleID: "com.apple.Notes", Title: "Shopping list", X: 100, Y: 80, Width: 500, Height: 800, OnScreen: true},
			{ID: "4211", App: "Safari", BundleID: "com.apple.Safari", X: 0, Y: 0, Width: 1200, Height: 800, Minimized: true},
		}},
		"apps-snapshot": protocol.AppsSnapshot{Apps: []protocol.AppInfo{
			{BundleID: "com.apple.Notes", Name: "Notes", Running: true}, {BundleID: "com.apple.Maps", Name: "Maps"},
		}},
		"app-icon":               protocol.AppIcon{BundleID: "com.apple.Notes", PNG: "iVBORw0KGgo="},
		"window-thumb":           protocol.WindowThumb{WindowID: "4210", JPEG: "/9j/4AAQ"},
		"app-opening":            protocol.BundleRef{BundleID: "com.apple.Maps"},
		"app-open-failed":        protocol.AppOpenFailed{BundleID: "com.apple.Maps", Reason: "no_window"},
		"window-gone":            protocol.WindowGone{WindowID: "4210", Reason: protocol.GoneAppQuit},
		"error-window-offscreen": protocol.ErrorMsg{Code: protocol.CodeWindowOffscreen},
		"session-state":          protocol.SessionStateMsg{SessionState: st},
		"session-state-locked":   protocol.SessionStateMsg{SessionState: locked},
		"error-unlock-failed":    protocol.ErrorMsg{Code: protocol.CodeUnlockFailed},
		"clipboard-text":         protocol.ClipboardText{Text: "copied on the Mac"},
		"error":                  protocol.ErrorMsg{Code: protocol.CodeViewOnly},
		"error-message":          protocol.ErrorMsg{Code: protocol.CodePermissionRequired, Message: "accessibility"},
		"pong":                   protocol.Pong{T: 1700000000000},
		"stats": protocol.StatsMsg{ConnectionStats: protocol.ConnectionStats{
			RTTMillis: 12.5, BitrateKbps: 4200, FPS: 29.7, FramesSent: 1800, FramesDropped: 2, KeyframesSent: 15, KeyframeRequest: 1, PathType: "host", UptimeSeconds: 60,
		}},
	}
	for name, msg := range macToPhone {
		raw, err := protocol.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		golden(t, "remote-desktop-ctl-"+name+".json", raw)
	}
}
