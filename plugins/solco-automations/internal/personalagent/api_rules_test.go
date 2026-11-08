package personalagent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
)

// The CLI of stage D: dry run → adopt (explicit) → rollback, with a one-time
// backup of the definitions and replay-safe request ids.
func TestAPIRuleLifecycle(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", dir)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)
	yaml := "automations:\n  - name: daily\n    cron: \"0 9 * * *\"\n    repo: /tmp/repo\n    prompt: go\n"
	if err := os.WriteFile(config.Path(), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	// The old scheduler's own file is the watermark source.
	state := `{"last_occurrence":{"daily":"` + at(9, 0).Add(-24*time.Hour).UTC().Format(time.RFC3339) + `"}}`
	if err := os.WriteFile(filepath.Join(dir, "schedule.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _ := openTest(t)
	api := &API{Store: s, Now: func() time.Time { return at(10, 0) }}

	got, err := call(t, api, ``, "assistant", "rule", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	cands := got["candidates"].([]any)
	if len(cands) != 1 || cands[0].(map[string]any)["state"] != LegacyDryRun {
		t.Fatalf("candidates = %v", got)
	}
	if owned, _ := s.OwnedLegacy(); len(owned) != 0 {
		t.Fatalf("a dry run owns %v", owned)
	}
	if _, err := os.Stat(config.Path() + ".pre-assistants.bak"); err == nil {
		t.Fatal("a dry run must not back up or touch anything")
	}

	if _, err := call(t, api, `{"requestId":"x1"}`, "assistant", "rule", "adopt", "ghost"); codeOf(err) != "not_found" {
		t.Fatalf("unknown rule = %q (%v)", codeOf(err), err)
	}
	a1, err := call(t, api, `{"requestId":"x2"}`, "assistant", "rule", "adopt", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if a1["replayed"] != false {
		t.Fatalf("first adopt replayed: %v", a1)
	}
	if raw, err := os.ReadFile(config.Path() + ".pre-assistants.bak"); err != nil || string(raw) != yaml {
		t.Fatalf("backup = %q %v", raw, err)
	}
	a2, err := call(t, api, `{"requestId":"x2"}`, "assistant", "rule", "adopt", "--all")
	if err != nil || a2["replayed"] != true {
		t.Fatalf("replay = %v %v", a2, err)
	}
	if owned, _ := s.OwnedLegacy(); !owned["daily"] {
		t.Fatalf("owned = %v", owned)
	}
	led, _ := call(t, api, ``, "assistant", "rule", "ledger")
	if rows := led["ledger"].([]any); len(rows) != 1 || rows[0].(map[string]any)["state"] != LegacyActive {
		t.Fatalf("ledger = %v", led)
	}
	if _, err := call(t, api, `{"requestId":"x3"}`, "assistant", "rule", "rollback", "daily"); err != nil {
		t.Fatal(err)
	}
	if owned, _ := s.OwnedLegacy(); len(owned) != 0 {
		t.Fatalf("still owned after rollback: %v", owned)
	}
	if _, err := call(t, api, `{"requestId":"x4"}`, "assistant", "rule", "rollback", "daily"); codeOf(err) != "bad_state" {
		t.Fatalf("double rollback = %q", codeOf(err))
	}
}
