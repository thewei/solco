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
	build.Env = append(os.Environ(), "GOTOOLCHAIN=auto", "GOWORK=off") // the plugin is its own module, outside the local go.work
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

// The management surface and the ingress share one core: a key made through
// the authenticated route delivers, shows up (without its secret) in the
// Autonomy read next to the receipt, and stops the moment it is revoked.
func TestAutonomyManagementAgainstTheRealCore(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the plugin")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(root, "bin", "solco-automations"), ".")
	build.Dir = "../../plugins/solco-automations"
	build.Env = append(os.Environ(), "GOTOOLCHAIN=auto", "GOWORK=off") // the plugin is its own module, outside the local go.work
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
	asst := run(`{"requestId":"mg-asst-1","name":"Ada"}`, "create")["assistant"].(map[string]any)["id"].(string)
	other := run(`{"requestId":"mg-asst-2","name":"Bob"}`, "create")["assistant"].(map[string]any)["id"].(string)
	task := run(fmt.Sprintf(`{"requestId":"mg-task-1","assistantId":%q,"title":"Watch CI","brief":"b","kind":"research"}`, asst), "task", "create")["task"].(map[string]any)["id"].(string)
	base := "/api/v3/personal-agent/assistants/" + asst

	status, raw := h.send("POST", base+"/hooks/keys", h.token, `{"requestId":"mg-key-0001","sources":["github"]}`)
	if status != http.StatusOK {
		t.Fatalf("create key = %d %s", status, raw)
	}
	var made struct {
		Key    struct{ ID string }
		Secret string
		Path   string
	}
	_ = json.Unmarshal(raw, &made)
	if len(made.Secret) != 64 || made.Path != "/api/v3/hooks/assistants/"+asst+"/wake" {
		t.Fatalf("created = %s", raw)
	}

	deliver := func(delivery string) int {
		t.Helper()
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		body := fmt.Sprintf(`{"source":"github","event":"check.requested","resource":{"type":"task","id":%q},"reason":"CI failed"}`, task)
		req, _ := http.NewRequest("POST", h.http.URL+made.Path, strings.NewReader(body))
		req.Header.Set("X-Solco-Hook-Key", made.Key.ID)
		req.Header.Set("X-Solco-Timestamp", ts)
		req.Header.Set("X-Solco-Delivery", delivery)
		req.Header.Set("X-Solco-Signature", hookSig(made.Secret, made.Path, ts, delivery, body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if got := deliver("mg-delivery-1"); got != http.StatusAccepted {
		t.Fatalf("delivery with the new key = %d", got)
	}

	read := string(bodyOf(t, h.get(base+"/autonomy", h.token, nil)))
	if !strings.Contains(read, made.Key.ID) || !strings.Contains(read, `"routing"`) || !strings.Contains(read, "CI failed") {
		t.Fatalf("autonomy = %s", read)
	}
	if strings.Contains(read, made.Secret) || strings.Contains(strings.ToLower(read), "secret") {
		t.Fatalf("the secret is listed: %s", read)
	}

	// Bob's path cannot revoke Ada's key; Ada's can, and then deliveries stop.
	if status, _ = h.send("POST", "/api/v3/personal-agent/assistants/"+other+"/hooks/keys/"+made.Key.ID+"/revoke", h.token, `{"requestId":"mg-rev-0001"}`); status != http.StatusNotFound {
		t.Fatalf("revoke through another Assistant = %d", status)
	}
	if got := deliver("mg-delivery-2"); got != http.StatusAccepted {
		t.Fatalf("delivery after a refused revoke = %d", got)
	}
	if status, raw = h.send("POST", base+"/hooks/keys/"+made.Key.ID+"/revoke", h.token, `{"requestId":"mg-rev-0002"}`); status != http.StatusOK || !strings.Contains(string(raw), `"revoked"`) {
		t.Fatalf("revoke = %d %s", status, raw)
	}
	if got := deliver("mg-delivery-3"); got != http.StatusUnauthorized {
		t.Fatalf("delivery with a revoked key = %d", got)
	}

	// A draft trigger cannot be paused (it never fired); it can be withdrawn.
	trg := run(fmt.Sprintf(`{"requestId":"mg-trg-0001","assistantId":%q,"name":"diagnose","on":"task.failed","taskId":%q,"title":"Diagnose","brief":"why"}`, asst, task), "trigger", "draft")["trigger"].(map[string]any)
	tid := trg["id"].(string)
	if status, _ = h.send("POST", base+"/triggers/"+tid+"/pause", h.token, `{"requestId":"mg-tp-0001","expectedRevision":1}`); status != http.StatusConflict {
		t.Fatalf("pausing a draft = %d", status)
	}
	if status, raw = h.send("POST", base+"/triggers/"+tid+"/cancel", h.token, `{"requestId":"mg-tp-0002","expectedRevision":1}`); status != http.StatusOK || !strings.Contains(string(raw), `"withdrawn"`) {
		t.Fatalf("withdraw = %d %s", status, raw)
	}
}
