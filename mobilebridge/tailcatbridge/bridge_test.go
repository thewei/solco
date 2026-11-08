package tailcatbridge

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

func TestNewClientRejectsBadInput(t *testing.T) {
	good := GenerateNodeKey()
	pin := strings.Repeat("ab", 32)
	for name, c := range map[string][3]string{
		"short pin":    {"tcabcdefghijklmnopqrstuvwxyz", good, "abcd"},
		"bad key":      {"tcabcdefghijklmnopqrstuvwxyz", "nope", pin},
		"bad address":  {"http://example.com", good, pin},
		"empty adress": {"", good, pin},
	} {
		if _, err := NewClient(c[0], c[1], c[2]); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestTunnelURLOnlyTakesAbsolutePaths(t *testing.T) {
	for _, bad := range []string{"", "api", "//evil.example/x", "http://evil.example/"} {
		if _, err := tunnelURL("https", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	got, err := tunnelURL("wss", "/api/v3/events?x=1")
	if err != nil || got != "wss://solco.tailcat/api/v3/events?x=1" {
		t.Fatalf("got %q %v", got, err)
	}
}

type recorder struct {
	mu     sync.Mutex
	events []string
	text   chan string
	closed chan struct{}
}

func newRecorder() *recorder {
	return &recorder{text: make(chan string, 8), closed: make(chan struct{})}
}
func (r *recorder) OnOpen() { r.add("open") }
func (r *recorder) OnText(s string) {
	r.add("text")
	r.text <- s
}
func (r *recorder) OnBinary([]byte) { r.add("binary") }
func (r *recorder) OnClose(int, string) {
	r.add("close")
	close(r.closed)
}
func (r *recorder) OnError(string, bool, int) { r.add("error") }
func (r *recorder) add(e string) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

// TestFacadeOverRealTunnel proves the facade end to end over the public DERP network: pinned HTTPS, wrong pin
// before any request byte, pinned WebSocket echo. Needs the internet: SOLCO_TAILCAT_NET=1.
func TestFacadeOverRealTunnel(t *testing.T) {
	if os.Getenv("SOLCO_TAILCAT_NET") != "1" {
		t.Skip("set SOLCO_TAILCAT_NET=1 to run against the public DERP network")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	clientKeyText := GenerateNodeKey()
	clientPub, _ := PublicKeyOf(clientKeyText)
	var served int32
	var smu sync.Mutex
	srv := &tailcat.Server{Logf: func(string, ...any) {}, AllowClient: func(k key.NodePublic) bool {
		text, _ := k.MarshalText()
		return string(text) == clientPub
	}}
	ln, err := srv.Listen(ctx, "tcp", ":7788")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		smu.Lock()
		served++
		smu.Unlock()
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Seen-Auth", r.Header.Get("Authorization"))
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"solco.bearer"}})
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			typ, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if c.Write(r.Context(), typ, data) != nil {
				return
			}
		}
	})
	ts := httptest.NewUnstartedServer(mux)
	ts.Listener.Close()
	ts.Listener = ln
	ts.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	ts.StartTLS()
	leaf := ts.TLS.Certificates[0].Certificate[0]
	if _, err := x509.ParseCertificate(leaf); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(leaf)
	pin := hex.EncodeToString(sum[:])

	// 1. wrong pin: zero request bytes reach the handler.
	bad, err := NewClient(string(srv.TailcatAddr()), clientKeyText, strings.Repeat("00", 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Request("POST", "/echo", `{"Authorization":"Bearer secret"}`, []byte("x"), 30000); err == nil || err.Error() != ErrPinMismatch {
		t.Fatalf("wrong pin: err = %v, want %s", err, ErrPinMismatch)
	}
	bad.Close()
	smu.Lock()
	if served != 0 {
		t.Fatalf("handler ran %d times under a wrong pin", served)
	}
	smu.Unlock()

	// 2. right pin: request and websocket.
	c, err := NewClient(string(srv.TailcatAddr()), clientKeyText, pin)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	big := strings.Repeat("a", 200<<10)
	resp, err := c.Request("POST", "/echo", `{"Authorization":"Bearer t"}`, []byte(big), 30000)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status() != 200 || string(resp.Body()) != big || !strings.Contains(resp.HeadersJSON(), "Bearer t") {
		t.Fatalf("bad response: %d len=%d", resp.Status(), len(resp.Body()))
	}
	rec := newRecorder()
	s, err := c.OpenSocket("/ws", "solco.bearer, tok", rec)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if _, err := s.get(); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := s.SendText("hello"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-rec.text:
		if got != "hello" {
			t.Fatalf("echo = %q", got)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no echo")
	}
	s.Close(1000, "bye")
	select {
	case <-rec.closed:
	case <-time.After(10 * time.Second):
		t.Fatal("no close event")
	}
}
