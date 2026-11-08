package personalagent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Gap A04 skeleton: grants are owner-sealed, immutable, scoped, limited and
// expiring; the engine fails closed. These tests pin the properties the plan's
// §4 and the identity threat model rest on — in particular that a grant row
// written straight into the database (what a shell-capable Agent can do) is
// inert without the owner's seal.

type grantEnv struct {
	s    *Store
	now  time.Time
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	ada  Assistant
	bob  Assistant
}

func newGrantEnv(t *testing.T, withVerifier bool) *grantEnv {
	t.Helper()
	e := &grantEnv{now: time.Unix(1_800_000_000, 0)}
	e.pub, e.priv, _ = ed25519.GenerateKey(rand.Reader)
	opts := []Option{WithClock(func() time.Time { return e.now })}
	if withVerifier {
		opts = append(opts, WithGrantVerifier(Ed25519Verifier(e.pub)))
	}
	e.s, _ = openTest(t, opts...)
	e.ada = mustAssistant(t, e.s, "a1", "Ada")
	e.bob = mustAssistant(t, e.s, "a2", "Bob")
	return e
}

// grant builds an input the way the owner's client does: it picks the id and
// the times, then signs the canonical payload with its private key.
func (e *grantEnv) grant(id, assistant, capability string, scope map[string][]string, mut ...func(*GrantInput)) GrantInput {
	in := GrantInput{
		ID: id, AssistantID: assistant, Capability: capability, Effect: "allow", Scope: scope,
		IssuedAt: e.now.UnixMilli(), ExpiresAt: e.now.Add(48 * time.Hour).UnixMilli(),
	}
	for _, m := range mut {
		m(&in)
	}
	in.Seal = base64.StdEncoding.EncodeToString(ed25519.Sign(e.priv, GrantPayload(in)))
	return in
}

func (e *grantEnv) create(t *testing.T, req string, in GrantInput) Grant {
	t.Helper()
	g, _, err := e.s.CreateGrant(req, in)
	if err != nil {
		t.Fatalf("create grant: %v", err)
	}
	return g
}

func (e *grantEnv) decide(t *testing.T, req, assistant, capability string, params map[string]string) AuthorizationResult {
	t.Helper()
	r, _, err := e.s.Authorize(AuthorizeInput{RequestID: req, AssistantID: assistant, Capability: capability, Params: params, ActionHash: "h-" + req})
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	return r
}

const gid1 = "gr_0000000000000001"

// A04-1: with no owner key configured nothing is ever auto-authorised, even a
// grant that carries a perfectly good seal. The default is the old behaviour
// (a person answers the card).
func TestGrantsFailClosedWithoutOwnerKey(t *testing.T) {
	e := newGrantEnv(t, false)
	e.create(t, "g1", e.grant(gid1, e.ada.ID, CapTaskDelegate, nil))
	r := e.decide(t, "d1", e.ada.ID, CapTaskDelegate, nil)
	if r.Decision != DecisionNeedApproval || r.Reason != "no_owner_key" || r.GrantID != "" {
		t.Fatalf("decision = %+v", r)
	}
}

// A04-2: inside the scope a sealed grant allows, every allow leaves a receipt
// that names the grant, and a replay of the request answers the same receipt.
func TestGrantAllowsInsideScopeWithReceiptAndReplay(t *testing.T) {
	e := newGrantEnv(t, true)
	e.create(t, "g1", e.grant(gid1, e.ada.ID, CapAutomationManage, map[string][]string{"agent": {"omp"}, "workdir": {"/work/a", "/work/b"}}))
	r := e.decide(t, "d1", e.ada.ID, CapAutomationManage, map[string]string{"agent": "omp", "workdir": "/work/a"})
	if r.Decision != DecisionAllow || r.GrantID != gid1 || r.ReceiptID == "" || r.AuthorizedBy != "grant:"+gid1 {
		t.Fatalf("decision = %+v", r)
	}
	again, replayed, err := e.s.Authorize(AuthorizeInput{RequestID: "d1", AssistantID: e.ada.ID, Capability: CapAutomationManage, Params: map[string]string{"agent": "omp", "workdir": "/work/a"}, ActionHash: "h-d1"})
	if err != nil || !replayed || again.ReceiptID != r.ReceiptID {
		t.Fatalf("replay = %+v replayed=%v err=%v", again, replayed, err)
	}
	for _, params := range []map[string]string{
		{"agent": "omp", "workdir": "/etc"}, // outside the scope
		{"agent": "claude", "workdir": "/work/a"},
		{"agent": "omp"}, // a scope key the action does not state fails closed
	} {
		if r := e.decide(t, fmt.Sprintf("o-%v", params), e.ada.ID, CapAutomationManage, params); r.Decision != DecisionNeedApproval || r.Reason != "scope_mismatch" {
			t.Fatalf("%v: %+v", params, r)
		}
	}
	// Ada's grant says nothing about Bob.
	if r := e.decide(t, "bob1", e.bob.ID, CapAutomationManage, map[string]string{"agent": "omp", "workdir": "/work/a"}); r.Decision != DecisionNeedApproval || r.Reason != "no_grant" {
		t.Fatalf("bob = %+v", r)
	}
}

// A04-3: what a shell-capable Agent can do — write rows into the database
// itself — authorises nothing: a forged seal, a seal from another key, a scope
// widened after sealing and a row with no seal at all are all inert.
func TestForgedGrantRowsAreInert(t *testing.T) {
	e := newGrantEnv(t, true)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	good := e.grant("gr_00000000000000aa", e.ada.ID, CapTaskDelegate, map[string][]string{"kind": {"preference"}})
	insert := func(g GrantInput, seal string) {
		t.Helper()
		if _, err := e.s.db.Exec(`INSERT INTO capability_grants(id,assistant_id,capability,effect,scope,max_per_day,issued_at,expires_at,issued_by,seal,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			g.ID, g.AssistantID, g.Capability, g.Effect, scopeJSON(g.Scope), g.MaxPerDay, g.IssuedAt, g.ExpiresAt, "owner", seal, e.now.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	forgedKey := good
	forgedKey.ID = "gr_00000000000000ab"
	insert(forgedKey, base64.StdEncoding.EncodeToString(ed25519.Sign(other, GrantPayload(forgedKey))))
	widened := good
	widened.ID = "gr_00000000000000ac"
	widened.Scope = map[string][]string{"kind": {"preference", "credential"}}
	insert(widened, good.Seal) // a valid seal, for a different scope
	unsealed := good
	unsealed.ID = "gr_00000000000000ad"
	insert(unsealed, "")
	for i, kind := range []string{"preference", "credential"} {
		r := e.decide(t, fmt.Sprintf("f%d", i), e.ada.ID, CapTaskDelegate, map[string]string{"kind": kind})
		if r.Decision == DecisionAllow {
			t.Fatalf("a forged grant authorised %q: %+v", kind, r)
		}
	}
	// The one honest grant still works, and only for what it says.
	e.create(t, "g-real", e.grant(gid1, e.ada.ID, CapTaskDelegate, map[string][]string{"kind": {"preference"}}))
	if r := e.decide(t, "real", e.ada.ID, CapTaskDelegate, map[string]string{"kind": "preference"}); r.Decision != DecisionAllow || r.GrantID != gid1 {
		t.Fatalf("honest grant = %+v", r)
	}
	if r := e.decide(t, "real2", e.ada.ID, CapTaskDelegate, map[string]string{"kind": "credential"}); r.Decision == DecisionAllow {
		t.Fatalf("the widened forgery authorised: %+v", r)
	}
}

// A04-4: time, revocation and the per-day limit end an authorisation.
func TestGrantExpiryRevocationAndDailyLimit(t *testing.T) {
	e := newGrantEnv(t, true)
	e.create(t, "g1", e.grant(gid1, e.ada.ID, CapTaskDelegate, nil, func(in *GrantInput) { in.MaxPerDay = 2 }))
	for i := 1; i <= 2; i++ {
		if r := e.decide(t, fmt.Sprintf("l%d", i), e.ada.ID, CapTaskDelegate, nil); r.Decision != DecisionAllow {
			t.Fatalf("use %d = %+v", i, r)
		}
	}
	if r := e.decide(t, "l3", e.ada.ID, CapTaskDelegate, nil); r.Decision != DecisionNeedApproval || r.Reason != "limit_reached" {
		t.Fatalf("third use = %+v", r)
	}
	e.now = e.now.Add(25 * time.Hour) // the count is per rolling day
	if r := e.decide(t, "l4", e.ada.ID, CapTaskDelegate, nil); r.Decision != DecisionAllow {
		t.Fatalf("next day = %+v", r)
	}
	if _, _, err := e.s.RevokeGrant("rv1", gid1); err != nil {
		t.Fatal(err)
	}
	if r := e.decide(t, "l5", e.ada.ID, CapTaskDelegate, nil); r.Decision != DecisionNeedApproval || r.Reason != "no_grant" {
		t.Fatalf("after revoke = %+v", r)
	}
	e.create(t, "g2", e.grant("gr_0000000000000002", e.ada.ID, CapTaskDelegate, nil))
	e.now = e.now.Add(49 * time.Hour)
	if r := e.decide(t, "l6", e.ada.ID, CapTaskDelegate, nil); r.Decision != DecisionNeedApproval || r.Reason != "no_grant" {
		t.Fatalf("after expiry = %+v", r)
	}
}

// A04-5: authority over authority is never grantable, and a grant must expire
// and be explicit where the harm is large.
func TestOwnerOnlyAndHighRiskRules(t *testing.T) {
	e := newGrantEnv(t, true)
	for _, c := range []string{CapGrantSet, CapAutonomySet, CapApprovalDecide} {
		if _, _, err := e.s.CreateGrant("o-"+c, e.grant("gr_00000000000000b0", e.ada.ID, c, nil)); err == nil {
			t.Fatalf("%s was grantable", c)
		}
		if r := e.decide(t, "d-"+c, e.ada.ID, c, nil); r.Decision != DecisionDeny || r.Reason != "owner_only" {
			t.Fatalf("%s decision = %+v", c, r)
		}
	}
	if _, _, err := e.s.CreateGrant("hr1", e.grant("gr_00000000000000b1", e.ada.ID, CapRepoPublish, nil)); err == nil {
		t.Fatal("a high-risk grant without an explicit scope was accepted")
	}
	e.create(t, "hr2", e.grant("gr_00000000000000b2", e.ada.ID, CapRepoPublish, map[string][]string{"repo": {"o/r"}}))
	if _, _, err := e.s.CreateGrant("long", e.grant("gr_00000000000000b3", e.ada.ID, CapTaskDelegate, nil, func(in *GrantInput) { in.ExpiresAt = e.now.Add(91 * 24 * time.Hour).UnixMilli() })); err == nil {
		t.Fatal("a grant longer than the 90-day ceiling was accepted")
	}
	if _, _, err := e.s.CreateGrant("past", e.grant("gr_00000000000000b4", e.ada.ID, CapTaskDelegate, nil, func(in *GrantInput) { in.ExpiresAt = e.now.Add(-time.Hour).UnixMilli() })); err == nil {
		t.Fatal("an already expired grant was accepted")
	}
	if _, _, err := e.s.CreateGrant("unknown", e.grant("gr_00000000000000b5", e.ada.ID, "shell.anything", nil)); err == nil {
		t.Fatal("a capability outside the closed list was accepted")
	}
}

// A04-6: a deny grant wins over an allow and needs no seal (tightening is
// always safe); the grant itself is immutable and its id cannot be reused.
func TestDenyWinsAndGrantsAreImmutable(t *testing.T) {
	e := newGrantEnv(t, true)
	e.create(t, "g1", e.grant(gid1, e.ada.ID, CapRepoWrite, map[string][]string{"repo": {"o/r", "o/s"}}))
	deny := GrantInput{ID: "gr_0000000000000009", AssistantID: e.ada.ID, Capability: CapRepoWrite, Effect: "deny", Scope: map[string][]string{"repo": {"o/s"}},
		IssuedAt: e.now.UnixMilli(), ExpiresAt: e.now.Add(time.Hour).UnixMilli()}
	if _, _, err := e.s.CreateGrant("g2", deny); err != nil {
		t.Fatalf("unsealed deny: %v", err)
	}
	if r := e.decide(t, "x1", e.ada.ID, CapRepoWrite, map[string]string{"repo": "o/r"}); r.Decision != DecisionAllow {
		t.Fatalf("o/r = %+v", r)
	}
	if r := e.decide(t, "x2", e.ada.ID, CapRepoWrite, map[string]string{"repo": "o/s"}); r.Decision != DecisionDeny || r.Reason != "denied_by_grant" {
		t.Fatalf("o/s = %+v", r)
	}
	if _, _, err := e.s.CreateGrant("g3", e.grant(gid1, e.ada.ID, CapRepoWrite, map[string][]string{"repo": {"o/r"}})); err == nil {
		t.Fatal("an existing grant id was overwritten")
	}
}

// A04-7: a child inherits the intersection of what its parent may do.
func TestChildScopeIsTheIntersection(t *testing.T) {
	parent := map[string][]string{"repo": {"o/r", "o/s"}, "agent": {"omp"}}
	child := map[string][]string{"repo": {"o/s", "o/t"}, "extra": {"x"}}
	got := NarrowScope(parent, child)
	if strings.Join(got["repo"], ",") != "o/s" || strings.Join(got["agent"], ",") != "omp" || strings.Join(got["extra"], ",") != "x" {
		t.Fatalf("narrowed = %v", got)
	}
	if len(NarrowScope(map[string][]string{"repo": {"o/r"}}, map[string][]string{"repo": {"o/x"}})["repo"]) != 0 {
		t.Fatal("disjoint scopes did not collapse to nothing")
	}
}

// A04-8: grants are the person's: a Coordinator reaches none of the commands.
func TestCoordinatorCannotTouchGrantsOrTheirDecisions(t *testing.T) {
	e := newGrantEnv(t, true)
	api := &API{Store: e.s}
	for _, args := range [][]string{
		{"assistant", "--as", e.ada.ID, "grant", "create"},
		{"assistant", "--as", e.ada.ID, "grant", "list", "--assistant", e.ada.ID},
		{"assistant", "--as", e.ada.ID, "grant", "revoke", gid1},
		{"assistant", "--as", e.ada.ID, "grant", "decide"},
	} {
		if _, err := call(t, api, `{}`, args...); codeOf(err) != "forbidden" {
			t.Fatalf("%v = %q", args, codeOf(err))
		}
	}
	in := e.grant(gid1, e.ada.ID, CapTaskDelegate, nil)
	body := fmt.Sprintf(`{"requestId":"cli-g1","id":%q,"assistantId":%q,"capability":%q,"effect":"allow","issuedAt":%d,"expiresAt":%d,"seal":%q}`, in.ID, in.AssistantID, in.Capability, in.IssuedAt, in.ExpiresAt, in.Seal)
	out, err := call(t, api, body, "assistant", "grant", "create")
	if err != nil || field(out, "grant", "id") != gid1 || field(out, "grant", "verified") != true {
		t.Fatalf("cli create = %v err=%v", out, err)
	}
	out, err = call(t, api, fmt.Sprintf(`{"requestId":"cli-d1","assistantId":%q,"capability":%q,"actionHash":"h"}`, e.ada.ID, CapTaskDelegate), "assistant", "grant", "decide")
	if err != nil || field(out, "result", "decision") != "allow" {
		t.Fatalf("cli decide = %v err=%v", out, err)
	}
	if out, err = call(t, api, ``, "assistant", "grant", "list", "--assistant", e.ada.ID); err != nil || strings.Contains(fmt.Sprint(out), in.Seal) {
		t.Fatalf("list = %v err=%v (a seal is not for listing)", out, err)
	}
}

// A grant row that cannot even be read (hand-edited with the wrong column
// types, found in the 2026-11-05 identity experiment) is inert and answers
// bad_seal; it does not turn the decision into an internal error.
func TestUnreadableGrantRowIsInert(t *testing.T) {
	e := newGrantEnv(t, true)
	if _, err := e.s.db.Exec(`INSERT INTO capability_grants(id,assistant_id,capability,effect,scope,max_per_day,issued_at,expires_at,issued_by,seal,created_at) VALUES('gr_00000000000000cd',?,?,'allow','{}',0,'not-a-number',?,'owner','AAAA',0)`,
		e.ada.ID, CapRepoWrite, e.now.Add(48*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	r, _, err := e.s.Authorize(AuthorizeInput{RequestID: "d-unreadable", AssistantID: e.ada.ID, Capability: CapRepoWrite, Params: map[string]string{"repo": "o/r"}})
	if err != nil {
		t.Fatalf("an unreadable row must not fail the decision: %v", err)
	}
	if r.Decision != DecisionNeedApproval || r.Reason != "bad_seal" {
		t.Fatalf("got %+v", r)
	}
}

func TestOwnerKeyOptionLoadsFailsClosedAndRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	missing, err := OwnerKeyOption(filepath.Join(dir, "none.pub"))
	if err != nil {
		t.Fatalf("a missing key is the fail-closed default, not an error: %v", err)
	}
	st := &Store{}
	missing(st)
	if st.verifier != nil {
		t.Fatal("a missing key must leave no verifier")
	}
	bad := filepath.Join(dir, "bad.pub")
	if err := os.WriteFile(bad, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OwnerKeyOption(bad); err == nil {
		t.Fatal("a malformed key must be an error, never a silent downgrade")
	}
}

// KNOWN RESIDUAL (docs/assistant-owner-agent-identity-threat-model.md §6, R1):
// the public key is a file the same user can write. An Agent with shell access
// can replace it with its own key and then seal grants itself. This test pins
// today's behaviour as a tripwire: it must flip to "inert" once the trust
// anchor moves out of reach of the Agent's user (Keychain / Secure Enclave /
// a separate OS user), and the test then changes with that decision.
func TestKnownResidualAgentThatCanWriteTheKeyFileCanMintGrants(t *testing.T) {
	e := newGrantEnv(t, false)
	path := filepath.Join(t.TempDir(), "owner.pub")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(e.pub)), 0o600); err != nil {
		t.Fatal(err)
	}
	opt, err := OwnerKeyOption(path)
	if err != nil {
		t.Fatal(err)
	}
	opt(e.s)
	if _, _, err := e.s.CreateGrant("cg-residual", e.grant(gid1, e.ada.ID, CapTaskDelegate, map[string][]string{"kind": {"preference"}})); err != nil {
		t.Fatal(err)
	}
	r, _, err := e.s.Authorize(AuthorizeInput{RequestID: "d-residual", AssistantID: e.ada.ID, Capability: CapTaskDelegate, Params: map[string]string{"kind": "preference"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Decision != DecisionAllow {
		t.Fatalf("the documented residual changed (good news?): %+v — update the threat model", r)
	}
}
