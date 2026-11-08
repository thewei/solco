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
	build.Env = append(os.Environ(), "GOTOOLCHAIN=auto", "GOWORK=off") // the plugin is its own module, outside the local go.work
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
// owner key is registered, a grant sealed here is verified there, a revoked
// grant stops at once and a forged one is stored inert. The same core's two
// memory files round-trip over the wire and the first Assistant is Primary.
func TestRealPluginBinaryOwnerGrantAndNotes(t *testing.T) {
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
	state := t.TempDir()
	c := NewClient(root, []string{
		"SOLCO_ASSISTANTS_DB=" + filepath.Join(state, "a.sqlite"),
		"SOLCO_OWNER_PUBKEY_FILE=" + filepath.Join(state, "owner.pub"),
		"HERDR_PLUGIN_STATE_DIR=" + state,
		"SOLCO_ASSISTANTS_MEMORY=" + filepath.Join(state, "memory"),
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
	in := SealGrant(priv, GrantInput{ID: "gr_00000000000000b1", AssistantID: a.ID, Capability: "task.delegate", Effect: "allow",
		Scope: map[string][]string{"kind": {"preference"}}, MaxPerDay: 3, IssuedAt: now.UnixMilli(), ExpiresAt: now.Add(24 * time.Hour).UnixMilli()})
	g, err := c.CreateGrant(ctx, "r4", in)
	if err != nil || !g.Verified || g.Status != "active" {
		t.Fatalf("grant = %+v %v", g, err)
	}
	if !a.Primary {
		t.Fatalf("the first Assistant is the Primary: %+v", a)
	}
	if list, err := c.Assistants(ctx); err != nil || len(list) != 1 || !list[0].Primary {
		t.Fatalf("primary on read: %+v %v", list, err)
	}
	if _, err := c.ArchiveAssistant(ctx, "r9", a.ID, a.Revision); !IsCode(err, "bad_state") {
		t.Fatalf("the Primary cannot be archived: %v", err)
	}

	// Two memory files: the person's, then the Assistant's own line, read together.
	n, err := c.ReadNotes(ctx, a.ID)
	if err != nil || n.User.Text != "" || n.Assistant.Text != "" {
		t.Fatalf("fresh notes: %+v %v", n, err)
	}
	if n, err = c.WriteNotes(ctx, a.ID, NotesUser, "I like short reports.\n", n.User.Rev); err != nil || n.User.Text == "" {
		t.Fatalf("write user: %+v %v", n, err)
	}
	if ack, err := c.AddNote(ctx, a.ID, "Likes the Monday digest"); err != nil || !ack.Added {
		t.Fatalf("add: %+v %v", ack, err)
	}
	if n2, err := c.ReadNotes(ctx, a.ID); err != nil || n2.Assistant.Text != "- Likes the Monday digest\n" || n2.User.Text != n.User.Text {
		t.Fatalf("read both: %+v %v", n2, err)
	}
	if _, err := c.WriteNotes(ctx, a.ID, NotesUser, "stale", n.User.Rev+"x"); !IsCode(err, "stale_revision") {
		t.Fatalf("a stale write: %v", err)
	}
	if dir, err := c.NotesFolder(ctx, a.ID); err != nil || dir == "" {
		t.Fatalf("folder: %q %v", dir, err)
	}
	if _, err := c.RevokeGrant(ctx, "r5", g.ID); err != nil {
		t.Fatal(err)
	}
	// Forged: the same shape sealed by a key the core never registered.
	other, _, _ := NewOwnerKey()
	forged := SealGrant(other, GrantInput{ID: "gr_00000000000000b2", AssistantID: a.ID, Capability: "task.delegate", Effect: "allow",
		Scope: map[string][]string{"kind": {"preference"}}, IssuedAt: now.UnixMilli(), ExpiresAt: now.Add(time.Hour).UnixMilli()})
	if fg, err := c.CreateGrant(ctx, "r6", forged); err != nil || fg.Verified {
		t.Fatalf("a grant sealed by a stranger must be stored inert: %+v %v", fg, err)
	}
}
