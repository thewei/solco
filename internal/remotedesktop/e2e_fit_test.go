package remotedesktop

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/thewei/solco/internal/remotedesktop/platform/macos"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// The P2 exit criteria on a real window: fit it to a phone, then leave every way the plan names and see the
// window's own frame come back (or, for a lost link, after the grace). Needs a window of the app
// named by SOLCO_RD_E2E_APP (default Notes) that can be resized, and this Mac's Screen Recording and
// Accessibility grants for the invoking process:
//
//	SOLCO_RD_E2E=1 go test ./internal/remotedesktop -run TestRealWindowFit -v
//
// The lost-link case waits the real grace (20 s) and only runs with SOLCO_RD_E2E_SLOW=1.
type fitHarness struct {
	t       *testing.T
	be      *macos.Backend
	helper  string
	svc     *Service
	pc      *webrtc.PeerConnection
	control *webrtc.DataChannel
	msgs    chan string
	id      string // the session
	window  protocol.WindowInfo
	orig    protocol.Rect
}

func helperPath(t *testing.T) string {
	t.Helper()
	exe, _ := os.Executable()
	cwd, _ := os.Getwd()
	loc, ok := macos.Locate(exe, cwd, os.Getenv)
	if !ok {
		t.Skip("no helper located")
	}
	return loc.Path
}

// frames lists the visible windows' frames through the helper's own CLI (independent of the session under test).
func (h *fitHarness) frame(id string) (protocol.Rect, bool) {
	out, err := exec.Command(h.helper, "windows").Output()
	if err != nil {
		h.t.Fatal(err)
	}
	var res struct {
		Windows []protocol.WindowInfo `json:"windows"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		h.t.Fatal(err)
	}
	for _, w := range res.Windows {
		if w.ID == id {
			return protocol.Rect{X: w.X, Y: w.Y, Width: w.Width, Height: w.Height}, true
		}
	}
	return protocol.Rect{}, false
}

func sameFrame(a, b protocol.Rect) bool {
	return math.Abs(a.X-b.X) < 3 && math.Abs(a.Y-b.Y) < 3 && math.Abs(a.Width-b.Width) < 3 && math.Abs(a.Height-b.Height) < 3
}

func (h *fitHarness) next(typ string, d time.Duration) []byte {
	h.t.Helper()
	deadline := time.After(d)
	for {
		select {
		case m := <-h.msgs:
			if strings.Contains(m, `"type":"`+typ+`"`) {
				return []byte(m)
			}
		case <-deadline:
			h.t.Fatalf("no %s message", typ)
		}
	}
}

func (h *fitHarness) nextState(match func(protocol.SessionStateMsg) bool) protocol.SessionStateMsg {
	h.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var st protocol.SessionStateMsg
		if err := json.Unmarshal(h.next("session.state", 20*time.Second), &st); err != nil {
			h.t.Fatal(err)
		}
		if match(st) {
			return st
		}
	}
	h.t.Fatal("no matching session.state")
	return protocol.SessionStateMsg{}
}

// openFitted starts a full-control idle session, shows the app's window and fits it to a 390×844 phone.
func openFitted(t *testing.T) *fitHarness {
	t.Helper()
	if os.Getenv("SOLCO_RD_E2E") != "1" {
		t.Skip("set SOLCO_RD_E2E=1 to run against the real helper")
	}
	h := &fitHarness{t: t, be: macos.New(macos.Options{}), helper: helperPath(t)}
	if ok, why := h.be.Available(); !ok {
		t.Fatalf("helper unavailable: %s", why)
	}
	h.svc = NewService(Config{Backend: h.be, InterfaceFilter: func(string) bool { return true }})
	h.svc.SetSettings(Settings{Enabled: true, RequireApproval: false, DefaultTier: protocol.TierSmooth})
	t.Cleanup(h.svc.Close)

	pc, control, msgs, _, sdp := newOffer(t)
	h.pc, h.control, h.msgs = pc, control, msgs
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ans, err := h.svc.Open(ctx, protocol.DeviceInfo{ID: "e2e", Name: "Pion phone"}, protocol.Offer{Protocol: protocol.Version, SDP: sdp, Mode: protocol.ModeFullControl, Idle: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: ans.SDP}); err != nil {
		t.Fatal(err)
	}
	h.id = ans.SessionID
	waitUntil(t, func() bool { return control.ReadyState() == webrtc.DataChannelStateOpen })

	app := os.Getenv("SOLCO_RD_E2E_APP")
	if app == "" {
		app = "Notes"
	}
	_ = control.SendText(`{"type":"windows.list"}`)
	var snap protocol.WindowsSnapshot
	if err := json.Unmarshal(h.next("windows.snapshot", 20*time.Second), &snap); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range snap.Windows {
		if w.App == app && w.OnScreen {
			h.window, found = w, true
			break
		}
	}
	if !found {
		t.Skipf("no on-screen %s window", app)
	}
	h.orig = protocol.Rect{X: h.window.X, Y: h.window.Y, Width: h.window.Width, Height: h.window.Height}
	_ = control.SendText(`{"type":"target.select","kind":"window","id":"` + h.window.ID + `"}`)
	h.nextState(func(st protocol.SessionStateMsg) bool { return st.Target == protocol.TargetWindow })
	_ = control.SendText(`{"type":"window.fit","viewport":{"w":390,"h":844,"scale":3,"insets":{"top":47,"bottom":34,"left":0,"right":0}}}`)
	st := h.nextState(func(st protocol.SessionStateMsg) bool { return st.Fitted })
	got, ok := h.frame(h.window.ID)
	if !ok {
		t.Fatal("the fitted window vanished")
	}
	t.Logf("%s window %.0f×%.0f → fitted %.0f×%.0f (clamped=%v), video %d×%d", app, h.orig.Width, h.orig.Height, got.Width, got.Height, st.Clamped != nil, st.Width, st.Height)
	if st.Clamped == nil && (math.Abs(got.Width-390) > 3 || math.Abs(got.Height-763) > 3) {
		t.Fatalf("window is %.0f×%.0f, want 390×763", got.Width, got.Height)
	}
	if sameFrame(got, h.orig) {
		t.Fatal("the window did not change size")
	}
	return h
}

func (h *fitHarness) wantRestored(within time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(within)
	var got protocol.Rect
	for time.Now().Before(deadline) {
		if f, ok := h.frame(h.window.ID); ok {
			got = f
			if sameFrame(f, h.orig) {
				return
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	h.t.Fatalf("window is %+v, want its original %+v", got, h.orig)
}

func TestRealWindowFitBackAndSessionEnd(t *testing.T) {
	h := openFitted(t)
	_ = h.control.SendText(`{"type":"session.end"}`)
	h.wantRestored(10 * time.Second)
}

func TestRealWindowFitSwitchToDisplay(t *testing.T) {
	h := openFitted(t)
	_ = h.control.SendText(`{"type":"target.select","kind":"display","id":"1"}`)
	h.wantRestored(10 * time.Second)
	var rest protocol.WindowRestored
	if err := json.Unmarshal(h.next("window.restored", 10*time.Second), &rest); err != nil || rest.Outcome != protocol.RestoreDone {
		t.Fatalf("restored report = %+v %v", rest, err)
	}
}

func TestRealWindowFitMacOwnerDisconnect(t *testing.T) {
	h := openFitted(t)
	h.svc.Disconnect(h.id)
	h.wantRestored(10 * time.Second)
}

func TestRealWindowFitRestoreButtonKeepsTheSession(t *testing.T) {
	h := openFitted(t)
	h.svc.RestoreWindow(h.id)
	h.wantRestored(10 * time.Second)
	st := h.nextState(func(st protocol.SessionStateMsg) bool { return !st.Fitted && st.Target == protocol.TargetWindow })
	if st.Window == nil || st.Window.ID != h.window.ID {
		t.Fatalf("the session lost its window: %+v", st)
	}
	if len(h.svc.Sessions()) != 1 {
		t.Fatal("Restore Window ended the session")
	}
}

func TestRealWindowFitHelperKilled(t *testing.T) {
	h := openFitted(t)
	if err := exec.Command("pkill", "-9", "-f", "solco-remote-helper serve").Run(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	h.wantRestored(20 * time.Second) // the journal one-shot gives it back
}

func TestRealWindowFitMovedByTheUserIsLeftAlone(t *testing.T) {
	h := openFitted(t)
	script := `tell application "System Events" to tell (first process whose name is "` + h.window.App + `") to set position of window 1 to {` +
		"60, 120}"
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		t.Skipf("cannot move the window: %v %s", err, out)
	}
	_ = h.control.SendText(`{"type":"window.fit.off"}`)
	var rest protocol.WindowRestored
	if err := json.Unmarshal(h.next("window.restored", 10*time.Second), &rest); err != nil || rest.Outcome != protocol.RestoreSkipped {
		t.Fatalf("restored report = %+v %v (want skipped)", rest, err)
	}
	if f, _ := h.frame(h.window.ID); sameFrame(f, h.orig) {
		t.Fatal("a window the user moved was put back over their move")
	}
	// put the user's window back where it was, for the next run
	_ = exec.Command("osascript", "-e", `tell application "System Events" to tell (first process whose name is "`+h.window.App+`") to set position of window 1 to {`+
		ftoa(h.orig.X)+", "+ftoa(h.orig.Y)+"}").Run()
}

func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', 0, 64) }

// A phone that vanishes keeps the window fitted for the grace, then it goes back.
func TestRealWindowFitLinkLostGrace(t *testing.T) {
	if os.Getenv("SOLCO_RD_E2E_SLOW") != "1" {
		t.Skip("set SOLCO_RD_E2E_SLOW=1 (waits the real grace)")
	}
	h := openFitted(t)
	lostAt := time.Now()
	_ = h.pc.Close()
	time.Sleep(8 * time.Second)
	if f, _ := h.frame(h.window.ID); sameFrame(f, h.orig) {
		t.Fatal("the window went back inside the grace")
	}
	h.wantRestored(40 * time.Second)
	t.Logf("restored %s after the link was lost", time.Since(lostAt).Round(time.Second))
}
