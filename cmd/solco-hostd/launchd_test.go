package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thewei/solco/internal/hostd"
	"github.com/thewei/solco/internal/remotehost"
	"github.com/thewei/solco/internal/settings"
)

// SOLCO-R01 on the real launchd: the installed user service answers a paired phone with no desktop running,
// is restarted by launchd when it dies, yields to the desktop, and is gone after uninstall. It touches the
// user's launchd domain (label com.whstudio.solco.hostd), so it runs only when asked:
//
//	SOLCO_HOSTD_LAUNCHD=1 go test ./cmd/solco-hostd -run Launchd
func TestInstalledServiceSurvivesKillsAndYieldsUnderLaunchd(t *testing.T) {
	if os.Getenv("SOLCO_HOSTD_LAUNCHD") != "1" {
		t.Skip("set SOLCO_HOSTD_LAUNCHD=1 to exercise the real launchd domain")
	}
	if hostd.Installed() {
		t.Skip("a real solco-hostd service is installed on this Mac; refusing to replace it")
	}
	bin := filepath.Join(t.TempDir(), "solco-hostd")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=auto")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	user := t.TempDir()
	port := freePort(t)
	s := settings.Default()
	s.Remote.Mode, s.Remote.Port = "loopback", port
	if err := settings.NewFileStore(user).Save(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	dir := hostd.RemoteDir(user)
	paired, err := remotehost.New(remotehost.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	_, token, _ := paired.Devices().Add("Pixel")

	if err := hostd.Install(bin, user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hostd.Uninstall() })
	waitFor(t, "service answers", func() bool { code, _, _, err := probe(port, token); return err == nil && code == 200 })
	if got := hostd.Status(); got != "running" {
		t.Fatalf("status = %q", got)
	}

	out, err := exec.Command("pgrep", "-f", bin+" run").Output()
	if err != nil {
		t.Fatalf("pgrep: %v", err)
	}
	pid := strings.Fields(string(out))[0]
	_ = exec.Command("kill", "-9", pid).Run()
	waitFor(t, "launchd restarts the killed service", func() bool {
		out, err := exec.Command("pgrep", "-f", bin+" run").Output()
		code, _, _, perr := probe(port, token)
		return err == nil && strings.Fields(string(out))[0] != pid && perr == nil && code == 200
	})

	lease, err := hostd.Claim(dir, 8*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := probe(port, token); err == nil {
		t.Fatal("service still listens while the desktop owns the port")
	}
	lease.Release()
	waitFor(t, "service resumes", func() bool { code, _, _, err := probe(port, token); return err == nil && code == 200 })

	if err := hostd.Uninstall(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "service is gone after uninstall", func() bool { _, _, _, err := probe(port, token); return err != nil })
}
