package hostd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tailscale/tailcat"
	"github.com/thewei/solco/internal/remotehost"
	"tailscale.com/types/key"
)

// [INPUT]: 真实 Tailcat 隧道（公共 DERP）+ 真实 remotehost.Server + hostd.TailcatOverlay
// [OUTPUT]: G1/G2/G6 的网络证据：配对经隧道绑定 peer 公钥、令牌只在其绑定 peer 上有效、未知 peer 被拒、撤销先拒后断、应用退出后常驻服务接管同一身份（同一隧道地址、同一设备库）
// [POS]: 需要互联网，仅 SOLCO_TAILCAT_NET=1 时运行

func tunnelClient(c *tailcat.Client, fingerprint string) *http.Client {
	want := strings.ToLower(strings.ReplaceAll(fingerprint, ":", ""))
	return &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return c.DialTCPPort(ctx, 7788)
			},
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // the pin below is the verification; the name is not a DNS host
				MinVersion:         tls.VersionTLS13,
				VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
					sum := sha256.Sum256(raw[0])
					if hex.EncodeToString(sum[:]) != want {
						return errors.New("pin mismatch")
					}
					return nil
				},
			},
		},
	}
}

func waitReady(t *testing.T, s *remotehost.Server) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if s.OverlayStatus().State == remotehost.OverlayReady {
			return s.TailcatAddress()
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("tunnel never became ready: %+v", s.OverlayStatus())
	return ""
}

func TestTailcatPairingBindsAndAHandoverKeepsTheIdentity(t *testing.T) {
	if os.Getenv("SOLCO_TAILCAT_NET") != "1" {
		t.Skip("set SOLCO_TAILCAT_NET=1 to run against the public DERP network")
	}
	user := t.TempDir()
	dir := RemoteDir(user)
	newHost := func() *remotehost.Server {
		s, err := remotehost.New(remotehost.Options{Dir: dir, Config: remotehost.Config{Tailcat: true}, Overlay: TailcatOverlay(dir), HostName: "Test Mac"})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Start(); err != nil {
			t.Fatal(err)
		}
		return s
	}

	// the desktop app owns the Host and pairs a phone through the tunnel
	app := newHost()
	addr1 := waitReady(t, app)
	offer, err := app.StartPairing()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(offer.URL, "tc=") || strings.Contains(offer.URL, "h=") {
		t.Fatalf("a tunnel-only offer must carry the tunnel and no direct address: %s", offer.URL)
	}
	phone := &tailcat.Client{Key: key.NewNode(), Server: tailcat.Addr(addr1), Logf: func(string, ...any) {}}
	hc := tunnelClient(phone, offer.Fingerprint)
	body, _ := json.Marshal(map[string]string{"code": offer.Code, "deviceName": "Pixel"})
	resp, err := hc.Post("https://mac/api/v3/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("pair over the tunnel: %v", err)
	}
	var paired struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&paired)
	resp.Body.Close()
	if resp.StatusCode != 200 || paired.Token == "" {
		t.Fatalf("pair status %d", resp.StatusCode)
	}
	get := func(c *http.Client, token string) int {
		req, _ := http.NewRequest("GET", "https://mac/api/v3/quota", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		r, err := c.Do(req)
		if err != nil {
			return -1
		}
		r.Body.Close()
		return r.StatusCode
	}
	if code := get(hc, paired.Token); code == 401 || code < 0 {
		t.Fatalf("the paired phone is not accepted on its own tunnel key: %d", code)
	}
	if code := get(hc, "not-a-token"); code != 401 {
		t.Fatalf("a bad token answered %d, want 401", code)
	}
	if devs := app.Devices().List(); len(devs) != 1 || devs[0].TailcatKey == "" {
		t.Fatalf("pairing did not bind the phone's tunnel key: %+v", devs)
	}

	// a stranger's key cannot even reach the Host once the window is closed
	app.CancelPairing()
	stranger := &tailcat.Client{Key: key.NewNode(), Server: tailcat.Addr(addr1), Logf: func(string, ...any) {}}
	sc := tunnelClient(stranger, offer.Fingerprint)
	sc.Timeout = 8 * time.Second
	if code := get(sc, paired.Token); code != -1 {
		t.Fatalf("an unknown tunnel peer reached the Host (%d); a stolen token must not be enough", code)
	}

	// the app quits; the resident service takes over the same directory and the phone carries on unchanged
	app.Close()
	resident := newHost()
	defer resident.Close()
	if addr2 := waitReady(t, resident); addr2 != addr1 {
		t.Fatal("the tunnel address changed across the hand-over: the phone's saved Mac would break")
	}
	began := time.Now()
	for {
		// a phone whose app was restarted (or whose client was rebuilt after a failure) keeps its saved key
		phone = &tailcat.Client{Key: phone.Key, Server: tailcat.Addr(addr1), Logf: func(string, ...any) {}}
		code := get(tunnelClient(phone, offer.Fingerprint), paired.Token)
		if code > 0 && code != 401 {
			t.Logf("a rebuilt phone client reached the resident service after %v", time.Since(began).Round(time.Second))
			break
		}
		if code == 401 || time.Since(began) > 90*time.Second {
			t.Fatalf("the paired phone is refused by the resident service: %d after %v", code, time.Since(began))
		}
	}

	// revoke on the resident service: the token dies and the tunnel peer is refused
	dev := resident.Devices().List()[0]
	if err := resident.Devices().Revoke(dev.ID); err != nil {
		t.Fatal(err)
	}
	rc := tunnelClient(phone, offer.Fingerprint)
	rc.Timeout = 8 * time.Second
	if code := get(rc, paired.Token); code != -1 && code != 401 {
		t.Fatalf("a revoked phone still gets %d", code)
	}
}
