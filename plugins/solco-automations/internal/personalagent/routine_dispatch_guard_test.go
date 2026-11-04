package personalagent

import (
	"testing"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
)

// A manually cleared YAML draft is not an Owner approval. The same holds for
// a half-finished Keeper write where YAML is enabled but SQLite is still draft.
func TestRuleSchedulerRefusesRoutineUntilApprovedAndActive(t *testing.T) {
	e := newRoutineEnv(t)
	got, err := e.draft(t, "crash-window", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Approve("morning-check"); err != nil {
		t.Fatal(err)
	}
	rule := &RuleScheduler{Store: e.s, Load: config.Load, Now: func() time.Time { return at(10, 0).Add(23*time.Hour + 5*time.Second) }}
	result, err := rule.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if result.Fired != 0 {
		t.Fatalf("unapproved YAML routine fired: %+v", result)
	}
	if tasks, err := e.s.ListTasks(""); err != nil || len(tasks) != 0 {
		t.Fatalf("unapproved routine emitted tasks: %d %v", len(tasks), err)
	}
	ap := got["approval"].(map[string]any)
	if _, _, err := e.s.DecideApproval("owner-decision", ap["id"].(string), ap["actionHash"].(string), true); err != nil {
		t.Fatal(err)
	}
	result, err = rule.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if result.Fired != 0 {
		t.Fatalf("YAML enabled before Keeper activated DB routine: %+v", result)
	}
	// The Keeper reconciles a fully approved definition whose YAML was already
	// enabled just before a simulated process death.
	keeper := e.api.keeper()
	kept, err := keeper.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if kept.Enabled != 1 {
		t.Fatalf("approved crash-window routine not resumed: %+v", kept)
	}
	rt, err := e.s.GetRoutine("morning-check")
	if err != nil || rt.State != RoutineActive {
		t.Fatalf("routine state %+v %v", rt, err)
	}
	result, err = rule.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if result.Fired != 1 {
		t.Fatalf("approved activated routine not dispatched: %+v", result)
	}
}

func TestRuleSchedulerRefusesEnabledRoutineWithChangedDefinition(t *testing.T) {
	e := newRoutineEnv(t)
	got, err := e.draft(t, "changed-definition", nil)
	if err != nil {
		t.Fatal(err)
	}
	ap := got["approval"].(map[string]any)
	if _, _, err = e.s.DecideApproval("owner", ap["id"].(string), ap["actionHash"].(string), true); err != nil {
		t.Fatal(err)
	}
	k := e.api.keeper()
	if _, err := k.Tick(); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	changed := *cfg.Find("morning-check")
	changed.Prompt = "Unapproved replacement command"
	if _, err := config.Put("morning-check", changed, false); err != nil {
		t.Fatal(err)
	}
	rule := &RuleScheduler{Store: e.s, Load: config.Load, Now: func() time.Time { return at(10, 0).Add(23*time.Hour + 5*time.Second) }}
	out, err := rule.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if out.Fired != 0 {
		t.Fatalf("changed executable definition fired: %+v", out)
	}
}
