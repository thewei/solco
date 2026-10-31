package remotehost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/thewei/solco/internal/herdr"
)

// TestRealHerdrObserveContract drives the whole Host against a real Herdr in a
// throw-away session: a phone-sized observe grid next to nothing else, input
// through pane.send_input, frames back. It is opt-in because it starts a Herdr
// server under the user's config dir (the session is stopped and deleted):
//
//	SOLCO_REMOTEHOST_CONTRACT=1 GOTOOLCHAIN=auto go test -run RealHerdr ./internal/remotehost/
func TestRealHerdrObserveContract(t *testing.T) {
	if os.Getenv("SOLCO_REMOTEHOST_CONTRACT") != "1" {
		t.Skip("set SOLCO_REMOTEHOST_CONTRACT=1 to run against a real herdr")
	}
	session, m, terminalID := startRealHerdr(t)

	srv, err := New(Options{Config: Config{Mode: ModeLoopback, Port: 7788}, Dir: t.TempDir(), Backend: NewManagerBackend(m)})
	if err != nil {
		t.Fatal(err)
	}
	_, token, _ := srv.Devices().Add("contract")
	ts := httptest.NewServer(srv.Handler())
	defer func() { srv.Close(); ts.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v3/terminals/" + terminalID + "/stream?instance=" + session + "&cols=51&rows=19"
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: []string{subprotocolBearer, token}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	var sawFull bool
	var screen bytes.Buffer
	send := func(text string, keys ...string) {
		raw, _ := json.Marshal(map[string]any{"t": "input", "text": text, "keys": keys})
		if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
			t.Fatal(err)
		}
	}
	sent := false
	for ctx.Err() == nil && !strings.Contains(screen.String(), "HELLO_FROM_2PHONE") {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v (screen so far: %q)", err, screen.String())
		}
		if typ == websocket.MessageText {
			var head struct {
				T    string `json:"t"`
				Full bool   `json:"full"`
				W, H int
			}
			_ = json.Unmarshal(data, &head)
			if head.T == "frame" && head.Full {
				if head.W != 51 || head.H != 19 {
					t.Fatalf("observe must render the phone's own grid, got %dx%d", head.W, head.H)
				}
				sawFull = true
			}
			continue
		}
		screen.Write(data)
		if sawFull && !sent {
			sent = true
			send("echo HELLO_FROM_$((1+1))PHONE", "enter")
			screen.Reset()
		}
	}
	if !sawFull {
		t.Fatal("the first frame must be a full screen")
	}
	if !strings.Contains(screen.String(), "HELLO_FROM_2PHONE") {
		t.Fatalf("typed input did not echo back through observe: %q", screen.String())
	}
}

// startRealHerdr starts a throw-away Herdr session with one workspace and returns
// the session name, a Manager bound to it and the first pane's terminal id. The
// session is stopped and deleted with the test.
func startRealHerdr(t *testing.T) (string, *herdr.Manager, string) {
	t.Helper()
	cli, err := exec.LookPath("herdr")
	if err != nil {
		t.Skip("herdr is not installed")
	}
	session := fmt.Sprintf("solco-rh-contract-%d", os.Getpid())
	env := append(os.Environ(), "HERDR_SESSION="+session)
	run := func(args ...string) []byte {
		cmd := exec.Command(cli, args...)
		cmd.Env = env
		out, _ := cmd.Output()
		return out
	}
	server := exec.Command(cli, "server")
	server.Env = env
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		run("server", "stop")
		_ = server.Wait()
		run("session", "delete", session)
	})
	m := herdr.NewManager()
	if err := m.EnsureRunning(session); err != nil {
		t.Fatalf("session did not start: %v", err)
	}
	run("workspace", "create", "--cwd", t.TempDir(), "--label", "contract")

	var terminalID string
	waitFor(t, func() bool {
		p, err := m.Projection(session)
		if err != nil || len(p.Panes) == 0 {
			return false
		}
		terminalID = p.Panes[0].TerminalID
		return terminalID != ""
	})
	return session, m, terminalID
}

// TestServeRealHerdrForClient keeps a real Host (real TLS, real Herdr session)
// up for an external client — the phone app's own test suite — and stops when
// that client creates <dir>/done (or after three minutes, or SOLCO_REMOTEHOST_SERVE_MINUTES). The pairing link and
// the throw-away session's name are written to <dir>/pair-url.txt and
// <dir>/instance.txt. Opt-in for the same reason as the test above:
//
//	SOLCO_REMOTEHOST_SERVE=/tmp/solco-e2e GOTOOLCHAIN=auto go test -run ServeRealHerdr ./internal/remotehost/
func TestServeRealHerdrForClient(t *testing.T) {
	dir := os.Getenv("SOLCO_REMOTEHOST_SERVE")
	if dir == "" {
		t.Skip("set SOLCO_REMOTEHOST_SERVE=<dir> to serve a real herdr to an external client")
	}
	session, m, _ := startRealHerdr(t)

	srv, err := New(Options{Config: Config{Mode: ModeLoopback, Port: 17788}, Dir: t.TempDir(), Backend: NewManagerBackend(m)})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	offer, err := srv.StartPairing()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(dir + "/done")
	if err := os.WriteFile(dir+"/instance.txt", []byte(session), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/pair-url.txt", []byte(offer.URL), 0o600); err != nil {
		t.Fatal(err)
	}
	hold := 3 * time.Minute
	if m, err := strconv.Atoi(os.Getenv("SOLCO_REMOTEHOST_SERVE_MINUTES")); err == nil && m > 0 {
		hold = time.Duration(m) * time.Minute
	}
	deadline := time.Now().Add(hold)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(dir + "/done"); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("client never finished")
}
