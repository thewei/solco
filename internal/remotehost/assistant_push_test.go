package remotehost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thewei/solco/internal/personalagent"
)

const testExpoToken = "ExponentPushToken[ABCDEFGHIJKLMNOPQRSTUVWX1234]"

func TestPushRegistryPrivatePersistenceAndRevoke(t *testing.T) {
	dir := t.TempDir()
	p, err := openPushRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.register("dev_1", testExpoToken); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(filepath.Join(dir, pushFile))
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("push token file must be private: %v %v", stat, err)
	}
	restored, err := openPushRegistry(dir)
	if err != nil || restored.current("dev_1").Token != testExpoToken {
		t.Fatalf("restart dropped push token: %v", err)
	}
	if err := restored.remove("dev_1"); err != nil {
		t.Fatal(err)
	}
	if restored.current("dev_1").Token != "" {
		t.Fatal("revoked token still present")
	}
	if err := os.WriteFile(filepath.Join(dir, pushFile), []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := openPushRegistry(dir); err == nil {
		t.Fatal("corrupted push registry silently reset")
	}
}
func TestPushRegistrationIsDeviceOwnedAndNeverLeaksToken(t *testing.T) {
	h, _ := paHarness(t, true)
	path := "/api/v3/notifications/push"
	if status, _ := h.send("POST", path+"/register", "", `{"token":"`+testExpoToken+`"}`); status != 401 {
		t.Fatalf("unpaired token accepted %d", status)
	}
	for _, input := range []string{
		`{"token":"nope"}`,
		`{"token":"` + testExpoToken + `","deviceId":"dev_victim"}`,
		`{"token":"` + testExpoToken + `"}{}`,
	} {
		if status, _ := h.send("POST", path+"/register", h.token, input); status != 400 {
			t.Fatalf("invalid %q accepted %d", input, status)
		}
	}
	if status, raw := h.send("POST", path+"/register", h.token, `{"token":"`+testExpoToken+`"}`); status != 200 || strings.Contains(string(raw), testExpoToken) {
		t.Fatalf("token leaked: %d %s", status, raw)
	}
	if resp := h.get(path, h.token, nil); resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	} else {
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), `"enabled":true`) || strings.Contains(string(body), testExpoToken) {
			t.Fatalf("status leaks: %s", body)
		}
	}
	if _, token, err := h.srv.Devices().Add("Second Phone"); err != nil {
		t.Fatal(err)
	} else {
		if resp := h.get(path, token, nil); resp.StatusCode != 200 {
			t.Fatalf("second phone status %d", resp.StatusCode)
		} else {
			body, _ := io.ReadAll(resp.Body)
			if strings.Contains(string(body), `"enabled":true`) {
				t.Fatalf("cross-device push token exposed: %s", body)
			}
		}
	}
	if err := h.srv.Devices().Revoke(h.devID); err != nil {
		t.Fatal(err)
	}
	if h.srv.push.registry.current(h.devID).Token != "" {
		t.Fatal("revoke left remote push enabled")
	}
	if status, _ := h.send("POST", path+"/register", h.token, `{"token":"`+testExpoToken+`"}`); status != 401 {
		t.Fatalf("revoked phone could register: %d", status)
	}
}
func TestPushTicketReceiptAndNoSensitiveExpoPayload(t *testing.T) {
	h, core := paHarness(t, true)
	now := time.Unix(1800000000, 0)
	h.srv.opts.Now = func() time.Time { return now }
	if err := h.srv.push.registry.register(h.devID, testExpoToken); err != nil {
		t.Fatal(err)
	}
	h.srv.mu.Lock()
	h.srv.fingerprint = "0123456789abcdef" + strings.Repeat("0", 48)
	h.srv.mu.Unlock()
	var mu sync.Mutex
	sent, receipts := 0, 0
	expo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("bad expo body: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/send" {
			sent++
			raw, _ := json.Marshal(payload)
			if strings.Contains(string(raw), "Collect numbers") || strings.Contains(string(raw), "Watch PR") ||
				strings.Contains(string(raw), "Delegated:") || strings.Contains(string(raw), "Ada") ||
				strings.Contains(string(raw), "Follows my") {
				t.Errorf("private Assistant data sent to Expo: %s", raw)
			}
			if payload["channelId"] != "agent-attention" || payload["to"] != testExpoToken {
				t.Errorf("invalid Expo push payload: %+v", payload)
			}
			data, ok := payload["data"].(map[string]any)
			if !ok || data["hostId"] != "0123456789abcdef" {
				t.Errorf("push must identify the paired Mac for correct tap routing: %+v", payload["data"])
			}
			_, _ = w.Write([]byte(`{"data":{"status":"ok","id":"receipt-one"}}`))
		} else {
			receipts++
			_, _ = w.Write([]byte(`{"data":{"receipt-one":{"status":"ok"}}}`))
		}
	}))
	defer expo.Close()
	h.srv.push.sendURL = expo.URL + "/send"
	h.srv.push.receiptURL = expo.URL + "/receipts"
	ctx := context.Background()
	h.srv.pushTick(ctx)
	if sent != 1 {
		t.Fatalf("did not send background push: %d", sent)
	}
	pending := h.srv.push.registry.current(h.devID).Pending[paAssistantA]
	if pending.Seq != 4 || pending.Ticket != "receipt-one" {
		t.Fatalf("pending receipt lost: %+v", pending)
	}
	// Ticket is NOT an ACK. Nor is repeated polling another push.
	if strings.Contains(core.lastCall(), "activity ack") {
		t.Fatal("ACK before FCM receipt")
	}
	h.srv.pushTick(ctx)
	if sent != 1 || receipts != 0 {
		t.Fatalf("replayed pending Expo delivery: send %d receipt %d", sent, receipts)
	}
	now = now.Add(receiptDelay + time.Second)
	h.srv.pushTick(ctx)
	if sent != 1 || receipts != 1 {
		t.Fatalf("receipt check: sends=%d receipts=%d", sent, receipts)
	}
	if last := core.lastCall(); !strings.Contains(last, "activity ack "+paAssistantA) ||
		!strings.Contains(last, ":notifications") {
		t.Fatalf("wrong ACK consumer or absent ACK: %q", last)
	}
	if len(h.srv.push.registry.current(h.devID).Pending) != 0 {
		t.Fatal("accepted FCM receipt not cleared")
	}
}
func TestPushOnlyOfflineAndObeysQuietHours(t *testing.T) {
	h, _ := paHarness(t, true)
	if err := h.srv.push.registry.register(h.devID, testExpoToken); err != nil {
		t.Fatal(err)
	}
	outbound := 0
	expo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outbound++
		_, _ = w.Write([]byte(`{"data":{"status":"ok","id":"receipt-one"}}`))
	}))
	defer expo.Close()
	h.srv.push.sendURL = expo.URL
	untrack := h.srv.trackConn(h.devID, func() {})
	h.srv.pushTick(context.Background())
	if outbound != 0 {
		t.Fatal("pushed while phone had live socket")
	}
	untrack()
	h.srv.pushTick(context.Background())
	if outbound != 1 {
		t.Fatal("offline phone did not get push")
	}
}
func TestPushInvalidProviderTokenDisablesOnlyThatDevice(t *testing.T) {
	h, _ := paHarness(t, true)
	if err := h.srv.push.registry.register(h.devID, testExpoToken); err != nil {
		t.Fatal(err)
	}
	expo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"status":"error","details":{"error":"DeviceNotRegistered"}}}`))
	}))
	defer expo.Close()
	h.srv.push.sendURL = expo.URL
	h.srv.pushTick(context.Background())
	if h.srv.push.registry.current(h.devID).Token != "" {
		t.Fatal("revoked FCM token was retained")
	}
}

func TestPushRefreshCannotReenableAfterOwnerDisabled(t *testing.T) {
	h, _ := paHarness(t, true)
	base := "/api/v3/notifications/push"
	rotated := "ExpoPushToken[ABCDEFGHIJKLMNOPQRSTUVWX67890]"
	if status, _ := h.send("POST", base+"/refresh", h.token, `{"token":"`+testExpoToken+`"}`); status != 409 {
		t.Fatalf("refresh enabled push without consent: %d", status)
	}
	if status, _ := h.send("POST", base+"/register", h.token, `{"token":"`+testExpoToken+`"}`); status != 200 {
		t.Fatal(status)
	}
	if status, _ := h.send("POST", base+"/refresh", h.token, `{"token":"`+rotated+`"}`); status != 200 {
		t.Fatalf("legit FCM rotation failed: %d", status)
	}
	if actual := h.srv.push.registry.current(h.devID).Token; actual != rotated {
		t.Fatalf("token rotation was not saved")
	}
	if status, _ := h.send("POST", base+"/disable", h.token, `{}`); status != 200 {
		t.Fatalf("disable: %d", status)
	}
	if status, _ := h.send("POST", base+"/refresh", h.token, `{"token":"`+testExpoToken+`"}`); status != 409 {
		t.Fatalf("background token renewal silently reenabled push: %d", status)
	}
	if h.srv.push.registry.current(h.devID).Token != "" {
		t.Fatal("opt-out not preserved")
	}
}

func TestPushQuietHoursPreventSendingAndExplicitTestDoesNotAckActivity(t *testing.T) {
	h, core := paHarness(t, true)
	c := personalagent.NewClientWithExec(func(ctx context.Context, stdin string, args ...string) ([]byte, error) {
		if len(args) >= 4 && args[1] == "proactivity" && args[2] == "show" {
			return []byte(`{"schema":1,"proactivity":{"assistantId":"` + paAssistantA + `","level":"suggest","quietNow":true,"revision":1}}`), nil
		}
		return core.exec(ctx, stdin, args...)
	})
	h.srv.opts.PersonalAgent = func(context.Context) (*personalagent.Client, bool) { return c, true }
	if err := h.srv.push.registry.register(h.devID, testExpoToken); err != nil {
		t.Fatal(err)
	}
	sent := 0
	expo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent++
		var msg map[string]any
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			t.Errorf("bad Expo payload %v", err)
		}
		if msg["title"] != "Solco · System push test" {
			t.Errorf("no sensitive data is allowed in a test message: %+v", msg)
		}
		_, _ = w.Write([]byte(`{"data":{"status":"ok","id":"ticket-test"}}`))
	}))
	defer expo.Close()
	h.srv.push.sendURL = expo.URL
	h.srv.pushTick(context.Background())
	if sent != 0 {
		t.Fatalf("quiet hours leaked automatic notification: %d", sent)
	}
	status, raw := h.send("POST", "/api/v3/notifications/push/test", h.token, `{}`)
	if status != 200 || !strings.Contains(string(raw), `"queued":true`) {
		t.Fatalf("explicit test: %d %s", status, raw)
	}
	if sent != 1 {
		t.Fatalf("manual test did not use Expo: %d", sent)
	}
	if strings.Contains(core.lastCall(), "activity ack") {
		t.Fatal("test push marked Activity seen")
	}
}

func TestPushUnknownReceiptNeverACKsAndEventuallyAllowsBoundedReplay(t *testing.T) {
	h, core := paHarness(t, true)
	now := time.Unix(1800000000, 0)
	h.srv.opts.Now = func() time.Time { return now }
	if err := h.srv.push.registry.register(h.devID, testExpoToken); err != nil {
		t.Fatal(err)
	}
	sent, receiptCalls := 0, 0
	expo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/send" {
			sent++
			_, _ = w.Write([]byte(`{"data":{"status":"ok","id":"delayed-ticket"}}`))
		} else {
			receiptCalls++
			_, _ = w.Write([]byte(`{"data":{}}`))
		}
	}))
	defer expo.Close()
	h.srv.push.sendURL = expo.URL + "/send"
	h.srv.push.receiptURL = expo.URL + "/receipt"
	h.srv.pushTick(context.Background())
	if sent != 1 {
		t.Fatalf("first send=%d", sent)
	}
	now = now.Add(receiptDelay + time.Second)
	h.srv.pushTick(context.Background())
	if receiptCalls != 1 || len(h.srv.push.registry.current(h.devID).Pending) != 1 {
		t.Fatal("unconfirmed provider receipt was prematurely discarded")
	}
	if strings.Contains(core.lastCall(), "activity ack") {
		t.Fatal("missing provider receipt ACKed facts")
	}
	now = now.Add(receiptExpiry)
	h.srv.pushTick(context.Background())
	if len(h.srv.push.registry.current(h.devID).Pending) != 0 {
		t.Fatal("missing receipt left notifications permanently blocked")
	}
	if strings.Contains(core.lastCall(), "activity ack") {
		t.Fatal("unknown receipt falsely acknowledged activity")
	}
	h.srv.pushTick(context.Background())
	if sent != 1 {
		t.Fatal("no cooldown after ambiguous receipt")
	}
	now = now.Add(uncertainRetryGap + time.Second)
	h.srv.pushTick(context.Background())
	if sent != 2 {
		t.Fatalf("long-cooldown resumption failed, sent %d", sent)
	}
}

func TestPushCorruptionDisablesOptionalPushWithoutTakingHostOffline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, pushFile), []byte("{bad"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{Config: Config{Mode: ModeLoopback, Port: 7788}, Dir: dir, Backend: &fakeBackend{instances: []string{"default"}}})
	if err != nil {
		t.Fatalf("corrupt optional push registry took Host offline: %v", err)
	}
	defer s.Close()
	if s.push != nil {
		t.Fatal("corrupt push data was ignored while push stayed enabled")
	}
}
