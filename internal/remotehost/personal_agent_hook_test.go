package remotehost

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thewei/solco/internal/personalagent"
)

/**
 * [INPUT]: server_test.go 的 harness、真实 plugins/solco-automations 二进制（测试内 go build）与一次性 SQLite
 * [OUTPUT]: Webhook 入站的 Host→CLI→SQLite 真核心回归：签名/重放/篡改/吊销/越权/限流/体积，且设备 Bearer 既不需要也无效
 * [POS]: personal_agent_hook.go 的整线证明（HOOK-01/02/04 的 Host 面）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
func hookSig(secret, path, ts, delivery, body string) string {
	sum := sha256.Sum256([]byte(body))
	m := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(m, "POST\n%s\n%s\n%s\n%s", path, ts, delivery, hex.EncodeToString(sum[:]))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestWebhookAgainstTheRealCore(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the plugin")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(root, "bin", "solco-automations"), ".")
	build.Dir = "../../plugins/solco-automations"
	build.Env = append(os.Environ(), "GOTOOLCHAIN=auto")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	db := filepath.Join(t.TempDir(), "a.sqlite")
	client := personalagent.NewClient(root, []string{"SOLCO_ASSISTANTS_DB=" + db})
	h := newHarness(t)
	h.srv.opts.PersonalAgent = func(context.Context) (*personalagent.Client, bool) { return client, true }

	run := func(body string, args ...string) map[string]any {
		t.Helper()
		cmd := exec.Command(filepath.Join(root, "bin", "solco-automations"), append([]string{"assistant"}, args...)...)
		cmd.Env = append(os.Environ(), "SOLCO_ASSISTANTS_DB="+db)
		cmd.Stdin = strings.NewReader(body)
		out, _ := cmd.Output()
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil || m["error"] != nil {
			t.Fatalf("%v: %s", args, out)
		}
		return m
	}
	asst := run(`{"requestId":"hook-asst-1","name":"Ada"}`, "create")["assistant"].(map[string]any)["id"].(string)
	task := run(fmt.Sprintf(`{"requestId":"hook-task-1","assistantId":%q,"title":"Watch CI","brief":"b","kind":"research"}`, asst), "task", "create")["task"].(map[string]any)["id"].(string)
	keyOut := run(fmt.Sprintf(`{"requestId":"hook-key-001","assistantId":%q,"sources":["github"]}`, asst), "hook", "key", "create")
	keyID := keyOut["key"].(map[string]any)["id"].(string)
	secret := keyOut["secret"].(string)
	path := "/api/v3/hooks/assistants/" + asst + "/wake"

	post := func(delivery, body string, mut func(*http.Request)) (int, []byte) {
		t.Helper()
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		req, _ := http.NewRequest("POST", h.http.URL+path, strings.NewReader(body))
		req.Header.Set("X-Solco-Hook-Key", keyID)
		req.Header.Set("X-Solco-Timestamp", ts)
		req.Header.Set("X-Solco-Delivery", delivery)
		req.Header.Set("X-Solco-Signature", hookSig(secret, path, ts, delivery, body))
		if mut != nil {
			mut(req)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, raw
	}
	body := fmt.Sprintf(`{"source":"github","event":"check.requested","resource":{"type":"task","id":%q},"reason":"CI failed"}`, task)

	status, raw := post("gh-delivery-100", body, nil) // no device Bearer: none is needed
	if status != http.StatusAccepted {
		t.Fatalf("accepted: %d %s", status, raw)
	}
	var rec struct {
		EventID, Receipt, Routing string
		Replayed                  bool
	}
	_ = json.Unmarshal(raw, &rec)
	if rec.Receipt != "accepted" || rec.EventID == "" || rec.Replayed {
		t.Fatalf("receipt = %s", raw)
	}
	if status, raw = post("gh-delivery-100", body, nil); status != http.StatusAccepted || !strings.Contains(string(raw), `"replayed":true`) {
		t.Fatalf("replay: %d %s", status, raw)
	}
	if status, raw = post("gh-delivery-100", strings.Replace(body, "CI failed", "other", 1), nil); status != http.StatusConflict {
		t.Fatalf("same id other body: %d %s", status, raw)
	}
	if status, _ = post("gh-delivery-101", body, func(r *http.Request) { r.Header.Set("X-Solco-Signature", "sha256="+strings.Repeat("0", 64)) }); status != http.StatusUnauthorized {
		t.Fatalf("bad signature: %d", status)
	}
	if status, _ = post("gh-delivery-102", body, func(r *http.Request) { r.Header.Set("X-Solco-Hook-Key", "hk_0000000000000000") }); status != http.StatusUnauthorized {
		t.Fatalf("unknown key: %d", status)
	}
	if status, _ = post("gh-delivery-103", body, func(r *http.Request) { r.Header.Del("X-Solco-Signature") }); status != http.StatusUnauthorized {
		t.Fatalf("no signature: %d", status)
	}
	if status, _ = post("gh-delivery-104", `{"source":"github","event":"check.requested","extra":1}`, nil); status != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", status)
	}
	if status, _ = post("gh-delivery-105", strings.Repeat("x", maxHookBody+1), nil); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize: %d", status)
	}
	// An authorised paired device's Bearer is no substitute for the key.
	if status, _ = post("gh-delivery-106", body, func(r *http.Request) {
		r.Header.Del("X-Solco-Signature")
		r.Header.Set("Authorization", "Bearer "+h.token)
	}); status != http.StatusUnauthorized {
		t.Fatalf("device token used as a hook key: %d", status)
	}
	run(`{"requestId":"hook-revoke-1"}`, "hook", "key", "revoke", keyID)
	if status, _ = post("gh-delivery-107", body, nil); status != http.StatusUnauthorized {
		t.Fatalf("revoked key: %d", status)
	}
	evs := run("", "event", "list", asst, "--limit", "100")
	n := 0
	for _, e := range evs["events"].([]any) {
		if e.(map[string]any)["kind"] == "hook.check" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("hook events = %d, want exactly 1 (%v)", n, evs)
	}
}
