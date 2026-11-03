package personalagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
