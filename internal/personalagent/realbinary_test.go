package personalagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The root client against the real plugin binary on a throwaway database: the
// whole CLI wire, not just the fixtures.
func TestRealPluginBinaryRoundTrip(t *testing.T) {
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
	c := NewClient(root, []string{"SOLCO_ASSISTANTS_DB=" + filepath.Join(t.TempDir(), "a.sqlite")})
	if !c.Available(t.Context()) {
		t.Fatal("gate closed for the real plugin")
	}
	a, err := c.CreateAssistant(t.Context(), "r1", "Ada", "")
	if err != nil || a.ID == "" || a.Status != "active" {
		t.Fatalf("create: %+v %v", a, err)
	}
	g, err := c.CreateGoal(t.Context(), "r2", CreateGoalInput{AssistantID: a.ID, Title: "Plan"})
	if err != nil || g.State != "draft" {
		t.Fatalf("goal: %+v %v", g, err)
	}
	if _, err := c.GoalAction(t.Context(), "r3", "pause", g.ID, g.Revision, ""); !IsCode(err, "bad_state") {
		t.Fatalf("pausing a draft: %v", err)
	}
	g, err = c.GoalAction(t.Context(), "r4", "approve", g.ID, g.Revision, "")
	if err != nil || g.State != "active" {
		t.Fatalf("approve: %+v %v", g, err)
	}
	if _, err := c.GoalAction(t.Context(), "r5", "pause", g.ID, 1, ""); !IsCode(err, "stale_revision") {
		t.Fatalf("stale pause: %v", err)
	}
	ov, err := c.Overview(t.Context())
	if err != nil || len(ov.Assistants) != 1 || ov.Assistants[0].Goals != 1 {
		t.Fatalf("overview: %+v %v", ov, err)
	}
}

// A04 end to end: the root's owner-side signing against the real core. The
// owner key is registered, a grant sealed here is verified there, an Agent's
// proposal (--as) is kept at once inside it and waits outside it, and a
// revoked grant stops at once.
func TestRealPluginBinaryOwnerGrantAutosave(t *testing.T) {
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
	state := t.TempDir()
	c := NewClient(root, []string{
		"SOLCO_ASSISTANTS_DB=" + filepath.Join(state, "a.sqlite"),
		"SOLCO_OWNER_PUBKEY_FILE=" + filepath.Join(state, "owner.pub"),
		"HERDR_PLUGIN_STATE_DIR=" + state,
	})
	ctx := t.Context()
	a, err := c.CreateAssistant(ctx, "r1", "Ada", "")
	if err != nil {
		t.Fatal(err)
	}
	if st, err := c.OwnerStatus(ctx); err != nil || st.Configured {
		t.Fatalf("status before = %+v %v", st, err)
	}
	priv, pub, err := NewOwnerKey()
	if err != nil {
		t.Fatal(err)
	}
	if st, err := c.RegisterOwner(ctx, "r2", pub); err != nil || !st.Configured || st.Fingerprint == "" {
		t.Fatalf("register = %+v %v", st, err)
	}
	_, otherPub, _ := NewOwnerKey()
	if _, err := c.RegisterOwner(ctx, "r3", otherPub); !IsCode(err, "owner_key_exists") {
		t.Fatalf("a second key = %v", err)
	}

	now := time.Now()
	in := SealGrant(priv, GrantInput{ID: "gr_00000000000000b1", AssistantID: a.ID, Capability: "memory.autosave", Effect: "allow",
		Scope: map[string][]string{"kind": {"preference"}}, MaxPerDay: 3, IssuedAt: now.UnixMilli(), ExpiresAt: now.Add(24 * time.Hour).UnixMilli()})
	g, err := c.CreateGrant(ctx, "r4", in)
	if err != nil || !g.Verified || g.Status != "active" {
		t.Fatalf("grant = %+v %v", g, err)
	}
	propose := func(req, kind, text string) MemoryItem {
		t.Helper()
		var out struct {
			Memory MemoryItem `json:"memory"`
		}
		body := map[string]any{"requestId": req, "assistantId": a.ID, "kind": kind, "content": text, "sourceRef": "run:r_1"}
		if err := c.do(ctx, body, &out, "--as", a.ID, "memory", "propose"); err != nil {
			t.Fatalf("propose: %v", err)
		}
		return out.Memory
	}
	if m := propose("p1", "preference", "Likes the Monday digest"); m.State != "approved" {
		t.Fatalf("inside the grant: %+v", m)
	}
	if m := propose("p2", "fact", "Works on Solco"); m.State != "candidate" {
		t.Fatalf("outside the grant: %+v", m)
	}
	if _, err := c.RevokeGrant(ctx, "r5", g.ID); err != nil {
		t.Fatal(err)
	}
	if m := propose("p3", "preference", "After revoke"); m.State != "candidate" {
		t.Fatalf("after revoke: %+v", m)
	}
	// Forged: the same shape sealed by a key the core never registered.
	other, _, _ := NewOwnerKey()
	forged := SealGrant(other, GrantInput{ID: "gr_00000000000000b2", AssistantID: a.ID, Capability: "memory.autosave", Effect: "allow",
		Scope: map[string][]string{"kind": {"preference"}}, IssuedAt: now.UnixMilli(), ExpiresAt: now.Add(time.Hour).UnixMilli()})
	if fg, err := c.CreateGrant(ctx, "r6", forged); err != nil || fg.Verified {
		t.Fatalf("a grant sealed by a stranger must be stored inert: %+v %v", fg, err)
	}
	if m := propose("p4", "preference", "Forged"); m.State != "candidate" {
		t.Fatalf("forged grant: %+v", m)
	}
}
