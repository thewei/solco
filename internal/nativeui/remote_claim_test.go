package nativeui

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thewei/solco/internal/hostd"
	"github.com/thewei/solco/internal/remotehost"
	"github.com/thewei/solco/internal/settings"
)

type residentStub struct{ closed atomic.Bool }

func init() { remoteClaimGo = func(f func()) { f() } }

func (*residentStub) SetP2P(remotehost.P2PConfig)                                 {}
func (*residentStub) Reconfigure(remotehost.Config, remotehost.RelayConfig) error { return nil }
func (r *residentStub) Close()                                                    { r.closed.Store(true) }

// SOLCO-R01: with the resident service holding the port, the desktop asks it to step aside before it listens,
// and hands the port back when Remote Access is switched off — so the phone always has exactly one Host.
func TestDesktopClaimsThePortFromTheResidentServiceAndHandsItBack(t *testing.T) {
	s := remoteShell(t)
	port := remoteFreePort(t)
	s.applySettings(func(v *settings.Settings) error { v.Remote.Port = port; return nil })
	dir := hostd.RemoteDir(s.userDataDir)

	var stubs atomic.Int32
	stub := &residentStub{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = hostd.Run(ctx, hostd.Options{
			Dir: dir,
			Settings: func() settings.RemoteAccessSettings {
				return settings.RemoteAccessSettings{Mode: "lan", Port: port, Address: "10.0.0.1"}
			},
			New: func() (hostd.Host, error) {
				if stubs.Add(1) == 1 {
					return stub, nil
				}
				return &residentStub{}, nil
			},
			Poll: 10 * time.Millisecond,
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.Now().Add(3 * time.Second)
	for stubs.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if stubs.Load() == 0 {
		t.Fatal("the resident service never took the port")
	}

	s.setRemote(func(r *settings.RemoteAccessSettings) { r.Mode = "loopback" })
	if s.remote.lease == nil || !stub.closed.Load() {
		t.Fatalf("desktop must hold the lease after the resident service yielded (lease=%v closed=%v err=%q)", s.remote.lease != nil, stub.closed.Load(), s.remote.err)
	}
	if s.remote.srv == nil || s.remote.srv.ListenAddress() == "" {
		t.Fatalf("desktop Host is not listening: err=%q", s.remote.err)
	}

	s.setRemote(func(r *settings.RemoteAccessSettings) { r.Mode = "off" })
	if s.remote.lease != nil {
		t.Fatal("an off desktop must release the port")
	}
	deadline = time.Now().Add(3 * time.Second)
	for stubs.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if stubs.Load() < 2 {
		t.Fatal("the resident service did not take the port back")
	}
}
