package vaultlink

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fakeProber struct {
	found   bool
	lookErr error
	paths   map[string]bool
	opened  []string
	openErr error
	lookups int
}

func (f *fakeProber) Lookup(context.Context, string) (bool, error) {
	f.lookups++
	return f.found, f.lookErr
}
func (f *fakeProber) Exists(p string) bool { return f.paths[p] }
func (f *fakeProber) Open(_ context.Context, id string) error {
	f.opened = append(f.opened, id)
	return f.openErr
}

func clientWith(t *testing.T, p *fakeProber, listening bool) *Client {
	t.Helper()
	var c *Client
	if listening {
		c = serve(t, func(map[string]any) string { return pendingJSON })
	} else {
		c = New(t.TempDir())
	}
	c.probe = p
	return c
}

func TestInstalledHasThreeAnswers(t *testing.T) {
	// Launch Services knows the app
	if got := clientWith(t, &fakeProber{found: true}, false).Installed(); got != InstallYes {
		t.Fatalf("registered: %v", got)
	}
	// definitely not registered, and not in the usual folders
	if got := clientWith(t, &fakeProber{}, false).Installed(); got != InstallNo {
		t.Fatalf("absent: %v", got)
	}
	// not indexed yet, but the bundle was just dropped into /Applications
	if got := clientWith(t, &fakeProber{paths: map[string]bool{"/Applications/Solco Vault.app": true}}, false).Installed(); got != InstallYes {
		t.Fatalf("dropped in: %v", got)
	}
	// Launch Services cannot answer (no GUI session): the path decides, else unknown
	if got := clientWith(t, &fakeProber{lookErr: errors.New("no session"), paths: map[string]bool{"/Applications/Solco Vault.app": true}}, false).Installed(); got != InstallYes {
		t.Fatalf("hostd fallback: %v", got)
	}
	if got := clientWith(t, &fakeProber{lookErr: errors.New("no session")}, false).Installed(); got != InstallUnknown {
		t.Fatalf("unknown: %v", got)
	}
}

func TestAvailableFollowsInstallThenTheHomeDirectory(t *testing.T) {
	home := t.TempDir() // exists
	mk := func(p *fakeProber, home string) *Client {
		c := New(home)
		c.probe = p
		return c
	}
	if !mk(&fakeProber{found: true}, filepath.Join(home, "never-ran")).Available() {
		t.Fatal("installed but never run must still be offered")
	}
	if mk(&fakeProber{}, home).Available() {
		t.Fatal("uninstalled with a leftover directory must not be offered")
	}
	if !mk(&fakeProber{lookErr: errors.New("x")}, home).Available() {
		t.Fatal("unknown + a home directory: offered")
	}
	if mk(&fakeProber{lookErr: errors.New("x")}, filepath.Join(home, "none")).Available() {
		t.Fatal("unknown and never set up: not offered")
	}
}

func TestInstalledIsCached(t *testing.T) {
	p := &fakeProber{found: true}
	c := clientWith(t, p, false)
	c.Installed()
	c.Installed()
	if p.lookups != 1 {
		t.Fatalf("%d lookups", p.lookups)
	}
}

func TestLaunchOnlyStartsAnInstalledAppThatIsNotRunning(t *testing.T) {
	p := &fakeProber{found: true}
	c := clientWith(t, p, false)
	if err := c.Launch(context.Background()); err != nil || len(p.opened) != 1 || p.opened[0] != BundleID {
		t.Fatalf("%v %v", err, p.opened)
	}

	running := &fakeProber{found: true}
	if err := clientWith(t, running, true).Launch(context.Background()); !errors.Is(err, ErrAlreadyRunning) || len(running.opened) != 0 {
		t.Fatalf("%v %v", err, running.opened)
	}

	none := &fakeProber{}
	if err := clientWith(t, none, false).Launch(context.Background()); !errors.Is(err, ErrNotInstalled) || len(none.opened) != 0 {
		t.Fatalf("%v %v", err, none.opened)
	}

	broken := &fakeProber{found: true, openErr: errors.New("no gui")}
	if err := clientWith(t, broken, false).Launch(context.Background()); err == nil {
		t.Fatal("a failed open must surface")
	}
	_ = os.Getenv
}

// The real prober against this Mac: a registered bundle is found, an unknown one is
// a quiet "no" (never a dialog), and nothing hangs.
func TestSystemProberLookup(t *testing.T) {
	if _, err := os.Stat("/usr/bin/mdfind"); err != nil {
		t.Skip("no mdfind")
	}
	p := systemProber{}
	if ok, err := p.Lookup(context.Background(), "com.apple.finder"); err != nil || !ok {
		t.Fatalf("finder: %v %v", ok, err)
	}
	if ok, err := p.Lookup(context.Background(), "com.example.definitely-not-installed"); err != nil || ok {
		t.Fatalf("missing: %v %v", ok, err)
	}
}
