package nativeui

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thewei/solco/internal/personalagent"
)

// ownerCore plays the plugin's owner/grant commands: a registered public key
// that refuses a different one, and a create that really verifies the seal.
type ownerCore struct {
	pub    ed25519.PublicKey
	grants []personalagent.Grant
	calls  []string
}

func (o *ownerCore) exec(_ context.Context, stdin string, args ...string) ([]byte, error) {
	line := strings.Join(args[1:], " ")
	o.calls = append(o.calls, line)
	var body map[string]any
	_ = json.Unmarshal([]byte(stdin), &body)
	owner := func() string {
		if o.pub == nil {
			return `{"configured":false}`
		}
		sum := sha256.Sum256(o.pub)
		return `{"configured":true,"fingerprint":"` + hex.EncodeToString(sum[:8]) + `"}`
	}
	switch {
	case line == "overview":
		return []byte(assistantsOverviewJSON), nil
	case line == "owner status":
		return []byte(`{"schema":1,"owner":` + owner() + `}`), nil
	case line == "owner register":
		raw, _ := base64.StdEncoding.DecodeString(body["publicKey"].(string))
		if o.pub != nil && string(o.pub) != string(raw) {
			return []byte(`{"schema":1,"error":{"code":"owner_key_exists","message":"x"}}`), context.Canceled
		}
		o.pub = raw
		return []byte(`{"schema":1,"owner":` + owner() + `}`), nil
	case strings.HasPrefix(line, "grant list"):
		raw, _ := json.Marshal(o.grants)
		return []byte(`{"schema":1,"grants":` + string(raw) + `,"capabilities":["memory.autosave"]}`), nil
	case line == "grant create":
		var in personalagent.GrantInput
		_ = json.Unmarshal([]byte(stdin), &in)
		sig, _ := base64.StdEncoding.DecodeString(in.Seal)
		if o.pub == nil || !ed25519.Verify(o.pub, personalagent.GrantPayload(in), sig) {
			return []byte(`{"schema":1,"error":{"code":"bad_seal","message":"x"}}`), context.Canceled
		}
		g := personalagent.Grant{ID: in.ID, AssistantID: in.AssistantID, Capability: in.Capability, Effect: in.Effect, Scope: in.Scope,
			MaxPerDay: in.MaxPerDay, IssuedAt: in.IssuedAt, ExpiresAt: in.ExpiresAt, IssuedBy: "owner", Sealed: true, Verified: true, Status: "active"}
		o.grants = append(o.grants, g)
		raw, _ := json.Marshal(g)
		return []byte(`{"schema":1,"replayed":false,"grant":` + string(raw) + `}`), nil
	case strings.HasPrefix(line, "grant revoke "):
		for i := range o.grants {
			if o.grants[i].ID == strings.TrimPrefix(line, "grant revoke ") {
				o.grants[i].Status = "revoked"
				raw, _ := json.Marshal(o.grants[i])
				return []byte(`{"schema":1,"replayed":false,"grant":` + string(raw) + `}`), nil
			}
		}
	}
	return []byte(`{"schema":1,"error":{"code":"internal","message":"unexpected ` + line + `"}}`), context.Canceled
}

func TestAutosaveIsSealedBySolcosOwnKeyMadeOnFirstUse(t *testing.T) {
	_ = appSecrets.Delete(ownerKeySecret)
	t.Cleanup(func() { _ = appSecrets.Delete(ownerKeySecret) })
	stubAssistants(t, false)
	core := &ownerCore{}
	newAssistantsClient = func(string, string) *personalagent.Client { return personalagent.NewClientWithExec(core.exec) }
	b := NewAssistantsBridge(openAssistantsShell(t))
	ctx := context.Background()

	v, err := b.Autosave(ctx, "a_1")
	if err != nil || v.OwnerKey != "none" || len(v.Grants) != 0 || len(v.Kinds) != 2 {
		t.Fatalf("before = %+v err=%v", v, err)
	}
	if err := b.EnableAutosave(ctx, "r1", "a_1", []string{"preference"}, 5, 30); err != nil {
		t.Fatal(err)
	}
	if core.pub == nil {
		t.Fatal("the first Enable must register the owner's public key")
	}
	if raw, ok := appSecrets.Get(ownerKeySecret); !ok || strings.Contains(strings.Join(core.calls, "|"), raw) {
		t.Fatal("the private key must live in Solco's store and never travel to the core")
	}
	v, _ = b.Autosave(ctx, "a_1")
	if v.OwnerKey != "ready" || len(v.Grants) != 1 || v.Grants[0].Scope["kind"][0] != "preference" || !v.Grants[0].Verified {
		t.Fatalf("after = %+v", v)
	}
	// A second grant reuses the same key (no second registration is accepted anyway).
	if err := b.EnableAutosave(ctx, "r2", "a_1", []string{"fact"}, 0, 7); err != nil {
		t.Fatal(err)
	}
	if err := b.RevokeAutosave(ctx, "r3", "a_1", v.Grants[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := b.RevokeAutosave(ctx, "r4", "a_1", "gr_ffffffffffffffff"); err == nil {
		t.Fatal("a grant the Assistant does not hold was revoked")
	}
}

func TestAutosaveRefusesWhatItCannotSealOrWhatIsOutOfBounds(t *testing.T) {
	_ = appSecrets.Delete(ownerKeySecret)
	t.Cleanup(func() { _ = appSecrets.Delete(ownerKeySecret) })
	other, _, _ := ed25519.GenerateKey(nil)
	stubAssistants(t, false)
	core := &ownerCore{pub: other}
	newAssistantsClient = func(string, string) *personalagent.Client { return personalagent.NewClientWithExec(core.exec) }
	b := NewAssistantsBridge(openAssistantsShell(t))
	ctx := context.Background()
	if v, _ := b.Autosave(ctx, "a_1"); v.OwnerKey != "foreign" {
		t.Fatalf("a key Solco does not hold must read as foreign: %+v", v)
	}
	if err := b.EnableAutosave(ctx, "r1", "a_1", []string{"fact"}, 1, 7); err == nil || !strings.Contains(err.Error(), "owner_key_foreign") {
		t.Fatalf("foreign key: %v", err)
	}
	for _, c := range []struct {
		kinds     []string
		max, days int
	}{{nil, 1, 7}, {[]string{"secret"}, 1, 7}, {[]string{"fact"}, 1, 0}, {[]string{"fact"}, 1, 91}, {[]string{"fact"}, -1, 7}, {[]string{"fact"}, 1001, 7}} {
		if err := b.EnableAutosave(ctx, "rb", "a_1", c.kinds, c.max, c.days); err == nil || !strings.HasPrefix(err.Error(), "invalid") {
			t.Fatalf("%+v: %v", c, err)
		}
	}
	for _, line := range core.calls {
		if line == "grant create" {
			t.Fatal("a refused request reached the core")
		}
	}
}

func TestBudgetBridgeReadsTheLedgerAndKeepsTokensUnmeasured(t *testing.T) {
	stubAssistants(t, false)
	b := NewAssistantsBridge(openAssistantsShell(t))
	got, err := b.Budget(context.Background(), "a_1")
	if err != nil || got.Remaining != 0 || got.Spent["trigger"] != 1 || len(got.Held) != 1 || got.Held[0].Reason != "budget" {
		t.Fatalf("budget = %+v err=%v", got, err)
	}
	if len(got.Unmeasured) != 2 {
		t.Fatalf("tokens and cost must be reported as unmeasured: %v", got.Unmeasured)
	}
}
