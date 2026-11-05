package nativeui

/**
 * [INPUT]: 依赖 assistants_bridge 的 client/write、internal/personalagent 的 OwnerStatus/RegisterOwner/Grants/CreateGrant/RevokeGrant/SealGrant、appSecrets（Solco 自己的加密凭据文件）
 * [OUTPUT]: AssistantsBridge.Autosave（owner 密钥状态 + 该 Assistant 的 memory.autosave 授权）、EnableAutosave（Solco 用 owner 私钥封印授权）、RevokeAutosave
 * [POS]: 2026-11-05 A04 的 Owner 端签名入口：私钥只在 Solco 的 secrets.enc（assistants.owner.key），核心只存公钥；首个公钥首次使用即注册，
 *        此后核心拒绝别的公钥（owner_key_exists），所以"Solco 以外的人"换不掉锚（文件层面仍可写，见威胁模型 R1）；
 *        私钥/封印/授权 id 不进日志也不进 Snapshot；本入口只做 memory.autosave（用户 2026-11-05 决定），其余能力等各自的接线
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/thewei/solco/internal/personalagent"
)

// ownerKeySecret is where Solco keeps the owner's Ed25519 private key
// (base64 seed). It lives in the encrypted credential file with Solco's other
// own credentials — the same level of protection as them, no more.
const ownerKeySecret = "assistants.owner.key"

// autosaveKinds are the memory kinds an owner can let an Assistant keep
// without asking. Anything else (and every other author) still waits for a yes.
var autosaveKinds = []string{"fact", "preference"}

const maxAutosaveDays = 90

// AssistantsAutosave is the Memory autosave section of the Autonomy tab.
type AssistantsAutosave struct {
	// OwnerKey: "none" (no key yet — the first Enable makes and registers
	// one), "ready" (Solco holds the registered key), or "foreign" (the core
	// has another key: Solco cannot seal grants for it).
	OwnerKey    string                `json:"ownerKey"`
	Fingerprint string                `json:"fingerprint"`
	Kinds       []string              `json:"kinds"`
	Grants      []personalagent.Grant `json:"grants"`
}

func ownerPrivate() (ed25519.PrivateKey, bool) {
	raw, ok := appSecrets.Get(ownerKeySecret)
	if !ok {
		return nil, false
	}
	seed, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, false
	}
	return ed25519.NewKeyFromSeed(seed), true
}

func fingerprintOf(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// ownerKeyState compares the core's key with the one Solco holds.
func ownerKeyState(core personalagent.OwnerStatus) (state, fingerprint string, priv ed25519.PrivateKey) {
	priv, have := ownerPrivate()
	switch {
	case !core.Configured:
		return "none", "", priv
	case have && fingerprintOf(priv.Public().(ed25519.PublicKey)) == core.Fingerprint:
		return "ready", core.Fingerprint, priv
	}
	return "foreign", core.Fingerprint, nil
}

// Autosave reads the owner-key state and this Assistant's memory.autosave grants.
func (b *AssistantsBridge) Autosave(ctx context.Context, id string) (AssistantsAutosave, error) {
	out := AssistantsAutosave{OwnerKey: "none", Kinds: slices.Clone(autosaveKinds), Grants: []personalagent.Grant{}}
	c, err := b.client()
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	core, err := c.OwnerStatus(ctx)
	if err != nil {
		return out, err
	}
	out.OwnerKey, out.Fingerprint, _ = ownerKeyState(core)
	grants, _, err := c.Grants(ctx, id)
	if err != nil {
		return out, err
	}
	for _, g := range grants {
		if g.Capability == "memory.autosave" {
			out.Grants = append(out.Grants, g)
		}
	}
	return out, nil
}

// EnableAutosave seals and stores a memory.autosave grant for the chosen kinds.
// The owner's key is made and registered on first use.
func (b *AssistantsBridge) EnableAutosave(ctx context.Context, requestID, id string, kinds []string, maxPerDay, days int) error {
	if len(kinds) == 0 {
		return errors.New("invalid: choose at least one kind of memory")
	}
	for _, k := range kinds {
		if !slices.Contains(autosaveKinds, k) {
			return fmt.Errorf("invalid: %q is not a memory kind that can be saved automatically", k)
		}
	}
	if days < 1 || days > maxAutosaveDays {
		return fmt.Errorf("invalid: a grant lasts 1 to %d days", maxAutosaveDays)
	}
	if maxPerDay < 0 || maxPerDay > 1000 {
		return errors.New("invalid: the daily limit is 0 to 1000")
	}
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		core, err := c.OwnerStatus(ctx)
		if err != nil {
			return err
		}
		state, _, priv := ownerKeyState(core)
		switch state {
		case "foreign":
			return errors.New("owner_key_foreign: another owner key is registered for these Assistants; Solco cannot sign for it")
		case "none":
			if priv, err = b.makeOwnerKey(ctx, c, requestID); err != nil {
				return err
			}
		}
		now := time.Now()
		sum := sha256.Sum256([]byte("grant\x00" + requestID))
		in := personalagent.SealGrant(priv, personalagent.GrantInput{
			ID: "gr_" + hex.EncodeToString(sum[:8]), AssistantID: id, Capability: "memory.autosave", Effect: "allow",
			Scope: map[string][]string{"kind": kinds}, MaxPerDay: maxPerDay,
			IssuedAt: now.UnixMilli(), ExpiresAt: now.Add(time.Duration(days) * 24 * time.Hour).UnixMilli(),
		})
		_, err = c.CreateGrant(ctx, requestID, in)
		return err
	})
}

// makeOwnerKey keeps the private key Solco already holds (a core that lost its
// public key is simply re-registered) or makes one, then registers the public key.
func (b *AssistantsBridge) makeOwnerKey(ctx context.Context, c *personalagent.Client, requestID string) (ed25519.PrivateKey, error) {
	priv, have := ownerPrivate()
	if !have {
		var err error
		if priv, _, err = personalagent.NewOwnerKey(); err != nil {
			return nil, err
		}
		if err := appSecrets.Set(ownerKeySecret, base64.StdEncoding.EncodeToString(priv.Seed())); err != nil {
			return nil, err
		}
	}
	pub := base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
	if _, err := c.RegisterOwner(ctx, requestID+":owner", pub); err != nil {
		return nil, err
	}
	return priv, nil
}

// RevokeAutosave ends one of this Assistant's grants at once. Revoking needs no
// seal: taking a permission away never needs the owner key.
func (b *AssistantsBridge) RevokeAutosave(ctx context.Context, requestID, id, grantID string) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		grants, _, err := c.Grants(ctx, id)
		if err != nil {
			return err
		}
		for _, g := range grants {
			if g.ID == grantID && g.Capability == "memory.autosave" {
				_, err := c.RevokeGrant(ctx, requestID, grantID)
				return err
			}
		}
		return errors.New("not_found: no such grant")
	})
}
