package remotehost

import (
	"crypto/x509"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseModeRejectsTypos(t *testing.T) {
	for in, want := range map[string]Mode{"": ModeOff, " OFF ": ModeOff, "Loopback": ModeLoopback, "lan": ModeLAN, "tailscale": ModeTailscale} {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Fatalf("ParseMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if m, err := ParseMode("0.0.0.0"); err == nil || m != ModeOff {
		t.Fatalf("a bad mode must error and stay off, got %q, %v", m, err)
	}
}

func TestListenAddrNeverBindsEveryInterface(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
		bad  bool
	}{
		{"off", Config{Mode: ModeOff}, "", false},
		{"zero value is off", Config{}, "", false},
		{"loopback", Config{Mode: ModeLoopback, Port: 7788}, "127.0.0.1:7788", false},
		{"lan private", Config{Mode: ModeLAN, Port: 7788, Address: "192.168.1.20"}, "192.168.1.20:7788", false},
		{"lan 10/8", Config{Mode: ModeLAN, Port: 1, Address: "10.0.0.5"}, "10.0.0.5:1", false},
		{"lan wildcard", Config{Mode: ModeLAN, Port: 7788, Address: "0.0.0.0"}, "", true},
		{"lan v6 wildcard", Config{Mode: ModeLAN, Port: 7788, Address: "::"}, "", true},
		{"lan empty", Config{Mode: ModeLAN, Port: 7788}, "", true},
		{"lan public", Config{Mode: ModeLAN, Port: 7788, Address: "8.8.8.8"}, "", true},
		{"lan loopback", Config{Mode: ModeLAN, Port: 7788, Address: "127.0.0.1"}, "", true},
		{"tailscale v4", Config{Mode: ModeTailscale, Port: 7788, Address: "100.101.102.103"}, "100.101.102.103:7788", false},
		{"tailscale v6", Config{Mode: ModeTailscale, Port: 7788, Address: "fd7a:115c:a1e0::1"}, "[fd7a:115c:a1e0::1]:7788", false},
		{"tailscale lan address", Config{Mode: ModeTailscale, Port: 7788, Address: "192.168.1.20"}, "", true},
		{"port zero", Config{Mode: ModeLoopback, Port: 0}, "", true},
		{"port too big", Config{Mode: ModeLoopback, Port: 70000}, "", true},
		{"unknown mode", Config{Mode: "all", Port: 7788}, "", true},
	}
	for _, c := range cases {
		got, err := c.cfg.ListenAddr()
		if c.bad != (err != nil) || got != c.want {
			t.Errorf("%s: ListenAddr = %q, %v; want %q (bad=%v)", c.name, got, err, c.want, c.bad)
		}
		if strings.HasPrefix(got, "0.0.0.0") || strings.HasPrefix(got, "[::]") || strings.HasPrefix(got, ":") {
			t.Errorf("%s: wildcard bind %q", c.name, got)
		}
	}
}

func TestCertIsStableAndKeyIsPrivate(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	c1, fp1, err := LoadOrCreateCert(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	_, fp2, err := LoadOrCreateCert(dir, now.Add(24*time.Hour))
	if err != nil || fp1 != fp2 {
		t.Fatalf("fingerprint must be stable across loads: %q vs %q (%v)", fp1, fp2, err)
	}
	if len(fp1) != 64 || fp1 != Fingerprint(c1.Certificate[0]) {
		t.Fatalf("fingerprint %q is not the DER sha256", fp1)
	}
	info, err := os.Stat(filepath.Join(dir, keyFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	leaf, _ := x509.ParseCertificate(c1.Certificate[0])
	if leaf.NotAfter.Before(now.Add(9 * 365 * 24 * time.Hour)) {
		t.Fatalf("certificate lifetime too short: %v", leaf.NotAfter)
	}
}

func TestCertIsRebuiltNearExpiryAndWhenCorrupt(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	_, fp1, err := LoadOrCreateCert(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	_, fp2, err := LoadOrCreateCert(dir, now.Add(certLifetime-renewBefore+time.Hour))
	if err != nil || fp1 == fp2 {
		t.Fatalf("a certificate inside the renew window must be rebuilt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, certFile), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreateCert(dir, now); err != nil {
		t.Fatalf("a corrupt certificate must be rebuilt: %v", err)
	}
}

func TestPairingCodeWorksOnceAndExpires(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	p := NewPairing(func() time.Time { return now })
	if p.Consume("anything") {
		t.Fatal("no code started: Consume must refuse")
	}
	code, exp, err := p.Start()
	if err != nil || len(code) < 26 || !exp.Equal(now.Add(PairingTTL)) {
		t.Fatalf("Start = %q, %v, %v", code, exp, err)
	}
	if p.Consume("WRONGWRONGWRONGWRONGWRONGWR") {
		t.Fatal("a wrong code must be refused")
	}
	if !p.Consume(strings.ToLower(code)) {
		t.Fatal("the right code (case-insensitive) must work once")
	}
	if p.Consume(code) {
		t.Fatal("a code must be single use")
	}

	code, _, _ = p.Start()
	now = now.Add(PairingTTL)
	if p.Consume(code) {
		t.Fatal("an expired code must be refused")
	}
}

func TestPairingNewCodeReplacesTheOldOne(t *testing.T) {
	p := NewPairing(nil)
	a, _, _ := p.Start()
	b, _, _ := p.Start()
	if a == b || p.Consume(a) || !p.Consume(b) {
		t.Fatal("only the newest code may work")
	}
	c, _, _ := p.Start()
	p.Cancel()
	if p.Consume(c) {
		t.Fatal("a cancelled code must not work")
	}
}

func TestDeviceStoreKeepsOnlyTheTokenHash(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenDeviceStore(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	dev, token, err := s.Add("  Test iPhone  ")
	if err != nil || token == "" || dev.Name != "Test iPhone" {
		t.Fatalf("Add = %+v, %q, %v", dev, token, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, devicesFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token) {
		t.Fatal("the plaintext token must never reach disk")
	}
	var disk devicesDisk
	if err := json.Unmarshal(raw, &disk); err != nil || len(disk.Devices) != 1 || disk.Devices[0].TokenHash != hashToken(token) {
		t.Fatalf("disk = %s (%v)", raw, err)
	}
	if info, _ := os.Stat(filepath.Join(dir, devicesFile)); info.Mode().Perm() != 0o600 {
		t.Fatalf("devices.json mode = %v, want 0600", info.Mode().Perm())
	}

	reopened, err := OpenDeviceStore(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reopened.Authenticate(token); !ok || got.ID != dev.ID {
		t.Fatalf("a restarted Host must still know the phone: %+v %v", got, ok)
	}
	if _, ok := reopened.Authenticate(token + "x"); ok {
		t.Fatal("a wrong token must not authenticate")
	}
	if _, ok := reopened.Authenticate(""); ok {
		t.Fatal("an empty token must not authenticate")
	}
}

func TestRevokeStopsTheTokenAndNotifies(t *testing.T) {
	s, _ := OpenDeviceStore(t.TempDir(), nil)
	dev, token, _ := s.Add("Phone")
	other, otherToken, _ := s.Add("Tablet")
	var revoked []string
	s.OnRevoke(func(id string) { revoked = append(revoked, id) })

	if err := s.Revoke(dev.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Authenticate(token); ok {
		t.Fatal("a revoked token must stop working at once")
	}
	if got, ok := s.Authenticate(otherToken); !ok || got.ID != other.ID {
		t.Fatal("revoking one device must not touch another")
	}
	if len(revoked) != 1 || revoked[0] != dev.ID {
		t.Fatalf("revoke callback = %v", revoked)
	}
	if err := s.Revoke(dev.ID); err == nil {
		t.Fatal("revoking an unknown device must error")
	}
}

func TestLastSeenWritesAreThrottled(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s, _ := OpenDeviceStore(dir, func() time.Time { return now })
	_, token, _ := s.Add("Phone")
	path := filepath.Join(dir, devicesFile)
	before, _ := os.ReadFile(path)

	now = now.Add(10 * time.Second)
	s.Authenticate(token)
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("a second authentication inside a minute must not rewrite the file")
	}
	now = now.Add(2 * time.Minute)
	s.Authenticate(token)
	if after, _ := os.ReadFile(path); string(after) == string(before) {
		t.Fatal("last seen must be persisted after the throttle window")
	}
}

func TestCorruptDeviceFileIsAnErrorNotASilentUnpair(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, devicesFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenDeviceStore(dir, nil); err == nil {
		t.Fatal("a corrupt device file must surface, not silently un-pair every phone")
	}
}

func TestDeviceNameIsBounded(t *testing.T) {
	s, _ := OpenDeviceStore(t.TempDir(), nil)
	dev, _, _ := s.Add(strings.Repeat("é", 200))
	if got := len([]rune(dev.Name)); got != maxDeviceName {
		t.Fatalf("name runes = %d, want %d", got, maxDeviceName)
	}
	if dev, _, _ := s.Add("   "); dev.Name != "Phone" {
		t.Fatalf("blank name = %q, want Phone", dev.Name)
	}
}
