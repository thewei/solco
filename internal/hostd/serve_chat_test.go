package hostd

/**
 * [INPUT]: Private Unix socket, resident Host lifecycle and real apphook packets.
 * [OUTPUT]: Tests socket handoff/off/close, refusal to steal another active listener.
 * [POS]: SC-06 resident realtime acceptance; no real user socket or Herdr process.
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thewei/solco/internal/apphook"
	"github.com/thewei/solco/internal/chatstream"
	"github.com/thewei/solco/internal/remotehost"
)

// macOS sockaddr_un has a short path limit: Go t.TempDir() may be too long.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "solco-hostd-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "events.sock")
}

func freeResidentPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func residentFixture(t *testing.T, socket string) *residentServer {
	t.Helper()
	s, err := remotehost.New(remotehost.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	h := &residentServer{Server: s, hookPath: socket}
	t.Cleanup(h.Close)
	return h
}

func TestResidentChatHookLivesOnlyWhileHostServes(t *testing.T) {
	sock := shortSocketPath(t)
	h := residentFixture(t, sock)
	if _, err := os.Lstat(sock); !os.IsNotExist(err) {
		t.Fatalf("started socket without lease: %v", err)
	}
	cfg := remotehost.Config{Mode: remotehost.ModeLoopback, Port: freeResidentPort(t)}
	if err := h.Reconfigure(cfg, remotehost.RelayConfig{}); err != nil {
		t.Fatal(err)
	}
	if h.hook == nil {
		t.Fatal("resident host did not take idle AppHook socket")
	}
	event := chatstream.Event{
		Agent: "omp", PaneID: "w1:p1", SessionPath: filepath.Join(t.TempDir(), "session.jsonl"),
		Epoch: "0000000000001-resident", Seq: 1, Kind: "message_start", MessageID: "m1",
	}
	raw, _ := json.Marshal(map[string]any{"method": "chat.stream", "params": event})
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 160)
	n, err := conn.Read(buf)
	_ = conn.Close()
	if err != nil || n == 0 {
		t.Fatalf("resident apphook did not respond: %v", err)
	}

	if err := h.Reconfigure(remotehost.Config{Mode: remotehost.ModeOff}, remotehost.RelayConfig{}); err != nil {
		t.Fatal(err)
	}
	if h.hook != nil {
		t.Fatal("kept OMP AppHook while host is off")
	}
	owner := apphook.NewServer(sock, apphook.Handler{})
	if err := owner.Start(); err != nil {
		t.Fatalf("desktop could not claim yielded socket: %v", err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	claimed, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatalf("owner could not connect: %v", err)
	}
	_ = claimed.Close()
}

func TestResidentNeverRemovesDesktopAppHookSocket(t *testing.T) {
	sock := shortSocketPath(t)
	owner := apphook.NewServer(sock, apphook.Handler{})
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	defer owner.Close()

	h := residentFixture(t, sock)
	cfg := remotehost.Config{Mode: remotehost.ModeLoopback, Port: freeResidentPort(t)}
	if err := h.Reconfigure(cfg, remotehost.RelayConfig{}); err != nil {
		t.Fatal(err)
	}
	if h.hook != nil {
		t.Fatal("resident stole desktop's live AppHook")
	}
	h.Close()
	if _, err := os.Lstat(sock); err != nil {
		t.Fatalf("resident closed desktop socket: %v", err)
	}
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatalf("desktop socket became unreachable: %v", err)
	}
	_ = conn.Close()
}
