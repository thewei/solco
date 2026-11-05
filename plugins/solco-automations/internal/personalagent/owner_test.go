package personalagent

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func newOwnerKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// A04 owner-side entry: the first public key is registered on first use; after
// that only the holder of the current private key can change it (a signed
// rotation), so the CLI never lets a bare call swap the trust anchor.
// (The key FILE stays writable by the same user — threat model R1.)
func TestOwnerKeyIsRegisteredOnFirstUseAndRotatedOnlyBySeal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.pub")
	t.Setenv("SOLCO_OWNER_PUBKEY_FILE", path)
	e := newGrantEnv(t, false)
	api := &API{Store: e.s}

	out, err := call(t, api, ``, "assistant", "owner", "status")
	if err != nil || field(out, "owner", "configured") != false {
		t.Fatalf("status = %v err=%v", out, err)
	}
	pub1, priv1 := newOwnerKey(t)
	b64 := func(k ed25519.PublicKey) string { return base64.StdEncoding.EncodeToString(k) }
	out, err = call(t, api, fmt.Sprintf(`{"requestId":"o1","publicKey":%q}`, b64(pub1)), "assistant", "owner", "register")
	sum := sha256.Sum256(pub1)
	if err != nil || field(out, "owner", "configured") != true || field(out, "owner", "fingerprint") != hex.EncodeToString(sum[:8]) {
		t.Fatalf("register = %v err=%v", out, err)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("owner.pub = %v %v", st, err)
	}
	// Same key again is harmless; another key without a seal is refused.
	if _, err := call(t, api, fmt.Sprintf(`{"requestId":"o2","publicKey":%q}`, b64(pub1)), "assistant", "owner", "register"); err != nil {
		t.Fatalf("idempotent register: %v", err)
	}
	pub2, priv2 := newOwnerKey(t)
	if _, err := call(t, api, fmt.Sprintf(`{"requestId":"o3","publicKey":%q}`, b64(pub2)), "assistant", "owner", "register"); codeOf(err) != "owner_key_exists" {
		t.Fatalf("second key = %q", codeOf(err))
	}
	// Rotation needs a seal by the current private key over the new public key.
	bad := base64.StdEncoding.EncodeToString(ed25519.Sign(priv2, OwnerRotatePayload(b64(pub2))))
	if _, err := call(t, api, fmt.Sprintf(`{"requestId":"o4","publicKey":%q,"seal":%q}`, b64(pub2), bad), "assistant", "owner", "rotate"); codeOf(err) != "bad_seal" {
		t.Fatalf("rotate with the new key's own seal = %q", codeOf(err))
	}
	good := base64.StdEncoding.EncodeToString(ed25519.Sign(priv1, OwnerRotatePayload(b64(pub2))))
	out, err = call(t, api, fmt.Sprintf(`{"requestId":"o5","publicKey":%q,"seal":%q}`, b64(pub2), good), "assistant", "owner", "rotate")
	sum2 := sha256.Sum256(pub2)
	if err != nil || field(out, "owner", "fingerprint") != hex.EncodeToString(sum2[:8]) {
		t.Fatalf("rotate = %v err=%v", out, err)
	}
	// A seal of the old key is spent: it does not replay onto a third key.
	pub3, _ := newOwnerKey(t)
	if _, err := call(t, api, fmt.Sprintf(`{"requestId":"o6","publicKey":%q,"seal":%q}`, b64(pub3), good), "assistant", "owner", "rotate"); codeOf(err) != "bad_seal" {
		t.Fatalf("replayed seal = %q", codeOf(err))
	}
}

func TestOwnerCommandsAreNotTheCoordinators(t *testing.T) {
	t.Setenv("SOLCO_OWNER_PUBKEY_FILE", filepath.Join(t.TempDir(), "owner.pub"))
	e := newGrantEnv(t, false)
	api := &API{Store: e.s}
	for _, c := range []string{"status", "register", "rotate"} {
		if _, err := call(t, api, `{}`, "assistant", "--as", e.ada.ID, "owner", c); codeOf(err) != "forbidden" {
			t.Fatalf("owner %s under --as = %q", c, codeOf(err))
		}
	}
}

// The root module signs grants without importing this package, so both sides
// pin the same bytes: a fixed key and grant produce a fixed payload and seal.
func TestGrantSealGoldenVector(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = 7
	}
	priv := ed25519.NewKeyFromSeed(seed)
	in := GrantInput{ID: "gr_00000000000000aa", AssistantID: "a_0000000000000001", Capability: CapMemoryAutosave, Effect: "allow",
		Scope: map[string][]string{"kind": {"preference", "fact"}}, MaxPerDay: 5, IssuedAt: 1800000000000, ExpiresAt: 1800086400000}
	payload := string(GrantPayload(in))
	const wantPayload = "solco-grant-v1\n{\"id\":\"gr_00000000000000aa\",\"assistantId\":\"a_0000000000000001\",\"capability\":\"memory.autosave\",\"effect\":\"allow\",\"scope\":{\"kind\":[\"fact\",\"preference\"]},\"maxPerDay\":5,\"issuedAt\":1800000000000,\"expiresAt\":1800086400000}"
	seal := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(payload)))
	if payload != wantPayload {
		t.Fatalf("payload = %q", payload)
	}
	const wantSeal = "j7x5VuKXUtOepb0z2POGe9AH1E2ofJlnU9Sq6mnJ+TYcpmeA8EYzzPThOYYfRcxU3QFWwt7ns8y72T+8gnL7BA=="
	if seal != wantSeal {
		t.Fatalf("seal = %q", seal)
	}
}
