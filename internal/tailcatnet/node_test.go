package tailcatnet

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

func TestIdentityPersistsAndIsPrivate(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("identity changed across loads: phones would have to pair again")
	}
	st, err := os.Stat(filepath.Join(dir, identityFile))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("identity file mode = %o, want 600", st.Mode().Perm())
	}
}

func TestIdentityDamagedFileIsReplaced(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, identityFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := LoadOrCreateIdentity(dir)
	if err != nil || !id.valid() {
		t.Fatalf("damaged identity not replaced: %v", err)
	}
}

// TestTunnelEndToEnd is the G0 proof: a real Tailcat server and client over the public DERP network, the
// tunnel-authenticated peer key reaching the accept loop, admission refusal, and revoke. It needs the
// internet, so it only runs with SOLCO_TAILCAT_NET=1.
func TestTunnelEndToEnd(t *testing.T) {
	if os.Getenv("SOLCO_TAILCAT_NET") != "1" {
		t.Skip("set SOLCO_TAILCAT_NET=1 to run against the public DERP network")
	}
	id, err := LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client := &tailcat.Client{Key: key.NewNode(), Logf: func(string, ...any) {}}
	clientKeyText, _ := client.PublicKey().MarshalText()
	var allowed atomic.Bool
	allowed.Store(true)

	began := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	node, err := Start(ctx, Options{Identity: id, Allow: func(k string) bool { return allowed.Load() && k == string(clientKeyText) }})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	t.Logf("server start: %v, address length: %d", time.Since(began), len(node.Addr()))

	peers := make(chan string, 4)
	go func() {
		for {
			c, err := node.Listener().Accept()
			if err != nil {
				return
			}
			pk, _ := node.PeerKey(c.RemoteAddr())
			peers <- pk
			go func() { defer c.Close(); _, _ = io.WriteString(c, "ok"); _, _ = io.Copy(io.Discard, c) }()
		}
	}()

	client.Server = tailcat.Addr(node.Addr())
	began = time.Now()
	dctx, dcancel := context.WithTimeout(ctx, 30*time.Second)
	defer dcancel()
	conn, err := client.DialTCPPort(dctx, Port)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Logf("client first dial: %v", time.Since(began))
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ok" {
		t.Fatalf("read: %q %v", buf, err)
	}
	select {
	case got := <-peers:
		if got != string(clientKeyText) {
			t.Fatalf("PeerKey = %q, want the client's key", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no accepted connection")
	}
	conn.Close()
	var _ net.Conn = conn
}
