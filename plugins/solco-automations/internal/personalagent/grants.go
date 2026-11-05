package personalagent

import (
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/thewei/solco/plugins/solco-automations/internal/hostpath"
)

// Capability grants and the PolicyEngine skeleton (Gap A04; plan
// docs/assistant-autonomy-sleep-wake-permissions-plan.md §4, threat model
// docs/assistant-owner-agent-identity-threat-model.md).
//
// A grant is the owner's standing "yes" for one Assistant and one capability:
// scoped to explicit values, limited per day, expiring within 90 days,
// immutable (a change is revoke + a new grant), and SEALED — an Ed25519
// signature by the owner's key over the canonical grant. The core verifies the
// seal every time a grant is used, so a row written straight into the database
// (all that a shell-capable Agent running as the same OS user can do) is inert.
//
// The engine fails closed: with no owner public key configured, or a grant that
// is unsealed, forged, widened after sealing, expired, revoked, out of scope or
// over its limit, the answer is "need approval" — exactly today's behaviour
// (a person answers the card). Only an explicit `deny` grant tightens without a
// seal. Owner-only capabilities (changing grants, autonomy or approvals) are
// never grantable. Nothing here approves an existing card: an allow writes its
// own authorization receipt (`authorizedBy grant:<id>`), it never pretends a
// person clicked.
//
// Where the owner's key lives and how the owner signs is the identity
// architecture decision the threat model leaves open; this file only verifies.

// Capabilities (a closed list; a string outside it is refused everywhere).
const (
	CapMemoryAutosave   = "memory.autosave"
	CapAutomationManage = "automation.manage"
	CapTaskDelegate     = "task.delegate"
	CapRepoWrite        = "repo.write"
	CapRepoPublish      = "repo.publish"

	// Owner-only: authority over authority is never grantable.
	CapGrantSet       = "grant.set"
	CapAutonomySet    = "autonomy.set"
	CapApprovalDecide = "approval.decide"
)

// Decisions.
const (
	DecisionAllow        = "allow"
	DecisionNeedApproval = "need_approval"
	DecisionDeny         = "deny"
)

const (
	// MaxGrantTTL is the longest a grant lives; a resurrected or stale grant is
	// bounded by it.
	MaxGrantTTL = 90 * 24 * 60 * 60 * 1000
	// grantClockSkew is how far an owner-chosen issuedAt may be from now.
	grantClockSkew = 5 * 60 * 1000
	// MaxActiveGrants bounds one Assistant's live grants.
	MaxActiveGrants = 50
)

type capSpec struct {
	ownerOnly bool
	// explicit means an allow grant must carry a non-empty scope.
	explicit bool
}

var capabilities = map[string]capSpec{
	CapMemoryAutosave:   {},
	CapAutomationManage: {},
	CapTaskDelegate:     {},
	CapRepoWrite:        {},
	CapRepoPublish:      {explicit: true},
	CapGrantSet:         {ownerOnly: true},
	CapAutonomySet:      {ownerOnly: true},
	CapApprovalDecide:   {ownerOnly: true},
}

// Capabilities lists the grantable capabilities, sorted.
func Capabilities() []string {
	out := []string{}
	for c, spec := range capabilities {
		if !spec.ownerOnly {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return out
}

var (
	grantIDRe    = regexp.MustCompile(`^gr_[0-9a-f]{16}$`)
	scopeKeyRe   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	errNoGrantID = fmt.Errorf("%w: grant id must look like gr_ + 16 hex digits", ErrInvalid)
)

// GrantVerifier checks an owner seal over a canonical grant payload.
type GrantVerifier interface {
	Verify(payload []byte, seal string) bool
}

type ed25519Verifier struct{ pub ed25519.PublicKey }

func (v ed25519Verifier) Verify(payload []byte, seal string) bool {
	sig, err := base64.StdEncoding.DecodeString(seal)
	return err == nil && len(sig) == ed25519.SignatureSize && ed25519.Verify(v.pub, payload, sig)
}

// Ed25519Verifier verifies seals made with the owner's matching private key.
func Ed25519Verifier(pub ed25519.PublicKey) GrantVerifier { return ed25519Verifier{pub} }

// WithGrantVerifier installs the owner's verifier. Without one no grant ever
// authorises anything.
func WithGrantVerifier(v GrantVerifier) Option { return func(s *Store) { s.verifier = v } }

// OwnerKeyPath is where the owner's PUBLIC key (hex or base64, 32 bytes) is
// read from; SOLCO_OWNER_PUBKEY_FILE overrides it.
func OwnerKeyPath() string {
	if p := os.Getenv("SOLCO_OWNER_PUBKEY_FILE"); p != "" {
		return p
	}
	return filepath.Join(hostpath.StateDir(), "owner.pub")
}

// OwnerKeyOption loads the owner's public key if one is configured. A missing
// file is the fail-closed default (no option); a present but unreadable or
// malformed one is an error rather than a silent downgrade.
func OwnerKeyOption(path string) (Option, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return func(*Store) {}, nil
	}
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(string(raw))
	key, err := hex.DecodeString(text)
	if err != nil || len(key) != ed25519.PublicKeySize {
		if key, err = base64.StdEncoding.DecodeString(text); err != nil || len(key) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("owner public key at %s is not a 32-byte Ed25519 key", path)
		}
	}
	return WithGrantVerifier(Ed25519Verifier(ed25519.PublicKey(key))), nil
}

// GrantInput is what the owner's client sends: it chooses the id and the times
// (they are part of what it signs) and attaches its seal.
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

// Grant is a stored grant as listed. The seal itself is never listed; Verified
// says whether the core's current owner key accepts it.
type Grant struct {
	ID          string              `json:"id"`
	AssistantID string              `json:"assistantId"`
	Capability  string              `json:"capability"`
	Effect      string              `json:"effect"`
	Scope       map[string][]string `json:"scope"`
	MaxPerDay   int                 `json:"maxPerDay,omitempty"`
	IssuedAt    int64               `json:"issuedAt"`
	ExpiresAt   int64               `json:"expiresAt"`
	RevokedAt   int64               `json:"revokedAt,omitempty"`
	IssuedBy    string              `json:"issuedBy"`
	Sealed      bool                `json:"sealed"`
	Verified    bool                `json:"verified"`
	// Status is active | expired | revoked.
	Status string `json:"status"`
}

func normalizeScope(scope map[string][]string) map[string][]string {
	out := map[string][]string{}
	for k, vs := range scope {
		clean := slices.Clone(vs)
		slices.Sort(clean)
		out[k] = slices.Compact(clean)
	}
	return out
}

func scopeJSON(scope map[string][]string) string {
	raw, _ := json.Marshal(normalizeScope(scope))
	return string(raw)
}

// GrantPayload is the canonical byte string an owner seals: every field that
// decides what the grant allows, in a fixed order, scope keys and values sorted.
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

func validateGrant(in *GrantInput, now int64) error {
	if !grantIDRe.MatchString(in.ID) {
		return errNoGrantID
	}
	spec, known := capabilities[in.Capability]
	if !known {
		return fmt.Errorf("%w: unknown capability %q (one of %s)", ErrInvalid, in.Capability, strings.Join(Capabilities(), ", "))
	}
	if spec.ownerOnly {
		return fmt.Errorf("%w: %s belongs to the owner and cannot be granted", ErrInvalid, in.Capability)
	}
	if in.Effect == "" {
		in.Effect = "allow"
	}
	if in.Effect != "allow" && in.Effect != "deny" {
		return fmt.Errorf("%w: effect is allow or deny", ErrInvalid)
	}
	if len(in.Scope) > 8 {
		return fmt.Errorf("%w: at most 8 scope keys", ErrInvalid)
	}
	for k, vs := range in.Scope {
		if !scopeKeyRe.MatchString(k) || len(vs) > 32 {
			return fmt.Errorf("%w: scope key %q", ErrInvalid, k)
		}
		for _, v := range vs {
			if v == "" || v == "*" || len(v) > 200 || strings.ContainsAny(v, "\x00\r\n") {
				return fmt.Errorf("%w: scope values are explicit (no wildcards, no control characters)", ErrInvalid)
			}
		}
	}
	if spec.explicit && in.Effect == "allow" && len(in.Scope) == 0 {
		return fmt.Errorf("%w: %s needs an explicit scope", ErrInvalid, in.Capability)
	}
	if in.MaxPerDay < 0 || in.MaxPerDay > 1000 {
		return fmt.Errorf("%w: maxPerDay is 0..1000", ErrInvalid)
	}
	if in.IssuedAt < now-grantClockSkew || in.IssuedAt > now+grantClockSkew {
		return fmt.Errorf("%w: issuedAt is not now", ErrInvalid)
	}
	if in.ExpiresAt <= now {
		return fmt.Errorf("%w: the grant has already expired", ErrInvalid)
	}
	if in.ExpiresAt-now > MaxGrantTTL {
		return fmt.Errorf("%w: a grant lives at most 90 days", ErrInvalid)
	}
	if in.Seal != "" {
		if sig, err := base64.StdEncoding.DecodeString(in.Seal); err != nil || len(sig) != ed25519.SignatureSize {
			return fmt.Errorf("%w: the seal is not an Ed25519 signature", ErrInvalid)
		}
	}
	return nil
}

const grantCols = `id,assistant_id,capability,effect,scope,max_per_day,issued_at,expires_at,revoked_at,issued_by,seal`

func (s *Store) scanGrant(r rowScanner, now int64) (Grant, GrantInput, error) {
	var g Grant
	var scope, seal string
	if err := r.Scan(&g.ID, &g.AssistantID, &g.Capability, &g.Effect, &scope, &g.MaxPerDay, &g.IssuedAt, &g.ExpiresAt, &g.RevokedAt, &g.IssuedBy, &seal); err != nil {
		return g, GrantInput{}, err
	}
	_ = json.Unmarshal([]byte(scope), &g.Scope)
	if g.Scope == nil {
		g.Scope = map[string][]string{}
	}
	in := GrantInput{ID: g.ID, AssistantID: g.AssistantID, Capability: g.Capability, Effect: g.Effect, Scope: g.Scope,
		MaxPerDay: g.MaxPerDay, IssuedAt: g.IssuedAt, ExpiresAt: g.ExpiresAt, Seal: seal}
	g.Sealed = seal != ""
	g.Verified = g.Sealed && s.verifier != nil && s.verifier.Verify(GrantPayload(in), seal)
	switch {
	case g.RevokedAt != 0:
		g.Status = "revoked"
	case g.ExpiresAt <= now:
		g.Status = "expired"
	default:
		g.Status = "active"
	}
	return g, in, nil
}

// CreateGrant stores an owner's grant. Whether its seal is good is decided
// when it is used, never trusted at storage.
func (s *Store) CreateGrant(requestID string, in GrantInput) (Grant, bool, error) {
	now := s.nowMS()
	if err := validateGrant(&in, now); err != nil {
		return Grant{}, false, err
	}
	return execute(s, requestID, "grant.create", in, func(tx *sql.Tx) (Grant, error) {
		a, err := getAssistantTx(tx, in.AssistantID)
		if err != nil {
			return Grant{}, err
		}
		if a.Status != StatusActive {
			return Grant{}, ErrAssistantDisabled
		}
		var live int
		_ = tx.QueryRow(`SELECT COUNT(*) FROM capability_grants WHERE assistant_id=? AND revoked_at=0 AND expires_at>?`, in.AssistantID, now).Scan(&live)
		if live >= MaxActiveGrants {
			return Grant{}, fmt.Errorf("%w: at most %d live grants per Assistant", ErrInvalid, MaxActiveGrants)
		}
		if _, err := tx.Exec(`INSERT INTO capability_grants(`+grantCols+`,created_at) VALUES(?,?,?,?,?,?,?,?,0,'owner',?,?)`,
			in.ID, in.AssistantID, in.Capability, in.Effect, scopeJSON(in.Scope), in.MaxPerDay, in.IssuedAt, in.ExpiresAt, in.Seal, now); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "PRIMARY") {
				return Grant{}, fmt.Errorf("%w: grant id %s already exists (grants are immutable; revoke and issue a new one)", ErrInvalid, in.ID)
			}
			return Grant{}, err
		}
		g, _, err := s.scanGrant(tx.QueryRow(`SELECT `+grantCols+` FROM capability_grants WHERE id=?`, in.ID), now)
		return g, err
	})
}

// ListGrants lists an Assistant's grants (everyone's when empty), newest first.
func (s *Store) ListGrants(assistantID string) ([]Grant, error) {
	now := s.nowMS()
	q, args := `SELECT `+grantCols+` FROM capability_grants`, []any{}
	if assistantID != "" {
		q += ` WHERE assistant_id=?`
		args = append(args, assistantID)
	}
	rows, err := s.db.Query(q+` ORDER BY issued_at DESC, id`, args...)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		g, _, err := s.scanGrant(rows, now)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetGrant reads one grant.
func (s *Store) GetGrant(id string) (Grant, error) {
	g, _, err := s.scanGrant(s.db.QueryRow(`SELECT `+grantCols+` FROM capability_grants WHERE id=?`, id), s.nowMS())
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	return g, wrapBusy(err)
}

// RevokeGrant ends a grant at once. Receipts already written stay.
func (s *Store) RevokeGrant(requestID, id string) (Grant, bool, error) {
	return execute(s, requestID, "grant.revoke", id, func(tx *sql.Tx) (Grant, error) {
		res, err := tx.Exec(`UPDATE capability_grants SET revoked_at=CASE WHEN revoked_at=0 THEN ? ELSE revoked_at END WHERE id=?`, s.nowMS(), id)
		if err != nil {
			return Grant{}, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return Grant{}, ErrNotFound
		}
		g, _, err := s.scanGrant(tx.QueryRow(`SELECT `+grantCols+` FROM capability_grants WHERE id=?`, id), s.nowMS())
		return g, err
	})
}

// NarrowScope is what a child (a sub-task, a run) may inherit from its parent:
// the intersection. A key only the parent names stays; a key only the child
// names is kept (it can only narrow); a key both name keeps the common values,
// and disjoint values leave an empty list that matches nothing.
func NarrowScope(parent, child map[string][]string) map[string][]string {
	out := normalizeScope(parent)
	for k, cv := range normalizeScope(child) {
		pv, ok := out[k]
		if !ok {
			out[k] = cv
			continue
		}
		common := []string{}
		for _, v := range pv {
			if slices.Contains(cv, v) {
				common = append(common, v)
			}
		}
		out[k] = common
	}
	return out
}

// scopeAllows is true when every key of the scope is stated by the action and
// its value is one the scope lists. A key the action omits fails closed.
func scopeAllows(scope map[string][]string, params map[string]string) bool {
	for k, allowed := range scope {
		v, ok := params[k]
		if !ok || !slices.Contains(allowed, v) {
			return false
		}
	}
	return true
}

// AuthorizeInput is one protected action asking whether a grant covers it.
type AuthorizeInput struct {
	RequestID   string            `json:"requestId"`
	AssistantID string            `json:"assistantId"`
	Capability  string            `json:"capability"`
	Params      map[string]string `json:"params"`
	ActionHash  string            `json:"actionHash"`
}

// AuthorizationResult is the engine's answer and the receipt that records it.
type AuthorizationResult struct {
	Decision     string `json:"decision"`
	Reason       string `json:"reason"`
	GrantID      string `json:"grantId,omitempty"`
	ReceiptID    string `json:"receiptId"`
	AuthorizedBy string `json:"authorizedBy,omitempty"`
}

// Authorize decides and records. It never approves an existing card and never
// widens: the only ways to "allow" are a verified, in-scope, in-limit, live
// grant of this Assistant for this capability.
func (s *Store) Authorize(in AuthorizeInput) (AuthorizationResult, bool, error) {
	if in.RequestID == "" || len(in.RequestID) > 128 {
		return AuthorizationResult{}, false, fmt.Errorf("%w: a requestId is required", ErrInvalid)
	}
	spec, known := capabilities[in.Capability]
	if !known {
		return AuthorizationResult{}, false, fmt.Errorf("%w: unknown capability %q", ErrInvalid, in.Capability)
	}
	return execute(s, in.RequestID, "grant.authorize", in, func(tx *sql.Tx) (AuthorizationResult, error) {
		a, err := getAssistantTx(tx, in.AssistantID)
		if err != nil {
			return AuthorizationResult{}, err
		}
		if a.Status != StatusActive {
			return AuthorizationResult{}, ErrAssistantDisabled
		}
		now := s.nowMS()
		res := AuthorizationResult{Decision: DecisionNeedApproval, Reason: "no_grant"}
		finish := func() (AuthorizationResult, error) {
			res.ReceiptID = newID("z")
			if res.Decision == DecisionAllow {
				res.AuthorizedBy = "grant:" + res.GrantID
			}
			_, err := tx.Exec(`INSERT INTO authorization_receipts(id,assistant_id,grant_id,capability,action_hash,request_id,decision,reason,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
				res.ReceiptID, in.AssistantID, res.GrantID, in.Capability, in.ActionHash, in.RequestID, res.Decision, res.Reason, now)
			return res, err
		}
		if spec.ownerOnly {
			res.Decision, res.Reason = DecisionDeny, "owner_only"
			return finish()
		}
		rows, err := tx.Query(`SELECT `+grantCols+` FROM capability_grants WHERE assistant_id=? AND capability=? AND revoked_at=0 AND expires_at>? ORDER BY issued_at, id`, in.AssistantID, in.Capability, now)
		if err != nil {
			return res, err
		}
		var allows []Grant
		var unreadable bool
		for rows.Next() {
			g, _, err := s.scanGrant(rows, now)
			if err != nil {
				// A row that cannot be read (hand-edited, corrupt) is inert,
				// never a reason to fail the whole decision either way.
				unreadable = true
				continue
			}
			if g.Effect == "deny" {
				if scopeAllows(g.Scope, in.Params) {
					rows.Close()
					res.Decision, res.Reason, res.GrantID = DecisionDeny, "denied_by_grant", g.ID
					return finish()
				}
				continue
			}
			allows = append(allows, g)
		}
		rows.Close()
		if len(allows) == 0 {
			if unreadable {
				res.Reason = "bad_seal"
			}
			return finish()
		}
		if s.verifier == nil {
			res.Reason = "no_owner_key"
			return finish()
		}
		var sawBadSeal, sawScope, sawLimit bool
		for _, g := range allows {
			if !g.Verified {
				sawBadSeal = true
				continue
			}
			if !scopeAllows(g.Scope, in.Params) {
				sawScope = true
				continue
			}
			if g.MaxPerDay > 0 {
				var used int
				_ = tx.QueryRow(`SELECT COUNT(*) FROM authorization_receipts WHERE grant_id=? AND decision='allow' AND created_at>?`, g.ID, now-24*60*60*1000).Scan(&used)
				if used >= g.MaxPerDay {
					sawLimit = true
					continue
				}
			}
			res.Decision, res.Reason, res.GrantID = DecisionAllow, "allowed", g.ID
			return finish()
		}
		switch {
		case sawLimit:
			res.Reason = "limit_reached"
		case sawScope:
			res.Reason = "scope_mismatch"
		case sawBadSeal:
			res.Reason = "bad_seal"
		}
		return finish()
	})
}
