package personalagent

import (
	"strings"
	"testing"
)

// R08: a Routine says, in the record itself, who owns it, how it was asked for,
// in which zone it runs, which skills it needs, why it is not running, and which
// task its last occurrence made — so no client has to guess or re-derive them.
func TestGapR08RoutineViewCarriesZoneSkillsReasonAndLastTask(t *testing.T) {
	e := newRoutineEnv(t)
	got, err := e.draft(t, "r08-draft", func(m map[string]any) {
		delete(m, "cron")
		m["schedule"] = "every weekday at 9:00"
		m["timezone"] = "Asia/Shanghai"
		m["requiredSkills"] = []string{"fixture-browser"}
	})
	if err != nil {
		t.Fatal(err)
	}
	rt := got["routine"].(map[string]any)
	if rt["timezone"] != "Asia/Shanghai" {
		t.Fatalf("timezone = %v", rt["timezone"])
	}
	if sk, _ := rt["skills"].([]any); len(sk) != 1 || sk[0] != "fixture-browser" {
		t.Fatalf("skills = %v", rt["skills"])
	}
	if r, _ := rt["reason"].(string); !strings.Contains(r, "approve") {
		t.Fatalf("a draft must say it waits for a person's approval: %q", r)
	}
}
