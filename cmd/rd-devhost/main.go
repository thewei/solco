// Command rd-devhost is a development harness: the real Remote Desktop stack (macOS helper → Pion →
// remotehost API) behind a stub Herdr backend, on its own data directory and port, so a phone can be
// tested without touching the user's running Solco. It is not shipped.
//
//	go run ./cmd/rd-devhost -addr 192.168.1.20 -port 17788 -approve dialog
//
// -approve dialog   a native macOS dialog asks Deny / View Only / Allow Control (the real approval flow)
// -approve control  allow Control without asking          -approve view  allow View Only
//
// The pairing link is printed on stdout (and written to <dir>/pair-url.txt).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/remotedesktop"
	"github.com/thewei/solco/internal/remotedesktop/platform/auto"
	"github.com/thewei/solco/internal/remotedesktop/platform/macos"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
	"github.com/thewei/solco/internal/remotehost"
)

type stubBackend struct{}

var errStub = errors.New("rd-devhost: no Herdr")

func (stubBackend) Instances() ([]string, error) { return []string{"default"}, nil }
func (stubBackend) Projection(string) (herdr.Projection, error) {
	return herdr.Projection{}, nil
}
func (stubBackend) Subscribe(string, []string) (remotehost.EventSource, error) { return nil, errStub }
func (stubBackend) SendPaneInput(context.Context, string, string, string, []string) error {
	return errStub
}
func (stubBackend) PromptAgent(context.Context, string, string, string) error { return errStub }
func (stubBackend) SendAgentKeys(context.Context, string, string, []string) error {
	return errStub
}
func (stubBackend) ReadPane(context.Context, string, string) (string, error)  { return "", errStub }
func (stubBackend) ObserveCommand(string, string, int, int) ([]string, error) { return nil, errStub }

type authorizer struct{ how string }

func (a authorizer) Authorize(ctx context.Context, req remotedesktop.AuthRequest) (remotedesktop.Decision, error) {
	switch a.how {
	case "control":
		return remotedesktop.Decision{Allow: true, Mode: protocol.ModeFullControl}, nil
	case "view":
		return remotedesktop.Decision{Allow: true, Mode: protocol.ModeViewOnly}, nil
	}
	script := fmt.Sprintf(`display dialog "Phone “%s” wants to %s your Mac’s screen." with title "Solco Remote Desktop (dev host)" buttons {"Deny","View Only","Allow Control"} default button "Deny" giving up after 40`, strings.ReplaceAll(req.Device.Name, `"`, ""), map[protocol.Mode]string{protocol.ModeFullControl: "view and control", protocol.ModeViewOnly: "view"}[req.RequestedMode])
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).Output()
	if err != nil {
		if ctx.Err() != nil {
			return remotedesktop.Decision{}, ctx.Err()
		}
		return remotedesktop.Decision{Allow: false}, nil
	}
	s := string(out)
	switch {
	case strings.Contains(s, "Allow Control"):
		return remotedesktop.Decision{Allow: true, Mode: protocol.ModeFullControl}, nil
	case strings.Contains(s, "View Only"):
		return remotedesktop.Decision{Allow: true, Mode: protocol.ModeViewOnly}, nil
	}
	return remotedesktop.Decision{Allow: false}, nil
}

func lanAddr() string {
	ifs, _ := net.Interfaces()
	for _, in := range ifs {
		if !strings.HasPrefix(in.Name, "en") || in.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := in.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && (ipn.IP.IsPrivate() || ipn.IP.IsLinkLocalUnicast()) {
				return ipn.IP.String()
			}
		}
	}
	return ""
}

func main() {
	addr := flag.String("addr", lanAddr(), "LAN interface address to listen on")
	port := flag.Int("port", 17788, "port")
	dir := flag.String("dir", filepath.Join(os.TempDir(), "solco-rd-devhost"), "data directory")
	approve := flag.String("approve", "dialog", "dialog | control | view")
	tier := flag.String("tier", "standard", "default quality tier")
	flag.Parse()
	if *addr == "" {
		fmt.Fprintln(os.Stderr, "no LAN address found; pass -addr")
		os.Exit(2)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		panic(err)
	}

	svc := remotedesktop.NewService(remotedesktop.Config{
		Backend:         auto.New(macos.Options{Logger: log}),
		Authorizer:      authorizer{*approve},
		Trust:           newMemTrust(),
		InterfaceFilter: func(name string) bool { return !remotehost.IsVirtualInterface(name) || strings.HasPrefix(name, "utun") },
		Log:             log,
	})
	svc.SetSettings(remotedesktop.Settings{Enabled: true, RequireApproval: *approve == "dialog", DefaultTier: protocol.Tier(*tier)})

	srv, err := remotehost.New(remotehost.Options{
		Config:        remotehost.Config{Mode: remotehost.ModeLAN, Port: *port, Address: *addr},
		Dir:           *dir,
		Backend:       stubBackend{},
		RemoteDesktop: svc,
		HostName:      "Solco RD dev host",
		Version:       "rd-devhost",
	})
	if err != nil {
		panic(err)
	}
	if err := srv.Start(); err != nil {
		panic(err)
	}
	offer, err := srv.StartPairing()
	if err != nil {
		panic(err)
	}
	_ = os.WriteFile(filepath.Join(*dir, "pair-url.txt"), []byte(offer.URL+"\n"), 0o600)
	fmt.Println("listening on", net.JoinHostPort(*addr, fmt.Sprint(*port)), "(approve:", *approve+")")
	fmt.Println("pair:", offer.URL)
	fmt.Println("expires:", offer.ExpiresAt.Format(time.TimeOnly))

	// A fresh pairing code every five minutes while the harness runs (codes are single-use, six-minute).
	go func() {
		for range time.Tick(5 * time.Minute) {
			if o, err := srv.StartPairing(); err == nil {
				_ = os.WriteFile(filepath.Join(*dir, "pair-url.txt"), []byte(o.URL+"\n"), 0o600)
			}
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	svc.Close()
	srv.Close()
}

// memTrust remembers approvals for the life of the dev host (the app keeps them in its settings).
type memTrust struct {
	mu sync.Mutex
	m  map[string]protocol.Mode
}

func newMemTrust() *memTrust { return &memTrust{m: map[string]protocol.Mode{}} }

func (t *memTrust) Granted(id string) (protocol.Mode, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	m, ok := t.m[id]
	return m, ok
}
func (t *memTrust) Remember(d protocol.DeviceInfo, m protocol.Mode) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.m[d.ID] = m
}
func (t *memTrust) Forget(id string) { t.mu.Lock(); defer t.mu.Unlock(); delete(t.m, id) }
