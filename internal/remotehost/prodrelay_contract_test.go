package remotehost

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 可选真生产 relay 契约（SOLCO_RELAY_PROD_CONTRACT=1，RELAY_KEY=注册钥匙）：
// 真拨出 https://relay.7200.online（经典 TLS 群组，GFW 丢 ML-KEM 首选握手）→
// 经 /c/<rt>/api/v3/pair 真配对（手机视角：普通 HTTPS，信任根=实物 QR）→
// 与 SOLCO_REMOTEHOST_CONTRACT 同一门控模式：未设环境变量一律跳过，不进 CI。
// 注意：newRelayHarness 无条件把 relayHooks.dial 指到进程内假 relay，本测试
// 因此自行组装 Server（默认 dialFn = 生产拨号），不复用该 harness。
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

func TestProdRelayContract(t *testing.T) {
	if os.Getenv("SOLCO_RELAY_PROD_CONTRACT") == "" {
		t.Skip("SOLCO_RELAY_PROD_CONTRACT 未设置")
	}
	const relayBase = "https://relay.7200.online"
	// 不用 newRelayHarness：它无条件把 relayHooks.dial 指到进程内假 relay，
	// 生产拨号必须走默认 dialFn（经典曲线 TLS）。其余夹具与 harness 相同。
	back := &fakeBackend{instances: []string{"default"}, proj: fixtureProjection(), observe: gridScript(filepath.Join(t.TempDir(), "pids"))}
	srv, err := New(Options{
		Config:   Config{Mode: ModeLoopback, Port: relayFreePort(t)},
		Dir:      t.TempDir(),
		Backend:  back,
		HostName: "Test Mac",
		Version:  "prod-contract",
		Relay:    RelayConfig{Enabled: true, URL: relayBase, RegistrationKey: os.Getenv("RELAY_KEY")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	h := &relayHarness{t: t, srv: srv, back: back}
	h.waitStatus(RelayOnline, 90*time.Second)

	offer, err := h.srv.StartPairing()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(offer.URL)
	if err != nil {
		t.Fatal(err)
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		t.Fatal(err)
	}
	if got := q.Get("r"); got != relayBase {
		t.Fatalf("QR r = %q, want %q", got, relayBase)
	}
	rt, code := q.Get("rt"), q.Get("c")
	if rt == "" || code == "" {
		t.Fatalf("QR missing rt/code: %+v", q)
	}
	sec, err := h.srv.relaySecret()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(rt))
	if hex.EncodeToString(sum[:]) != RelayTokenHash(sec) {
		t.Fatalf("QR rt does not hash to the dialled secret")
	}

	// 手机侧同样避开 GFW 丢弃的 ML-KEM 首选握手（与 relayTLSConfig 同理）。
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256},
		}},
	}
	base := relayBase + "/c/" + rt

	pair := func(code string) (*http.Response, []byte) {
		body, _ := json.Marshal(map[string]string{"code": code, "deviceName": "prod-relay-contract"})
		resp, err := client.Post(base+"/api/v3/pair", "application/json", bytes.NewReader(body))
		if err != nil {
			st := h.srv.RelayStatus()
			t.Fatalf("relay pair: %v | relayState=%s relayMsg=%q inflight=%d bridges=%d",
				err, st.State, st.Message, relayInflight(h.client()), relayBridges(h.client()))
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, b
	}

	// online 后零间隙发起 relay 配对：任何 >~1s 的空闲都可能丢 agent 流。
	resp, body := pair(code)
	if resp.StatusCode != http.StatusOK {
		st := h.srv.RelayStatus()
		t.Fatalf("pair status = %d body=%s relayState=%s relayMsg=%q", resp.StatusCode, string(body), st.State, st.Message)
	}
	var paired struct {
		DeviceID string `json:"deviceId"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal(body, &paired); err != nil || paired.Token == "" {
		t.Fatalf("pair payload = %s", body)
	}

	get := func(path, token string) (*http.Response, []byte) {
		req, err := http.NewRequest(http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, b
	}

	boot, b := get("/api/v3/bootstrap", paired.Token)
	var parsed struct {
		HostName  string `json:"hostName"`
		Instances []struct {
			ID string `json:"id"`
		} `json:"instances"`
	}
	_ = json.Unmarshal(b, &parsed)
	if boot.StatusCode != http.StatusOK || parsed.HostName == "" || len(parsed.Instances) == 0 {
		t.Fatalf("bootstrap = %d %q instances=%d", boot.StatusCode, parsed.HostName, len(parsed.Instances))
	}

	tree, tb := get("/api/v3/tree", paired.Token)
	if tree.StatusCode != http.StatusOK || len(tb) == 0 {
		t.Fatalf("tree = %d len=%d", tree.StatusCode, len(tb))
	}

	if bad, _ := get("/api/v3/bootstrap", "definitely-wrong-token"); bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d, want 401", bad.StatusCode)
	}

	h.srv.Devices().Revoke(paired.DeviceID)
	if revoked, _ := get("/api/v3/bootstrap", paired.Token); revoked.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token = %d, want 401", revoked.StatusCode)
	}
}
