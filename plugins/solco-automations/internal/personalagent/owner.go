package personalagent

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The owner's trust anchor (A04, user decision 2026-11-05: stay at L1). The
// owner's client holds the private key; the core holds only the public key in
// OwnerKeyPath(). The first public key is registered on first use; after that
// the CLI replaces it only for a rotation sealed by the current private key, so
// no bare call swaps the anchor. The file itself remains writable by the same
// OS user — docs/assistant-owner-agent-identity-threat-model.md, R1.

// OwnerStatus is what the owner surfaces may show: never the key material
// beyond a short fingerprint, never the path.
type OwnerStatus struct {
	Configured  bool   `json:"configured"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

// OwnerRotatePayload is what the current private key seals to hand the anchor
// to a new public key.
func OwnerRotatePayload(newPublicKeyB64 string) []byte {
	return []byte("solco-owner-rotate-v1\n" + newPublicKeyB64)
}

func readOwnerKey(path string) (ed25519.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseOwnerKey(string(raw), path)
}

func parseOwnerKey(text, where string) (ed25519.PublicKey, error) {
	text = strings.TrimSpace(text)
	key, err := hex.DecodeString(text)
	if err != nil || len(key) != ed25519.PublicKeySize {
		if key, err = base64.StdEncoding.DecodeString(text); err != nil || len(key) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("owner public key %s is not a 32-byte Ed25519 key", where)
		}
	}
	return ed25519.PublicKey(key), nil
}

func ownerStatusOf(pub ed25519.PublicKey) OwnerStatus {
	if len(pub) == 0 {
		return OwnerStatus{}
	}
	sum := sha256.Sum256(pub)
	return OwnerStatus{Configured: true, Fingerprint: hex.EncodeToString(sum[:8])}
}

// OwnerKeyStatus reports whether an owner key is registered.
func OwnerKeyStatus(path string) (OwnerStatus, error) {
	pub, err := readOwnerKey(path)
	if errors.Is(err, os.ErrNotExist) {
		return OwnerStatus{}, nil
	}
	if err != nil {
		return OwnerStatus{}, err
	}
	return ownerStatusOf(pub), nil
}

func writeOwnerKey(path string, pub ed25519.PublicKey) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".owner.pub-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(base64.StdEncoding.EncodeToString(pub) + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// RegisterOwnerKey stores the first owner public key. The same key again is a
// no-op; a different one needs RotateOwnerKey.
func RegisterOwnerKey(path, publicKeyB64 string) (OwnerStatus, error) {
	pub, err := parseOwnerKey(publicKeyB64, "from the request")
	if err != nil {
		return OwnerStatus{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	cur, err := readOwnerKey(path)
	switch {
	case err == nil && cur.Equal(pub):
		return ownerStatusOf(pub), nil
	case err == nil:
		return OwnerStatus{}, &CodedError{Code: "owner_key_exists", Message: "an owner key is already registered; replace it with a rotation sealed by its private key"}
	case !errors.Is(err, os.ErrNotExist):
		return OwnerStatus{}, err
	}
	return ownerStatusOf(pub), writeOwnerKey(path, pub)
}

// RotateOwnerKey hands the anchor to a new public key, sealed by the current
// private key. Grants sealed by the old key stop verifying and must be signed
// again — a rotation is a clean break, not a migration.
func RotateOwnerKey(path, newPublicKeyB64, seal string) (OwnerStatus, error) {
	next, err := parseOwnerKey(newPublicKeyB64, "from the request")
	if err != nil {
		return OwnerStatus{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	cur, err := readOwnerKey(path)
	if errors.Is(err, os.ErrNotExist) {
		return OwnerStatus{}, &CodedError{Code: "no_owner_key", Message: "no owner key is registered yet; register one first"}
	}
	if err != nil {
		return OwnerStatus{}, err
	}
	if !Ed25519Verifier(cur).Verify(OwnerRotatePayload(strings.TrimSpace(newPublicKeyB64)), seal) {
		return OwnerStatus{}, &CodedError{Code: "bad_seal", Message: "the rotation is not sealed by the current owner key"}
	}
	return ownerStatusOf(next), writeOwnerKey(path, next)
}
