package hostd

import (
	"strings"
	"testing"
	"time"

	"github.com/thewei/solco/internal/remotehost"
)

// SOLCO-R02: the desktop and the resident service are two owners of one identity — the same certificate
// fingerprint, the same paired-device store and tokens, the same revoke authority — because both build their
// Host over the same RemoteHost directory.
func TestBothOwnersShareOneCertificateAndOneDeviceStore(t *testing.T) {
	user := t.TempDir()
	dir := RemoteDir(user)
	desktop, err := remotehost.New(remotehost.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := desktop.Devices().Add("Pixel")
	if err != nil {
		t.Fatal(err)
	}
	_, fpDesktop, err := remotehost.LoadOrCreateCert(dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	h, err := NewServer(user, "test")()
	if err != nil {
		t.Fatal(err)
	}
	resident := h.(*remotehost.Server)
	_, fpResident, err := remotehost.LoadOrCreateCert(dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if fpDesktop != fpResident || fpDesktop == "" {
		t.Fatalf("fingerprints differ: %q vs %q", fpDesktop, fpResident)
	}
	dev, ok := resident.Devices().Authenticate(token)
	if !ok {
		t.Fatal("a phone paired on the desktop must be accepted by the resident service")
	}
	if err := resident.Devices().Revoke(dev.ID); err != nil {
		t.Fatal(err)
	}
	back, err := remotehost.New(remotehost.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := back.Devices().Authenticate(token); ok {
		t.Fatal("a phone revoked while the resident service owned the port must stay revoked on the desktop")
	}
}

func TestPlistRunsTheServiceAgainstTheAppDataDirectory(t *testing.T) {
	p := Plist("/Apps/Solco & Co.app/Contents/Helpers/solco-hostd", "/Users/x/Library/Application Support/Solco", "/Users/x/Library/Logs/Solco")
	for _, want := range []string{"<string>com.whstudio.solco.hostd</string>", "<key>KeepAlive</key><true/>", "Solco &amp; Co.app", "<string>-data</string>", "hostd.log"} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %q:\n%s", want, p)
		}
	}
}
