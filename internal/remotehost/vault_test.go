package remotehost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thewei/solco/internal/vaultlink"
)

type fakeVault struct {
	mu      sync.Mutex
	pending vaultlink.Pending
	items   []vaultlink.Item
	value   string
	err     error // returned by every call
	allowed map[int]bool
	revealN int
}

func (f *fakeVault) Pending(context.Context) (vaultlink.Pending, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pending, f.err
}
func (f *fakeVault) Answer(_ context.Context, id int, allow bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if f.allowed == nil {
		f.allowed = map[int]bool{}
	}
	f.allowed[id] = allow
	return nil
}
func (f *fakeVault) Browse(context.Context) ([]vaultlink.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.items, f.err
}
func (f *fakeVault) Reveal(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revealN++
	return f.value, f.err
}
func (f *fakeVault) set(err error) { f.mu.Lock(); f.err = err; f.mu.Unlock() }

func sampleVault() *fakeVault {
	return &fakeVault{
		pending: vaultlink.Pending{State: "unlocked", CanReveal: true, Approvals: []vaultlink.Question{
			{ID: 7, Project: "api", Command: "make deploy", Refs: []vaultlink.Ref{{Ref: "vault://agent/prod/pg#password", Standing: "new"}, {Ref: "vault://agent/prod/pg#username", Standing: "new"}}, Asked: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)},
		}},
		items: []vaultlink.Item{{Path: "agent/prod/pg", Kind: "login", Fields: []string{"username", "password"}}, {Path: "agent/prod/app-env", Kind: "env", Fields: []string{"DB_HOST"}}},
		value: "demo-value-123",
	}
}

func TestVaultAbsentWithoutSource(t *testing.T) {
	h := newHarness(t)
	if resp := h.get("/api/v3/vault", h.token, nil); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "vault_unavailable" {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if code, _ := h.send("POST", "/api/v3/vault/reveal", h.token, `{"ref":"vault://a/b#c"}`); code != http.StatusNotFound {
		t.Fatalf("reveal without a source: %d", code)
	}
}

func TestVaultStatesAndErrors(t *testing.T) {
	h := newHarness(t)
	v := sampleVault()
	h.srv.opts.Vault = v

	// off / offline are states the page draws, not errors
	v.set(&vaultlink.Error{Code: vaultlink.CodePhoneOff})
	body := string(bodyOf(t, h.get("/api/v3/vault", h.token, nil)))
	if !strings.Contains(body, `"online":true`) || !strings.Contains(body, `"access":false`) || !strings.Contains(body, `"approvals":[]`) {
		t.Fatalf("phone off: %s", body)
	}
	v.set(vaultlink.ErrOffline)
	if body := string(bodyOf(t, h.get("/api/v3/vault", h.token, nil))); !strings.Contains(body, `"online":false`) {
		t.Fatalf("offline: %s", body)
	}

	cases := []struct {
		err    error
		status int
		code   string
	}{
		{vaultlink.ErrOffline, http.StatusServiceUnavailable, "vault_offline"},
		{&vaultlink.Error{Code: vaultlink.CodePhoneOff}, http.StatusConflict, "vault_phone_off"},
		{&vaultlink.Error{Code: vaultlink.CodeRevealOff}, http.StatusForbidden, "vault_reveal_off"},
		{&vaultlink.Error{Code: vaultlink.CodeLocked}, http.StatusConflict, "vault_locked"},
		{&vaultlink.Error{Code: vaultlink.CodeNotFound}, http.StatusNotFound, "vault_not_found"},
		{&vaultlink.Error{Code: vaultlink.CodeNote}, http.StatusUnprocessableEntity, "vault_note"},
		{&vaultlink.Error{Code: vaultlink.CodeGone}, http.StatusGone, "vault_gone"},
		{&vaultlink.Error{Code: "weird"}, http.StatusBadGateway, "vault_failed"},
	}
	for _, c := range cases {
		v.set(c.err)
		code, body := h.send("POST", "/api/v3/vault/reveal", h.token, `{"ref":"vault://agent/prod/pg#password"}`)
		if code != c.status || codeOf(body) != c.code {
			t.Fatalf("reveal %v: %d %s", c.err, code, body)
		}
	}
}

func TestVaultAnswerAndRevealRules(t *testing.T) {
	h := newHarness(t)
	v := sampleVault()
	h.srv.opts.Vault = v
	if code, _ := h.send("POST", "/api/v3/vault/approvals/7/answer", h.token, `{"allow":true}`); code != http.StatusOK || !v.allowed[7] {
		t.Fatalf("allow: %d %v", code, v.allowed)
	}
	if code, _ := h.send("POST", "/api/v3/vault/approvals/7/answer", h.token, `{"allow":false}`); code != http.StatusOK || v.allowed[7] {
		t.Fatalf("deny: %d %v", code, v.allowed)
	}
	for _, bad := range []struct{ path, body string }{
		{"/api/v3/vault/approvals/x/answer", `{"allow":true}`},
		{"/api/v3/vault/approvals/0/answer", `{"allow":true}`},
		{"/api/v3/vault/approvals/7/answer", `{}`},
		{"/api/v3/vault/approvals/7/answer", `nope`},
		{"/api/v3/vault/reveal", `{"ref":"vault://agent/prod/pg"}`},
		{"/api/v3/vault/reveal", `{}`},
	} {
		if code, _ := h.send("POST", bad.path, h.token, bad.body); code != http.StatusBadRequest {
			t.Fatalf("%s %s: %d", bad.path, bad.body, code)
		}
	}
	if v.revealN != 0 {
		t.Fatal("a malformed reveal reached the Vault")
	}
	req, _ := http.NewRequest("POST", h.http.URL+"/api/v3/vault/reveal", strings.NewReader(`{"ref":"vault://agent/prod/pg#password"}`))
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("reveal: %d %v", resp.StatusCode, resp.Header)
	}
	if !strings.Contains(string(raw), "demo-value-123") {
		t.Fatal("reveal lost the value")
	}
}

func TestVaultWatchSendsOneFramePerQuestion(t *testing.T) {
	h := newHarness(t)
	v := &fakeVault{}
	h.srv.opts.Vault = v
	q, unsub := h.srv.subscribeNotices()
	defer unsub()
	seen := map[int]bool{}
	ctx := context.Background()

	h.srv.vaultPoll(ctx, v, seen) // nothing waiting
	v.mu.Lock()
	v.pending = sampleVault().pending
	v.mu.Unlock()
	h.srv.vaultPoll(ctx, v, seen)
	h.srv.vaultPoll(ctx, v, seen) // the same question again: no second frame
	select {
	case f := <-q:
		if f["t"] != "vault" || f["kind"] != "approval" || f["id"] != 7 || f["project"] != "api" || f["waiting"] != 1 {
			t.Fatalf("frame: %v", f)
		}
		if p, _ := f["preview"].(string); !strings.Contains(p, "agent/prod/pg#password") || strings.Contains(p, "vault://") {
			t.Fatalf("preview: %q", p)
		}
	default:
		t.Fatal("no frame for the new question")
	}
	select {
	case f := <-q:
		t.Fatalf("second frame: %v", f)
	default:
	}
	v.mu.Lock()
	v.pending.Approvals = nil
	v.mu.Unlock()
	h.srv.vaultPoll(ctx, v, seen)
	select {
	case f := <-q:
		if f["kind"] != "resolved" || f["waiting"] != 0 {
			t.Fatalf("resolved: %v", f)
		}
	default:
		t.Fatal("no resolved frame")
	}
	// an offline Vault forgets what it saw: the same id after a restart is new again
	v.set(vaultlink.ErrOffline)
	seen[7] = true
	h.srv.vaultPoll(ctx, v, seen)
	if len(seen) != 0 {
		t.Fatal("offline did not reset the seen set")
	}
}

type unsetVault struct{ *fakeVault }

func (unsetVault) Available() bool { return false }

func TestVaultIsOfferedOnlyWhereItIsSetUp(t *testing.T) {
	h := newHarness(t)
	caps := func() bool {
		var boot struct{ Capabilities map[string]bool }
		if err := json.Unmarshal(bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)), &boot); err != nil {
			t.Fatal(err)
		}
		return boot.Capabilities["vault"]
	}
	if caps() {
		t.Fatal("advertised without a source")
	}
	h.srv.opts.Vault = unsetVault{sampleVault()}
	if caps() {
		t.Fatal("advertised although Solco Vault is not set up")
	}
	h.srv.opts.Vault = sampleVault()
	if !caps() {
		t.Fatal("not advertised")
	}
}

// launchVault is a Vault source that also knows whether the app is installed and can start it.
type launchVault struct {
	*fakeVault
	install  vaultlink.Install
	launchEr error
	launches int
}

func (l *launchVault) Installed() vaultlink.Install { return l.install }
func (l *launchVault) Launch(context.Context) error {
	l.launches++
	return l.launchEr
}

func TestVaultLaunch(t *testing.T) {
	h := newHarness(t)

	// a source that cannot launch keeps the page as it was: no bit, no route
	h.srv.opts.Vault = sampleVault()
	if body := string(bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil))); strings.Contains(body, "vaultLaunch") {
		t.Fatalf("advertised without a launcher: %s", body)
	}
	if code, _ := h.send("POST", "/api/v3/vault/launch", h.token, ""); code != http.StatusNotFound {
		t.Fatalf("launch without a launcher: %d", code)
	}

	lv := &launchVault{fakeVault: sampleVault(), install: vaultlink.InstallYes}
	lv.set(vaultlink.ErrOffline)
	h.srv.opts.Vault = lv
	if body := string(bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil))); !strings.Contains(body, `"vaultLaunch":true`) {
		t.Fatalf("not advertised: %s", body)
	}
	// installed + not running: the page learns it can offer "Open on Mac"
	if body := string(bodyOf(t, h.get("/api/v3/vault", h.token, nil))); !strings.Contains(body, `"installed":true`) || !strings.Contains(body, `"online":false`) {
		t.Fatalf("installed offline: %s", body)
	}
	lv.install = vaultlink.InstallNo
	if body := string(bodyOf(t, h.get("/api/v3/vault", h.token, nil))); !strings.Contains(body, `"installed":false`) {
		t.Fatalf("not installed: %s", body)
	}
	lv.install = vaultlink.InstallUnknown
	if body := string(bodyOf(t, h.get("/api/v3/vault", h.token, nil))); strings.Contains(body, "installed") {
		t.Fatalf("unknown must stay silent: %s", body)
	}

	cases := []struct {
		err    error
		status int
		code   string
	}{
		{vaultlink.ErrNotInstalled, http.StatusNotFound, "vault_not_installed"},
		{vaultlink.ErrAlreadyRunning, http.StatusConflict, "vault_already_running"},
		{context.DeadlineExceeded, http.StatusBadGateway, "vault_launch_failed"},
	}
	for _, c := range cases {
		lv.launchEr = c.err
		code, raw := h.send("POST", "/api/v3/vault/launch", h.token, "")
		if code != c.status || !strings.Contains(string(raw), c.code) {
			t.Fatalf("%v -> %d %s", c.err, code, raw)
		}
	}
	lv.launchEr = nil
	before := lv.launches
	if code, body := h.send("POST", "/api/v3/vault/launch", h.token, ""); code != http.StatusOK || !strings.Contains(string(body), `"ok":true`) || lv.launches != before+1 {
		t.Fatalf("launch: %d %s", code, body)
	}
}
