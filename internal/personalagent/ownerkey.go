package personalagent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"sort"
)

// The owner's side of the grant model (A04). The core only verifies; Solco —
// the owner's client — holds the private key and seals each grant. The byte
// layout below is the plugin's `GrantPayload`/`OwnerRotatePayload`, repeated
// here because the root module never imports the plugin; both sides pin the
// same golden vector (TestGrantSealGoldenVector in each package).

// Grant is one standing, owner-sealed permission as the core lists it.
type Grant struct {
	ID          string              `json:"id"`
	AssistantID string              `json:"assistantId"`
	Capability  string              `json:"capability"`
	Effect      string              `json:"effect"`
	Scope       map[string][]string `json:"scope"`
	MaxPerDay   int                 `json:"maxPerDay"`
	IssuedAt    int64               `json:"issuedAt"`
	ExpiresAt   int64               `json:"expiresAt"`
	RevokedAt   int64               `json:"revokedAt,omitempty"`
	IssuedBy    string              `json:"issuedBy"`
	Sealed      bool                `json:"sealed"`
	Verified    bool                `json:"verified"`
	Status      string              `json:"status"`
}

// GrantInput is what the owner's client sends: it picks the id and the times
// (they are part of what it seals) and attaches the seal.
type GrantInput struct {
	ID          string              `json:"id"`
	AssistantID string              `json:"assistantId"`
	Capability  string              `json:"capability"`
	Effect      string              `json:"effect"`
	Scope       map[string][]string `json:"scope"`
	MaxPerDay   int                 `json:"maxPerDay"`
	IssuedAt    int64               `json:"issuedAt"`
	ExpiresAt   int64               `json:"expiresAt"`
	Seal        string              `json:"seal"`
}

// OwnerStatus is whether the core has an owner public key.
type OwnerStatus struct {
	Configured  bool   `json:"configured"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

func normalizeScope(scope map[string][]string) map[string][]string {
	out := map[string][]string{}
	for k, vs := range scope {
		clean := append([]string{}, vs...)
		sort.Strings(clean)
		w := clean[:0]
		for i, v := range clean {
			if i == 0 || v != clean[i-1] {
				w = append(w, v)
			}
		}
		out[k] = w
	}
	return out
}

// GrantPayload is the canonical byte string that gets sealed.
func GrantPayload(in GrantInput) []byte {
	raw, _ := json.Marshal(struct {
		ID          string              `json:"id"`
		AssistantID string              `json:"assistantId"`
		Capability  string              `json:"capability"`
		Effect      string              `json:"effect"`
		Scope       map[string][]string `json:"scope"`
		MaxPerDay   int                 `json:"maxPerDay"`
		IssuedAt    int64               `json:"issuedAt"`
		ExpiresAt   int64               `json:"expiresAt"`
	}{in.ID, in.AssistantID, in.Capability, in.Effect, normalizeScope(in.Scope), in.MaxPerDay, in.IssuedAt, in.ExpiresAt})
	return append([]byte("solco-grant-v1\n"), raw...)
}

// RotatePayload is what the current private key seals to hand the anchor on.
func RotatePayload(newPublicKeyB64 string) []byte {
	return []byte("solco-owner-rotate-v1\n" + newPublicKeyB64)
}

// SealGrant signs a grant with the owner's private key.
func SealGrant(priv ed25519.PrivateKey, in GrantInput) GrantInput {
	in.Seal = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, GrantPayload(in)))
	return in
}

// SealRotation signs the hand-over to a new public key.
func SealRotation(priv ed25519.PrivateKey, newPublicKeyB64 string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, RotatePayload(newPublicKeyB64)))
}

// NewOwnerKey makes a fresh owner key pair; the public half is base64.
func NewOwnerKey() (priv ed25519.PrivateKey, publicB64 string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	return priv, base64.StdEncoding.EncodeToString(pub), nil
}
