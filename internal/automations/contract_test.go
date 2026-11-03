package automations

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The Client's argv and stdin shapes are a contract with a binary in another
// module, and a fake Exec can only ever agree with itself — the first real
// run of this feature found Add, Put and Run all speaking a CLI that did not
// exist. This test builds the plugin from this repository and drives the real
// Client against it in a sandboxed config/state folder (no Herdr needed: none
// of these verbs touch it; Run is the one verb left to the real-app check).
func TestClientAgainstTheRealPluginBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the plugin")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain on PATH")
	}
	pluginDir, err := filepath.Abs(filepath.Join("..", "..", "plugins", "solco-automations"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(root, "bin", "solco-automations"), ".")
	build.Dir = pluginDir
	build.Env = append(os.Environ(), "GOTOOLCHAIN=auto")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the plugin: %v\n%s", err, out)
	}

	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", t.TempDir())
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	repo := t.TempDir()
	c := NewClient(root, "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if got, err := c.List(ctx); err != nil || len(got.Automations) != 0 {
		t.Fatalf("empty list: %+v, %v", got, err)
	}

	// Add: a draft, with everything the form carries.
	err = c.Add(ctx, "nightly", Fields{
		Cron: "0 9 * * 1-5", Repo: repo, Workspace: "root", Agent: "claude",
		Prompt: "- triage the failures", Rationale: "keeps CI honest", OnOverlap: "queue", TimeoutMinutes: 30,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := c.List(ctx)
	if err != nil || len(got.Automations) != 1 {
		t.Fatalf("list after add: %+v, %v", got, err)
	}
	a := got.Automations[0]
	if !a.Draft || a.Cron != "0 9 * * 1-5" || a.Prompt != "- triage the failures" || a.OnOverlap != "queue" ||
		a.WorkspaceMode != "root" || a.TimeoutMinutes != 30 || a.Rationale != "keeps CI honest" {
		t.Fatalf("the plugin stored something else than was sent: %+v", a)
	}
	if got.Scheduler != 0 {
		t.Errorf("no daemon was started, scheduler = %d", got.Scheduler)
	}

	// A refusal reaches the caller in the plugin's words.
	if err := c.Add(ctx, "nightly", Fields{Cron: "0 9 * * *", Repo: repo, Prompt: "again"}); err == nil {
		t.Error("a second add under the same name must be refused")
	}
	if err := c.Add(ctx, "badcron", Fields{Cron: "not a cron", Repo: repo, Prompt: "x"}); err == nil {
		t.Error("an invalid cron must be refused")
	}

	// Put: the edit keeps the draft state and the fields the form never sees.
	if err := c.Put(ctx, "nightly", Fields{Cron: "30 9 * * 1-5", Prompt: "triage harder", OnOverlap: "skip", Rationale: "new why"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, _ = c.List(ctx)
	a = got.Automations[0]
	if !a.Draft || a.Cron != "30 9 * * 1-5" || a.Prompt != "triage harder" || a.OnOverlap != "skip" ||
		a.Rationale != "new why" || a.WorkspaceMode != "root" || a.Repo != repo {
		t.Fatalf("after Put: %+v", a)
	}
	err = c.Put(ctx, "nightly", Fields{Cron: "not a cron"})
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("a put the plugin rejects must surface its problem, got %v", err)
	}
	got, _ = c.List(ctx)
	if got.Automations[0].Cron != "30 9 * * 1-5" {
		t.Fatalf("a refused put must change nothing, cron = %q", got.Automations[0].Cron)
	}

	// Approve, pause, resume, remove.
	if err := c.Approve(ctx, "nightly"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := c.Pause(ctx, "nightly"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	got, _ = c.List(ctx)
	if a = got.Automations[0]; a.Draft || !a.Disabled {
		t.Fatalf("after approve+pause: %+v", a)
	}
	if err := c.Resume(ctx, "nightly"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if h, err := c.History(ctx, "nightly"); err != nil || len(h.Runs) != 0 {
		t.Fatalf("history: %+v, %v", h, err)
	}
	if err := c.Remove(ctx, "nightly"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got, _ = c.List(ctx); len(got.Automations) != 0 {
		t.Fatalf("after remove: %+v", got)
	}
}
